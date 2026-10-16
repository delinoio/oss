// SPDX-License-Identifier: Apache-2.0
package subscription_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func (f *refreshFixture) revoke(client *http.Client) (subscription.GrokRevocation, error) {
	if client == nil {
		client = f.idp.Client()
	}
	return subscription.RevokeGrokRefreshToken(context.Background(), client, f.profile, f.endpoints, f.bound.RefreshToken())
}

func TestGrokRevocationEndpointIsDiscoveredOnTheIssuerOrigin(t *testing.T) {
	f := newGrokFlow(t)
	if f.endpoints.Revocation != "https://auth.x.ai/oauth2/revoke" {
		t.Fatalf("revocation endpoint = %q", f.endpoints.Revocation)
	}
	f.idp.RevokeOmitEndpoint = true
	endpoints, err := subscription.DiscoverGrokEndpoints(context.Background(), f.idp.Client(), f.profile)
	if err != nil || endpoints.Revocation != "" || endpoints.Token == "" {
		t.Fatal("discovery without a revocation endpoint must still serve login and refresh")
	}
}

func TestGrokRevocationSendsTheObservedPublicClientForm(t *testing.T) {
	f := newRefreshFixture(t)
	outcome, err := f.revoke(nil)
	if err != nil || outcome != subscription.GrokRevoked || !outcome.Confirmed() {
		t.Fatalf("revocation = %q, %v", outcome, err)
	}
	if f.idp.RevokeRequests != 1 {
		t.Fatal("revocation must send exactly one request")
	}
	form := f.idp.RevokeForms[0]
	if len(form) != 3 || form.Get("token") != f.bound.RefreshToken() || form.Get("token_type_hint") != "refresh_token" || form.Get("client_id") != subscription.GrokClientID {
		t.Fatal("revocation form is not token, token_type_hint and the public client id")
	}
	if f.idp.RevokeAuthHeaders[0] != "" {
		t.Fatal("the public client must not authenticate")
	}
	if !f.idp.RefreshRevoked() {
		t.Fatal("the provider did not revoke the token")
	}
	// The revoked token fails its refresh grant with invalid_grant.
	_, err = f.refresh(nil)
	if reason, _ := reasonOf(t, err); reason != subscription.GrokReasonInvalidGrant {
		t.Fatalf("refresh after revocation = %q", reason)
	}
}

func TestGrokRevocationOfAnAlreadyInvalidTokenCountsAsRevoked(t *testing.T) {
	f := newRefreshFixture(t)
	if _, err := f.revoke(nil); err != nil {
		t.Fatal(err)
	}
	outcome, err := f.revoke(nil)
	if err != nil || outcome != subscription.GrokRevokedAlreadyInvalid || !outcome.Confirmed() {
		t.Fatalf("second revocation = %q, %v", outcome, err)
	}
	for _, answer := range []string{"invalid_token", "invalid_grant"} {
		g := newRefreshFixture(t)
		g.idp.RevokeStatus, g.idp.RevokeError = http.StatusBadRequest, answer
		if outcome, err := g.revoke(nil); err != nil || outcome != subscription.GrokRevokedAlreadyInvalid {
			t.Fatalf("%s must count as revoked, got %q, %v", answer, outcome, err)
		}
	}
}

func TestGrokRevocationFailuresAreNeverConfirmed(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		failure   string
		reason    subscription.GrokOAuthReason
		uncertain bool
	}{
		{"client error", http.StatusBadRequest, "invalid_request", subscription.GrokReasonRevocationRejected, false},
		{"unauthorized", http.StatusUnauthorized, "invalid_client", subscription.GrokReasonRevocationRejected, false},
		{"forbidden", http.StatusForbidden, "", subscription.GrokReasonRevocationRejected, false},
		{"server error", http.StatusInternalServerError, "server_error", subscription.GrokReasonRevocationUncertain, true},
		{"unavailable", http.StatusServiceUnavailable, "", subscription.GrokReasonRevocationUncertain, true},
		{"redirect", http.StatusFound, "", subscription.GrokReasonRevocationUncertain, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newRefreshFixture(t)
			f.idp.RevokeStatus, f.idp.RevokeError = test.status, test.failure
			outcome, err := f.revoke(nil)
			if outcome.Confirmed() {
				t.Fatal("a failed revocation must not be confirmed")
			}
			reason, uncertain := reasonOf(t, err)
			if reason != test.reason || uncertain != test.uncertain {
				t.Fatalf("reason = %q uncertain = %v", reason, uncertain)
			}
			if f.idp.RefreshRevoked() {
				t.Fatal("the fake provider revoked the token on a failure")
			}
		})
	}
}

func TestGrokRevocationTimeoutAndTransportFailureAreUncertain(t *testing.T) {
	f := newRefreshFixture(t)
	f.idp.BeforeRevoke = func() { time.Sleep(300 * time.Millisecond) }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	outcome, err := subscription.RevokeGrokRefreshToken(ctx, f.idp.Client(), f.profile, f.endpoints, f.bound.RefreshToken())
	if outcome.Confirmed() {
		t.Fatal("a timed out revocation must not be confirmed")
	}
	if reason, uncertain := reasonOf(t, err); reason != subscription.GrokReasonRevocationUncertain || !uncertain {
		t.Fatalf("timeout reason = %q uncertain = %v", reason, uncertain)
	}
	broken := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") })}
	if _, err := f.revoke(broken); err == nil {
		t.Fatal("a transport failure must fail")
	} else if reason, uncertain := reasonOf(t, err); reason != subscription.GrokReasonRevocationUncertain || !uncertain {
		t.Fatal("a transport failure must be uncertain")
	}
}

func TestGrokRevocationWithoutAnEndpointSendsNothing(t *testing.T) {
	f := newRefreshFixture(t)
	f.idp.RevokeOmitEndpoint = true
	endpoints, err := subscription.DiscoverGrokEndpoints(context.Background(), f.idp.Client(), f.profile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = subscription.RevokeGrokRefreshToken(context.Background(), f.idp.Client(), f.profile, endpoints, f.bound.RefreshToken())
	if reason, uncertain := reasonOf(t, err); reason != subscription.GrokReasonRevocationEndpoint || uncertain {
		t.Fatalf("missing endpoint reason = %q uncertain = %v", reason, uncertain)
	}
	if f.idp.RevokeRequests != 0 {
		t.Fatal("nothing may be sent without a discovered endpoint")
	}
}

func TestGrokDiscoveryDropsAForeignOriginRevocationEndpoint(t *testing.T) {
	f := newGrokFlow(t)
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			return f.idp.Transport().RoundTrip(r)
		}
		return recorded(func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issuer":"https://auth.x.ai","authorization_endpoint":"https://auth.x.ai/oauth2/authorize","token_endpoint":"https://auth.x.ai/oauth2/token","jwks_uri":"https://auth.x.ai/.well-known/jwks.json","userinfo_endpoint":"https://auth.x.ai/oauth2/userinfo","revocation_endpoint":"https://evil.example/oauth2/revoke","response_types_supported":["code"],"code_challenge_methods_supported":["S256"],"token_endpoint_auth_methods_supported":["none"],"id_token_signing_alg_values_supported":["ES256"]}`))
		}), nil
	})}
	endpoints, err := subscription.DiscoverGrokEndpoints(context.Background(), client, f.profile)
	if err != nil || endpoints.Revocation != "" {
		t.Fatalf("a foreign-origin revocation endpoint must be dropped, got %q, %v", endpoints.Revocation, err)
	}
}
