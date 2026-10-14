// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// The fixed official xAI OAuth profile for the public Grok Build client. Every
// value was observed from the real native CLI; the issuer and client identifier
// are shared with the credential bundle (GrokIssuer, GrokClientID).
const (
	// GrokReferrer is the fixed `referrer` query value the native CLI sends.
	GrokReferrer = "grok-build"
	// GrokCallbackHost is the only host a browser login redirects to.
	GrokCallbackHost = "127.0.0.1"
	// GrokCallbackPath is the exact loopback callback path.
	GrokCallbackPath = "/callback"
	// GrokMinCallbackPort keeps the loopback redirect unprivileged.
	GrokMinCallbackPort = 1024
	// GrokOpaqueValueMax bounds state and nonce, matching the trusted desktop
	// receiver's validator.
	GrokOpaqueValueMax = 512

	grokDiscoveryPath     = "/.well-known/openid-configuration"
	grokAuthorizationHost = "auth.x.ai"
	grokChallengeMethod   = "S256"
	grokResponseType      = "code"
	grokAuthMethodNone    = "none"
	grokIDTokenAlgorithm  = "ES256"
	maxGrokOAuthResponse  = 64 << 10
	grokOAuthTimeout      = 20 * time.Second
	grokClockSkew         = time.Minute
	grokRandomBytes       = 32
)

// grokScopes are the exact scopes the native CLI requests, in its order.
var grokScopes = [...]string{
	"openid", "profile", "email", "offline_access", "grok-cli:access", "api:access",
	"conversations:read", "conversations:write", "workspaces:read", "workspaces:write",
}

// GrokScope returns the space-separated scope list of the fixed profile.
func GrokScope() string { return strings.Join(grokScopes[:], " ") }

// GrokOAuthReason is the closed, value-free classification of a failed Grok
// OAuth step. It never carries a URL, code, verifier, token or claim.
type GrokOAuthReason string

const (
	GrokReasonDiscovery      GrokOAuthReason = "discovery"
	GrokReasonRandom         GrokOAuthReason = "random"
	GrokReasonDenied         GrokOAuthReason = "access-denied"
	GrokReasonTokenRejected  GrokOAuthReason = "token-rejected"
	GrokReasonTokenUncertain GrokOAuthReason = "token-uncertain"
	GrokReasonTokenResponse  GrokOAuthReason = "token-response"
	GrokReasonIDToken        GrokOAuthReason = "id-token"
	GrokReasonKeys           GrokOAuthReason = "jwks"
	GrokReasonUserInfo       GrokOAuthReason = "userinfo"
	GrokReasonMapping        GrokOAuthReason = "mapping"
	GrokReasonIdentity       GrokOAuthReason = "identity"
)

// GrokOAuthError reports one failed step by closed reason only.
type GrokOAuthError struct{ Reason GrokOAuthReason }

func (e *GrokOAuthError) Error() string { return "grok oauth failure: " + string(e.Reason) }

func grokFailure(reason GrokOAuthReason) error { return &GrokOAuthError{Reason: reason} }

// GrokEndpoints are the OIDC endpoints read from discovery and validated
// against the fixed issuer.
type GrokEndpoints struct {
	Authorization string
	Token         string
	UserInfo      string
	JWKS          string
}

// GrokOAuthClient performs the owned outbound calls of the Grok profile. The
// transport is supplied by the caller (production: the server's explicit
// outbound route) and the client never follows a redirect or reuses a
// connection.
type GrokOAuthClient struct {
	Transport http.RoundTripper
	// Now overrides the clock in tests.
	Now func() time.Time
}

func (c GrokOAuthClient) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func (c GrokOAuthClient) do(ctx context.Context, method, target string, body []byte, bearer string) (int, []byte, error) {
	if c.Transport == nil {
		return 0, nil, grokFailure(GrokReasonTokenUncertain)
	}
	ctx, cancel := context.WithTimeout(ctx, grokOAuthTimeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		// An opaque reader supplies no GetBody retry authority.
		reader = io.NopCloser(bytes.NewReader(body))
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, grokFailure(GrokReasonTokenUncertain)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	client := &http.Client{Transport: c.Transport, Timeout: grokOAuthTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, grokFailure(GrokReasonTokenUncertain)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxGrokOAuthResponse+1))
	if err != nil || len(raw) > maxGrokOAuthResponse {
		clear(raw)
		return 0, nil, grokFailure(GrokReasonTokenUncertain)
	}
	return response.StatusCode, raw, nil
}

// grokEndpointURL accepts only an https URL on the issuer host, without
// credentials, query or fragment.
func grokEndpointURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != grokAuthorizationHost || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path == "" || u.Path == "/" || len(raw) > 2048 {
		return "", false
	}
	return u.String(), true
}

func grokListContains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// Discover reads the OIDC metadata of the fixed issuer and validates it. The
// issuer must match exactly, every endpoint must live on the issuer origin, and
// the metadata must not contradict the S256, public-client and ES256 profile.
func (c GrokOAuthClient) Discover(ctx context.Context) (GrokEndpoints, error) {
	status, raw, err := c.do(ctx, http.MethodGet, GrokIssuer+grokDiscoveryPath, nil, "")
	defer clear(raw)
	if err != nil || status != http.StatusOK {
		return GrokEndpoints{}, grokFailure(GrokReasonDiscovery)
	}
	var doc struct {
		Issuer        string   `json:"issuer"`
		Authorization string   `json:"authorization_endpoint"`
		Token         string   `json:"token_endpoint"`
		UserInfo      string   `json:"userinfo_endpoint"`
		JWKS          string   `json:"jwks_uri"`
		ResponseTypes []string `json:"response_types_supported"`
		Challenges    []string `json:"code_challenge_methods_supported"`
		AuthMethods   []string `json:"token_endpoint_auth_methods_supported"`
		Algorithms    []string `json:"id_token_signing_alg_values_supported"`
	}
	if json.Unmarshal(raw, &doc) != nil || doc.Issuer != GrokIssuer {
		return GrokEndpoints{}, grokFailure(GrokReasonDiscovery)
	}
	// Absent capability lists are not a contradiction, but a present list must
	// admit the fixed profile. S256 is the only challenge method ever sent.
	if doc.Challenges != nil && !grokListContains(doc.Challenges, grokChallengeMethod) ||
		doc.AuthMethods != nil && !grokListContains(doc.AuthMethods, grokAuthMethodNone) ||
		doc.Algorithms != nil && !grokListContains(doc.Algorithms, grokIDTokenAlgorithm) ||
		doc.ResponseTypes != nil && !grokListContains(doc.ResponseTypes, grokResponseType) {
		return GrokEndpoints{}, grokFailure(GrokReasonDiscovery)
	}
	var endpoints GrokEndpoints
	var ok [4]bool
	endpoints.Authorization, ok[0] = grokEndpointURL(doc.Authorization)
	endpoints.Token, ok[1] = grokEndpointURL(doc.Token)
	endpoints.UserInfo, ok[2] = grokEndpointURL(doc.UserInfo)
	endpoints.JWKS, ok[3] = grokEndpointURL(doc.JWKS)
	for _, valid := range ok {
		if !valid {
			return GrokEndpoints{}, grokFailure(GrokReasonDiscovery)
		}
	}
	return endpoints, nil
}

// GrokAuthorization is the per-login secret material: the PKCE verifier and
// the opaque state and nonce. Clear it once the login settles.
type GrokAuthorization struct {
	Verifier  []byte
	Challenge string
	State     string
	Nonce     string
}

// Clear drops the verifier bytes.
func (a *GrokAuthorization) Clear() {
	clear(a.Verifier)
	*a = GrokAuthorization{}
}

// String and the other formatters never reveal the secret values.
func (GrokAuthorization) String() string { return "grok-authorization{redacted}" }
func (a GrokAuthorization) GoString() string {
	return a.String()
}

// NewGrokAuthorization draws a PKCE verifier, state and nonce from random
// (crypto/rand when nil). Each is 43 characters of unpadded base64url, inside
// the [A-Za-z0-9-_.~] alphabet.
func NewGrokAuthorization(random io.Reader) (GrokAuthorization, error) {
	if random == nil {
		random = rand.Reader
	}
	draw := func() ([]byte, error) {
		raw := make([]byte, grokRandomBytes)
		defer clear(raw)
		if _, err := io.ReadFull(random, raw); err != nil {
			return nil, grokFailure(GrokReasonRandom)
		}
		encoded := make([]byte, base64.RawURLEncoding.EncodedLen(len(raw)))
		base64.RawURLEncoding.Encode(encoded, raw)
		return encoded, nil
	}
	verifier, err := draw()
	if err != nil {
		return GrokAuthorization{}, err
	}
	state, err := draw()
	if err != nil {
		clear(verifier)
		return GrokAuthorization{}, err
	}
	nonce, err := draw()
	if err != nil {
		clear(verifier)
		clear(state)
		return GrokAuthorization{}, err
	}
	sum := sha256.Sum256(verifier)
	a := GrokAuthorization{Verifier: verifier, Challenge: base64.RawURLEncoding.EncodeToString(sum[:]), State: string(state), Nonce: string(nonce)}
	clear(state)
	clear(nonce)
	return a, nil
}

// GrokOpaqueValue reports whether value is a valid state or nonce.
func GrokOpaqueValue(value string) bool {
	if len(value) < 1 || len(value) > GrokOpaqueValueMax {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b == '.' || b == '~') {
			return false
		}
	}
	return true
}

func grokQueryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

// GrokRedirectURI is the exact loopback redirect of one login.
func GrokRedirectURI(port int) string {
	return "http://" + GrokCallbackHost + ":" + strconv.Itoa(port) + GrokCallbackPath
}

// AuthorizationURL builds the browser URL in the native CLI's key order: the
// fixed OAuth keys plus scope and referrer, and nothing else.
func (e GrokEndpoints) AuthorizationURL(redirectURI string, a GrokAuthorization) string {
	pairs := [][2]string{
		{"response_type", grokResponseType},
		{"client_id", GrokClientID},
		{"redirect_uri", redirectURI},
		{"scope", GrokScope()},
		{"code_challenge", a.Challenge},
		{"code_challenge_method", grokChallengeMethod},
		{"state", a.State},
		{"nonce", a.Nonce},
		{"referrer", GrokReferrer},
	}
	var b strings.Builder
	b.WriteString(e.Authorization)
	for i, pair := range pairs {
		if i == 0 {
			b.WriteByte('?')
		} else {
			b.WriteByte('&')
		}
		b.WriteString(pair[0])
		b.WriteByte('=')
		b.WriteString(grokQueryEscape(pair[1]))
	}
	return b.String()
}

// GrokAuthorizationDetails validates a server-produced browser URL against the
// fixed profile and returns its state and loopback port. It mirrors the trusted
// desktop receiver's validator so the server never publishes a URL the receiver
// would refuse.
func GrokAuthorizationDetails(raw string) (state string, port int, ok bool) {
	if len(raw) > 4096 {
		return "", 0, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != grokAuthorizationHost || u.User != nil || u.Fragment != "" || u.Path != "/oauth2/authorize" {
		return "", 0, false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", 0, false
	}
	allowed := map[string]bool{"response_type": true, "client_id": true, "redirect_uri": true, "scope": true, "code_challenge": true, "code_challenge_method": true, "state": true, "nonce": true, "referrer": true}
	for key, values := range query {
		if !allowed[key] || len(values) != 1 {
			return "", 0, false
		}
	}
	redirect := query.Get("redirect_uri")
	digits := strings.TrimSuffix(strings.TrimPrefix(redirect, "http://"+GrokCallbackHost+":"), GrokCallbackPath)
	if digits == redirect || len(digits) < 1 || len(digits) > 5 || digits[0] == '0' {
		return "", 0, false
	}
	port = 0
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return "", 0, false
		}
		port = port*10 + int(digits[i]-'0')
	}
	challenge := query.Get("code_challenge")
	if port < GrokMinCallbackPort || port > 65535 || query.Get("response_type") != grokResponseType || query.Get("client_id") != GrokClientID || query.Get("code_challenge_method") != grokChallengeMethod ||
		len(challenge) != 43 || !GrokOpaqueValue(challenge) || strings.ContainsAny(challenge, ".~") ||
		!GrokOpaqueValue(query.Get("state")) || !GrokOpaqueValue(query.Get("nonce")) || query.Get("referrer") != GrokReferrer || query.Get("scope") != GrokScope() {
		return "", 0, false
	}
	return query.Get("state"), port, true
}

// grokForm percent-encodes secret values directly into owned bytes so the code
// and verifier never become immutable strings. The field order is fixed.
func grokForm(fields [][2][]byte) []byte {
	var payload []byte
	const digits = "0123456789ABCDEF"
	for i, field := range fields {
		if i > 0 {
			payload = append(payload, '&')
		}
		payload = append(payload, field[0]...)
		payload = append(payload, '=')
		for _, b := range field[1] {
			if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b == '.' || b == '~' {
				payload = append(payload, b)
			} else {
				payload = append(payload, '%', digits[b>>4], digits[b&15])
			}
		}
	}
	return payload
}

// GrokTokens is the successful token endpoint result. Callers drop it once the
// bundle is built.
type GrokTokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresIn    time.Duration
}

// String and the other formatters never reveal a token.
func (GrokTokens) String() string     { return "grok-tokens{redacted}" }
func (t GrokTokens) GoString() string { return t.String() }
func (t GrokTokens) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(t.String()))
}

// grokTokenBody is the JSON body of a token endpoint response. Every member is
// optional on the wire; the callers decide which ones their grant requires.
type grokTokenBody struct {
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"refresh_token"`
	IDToken      string      `json:"id_token"`
	TokenType    string      `json:"token_type"`
	ExpiresIn    json.Number `json:"expires_in"`
	Error        string      `json:"error"`
}

// grokStrictObject reports whether raw is exactly one JSON object whose
// top-level members are unique and that is followed by nothing but whitespace.
// Member names are compared case-insensitively when fold is set (as
// encoding/json matches struct fields) and exactly otherwise (a claim set).
// encoding/json keeps the last of duplicate members, and implementations
// disagree about duplicates, so a body that repeats a member is ambiguous and is
// never trusted.
func grokStrictObject(raw []byte, fold bool) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if open, err := decoder.Token(); err != nil || open != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		name, isString := key.(string)
		if err != nil || !isString {
			return false
		}
		if fold {
			name = strings.ToLower(name)
		}
		if seen[name] {
			return false
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

// parseGrokTokenBody decodes a token endpoint body. It reports true only when
// the body is exactly one JSON object with unique members: another JSON value,
// any other trailing byte or a repeated member makes the whole response
// unparsed, whatever its status. A claimed grant must treat such a body as
// uncertain, never as a published success and never as a definitive rejection
// that would release the claim.
func parseGrokTokenBody(raw []byte) (grokTokenBody, bool) {
	if !grokStrictObject(raw, true) {
		return grokTokenBody{}, false
	}
	var body grokTokenBody
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&body) != nil {
		return grokTokenBody{}, false
	}
	return body, true
}

// ExchangeCode redeems one authorization code at the token endpoint as a
// public client with the PKCE verifier. It sends exactly one request and never
// retries. A definitive provider rejection is GrokReasonTokenRejected; every
// transport failure, cancellation, unreadable body or unexpected status is
// GrokReasonTokenUncertain, because the code may have been redeemed.
func (c GrokOAuthClient) ExchangeCode(ctx context.Context, endpoints GrokEndpoints, code, verifier []byte, redirectURI string) (GrokTokens, error) {
	payload := grokForm([][2][]byte{
		{[]byte("grant_type"), []byte("authorization_code")},
		{[]byte("client_id"), []byte(GrokClientID)},
		{[]byte("redirect_uri"), []byte(redirectURI)},
		{[]byte("code"), code},
		{[]byte("code_verifier"), verifier},
	})
	defer clear(payload)
	status, raw, err := c.do(ctx, http.MethodPost, endpoints.Token, payload, "")
	defer clear(raw)
	if err != nil {
		return GrokTokens{}, err
	}
	body, parsed := parseGrokTokenBody(raw)
	if status != http.StatusOK {
		if parsed && (status == http.StatusBadRequest || status == http.StatusUnauthorized) && body.Error != "" {
			return GrokTokens{}, grokFailure(GrokReasonTokenRejected)
		}
		return GrokTokens{}, grokFailure(GrokReasonTokenUncertain)
	}
	seconds, numErr := body.ExpiresIn.Int64()
	if !parsed || numErr != nil || seconds <= 0 || seconds > 86400*365 || !strings.EqualFold(body.TokenType, "bearer") ||
		!grokSecret(body.AccessToken) || !grokSecret(body.RefreshToken) || !grokSecret(body.IDToken) {
		return GrokTokens{}, grokFailure(GrokReasonTokenResponse)
	}
	return GrokTokens{AccessToken: body.AccessToken, RefreshToken: body.RefreshToken, IDToken: body.IDToken, ExpiresIn: time.Duration(seconds) * time.Second}, nil
}

// GrokClaims are the verified ID-token claims the profile uses.
type GrokClaims struct {
	Subject string
	fields  map[string]json.RawMessage
}

func (c GrokClaims) String() string { return "grok-claims{redacted}" }
func (c GrokClaims) GoString() string {
	return c.String()
}
func (c GrokClaims) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(c.String())) }

func grokClaimString(fields map[string]json.RawMessage, name string) string {
	var value string
	if raw, ok := fields[name]; ok && json.Unmarshal(raw, &value) == nil {
		return value
	}
	return ""
}

func grokClaimBool(fields map[string]json.RawMessage, name string) (bool, bool) {
	var value bool
	if raw, ok := fields[name]; ok && json.Unmarshal(raw, &value) == nil {
		return value, true
	}
	return false, false
}

func grokClaimTime(fields map[string]json.RawMessage, name string) (time.Time, bool) {
	raw, ok := fields[name]
	if !ok {
		return time.Time{}, false
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&number) != nil {
		return time.Time{}, false
	}
	seconds, err := number.Float64()
	if err != nil || seconds < 0 || seconds > 253402300799 {
		return time.Time{}, false
	}
	return time.Unix(int64(seconds), 0).UTC(), true
}

func (c GrokOAuthClient) keys(ctx context.Context, endpoints GrokEndpoints) (jose.JSONWebKeySet, error) {
	status, raw, err := c.do(ctx, http.MethodGet, endpoints.JWKS, nil, "")
	defer clear(raw)
	var set jose.JSONWebKeySet
	if err != nil || status != http.StatusOK || json.Unmarshal(raw, &set) != nil || len(set.Keys) == 0 {
		return jose.JSONWebKeySet{}, grokFailure(GrokReasonKeys)
	}
	return set, nil
}

// ValidateIDToken verifies an ES256 signature with the issuer's JWKS and then
// the issuer, audience, nonce and time claims. Nothing but ES256 is accepted.
func (c GrokOAuthClient) ValidateIDToken(ctx context.Context, endpoints GrokEndpoints, token, nonce string) (GrokClaims, error) {
	return c.validateIDToken(ctx, endpoints, token, nonce, true)
}

// validateIDToken is ValidateIDToken with an optional nonce check. A refresh
// response is not tied to an authorization request, so it carries no nonce to
// compare; every other check is identical.
func (c GrokOAuthClient) validateIDToken(ctx context.Context, endpoints GrokEndpoints, token, nonce string, checkNonce bool) (GrokClaims, error) {
	invalid := grokFailure(GrokReasonIDToken)
	if len(token) > maxGrokSecretField {
		return GrokClaims{}, invalid
	}
	signed, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil || len(signed.Signatures) != 1 {
		return GrokClaims{}, invalid
	}
	kid := signed.Signatures[0].Header.KeyID
	if kid == "" {
		return GrokClaims{}, invalid
	}
	set, err := c.keys(ctx, endpoints)
	if err != nil {
		return GrokClaims{}, err
	}
	var payload []byte
	for _, key := range set.Key(kid) {
		public, isECDSA := key.Key.(*ecdsa.PublicKey)
		if !key.Valid() || !key.IsPublic() || !isECDSA || public.Curve != elliptic.P256() || key.Use != "" && key.Use != "sig" || key.Algorithm != "" && key.Algorithm != grokIDTokenAlgorithm {
			continue
		}
		if verified, verifyErr := signed.Verify(public); verifyErr == nil {
			payload = verified
			break
		}
	}
	if payload == nil {
		return GrokClaims{}, invalid
	}
	defer clear(payload)
	var fields map[string]json.RawMessage
	if !grokStrictObject(payload, false) || json.Unmarshal(payload, &fields) != nil {
		return GrokClaims{}, invalid
	}
	now := c.now()
	expiry, hasExpiry := grokClaimTime(fields, "exp")
	if grokClaimString(fields, "iss") != GrokIssuer || !hasExpiry || !now.Before(expiry.Add(grokClockSkew)) {
		return GrokClaims{}, invalid
	}
	if issued, ok := grokClaimTime(fields, "iat"); ok && issued.After(now.Add(grokClockSkew)) {
		return GrokClaims{}, invalid
	}
	if _, present := fields["nbf"]; present {
		if notBefore, ok := grokClaimTime(fields, "nbf"); !ok || notBefore.After(now.Add(grokClockSkew)) {
			return GrokClaims{}, invalid
		}
	}
	if !grokAudience(fields) {
		return GrokClaims{}, invalid
	}
	if got := grokClaimString(fields, "nonce"); checkNonce && (nonce == "" || subtle.ConstantTimeCompare([]byte(got), []byte(nonce)) != 1) {
		return GrokClaims{}, invalid
	}
	subject := grokClaimString(fields, "sub")
	if !grokControlFree(subject, maxGrokIdentityField, true) {
		return GrokClaims{}, invalid
	}
	return GrokClaims{Subject: subject, fields: fields}, nil
}

// grokAudience requires the fixed client as the audience; a multi-valued
// audience must also name it as the authorized party.
func grokAudience(fields map[string]json.RawMessage) bool {
	raw, ok := fields["aud"]
	if !ok {
		return false
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return single == GrokClientID
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil || !grokListContains(many, GrokClientID) {
		return false
	}
	return len(many) == 1 || grokClaimString(fields, "azp") == GrokClientID
}

// GrokUserInfo is the userinfo response; its claims are never persisted
// except through the mapped bundle fields.
type GrokUserInfo struct{ fields map[string]json.RawMessage }

func (GrokUserInfo) String() string     { return "grok-userinfo{redacted}" }
func (u GrokUserInfo) GoString() string { return u.String() }
func (u GrokUserInfo) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(u.String()))
}

// UserInfo reads the userinfo endpoint with the access token. It sends one
// idempotent GET and requires the subject to equal the verified ID token's.
func (c GrokOAuthClient) UserInfo(ctx context.Context, endpoints GrokEndpoints, accessToken string, claims GrokClaims) (GrokUserInfo, error) {
	info, _, err := c.userInfo(ctx, endpoints, accessToken, claims, false)
	return info, err
}

// userInfo reads the userinfo endpoint. With requireSubject the response must
// itself name a valid subject (a refresh response without an ID token has no
// other source) and that subject is returned.
func (c GrokOAuthClient) userInfo(ctx context.Context, endpoints GrokEndpoints, accessToken string, claims GrokClaims, requireSubject bool) (GrokUserInfo, string, error) {
	status, raw, err := c.do(ctx, http.MethodGet, endpoints.UserInfo, nil, accessToken)
	defer clear(raw)
	if err != nil || status != http.StatusOK {
		return GrokUserInfo{}, "", grokFailure(GrokReasonUserInfo)
	}
	var fields map[string]json.RawMessage
	if !grokStrictObject(raw, false) || json.Unmarshal(raw, &fields) != nil {
		return GrokUserInfo{}, "", grokFailure(GrokReasonUserInfo)
	}
	subject := grokClaimString(fields, "sub")
	if requireSubject && !grokControlFree(subject, maxGrokIdentityField, true) {
		return GrokUserInfo{}, "", grokFailure(GrokReasonUserInfo)
	}
	if subject != "" && claims.Subject != "" && subject != claims.Subject {
		return GrokUserInfo{}, "", grokFailure(GrokReasonUserInfo)
	}
	return GrokUserInfo{fields: fields}, subject, nil
}

// grokPick returns the first non-empty string among the verified ID token and
// userinfo claims, in that order of authority.
func grokPick(claims GrokClaims, info GrokUserInfo, names ...string) string {
	for _, name := range names {
		if value := grokClaimString(claims.fields, name); value != "" {
			return value
		}
	}
	for _, name := range names {
		if value := grokClaimString(info.fields, name); value != "" {
			return value
		}
	}
	return ""
}

// Fixed principal assumption used only when the IdP supplies neither claim.
const grokDefaultPrincipalType = "user"

// BuildGrokBundle maps a completed login to the native auth.json scope entry.
//
// The mapping is the best available evidence and is NOT verified against the
// real native CLI (see the Grok subscription contract):
//   - key is the access token and refresh_token the refresh token;
//   - create_time is the exchange time and expires_at is create_time plus the
//     token response's expires_in;
//   - user_id is the `user_id` claim, else the verified ID token subject;
//   - principal_type and principal_id are the claims of those names, else
//     "user" and the user identifier; one without the other is refused;
//   - email, first_name (`first_name` or `given_name`), profile_image_asset_id,
//     team_id and coding_data_retention_opt_out are copied only when present.
//
// The result is validated by ParseGrokAuthFile, so a bundle that cannot be
// stored or accepted by the Worker is refused here.
func BuildGrokBundle(now time.Time, tokens GrokTokens, claims GrokClaims, info GrokUserInfo) (GrokBundle, error) {
	userID := grokPick(claims, info, "user_id")
	if userID == "" {
		userID = claims.Subject
	}
	principalType, principalID := grokPick(claims, info, "principal_type"), grokPick(claims, info, "principal_id")
	switch {
	case principalType == "" && principalID == "":
		principalType, principalID = grokDefaultPrincipalType, userID
	case principalType == "" || principalID == "":
		return GrokBundle{}, grokFailure(GrokReasonMapping)
	}
	created := now.UTC()
	entry := map[string]any{
		"key": tokens.AccessToken, "auth_mode": grokAuthMode, "create_time": created.Format(time.RFC3339Nano),
		"user_id": userID, "principal_type": principalType, "principal_id": principalID,
		"refresh_token": tokens.RefreshToken, "expires_at": created.Add(tokens.ExpiresIn).Format(time.RFC3339Nano),
		"oidc_issuer": GrokIssuer, "oidc_client_id": GrokClientID,
	}
	for member, names := range map[string][]string{
		"email": {"email"}, "first_name": {"first_name", "given_name"}, "profile_image_asset_id": {"profile_image_asset_id"}, "team_id": {"team_id"},
	} {
		if value := grokPick(claims, info, names...); value != "" {
			entry[member] = value
		}
	}
	if value, ok := grokClaimBool(claims.fields, "coding_data_retention_opt_out"); ok {
		entry["coding_data_retention_opt_out"] = value
	} else if value, ok := grokClaimBool(info.fields, "coding_data_retention_opt_out"); ok {
		entry["coding_data_retention_opt_out"] = value
	}
	raw, err := marshalGrokJSON(map[string]any{GrokScopeKey: entry})
	if err != nil {
		return GrokBundle{}, grokFailure(GrokReasonMapping)
	}
	defer clear(raw)
	bundle, err := ParseGrokAuthFile(raw)
	if err != nil {
		return GrokBundle{}, grokFailure(GrokReasonMapping)
	}
	return bundle, nil
}

// GrokOAuthFailure extracts the closed reason of err, if any.
func GrokOAuthFailure(err error) (GrokOAuthReason, bool) {
	var failure *GrokOAuthError
	if errors.As(err, &failure) {
		return failure.Reason, true
	}
	return "", false
}

// SuggestedName is the default account name offered after a successful login:
// the profile email when present and valid, otherwise "Grok". It is transient
// presentation data and is never persisted.
func (b GrokBundle) SuggestedName() string {
	if b.entry.Email != nil && *b.entry.Email != "" && len(*b.entry.Email) <= 256 && grokControlFree(*b.entry.Email, 256, true) {
		return *b.entry.Email
	}
	return "Grok"
}
