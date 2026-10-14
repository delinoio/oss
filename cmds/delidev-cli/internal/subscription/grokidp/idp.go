// SPDX-License-Identifier: Apache-2.0

// Package grokidp is a fake xAI identity provider for tests only. It serves
// OIDC discovery, JWKS, token and userinfo endpoints for the fixed Grok
// issuer over a local TLS listener, and redirects an owned HTTP client to it,
// so production code under test keeps its real https://auth.x.ai URLs. No
// production package imports it.
package grokidp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const keyID = "fake-grok-key-1"

// Grant is one approved authorization request.
type Grant struct {
	Code        string
	Challenge   string
	Nonce       string
	RedirectURI string
}

// RefreshMode selects how the fake provider answers a refresh_token grant.
type RefreshMode int

const (
	// RefreshRotate issues a new refresh token and invalidates the presented one.
	RefreshRotate RefreshMode = iota
	// RefreshOmit omits refresh_token from the response (no rotation).
	RefreshOmit
	// RefreshUnchanged returns the presented refresh token unchanged.
	RefreshUnchanged
)

// IdP is the fake provider. Fields prefixed with Override are test knobs and
// must be set before the flow under test reaches the matching endpoint.
type IdP struct {
	Transport http.RoundTripper

	server *httptest.Server
	key    *ecdsa.PrivateKey
	other  *ecdsa.PrivateKey

	mu     sync.Mutex
	grants map[string]Grant

	tokenRequests     atomic.Int32
	userInfoRequests  atomic.Int32
	discoveryRequests atomic.Int32
	lastToken         url.Values
	lastTokenHeaders  http.Header
	tokenForms        []url.Values
	refreshCurrent    string
	issued            int

	// Subject, Email, FirstName and the principal fields feed the ID token and
	// userinfo. PrincipalType/PrincipalID stay empty by default (fallback).
	Subject       string
	Email         string
	FirstName     string
	PrincipalType string
	PrincipalID   string
	TeamID        string

	// Token knobs.
	TokenStatus      int           // overrides the token response status
	TokenBody        string        // overrides the token response body
	TokenGate        chan struct{} // when set, the token endpoint blocks until closed
	TokenEntered     chan struct{} // when set, receives once per token request
	ExpiresIn        int
	OverrideNonce    string
	OverrideIssuer   string
	OverrideAudience any
	ExpiredIDToken   bool
	SignWithWrongKey bool
	UserInfoStatus   int
	UserInfoSubject  string
	DiscoveryIssuer  string
	DiscoveryHost    string
	NoRefreshToken   bool

	// Refresh knobs. RefreshMode shapes a successful refresh_token grant,
	// OmitRefreshIDToken drops the ID token from it, and TruncateTokenBody makes
	// any token response end before its declared length.
	RefreshMode        RefreshMode
	OmitRefreshIDToken bool
	TruncateTokenBody  bool
	// TokenTrailer is appended verbatim after the body of every token response,
	// including a configured TokenBody and an invalid_grant rejection.
	TokenTrailer string
}

// New starts the fake provider and registers cleanup.
func New(t testing.TB) *IdP {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	idp := &IdP{key: key, other: other, grants: map[string]Grant{}, Subject: "fake-user-1", Email: "fixture@example.invalid", FirstName: "Fixture", ExpiresIn: 6 * 3600}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", idp.discovery)
	mux.HandleFunc("/.well-known/jwks.json", idp.jwks)
	mux.HandleFunc("/oauth2/token", idp.token)
	mux.HandleFunc("/oauth2/userinfo", idp.userinfo)
	idp.server = httptest.NewTLSServer(mux)
	address := idp.server.Listener.Addr().String()
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
		DisableKeepAlives: true,
	}
	idp.Transport = transport
	t.Cleanup(func() { transport.CloseIdleConnections(); idp.server.Close() })
	return idp
}

// TokenRequests counts token endpoint requests.
func (i *IdP) TokenRequests() int { return int(i.tokenRequests.Load()) }

// UserInfoRequests counts userinfo requests.
func (i *IdP) UserInfoRequests() int { return int(i.userInfoRequests.Load()) }

// LastTokenForm returns the form of the last token request.
func (i *IdP) LastTokenForm() url.Values {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.lastToken
}

// TokenForms returns the form of every token request in arrival order.
func (i *IdP) TokenForms() []url.Values {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]url.Values(nil), i.tokenForms...)
}

// RefreshTokensPresented returns every refresh token value a refresh_token
// grant presented, in arrival order.
func (i *IdP) RefreshTokensPresented() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	var presented []string
	for _, form := range i.tokenForms {
		if form.Get("grant_type") == "refresh_token" {
			presented = append(presented, form.Get("refresh_token"))
		}
	}
	return presented
}

// CurrentRefreshToken is the one refresh token the provider currently honors.
func (i *IdP) CurrentRefreshToken() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.refreshCurrent
}

// LastTokenHeaders returns the headers of the last token request.
func (i *IdP) LastTokenHeaders() http.Header {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.lastTokenHeaders
}

// Approve validates an authorization URL like the real provider would for the
// public client and returns the authorization code for it.
func (i *IdP) Approve(t testing.TB, authorization string) Grant {
	t.Helper()
	u, err := url.Parse(authorization)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != subscription.GrokClientID || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatal("fake provider refused the authorization request")
	}
	raw := make([]byte, 24)
	_, _ = rand.Read(raw)
	grant := Grant{Code: base64.RawURLEncoding.EncodeToString(raw), Challenge: q.Get("code_challenge"), Nonce: q.Get("nonce"), RedirectURI: q.Get("redirect_uri")}
	i.mu.Lock()
	i.grants[grant.Code] = grant
	i.mu.Unlock()
	return grant
}

func (i *IdP) host() string {
	if i.DiscoveryHost != "" {
		return i.DiscoveryHost
	}
	return "auth.x.ai"
}

func (i *IdP) discovery(w http.ResponseWriter, _ *http.Request) {
	i.discoveryRequests.Add(1)
	issuer := subscription.GrokIssuer
	if i.DiscoveryIssuer != "" {
		issuer = i.DiscoveryIssuer
	}
	base := "https://" + i.host()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer": issuer, "authorization_endpoint": base + "/oauth2/authorize", "token_endpoint": base + "/oauth2/token",
		"userinfo_endpoint": base + "/oauth2/userinfo", "jwks_uri": base + "/.well-known/jwks.json",
		"response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post", "none"},
		"id_token_signing_alg_values_supported": []string{"ES256"},
	})
}

func (i *IdP) jwks(w http.ResponseWriter, _ *http.Request) {
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &i.key.PublicKey, KeyID: keyID, Algorithm: "ES256", Use: "sig"}}}
	_ = json.NewEncoder(w).Encode(set)
}

func (i *IdP) token(w http.ResponseWriter, r *http.Request) {
	i.tokenRequests.Add(1)
	_ = r.ParseForm()
	i.mu.Lock()
	i.lastToken = r.PostForm
	i.tokenForms = append(i.tokenForms, r.PostForm)
	i.lastTokenHeaders = r.Header.Clone()
	i.mu.Unlock()
	if i.TokenEntered != nil {
		select {
		case i.TokenEntered <- struct{}{}:
		default:
		}
	}
	if i.TokenGate != nil {
		select {
		case <-i.TokenGate:
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if i.TokenBody != "" || i.TokenStatus != 0 {
		status := i.TokenStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(i.TokenBody + i.TokenTrailer))
		return
	}
	reject := func() {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
		_, _ = w.Write([]byte(i.TokenTrailer))
	}
	if r.PostForm.Get("grant_type") == "refresh_token" {
		i.refresh(w, r, reject)
		return
	}
	i.mu.Lock()
	grant, ok := i.grants[r.PostForm.Get("code")]
	delete(i.grants, r.PostForm.Get("code"))
	i.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("client_id") != subscription.GrokClientID || r.PostForm.Get("redirect_uri") != grant.RedirectURI ||
		base64.RawURLEncoding.EncodeToString(sum[:]) != grant.Challenge || r.PostForm.Get("client_secret") != "" || r.Header.Get("Authorization") != "" {
		reject()
		return
	}
	body := map[string]any{"access_token": "fake-access-token", "token_type": "Bearer", "expires_in": i.ExpiresIn, "id_token": i.idToken(grant.Nonce)}
	if !i.NoRefreshToken {
		body["refresh_token"] = "fake-refresh-token"
		i.mu.Lock()
		i.refreshCurrent = "fake-refresh-token"
		i.mu.Unlock()
	}
	i.write(w, body)
}

// write encodes a successful token response, optionally cut short.
func (i *IdP) write(w http.ResponseWriter, body map[string]any) {
	raw, _ := json.Marshal(body)
	if i.TruncateTokenBody {
		// Declare the full length and send half: the client sees an unexpected EOF.
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
		_, _ = w.Write(raw[:len(raw)/2])
		return
	}
	_, _ = w.Write(append(raw, i.TokenTrailer...))
}

// refresh answers a refresh_token grant for the public client. Only the
// currently honored refresh token is accepted; a rotated-away token is refused
// exactly like a real provider would refuse a replayed one.
func (i *IdP) refresh(w http.ResponseWriter, r *http.Request, reject func()) {
	i.mu.Lock()
	current := i.refreshCurrent
	i.mu.Unlock()
	presented := r.PostForm.Get("refresh_token")
	if r.PostForm.Get("client_id") != subscription.GrokClientID || presented == "" || presented != current || r.PostForm.Get("client_secret") != "" || r.Header.Get("Authorization") != "" {
		reject()
		return
	}
	i.mu.Lock()
	i.issued++
	n := i.issued
	i.mu.Unlock()
	body := map[string]any{"access_token": "fake-access-token-r" + strconv.Itoa(n), "token_type": "Bearer", "expires_in": i.ExpiresIn}
	if !i.OmitRefreshIDToken {
		body["id_token"] = i.idToken("")
	}
	switch i.RefreshMode {
	case RefreshRotate:
		next := "fake-refresh-token-r" + strconv.Itoa(n)
		body["refresh_token"] = next
		i.mu.Lock()
		i.refreshCurrent = next
		i.mu.Unlock()
	case RefreshUnchanged:
		body["refresh_token"] = presented
	}
	i.write(w, body)
}

func (i *IdP) idToken(nonce string) string {
	if i.OverrideNonce != "" {
		nonce = i.OverrideNonce
	}
	issuer := subscription.GrokIssuer
	if i.OverrideIssuer != "" {
		issuer = i.OverrideIssuer
	}
	var audience any = []string{subscription.GrokClientID}
	if i.OverrideAudience != nil {
		audience = i.OverrideAudience
	}
	now := time.Now()
	expiry := now.Add(time.Hour)
	if i.ExpiredIDToken {
		expiry = now.Add(-time.Hour)
	}
	claims := map[string]any{"iss": issuer, "aud": audience, "sub": i.Subject, "exp": expiry.Unix(), "iat": now.Unix(), "email": i.Email, "given_name": i.FirstName}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	if i.PrincipalType != "" {
		claims["principal_type"], claims["principal_id"] = i.PrincipalType, i.PrincipalID
	}
	if i.TeamID != "" {
		claims["team_id"] = i.TeamID
	}
	signingKey := i.key
	if i.SignWithWrongKey {
		signingKey = i.other
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: signingKey}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID))
	if err != nil {
		panic(err)
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		panic(err)
	}
	return token
}

func (i *IdP) userinfo(w http.ResponseWriter, r *http.Request) {
	i.userInfoRequests.Add(1)
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer fake-access-token") {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if i.UserInfoStatus != 0 {
		w.WriteHeader(i.UserInfoStatus)
		return
	}
	subject := i.Subject
	if i.UserInfoSubject != "" {
		subject = i.UserInfoSubject
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"sub": subject, "email": i.Email, "given_name": i.FirstName})
}
