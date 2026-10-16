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

	// Device grant knobs. DeviceScript answers successive device_code token
	// requests in order; once it is exhausted every request succeeds.
	DeviceScript            []DeviceStep
	DeviceInterval          int
	DeviceExpiresIn         int
	DeviceStatus            int
	DeviceUserCode          string
	DeviceVerificationURI   string
	DeviceVerificationFull  string
	DeviceOmitEndpoint      bool
	DeviceCodeValue         string
	DeviceIDTokenNonce      string
	DeviceTokenSuccessCount int
	// DeviceTokenBody replaces the successful device_code token body verbatim and
	// DeviceTokenStatus answers every device_code poll with that bare status.
	DeviceTokenBody   string
	DeviceTokenStatus int
	// Clock stamps DevicePollTimes; time.Now is used when nil.
	Clock interface{ Now() time.Time }
	// BeforeDeviceAuthorization runs inside the device authorization handler.
	BeforeDeviceAuthorization func()
	// BeforeDevicePoll runs inside the token handler for a device_code request.
	BeforeDevicePoll func(poll int)

	// Refresh grant knobs. A successful refresh issues a fresh access token and,
	// when RefreshRotate is set, a fresh refresh token that replaces the accepted
	// one (the old one is then answered with invalid_grant).
	RefreshRotate     bool
	RefreshOmitID     bool
	RefreshStatus     int
	RefreshError      string
	RefreshMalformed  bool
	RefreshTruncated  bool
	RefreshExpiresIn  int
	refreshCount      int
	issuedCredentials []string

	// Observations.
	// RefreshRequests counts refresh_token grant requests; RefreshForms keeps them.
	RefreshRequests  int
	RefreshForms     []url.Values
	TokenRequests    int
	TokenFormSeen    []url.Values
	TokenAuthHeaders []string
	// DeviceRequests counts device authorization requests; DeviceForms keeps them.
	DeviceRequests  int
	DeviceForms     []url.Values
	DevicePollForms []url.Values
	DevicePollTimes []time.Time
	DiscoveryCalls  int
	UserinfoCalls   int
	AccessToken     string
	RefreshToken    string
}

// DeviceStep is one scripted device_code token answer.
type DeviceStep string

const (
	DevicePending  DeviceStep = "authorization_pending"
	DeviceSlowDown DeviceStep = "slow_down"
	DeviceDenied   DeviceStep = "access_denied"
	DeviceExpired  DeviceStep = "expired_token"
	DeviceUnknown  DeviceStep = "invalid_request"
	DeviceFailure  DeviceStep = "server_error"
	DeviceSuccess  DeviceStep = "success"
)

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
		document := map[string]any{
			"issuer": Issuer, "authorization_endpoint": Issuer + "/oauth2/authorize", "token_endpoint": Issuer + "/oauth2/token",
			"jwks_uri": Issuer + "/.well-known/jwks.json", "userinfo_endpoint": Issuer + "/oauth2/userinfo",
			"response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post", "none"},
			"id_token_signing_alg_values_supported": []string{"ES256"},
			"grant_types_supported":                 []string{"authorization_code", "refresh_token", DeviceGrantType},
		}
		i.mu.Lock()
		omit := i.DeviceOmitEndpoint
		i.mu.Unlock()
		if !omit {
			document["device_authorization_endpoint"] = Issuer + "/oauth2/device/code"
		}
		writeJSON(w, http.StatusOK, document)
	case "/.well-known/jwks.json":
		writeJSON(w, http.StatusOK, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &i.key.PublicKey, KeyID: KeyID, Algorithm: "ES256", Use: "sig"}}})
	case "/oauth2/token":
		i.token(w, r)
	case "/oauth2/device/code":
		i.deviceAuthorization(w, r)
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
	if r.PostForm.Get("grant_type") == DeviceGrantType {
		i.devicePoll(w, r)
		return
	}
	if hook != nil {
		hook()
	}
	if r.PostForm.Get("grant_type") == "refresh_token" {
		i.refresh(w, r)
		return
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

// refresh answers the refresh_token grant. The token request counters were
// already incremented by the caller.
func (i *IdP) refresh(w http.ResponseWriter, r *http.Request) {
	i.mu.Lock()
	i.RefreshRequests++
	i.RefreshForms = append(i.RefreshForms, r.PostForm)
	status, failure := i.RefreshStatus, i.RefreshError
	malformed, truncated, rotate, omit := i.RefreshMalformed, i.RefreshTruncated, i.RefreshRotate, i.RefreshOmitID
	expires := i.RefreshExpiresIn
	accepted := i.RefreshToken
	i.mu.Unlock()
	if status != 0 {
		if failure == "" {
			failure = "fake_failure"
		}
		writeJSON(w, status, map[string]string{"error": failure})
		return
	}
	if r.Method != http.MethodPost || r.PostForm.Get("client_id") != ClientID || r.PostForm.Get("refresh_token") != accepted || r.PostForm.Has("client_secret") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	if malformed {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("this is not json"))
		return
	}
	if truncated {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fake-truncated-acc`))
		return
	}
	if expires == 0 {
		expires = 21600
	}
	i.mu.Lock()
	i.refreshCount++
	i.AccessToken = fmt.Sprintf("fake-access-token-r%d", i.refreshCount)
	i.issuedCredentials = append(i.issuedCredentials, i.AccessToken)
	answer := map[string]any{"access_token": i.AccessToken, "token_type": "Bearer", "expires_in": expires}
	if rotate {
		i.RefreshToken = fmt.Sprintf("fake-refresh-token-r%d", i.refreshCount)
		i.issuedCredentials = append(i.issuedCredentials, i.RefreshToken)
		answer["refresh_token"] = i.RefreshToken
	}
	i.mu.Unlock()
	if !omit {
		answer["id_token"] = i.idToken("")
	}
	writeJSON(w, http.StatusOK, answer)
}

// DeviceGrantType is the RFC 8628 token endpoint grant type.
const DeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

func (i *IdP) now() time.Time {
	if i.Clock != nil {
		return i.Clock.Now()
	}
	return time.Now()
}

func (i *IdP) deviceAuthorization(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	i.mu.Lock()
	i.DeviceRequests++
	i.DeviceForms = append(i.DeviceForms, r.PostForm)
	hook := i.BeforeDeviceAuthorization
	status, userCode, uri, full, expires, interval, code := i.DeviceStatus, i.DeviceUserCode, i.DeviceVerificationURI, i.DeviceVerificationFull, i.DeviceExpiresIn, i.DeviceInterval, i.DeviceCodeValue
	i.mu.Unlock()
	if hook != nil {
		hook()
	}
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": "fake_failure"})
		return
	}
	if r.Method != http.MethodPost || r.PostForm.Get("client_id") != ClientID || r.PostForm.Get("scope") == "" || r.PostForm.Has("client_secret") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if userCode == "" {
		userCode = "ABCD-1234"
	}
	if uri == "" {
		uri = "https://accounts.x.ai/oauth2/device"
	}
	if code == "" {
		code = "fake-device-code-1"
	}
	if expires == 0 {
		expires = 900
	}
	document := map[string]any{"device_code": code, "user_code": userCode, "verification_uri": uri, "expires_in": expires}
	if interval != 0 {
		document["interval"] = interval
	}
	if full != "" {
		document["verification_uri_complete"] = full
	}
	writeJSON(w, http.StatusOK, document)
}

func (i *IdP) devicePoll(w http.ResponseWriter, r *http.Request) {
	i.mu.Lock()
	i.DevicePollForms = append(i.DevicePollForms, r.PostForm)
	i.DevicePollTimes = append(i.DevicePollTimes, i.now())
	poll := len(i.DevicePollForms)
	hook, code := i.BeforeDevicePoll, i.DeviceCodeValue
	step := DeviceSuccess
	if poll <= len(i.DeviceScript) {
		step = i.DeviceScript[poll-1]
	}
	i.mu.Unlock()
	if hook != nil {
		hook(poll)
	}
	if code == "" {
		code = "fake-device-code-1"
	}
	if r.Method != http.MethodPost || r.PostForm.Get("client_id") != ClientID || r.PostForm.Get("device_code") != code || r.PostForm.Has("client_secret") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	if i.DeviceTokenStatus != 0 {
		w.WriteHeader(i.DeviceTokenStatus)
		return
	}
	switch step {
	case DeviceSuccess:
		i.mu.Lock()
		i.DeviceTokenSuccessCount++
		nonce := i.DeviceIDTokenNonce
		i.mu.Unlock()
		if i.DeviceTokenBody != "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(i.DeviceTokenBody))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"access_token": i.AccessToken, "token_type": "Bearer", "expires_in": i.ExpiresIn, "refresh_token": i.RefreshToken, "id_token": i.idToken(nonce)})
	case DeviceFailure:
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": string(step)})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": string(step)})
	}
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
	claims := map[string]any{"iss": issuer, "sub": i.Subject, "aud": aud, "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(i.IDTokenLifetime).Unix(), "email": i.Email}
	if nonce != "" {
		claims["nonce"] = nonce
	}
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
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, secret := range append([]string{i.AccessToken, i.RefreshToken}, i.issuedCredentials...) {
		if strings.Contains(text, secret) {
			return false
		}
	}
	return true
}
