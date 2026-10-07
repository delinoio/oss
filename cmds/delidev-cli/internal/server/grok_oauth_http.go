// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/go-jose/go-jose/v4"
)

const (
	grokDiscovery      = subscription.GrokIssuer + "/.well-known/openid-configuration"
	grokAuthorization  = subscription.GrokIssuer + "/oauth2/authorize"
	grokTokenEndpoint  = subscription.GrokIssuer + "/oauth2/token"
	grokDeviceEndpoint = subscription.GrokIssuer + "/oauth2/device/code"
	grokUserInfo       = subscription.GrokIssuer + "/oauth2/userinfo"
	grokJWKS           = subscription.GrokIssuer + "/.well-known/jwks.json"
	grokScopes         = "openid profile email offline_access grok-cli:access api:access conversations:read conversations:write workspaces:read workspaces:write"
)

// This client has no configurable issuer, client, endpoint or ambient proxy.
// The transport seam is private and permits isolated TLS protocol fixtures.
type grokOAuthHTTP struct {
	route     outbound.Resolver
	transport http.RoundTripper
}

func grokOAuthProblem() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The original Grok authorization has an uncertain or invalid result.", "Retain the original operation and protected material. Do not resend an uncertain token exchange or refresh.")
}

func (c grokOAuthHTTP) client() (*http.Client, func(), error) {
	transport := c.transport
	close := func() {}
	if transport == nil {
		if c.route == nil {
			return nil, close, grokOAuthProblem()
		}
		base := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 20 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 20 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true, DisableCompression: true}
		transport = &outbound.Transport{Base: base, Resolve: c.route}
		close = base.CloseIdleConnections
	}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, close, nil
}

func (c grokOAuthHTTP) request(ctx context.Context, method, endpoint string, fields map[string][]byte, access []byte) (int, map[string]json.RawMessage, error) {
	if !(method == http.MethodGet && (endpoint == grokDiscovery || endpoint == grokJWKS || endpoint == grokUserInfo) || method == http.MethodPost && (endpoint == grokTokenEndpoint || endpoint == grokDeviceEndpoint)) || len(access) > 24<<10 || (endpoint == grokUserInfo) != (len(access) != 0) || method == http.MethodGet && len(fields) != 0 {
		return 0, nil, grokOAuthProblem()
	}
	client, close, err := c.client()
	if err != nil {
		return 0, nil, err
	}
	defer close()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	payload := oauthForm(fields)
	defer clear(payload)
	if len(payload) > 64<<10 {
		return 0, nil, grokOAuthProblem()
	}
	var body io.Reader
	if method == http.MethodPost {
		body = io.NopCloser(bytes.NewReader(payload))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return 0, nil, grokOAuthProblem()
	}
	// The opaque reader deliberately supplies no GetBody retry authority.
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Grok-Client-Version", subscription.GrokVersion)
	req.Header.Set("X-Grok-Client-Surface", "ui")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if len(access) != 0 {
		req.Header.Set("Authorization", "Bearer "+string(access))
	}
	response, err := client.Do(req)
	if err != nil {
		return 0, nil, grokOAuthProblem()
	}
	defer response.Body.Close()
	buffer := make([]byte, (64<<10)+1)
	defer clear(buffer)
	n, err := io.ReadFull(response.Body, buffer)
	if err != io.EOF && err != io.ErrUnexpectedEOF || n > 64<<10 {
		return 0, nil, grokOAuthProblem()
	}
	parsed, err := decodeOAuthSecretObject(buffer[:n], 0)
	if err != nil {
		return 0, nil, grokOAuthProblem()
	}
	return response.StatusCode, parsed, nil
}

func (c grokOAuthHTTP) verifyDiscovery(ctx context.Context) error {
	status, fields, err := c.request(ctx, http.MethodGet, grokDiscovery, nil, nil)
	if err != nil {
		return err
	}
	defer clearOAuthObject(fields)
	if status != http.StatusOK {
		return grokOAuthProblem()
	}
	for field, want := range map[string]string{"issuer": subscription.GrokIssuer, "authorization_endpoint": grokAuthorization, "token_endpoint": grokTokenEndpoint, "device_authorization_endpoint": grokDeviceEndpoint, "userinfo_endpoint": grokUserInfo, "jwks_uri": grokJWKS} {
		var actual string
		if json.Unmarshal(fields[field], &actual) != nil || actual != want {
			return grokOAuthProblem()
		}
	}
	var algorithms, challenges []string
	if json.Unmarshal(fields["id_token_signing_alg_values_supported"], &algorithms) != nil || len(algorithms) != 1 || algorithms[0] != "ES256" || json.Unmarshal(fields["code_challenge_methods_supported"], &challenges) != nil || len(challenges) != 1 || challenges[0] != "S256" {
		return grokOAuthProblem()
	}
	return nil
}

func grokCallback(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "/callback" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return false
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	return err == nil && port != 0 && strconv.FormatUint(port, 10) == u.Port() && u.Host == "127.0.0.1:"+u.Port()
}

func grokAuthorizeURL(callback string, verifier []byte, state, nonce string) (string, error) {
	if !grokCallback(callback) || len(verifier) != 43 || !subscriptionBrowserState.MatchString(state) || !subscriptionBrowserState.MatchString(nonce) {
		return "", grokOAuthProblem()
	}
	decoded, err := base64.RawURLEncoding.DecodeString(string(verifier))
	if err != nil || len(decoded) != 32 {
		return "", grokOAuthProblem()
	}
	clear(decoded)
	digest := sha256.Sum256(verifier)
	u, _ := url.Parse(grokAuthorization)
	u.RawQuery = url.Values{"response_type": {"code"}, "client_id": {subscription.GrokClientID}, "redirect_uri": {callback}, "scope": {grokScopes}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"}, "state": {state}, "nonce": {nonce}, "referrer": {"grok-build"}}.Encode()
	return u.String(), nil
}

type grokTokenResult struct {
	access, refresh, id []byte
	expires             time.Time
	identity            subscription.Identity
}

func (v *grokTokenResult) clear() { clear(v.access); clear(v.refresh); clear(v.id) }

// Called only after the caller's durable original send claim. This primitive
// never chooses another method, retries a mutation or publishes credentials.
func (c grokOAuthHTTP) token(ctx context.Context, fields map[string][]byte, nonce, originalUser string) (result grokTokenResult, returned error) {
	status, parsed, err := c.request(ctx, http.MethodPost, grokTokenEndpoint, fields, nil)
	if err != nil {
		return result, err
	}
	defer clearOAuthObject(parsed)
	return c.parseToken(ctx, status, parsed, nonce, originalUser)
}

func (c grokOAuthHTTP) parseToken(ctx context.Context, status int, parsed map[string]json.RawMessage, nonce, originalUser string) (result grokTokenResult, returned error) {
	defer func() {
		if returned != nil {
			result.clear()
		}
	}()
	if status != http.StatusOK {
		return result, grokOAuthProblem()
	}
	var kind string
	seconds, err := strconv.ParseInt(string(parsed["expires_in"]), 10, 32)
	if json.Unmarshal(parsed["token_type"], &kind) != nil || !strings.EqualFold(kind, "Bearer") || err != nil || seconds < 1 || seconds > 86400*365 {
		return result, grokOAuthProblem()
	}
	var granted string
	if json.Unmarshal(parsed["scope"], &granted) != nil {
		return result, grokOAuthProblem()
	}
	actual := strings.Fields(granted)
	for _, required := range strings.Fields(grokScopes) {
		found := false
		for _, scope := range actual {
			if scope == required {
				found = true
			}
		}
		if !found {
			return result, grokOAuthProblem()
		}
	}
	result.access, err = decodeOAuthASCIIKey(parsed["access_token"])
	if err == nil && (originalUser == "" || len(parsed["refresh_token"]) != 0) {
		result.refresh, err = decodeOAuthASCIIKey(parsed["refresh_token"])
	}
	if err == nil && len(parsed["id_token"]) != 0 {
		result.id, err = decodeOAuthASCIIKey(parsed["id_token"])
	}
	if err != nil || len(result.access) > 24<<10 || len(result.refresh) > 24<<10 || len(result.id) > 24<<10 {
		return result, grokOAuthProblem()
	}
	var identity subscription.Identity
	if len(result.id) != 0 {
		identity, err = c.verifyID(ctx, result.id, result.access, nonce)
	} else if originalUser != "" && nonce == "" {
		// Refresh responses may omit an ID token. Only an authenticated read at
		// the fixed issuer can prove the original identity; token hints cannot.
		identity, err = c.userIdentity(ctx, result.access)
	} else {
		err = grokOAuthProblem()
	}
	if err != nil || originalUser != "" && identity.User != originalUser {
		return result, grokOAuthProblem()
	}
	result.identity = identity
	result.expires = time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	return result, nil
}

func (c grokOAuthHTTP) verifyID(ctx context.Context, id, access []byte, nonce string) (subscription.Identity, error) {
	var identity subscription.Identity
	if len(id) == 0 || len(id) > 24<<10 {
		return identity, grokOAuthProblem()
	}
	// Reject duplicate JSON members before the signature library interprets them.
	parts := bytes.Split(id, []byte("."))
	if len(parts) != 3 {
		return identity, grokOAuthProblem()
	}
	var kid string
	for index, part := range parts[:2] {
		raw, err := base64.RawURLEncoding.DecodeString(string(part))
		if err != nil {
			return identity, grokOAuthProblem()
		}
		fields, err := decodeOAuthSecretObject(raw, 0)
		clear(raw)
		if err != nil {
			return identity, grokOAuthProblem()
		}
		if index == 0 && (json.Unmarshal(fields["kid"], &kid) != nil || len(kid) == 0 || len(kid) > 256 || domain.Text(kid, "key identity", 256, true) != nil) {
			clearOAuthObject(fields)
			return identity, grokOAuthProblem()
		}
		clearOAuthObject(fields)
	}
	// Fetch JWKS through the same bounded, duplicate-rejecting, unretried fixed
	// HTTP path. The generic remote key set otherwise owns an unbounded JSON read.
	status, fields, err := c.request(ctx, http.MethodGet, grokJWKS, nil, nil)
	if err != nil {
		return identity, err
	}
	defer clearOAuthObject(fields)
	var keys []jose.JSONWebKey
	if status != http.StatusOK || json.Unmarshal(fields["keys"], &keys) != nil || len(keys) == 0 || len(keys) > 64 {
		return identity, grokOAuthProblem()
	}
	keyset := &oidc.StaticKeySet{}
	seen := map[string]bool{}
	for _, key := range keys {
		public, ok := key.Key.(*ecdsa.PublicKey)
		if !ok || public.Curve != elliptic.P256() || key.Algorithm != "ES256" || key.Use != "sig" || !key.IsPublic() || key.KeyID == "" || seen[key.KeyID] {
			return identity, grokOAuthProblem()
		}
		seen[key.KeyID] = true
		if key.KeyID == kid {
			keyset.PublicKeys = []crypto.PublicKey{public}
		}
	}
	if len(keyset.PublicKeys) != 1 {
		return identity, grokOAuthProblem()
	}
	verifier := oidc.NewVerifier(subscription.GrokIssuer, keyset, &oidc.Config{ClientID: subscription.GrokClientID, SupportedSigningAlgs: []string{"ES256"}})
	token, err := verifier.Verify(ctx, string(id))
	if err != nil || token.Subject == "" || len(token.Subject) > 256 || token.IssuedAt.IsZero() || token.IssuedAt.After(time.Now().Add(time.Minute)) || !token.Expiry.After(token.IssuedAt) || nonce != "" && subtle.ConstantTimeCompare([]byte(token.Nonce), []byte(nonce)) != 1 || token.AccessTokenHash != "" && token.VerifyAccessToken(string(access)) != nil {
		return identity, grokOAuthProblem()
	}
	var claims struct {
		Email, Name     string
		Given           string `json:"given_name"`
		Family          string `json:"family_name"`
		AuthorizedParty string `json:"azp"`
	}
	if token.Claims(&claims) != nil || (len(token.Audience) > 1 || claims.AuthorizedParty != "") && claims.AuthorizedParty != subscription.GrokClientID {
		return identity, grokOAuthProblem()
	}
	for _, value := range []string{token.Subject, claims.Email, claims.Name, claims.Given, claims.Family} {
		if domain.Text(value, "verified identity", 1024, false) != nil {
			return identity, grokOAuthProblem()
		}
	}
	identity = subscription.Identity{Account: token.Subject, User: token.Subject, Service: domain.SubscriptionGrok, Issuer: subscription.GrokIssuer, PrincipalType: "User", PrincipalID: token.Subject, Email: claims.Email, DisplayName: claims.Name}
	if identity.DisplayName == "" {
		identity.DisplayName = strings.TrimSpace(claims.Given + " " + claims.Family)
	}
	return identity, nil
}
