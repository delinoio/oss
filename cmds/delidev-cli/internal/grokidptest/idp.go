// SPDX-License-Identifier: Apache-2.0
// Package grokidptest provides an in-process fake xAI authorization server
// (OIDC discovery, authorize, token, JWKS and userinfo) for tests. It is never a
// runtime dependency; only test code imports it. Requests are served through an
// http.RoundTripper that answers for the real issuer host, so the issuer string
// in every document is the production value and nothing touches the network.
package grokidptest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const (
	Issuer   = "https://auth.x.ai"
	ClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	KeyID    = "fake-es256-key"
)

// IdP is the fake authorization server. Fields marked "knob" may be changed
// before the matching request to inject one failure.
type IdP struct {
	mu    sync.Mutex
	key   *ecdsa.PrivateKey
	rogue *ecdsa.PrivateKey
	codes map[string]issued
	next  int

	// Knobs for ID-token and endpoint failures.
	SignWithOtherKey bool
	IDTokenIssuer    string
	IDTokenAudience  string
	// IDTokenAudiences replaces the single audience with a list, and
	// IDTokenAuthorizedParty adds an azp claim.
	IDTokenAudiences       []string
	IDTokenAuthorizedParty string
	// TokenBody replaces the successful token response body verbatim, after the
	// code was consumed, so a truncated or malformed success can be injected.
	TokenBody       string
	NonceOverride   string
	IDTokenLifetime time.Duration
	TokenStatus     int
	ExpiresIn       int
	UserinfoStatus  int
	Subject         string
	Email           string
	PrincipalType   string
	PrincipalID     string
	TeamID          string
	UserinfoSubject string
	// BeforeToken runs inside the token handler before it answers.
	BeforeToken func()

	// Observations.
	TokenRequests    int
	TokenFormSeen    []url.Values
	TokenAuthHeaders []string
	DiscoveryCalls   int
	UserinfoCalls    int
	AccessToken      string
	RefreshToken     string
}

type issued struct {
	challenge, nonce, redirect, clientID string
	used                                 bool
}

// New returns a fake server with a fresh signing key and default identity.
func New() *IdP {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rogue, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	return &IdP{key: key, rogue: rogue, codes: map[string]issued{}, IDTokenLifetime: time.Hour, ExpiresIn: 21600,
		Subject: "fake-user-1", Email: "person@example.test", PrincipalType: "user", PrincipalID: "fake-principal-1", TeamID: "fake-team-1",
		AccessToken: "fake-access-token-1", RefreshToken: "fake-refresh-token-1"}
}

// Transport answers for the fake issuer host and refuses every other host.
func (i *IdP) Transport() http.RoundTripper {
	return roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "auth.x.ai" {
			return nil, errors.New("fake idp: foreign host")
		}
		// The handler runs apart so that a canceled request returns at once, as a
		// real network transport does, while a blocked handler keeps running.
		answer := make(chan *http.Response, 1)
		go func() {
			recorder := httptest.NewRecorder()
			i.ServeHTTP(recorder, r.Clone(context.WithoutCancel(r.Context())))
			answer <- recorder.Result()
		}()
		select {
		case response := <-answer:
			return response, nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
}

// Client returns an HTTP client bound to Transport.
func (i *IdP) Client() *http.Client { return &http.Client{Transport: i.Transport()} }

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Authorize plays the browser: it validates the authorization URL the way the
// real server would and returns the redirect URL carrying a fresh code. Only the
// observed parameters are accepted.
func (i *IdP) Authorize(authorizationURL string) (string, error) {
	u, err := url.Parse(authorizationURL)
	if err != nil || u.Scheme != "https" || u.Host != "auth.x.ai" || u.Path != "/oauth2/authorize" {
		return "", errors.New("fake idp: bad authorization endpoint")
	}
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != ClientID || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("nonce") == "" || q.Get("referrer") != "grok-build" {
		return "", errors.New("fake idp: bad authorization request")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.next++
	code := fmt.Sprintf("fake-code-%d", i.next)
	i.codes[code] = issued{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), redirect: q.Get("redirect_uri"), clientID: q.Get("client_id")}
	return q.Get("redirect_uri") + "?code=" + code + "&state=" + url.QueryEscape(q.Get("state")), nil
}

// Code extracts the code from a callback URL returned by Authorize.
func Code(callback string) string {
	u, _ := url.Parse(callback)
	return u.Query().Get("code")
}

func (i *IdP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		i.mu.Lock()
		i.DiscoveryCalls++
		i.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer": Issuer, "authorization_endpoint": Issuer + "/oauth2/authorize", "token_endpoint": Issuer + "/oauth2/token",
			"jwks_uri": Issuer + "/.well-known/jwks.json", "userinfo_endpoint": Issuer + "/oauth2/userinfo",
			"response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post", "none"},
			"id_token_signing_alg_values_supported": []string{"ES256"},
		})
	case "/.well-known/jwks.json":
		writeJSON(w, http.StatusOK, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &i.key.PublicKey, KeyID: KeyID, Algorithm: "ES256", Use: "sig"}}})
	case "/oauth2/token":
		i.token(w, r)
	case "/oauth2/userinfo":
		i.mu.Lock()
		i.UserinfoCalls++
		status, subject := i.UserinfoStatus, i.UserinfoSubject
		i.mu.Unlock()
		if status != 0 {
			writeJSON(w, status, map[string]string{"error": "fake"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+i.AccessToken {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
			return
		}
		if subject == "" {
			subject = i.Subject
		}
		writeJSON(w, http.StatusOK, map[string]string{"sub": subject, "email": i.Email, "given_name": "Fake", "principal_type": i.PrincipalType, "principal_id": i.PrincipalID, "team_id": i.TeamID})
	default:
		http.NotFound(w, r)
	}
}

func (i *IdP) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	i.mu.Lock()
	i.TokenRequests++
	i.TokenFormSeen = append(i.TokenFormSeen, r.PostForm)
	i.TokenAuthHeaders = append(i.TokenAuthHeaders, r.Header.Get("Authorization"))
	hook, status := i.BeforeToken, i.TokenStatus
	i.mu.Unlock()
	if hook != nil {
		hook()
	}
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": "fake_failure"})
		return
	}
	i.mu.Lock()
	code := r.PostForm.Get("code")
	entry, ok := i.codes[code]
	if ok {
		i.codes[code] = issued{challenge: entry.challenge, nonce: entry.nonce, redirect: entry.redirect, clientID: entry.clientID, used: true}
	}
	i.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if r.Method != http.MethodPost || !ok || entry.used || r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("client_id") != ClientID ||
		r.PostForm.Get("redirect_uri") != entry.redirect || base64.RawURLEncoding.EncodeToString(sum[:]) != entry.challenge || r.PostForm.Has("client_secret") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	if i.TokenBody != "" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(i.TokenBody))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": i.AccessToken, "token_type": "Bearer", "expires_in": i.ExpiresIn, "refresh_token": i.RefreshToken, "id_token": i.idToken(entry.nonce)})
}

func (i *IdP) idToken(nonce string) string {
	issuer, audience := i.IDTokenIssuer, i.IDTokenAudience
	if issuer == "" {
		issuer = Issuer
	}
	if audience == "" {
		audience = ClientID
	}
	if i.NonceOverride != "" {
		nonce = i.NonceOverride
	}
	signer := i.key
	if i.SignWithOtherKey {
		signer = i.rogue
	}
	now := time.Now()
	var aud any = audience
	if len(i.IDTokenAudiences) > 0 {
		aud = i.IDTokenAudiences
	}
	claims := map[string]any{"iss": issuer, "sub": i.Subject, "aud": aud, "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(i.IDTokenLifetime).Unix(), "nonce": nonce, "email": i.Email}
	if i.IDTokenAuthorizedParty != "" {
		claims["azp"] = i.IDTokenAuthorizedParty
	}
	payload, _ := json.Marshal(claims)
	options := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", KeyID)
	sig, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: signer}, options)
	object, _ := sig.Sign(payload)
	compact, _ := object.CompactSerialize()
	return compact
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// Redacted reports whether text is free of every secret the fake issued.
func (i *IdP) Redacted(text string) bool {
	for _, secret := range []string{i.AccessToken, i.RefreshToken} {
		if strings.Contains(text, secret) {
			return false
		}
	}
	return true
}
