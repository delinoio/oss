// SPDX-License-Identifier: Apache-2.0
package subscription_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/grokidptest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// refreshFixture is a completed fake login whose bundle the refresh binds to.
type refreshFixture struct {
	*grokFlow
	bound subscription.GrokBundle
	later time.Time
}

func newRefreshFixture(t *testing.T) *refreshFixture {
	t.Helper()
	f := newGrokFlow(t)
	tokens, err := f.exchange()
	if err != nil {
		t.Fatal(err)
	}
	keys, err := subscription.FetchGrokJWKS(context.Background(), f.idp.Client(), f.endpoints)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := subscription.CompleteGrokLogin(context.Background(), f.idp.Client(), f.profile, f.endpoints, keys, tokens, subscription.GrokLoginBrowser, f.auth.Nonce, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bound.Clear)
	return &refreshFixture{grokFlow: f, bound: bound, later: time.Now().Add(10 * time.Minute)}
}

func (f *refreshFixture) refresh(client *http.Client) (subscription.GrokRefreshTokens, error) {
	if client == nil {
		client = f.idp.Client()
	}
	return subscription.RefreshGrokTokens(context.Background(), client, f.profile, f.endpoints, f.bound.RefreshToken())
}

func (f *refreshFixture) complete(t *testing.T, tokens subscription.GrokRefreshTokens) (subscription.GrokBundle, error) {
	t.Helper()
	keys, err := subscription.FetchGrokJWKS(context.Background(), f.idp.Client(), f.endpoints)
	if err != nil {
		t.Fatal(err)
	}
	return subscription.CompleteGrokRefresh(context.Background(), f.idp.Client(), f.profile, f.endpoints, keys, f.bound, tokens, f.later)
}

func reasonOf(t *testing.T, err error) (subscription.GrokOAuthReason, bool) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a failure")
	}
	reason, uncertain := subscription.GrokOAuthReasonOf(err)
	if reason == "" {
		t.Fatalf("failure carries no closed reason: %v", err)
	}
	return reason, uncertain
}

func TestGrokRefreshSendsOnePublicClientRefreshGrant(t *testing.T) {
	f := newRefreshFixture(t)
	before := f.idp.TokenRequests
	tokens, err := f.refresh(nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.idp.TokenRequests != before+1 || f.idp.RefreshRequests != 1 {
		t.Fatal("a refresh must send exactly one token request")
	}
	form := f.idp.RefreshForms[0]
	if len(form) != 3 || form.Get("grant_type") != "refresh_token" || form.Get("client_id") != subscription.GrokClientID || form.Get("refresh_token") != f.bound.RefreshToken() {
		t.Fatal("refresh form is not the public-client refresh_token grant")
	}
	if f.idp.TokenAuthHeaders[len(f.idp.TokenAuthHeaders)-1] != "" {
		t.Fatal("the public client must not authenticate")
	}
	if tokens.Rotated() || tokens.IDToken == "" || tokens.ExpiresIn != 21600*time.Second {
		t.Fatal("unrotated refresh answer was not parsed")
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v %s", tokens, tokens, tokens, tokens), tokens.AccessToken) {
		t.Fatal("refresh tokens must format as redacted")
	}
}

func TestGrokRefreshWithoutRotationKeepsTheRefreshToken(t *testing.T) {
	f := newRefreshFixture(t)
	tokens, err := f.refresh(nil)
	if err != nil {
		t.Fatal(err)
	}
	next, err := f.complete(t, tokens)
	if err != nil {
		t.Fatal(err)
	}
	if next.RefreshToken() != f.bound.RefreshToken() || next.Equal(f.bound) || next.IdentityKey() != f.bound.IdentityKey() {
		t.Fatal("an unrotated refresh must keep the refresh token and change the access key")
	}
	if err := subscription.GrokRefreshed(f.bound, next); err != nil {
		t.Fatal("the successor must pass the refresh rules")
	}
	if !next.Identity().Expiry.After(f.bound.Identity().Expiry) {
		t.Fatal("the expiry must move forward")
	}
}

func TestGrokRefreshWithRotationReplacesTheRefreshToken(t *testing.T) {
	f := newRefreshFixture(t)
	f.idp.RefreshRotate = true
	old := f.bound.RefreshToken()
	tokens, err := f.refresh(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !tokens.Rotated() {
		t.Fatal("the rotated token was not parsed")
	}
	next, err := f.complete(t, tokens)
	if err != nil {
		t.Fatal(err)
	}
	if next.RefreshToken() == old || next.RefreshToken() != tokens.RefreshToken {
		t.Fatal("the rotated refresh token was not adopted")
	}
	// The provider no longer accepts the superseded token.
	f.bound.Clear()
	if _, err := subscription.RefreshGrokTokens(context.Background(), f.idp.Client(), f.profile, f.endpoints, old); err == nil {
		t.Fatal("the fake must refuse a superseded refresh token")
	}
}

func TestGrokRefreshAcceptsAnAnswerWithoutAnIDToken(t *testing.T) {
	f := newRefreshFixture(t)
	f.idp.RefreshOmitID = true
	tokens, err := f.refresh(nil)
	if err != nil || tokens.IDToken != "" {
		t.Fatal("an answer without an ID token must be accepted")
	}
	next, err := f.complete(t, tokens)
	if err != nil {
		t.Fatal("identity must be proven by userinfo alone")
	}
	next.Clear()
}

func TestGrokRefreshOutcomesAreClassified(t *testing.T) {
	cases := []struct {
		name      string
		edit      func(*grokidptest.IdP)
		reason    subscription.GrokOAuthReason
		uncertain bool
	}{
		{"invalid_grant", func(i *grokidptest.IdP) { i.RefreshStatus, i.RefreshError = http.StatusBadRequest, "invalid_grant" }, subscription.GrokReasonInvalidGrant, false},
		{"other client error", func(i *grokidptest.IdP) { i.RefreshStatus, i.RefreshError = http.StatusUnauthorized, "invalid_client" }, subscription.GrokReasonTokenRejected, false},
		// Only an RFC 6749 section 5.2 error response (400 or 401 with a structured
		// error) proves the token was not consumed; every other 4xx stays uncertain.
		{"rate limited", func(i *grokidptest.IdP) { i.RefreshStatus = http.StatusTooManyRequests }, subscription.GrokReasonTokenUncertain, true},
		{"request timeout", func(i *grokidptest.IdP) { i.RefreshStatus = http.StatusRequestTimeout }, subscription.GrokReasonTokenUncertain, true},
		{"forbidden", func(i *grokidptest.IdP) { i.RefreshStatus = http.StatusForbidden }, subscription.GrokReasonTokenUncertain, true},
		{"server error", func(i *grokidptest.IdP) { i.RefreshStatus = http.StatusInternalServerError }, subscription.GrokReasonTokenUncertain, true},
		{"bad gateway", func(i *grokidptest.IdP) { i.RefreshStatus = http.StatusBadGateway }, subscription.GrokReasonTokenUncertain, true},
		{"malformed body", func(i *grokidptest.IdP) { i.RefreshMalformed = true }, subscription.GrokReasonTokenResponse, true},
		{"truncated json", func(i *grokidptest.IdP) { i.RefreshTruncated = true }, subscription.GrokReasonTokenResponse, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRefreshFixture(t)
			tc.edit(f.idp)
			_, err := f.refresh(nil)
			reason, uncertain := reasonOf(t, err)
			if reason != tc.reason || uncertain != tc.uncertain {
				t.Fatalf("outcome = %s uncertain=%v", reason, uncertain)
			}
			if f.idp.RefreshRequests != 1 {
				t.Fatal("a failed refresh must not retry")
			}
			if strings.Contains(err.Error(), f.bound.RefreshToken()) {
				t.Fatal("the error leaked the refresh token")
			}
		})
	}
}

// A 4xx answer without a structured OAuth error body may come from an
// intermediary after the refresh token was received, so it keeps the claim.
func TestGrokRefreshUnstructuredClientErrorsStayUncertain(t *testing.T) {
	for name, serve := range map[string]func(http.ResponseWriter){
		"html 403": func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, "<html>blocked</html>")
		},
		"empty 400":    func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadRequest) },
		"html 400":     func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadRequest); fmt.Fprint(w, "<html>bad</html>") },
		"400 no error": func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadRequest); fmt.Fprint(w, `{"message":"nope"}`) },
		"throttled json": func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRefreshFixture(t)
			client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return recorded(serve), nil })}
			_, err := f.refresh(client)
			if reason, uncertain := reasonOf(t, err); reason != subscription.GrokReasonTokenUncertain || !uncertain {
				t.Fatalf("outcome = %s uncertain=%v", reason, uncertain)
			}
		})
	}
}

// brokenBody is a response body that fails mid-read, as a truncated transfer.
type brokenBody struct{ sent bool }

func (b *brokenBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, io.ErrUnexpectedEOF
	}
	b.sent = true
	return copy(p, `{"access_token":"fake-cut`), nil
}
func (*brokenBody) Close() error { return nil }

func TestGrokRefreshTransportFailuresAreUncertain(t *testing.T) {
	f := newRefreshFixture(t)
	discovery := f.idp.Client()
	_ = discovery
	truncated := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: &brokenBody{}, Request: r}, nil
	})}
	broken := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") })}
	for name, client := range map[string]*http.Client{"truncated transfer": truncated, "transport error": broken} {
		_, err := f.refresh(client)
		if reason, uncertain := reasonOf(t, err); reason != subscription.GrokReasonTokenUncertain || !uncertain {
			t.Fatalf("%s = %s uncertain=%v", name, reason, uncertain)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := subscription.RefreshGrokTokens(canceled, f.idp.Client(), f.profile, f.endpoints, f.bound.RefreshToken())
	if _, uncertain := reasonOf(t, err); !uncertain {
		t.Fatal("an interrupted request is uncertain")
	}
}

func TestGrokRefreshIdentityMismatchNeverBuildsABundle(t *testing.T) {
	cases := map[string]func(*grokidptest.IdP){
		"team id":          func(i *grokidptest.IdP) { i.TeamID = "another-team" },
		"principal id":     func(i *grokidptest.IdP) { i.PrincipalID = "another-principal" },
		"principal type":   func(i *grokidptest.IdP) { i.PrincipalType = "service" },
		"id token subject": func(i *grokidptest.IdP) { i.Subject = "another-user"; i.UserinfoSubject = "fake-user-1" },
		"userinfo subject": func(i *grokidptest.IdP) { i.UserinfoSubject = "another-user" },
		"userinfo failure": func(i *grokidptest.IdP) { i.UserinfoStatus = http.StatusInternalServerError },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRefreshFixture(t)
			edit(f.idp)
			tokens, err := f.refresh(nil)
			if err != nil {
				t.Fatal(err)
			}
			next, err := f.complete(t, tokens)
			if err == nil {
				next.Clear()
				t.Fatal("a bundle was built for another identity")
			}
			reasonOf(t, err)
		})
	}
	t.Run("rogue signature", func(t *testing.T) {
		f := newRefreshFixture(t)
		f.idp.SignWithOtherKey = true
		tokens, err := f.refresh(nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.complete(t, tokens); err == nil {
			t.Fatal("an ID token with a foreign signature was accepted")
		}
	})
}

func TestGrokRefreshRequiresAChangedCredentialAndALaterExpiry(t *testing.T) {
	f := newRefreshFixture(t)
	// The answer repeats the bound access token and does not rotate.
	same := subscription.GrokRefreshTokens{AccessToken: f.idp.AccessToken, ExpiresIn: 6 * time.Hour}
	if next, err := f.complete(t, same); err == nil {
		next.Clear()
		t.Fatal("an unchanged credential was accepted as a refresh")
	}
	// A fresh access token with an expiry that does not move forward.
	f.idp.AccessToken = "fake-access-token-short"
	short := subscription.GrokRefreshTokens{AccessToken: f.idp.AccessToken, ExpiresIn: time.Second}
	f.later = time.Now()
	if next, err := f.complete(t, short); err == nil {
		next.Clear()
		t.Fatal("an expiry that did not move forward was accepted")
	}
}

func TestGrokRefreshErrorsAndFormattersNeverEchoSecrets(t *testing.T) {
	f := newRefreshFixture(t)
	f.idp.RefreshRotate = true
	tokens, err := f.refresh(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{fmt.Sprint(tokens), fmt.Sprintf("%+v", tokens), fmt.Sprintf("%#v", tokens), fmt.Sprint(f.bound), fmt.Sprintf("%+v", f.bound)} {
		if !f.idp.Redacted(text) {
			t.Fatal("a formatter revealed a credential")
		}
	}
	tokens.Clear()
	if tokens.AccessToken != "" || tokens.RefreshToken != "" || tokens.IDToken != "" {
		t.Fatal("Clear left credential references")
	}
}

func TestGrokRefreshPinsTheNativePrincipal(t *testing.T) {
	f := newRefreshFixture(t)
	tokens, err := subscription.RefreshGrokTokens(context.Background(), f.idp.Client(), f.profile, f.endpoints, f.bound.RefreshToken(), f.bound.Identity())
	if err != nil {
		t.Fatal(err)
	}
	defer tokens.Clear()
	form := f.idp.RefreshForms[0]
	if form.Get("principal_type") != f.bound.Identity().PrincipalType || form.Get("principal_id") != f.bound.Identity().PrincipalID {
		t.Fatal("refresh did not pin the original native principal")
	}
}

func TestGrokRefreshReadsPrincipalFromTheTrustedTokenResponse(t *testing.T) {
	for _, camel := range []bool{false, true} {
		for _, mismatch := range []bool{false, true} {
			t.Run(fmt.Sprintf("camel=%v/mismatch=%v", camel, mismatch), func(t *testing.T) {
				f := newRefreshFixture(t)
				identity := f.bound.Identity()
				// Real OIDC userinfo and ID tokens can omit native principal metadata.
				f.idp.PrincipalType = ""
				f.idp.PrincipalID = ""
				f.idp.RefreshOmitID = true
				tokens, err := f.refresh(nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tokens.Clear()
				principal := identity.PrincipalID
				if mismatch {
					principal = "another-native-principal"
				}
				claims := map[string]string{"principal_type": identity.PrincipalType, "principal_id": principal}
				if camel {
					claims = map[string]string{"principalType": identity.PrincipalType, "principalId": principal}
				}
				payload, _ := json.Marshal(claims)
				tokens.AccessToken = "fixture." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
				f.idp.AccessToken = tokens.AccessToken
				next, err := f.complete(t, tokens)
				if mismatch {
					if err == nil {
						next.Clear()
						t.Fatal("another native principal was accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				defer next.Clear()
				if next.IdentityKey() != f.bound.IdentityKey() {
					t.Fatal("native identity changed")
				}
			})
		}
	}
}
