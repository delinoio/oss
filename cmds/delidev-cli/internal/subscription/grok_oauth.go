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
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-jose/go-jose/v4"
)

// This file is the issue-neutral xAI OAuth client used by the server-owned Grok
// login. It owns the fixed official profile, OIDC discovery, PKCE/state/nonce
// generation, the public-client token exchange, ID-token validation and the one
// mapping from verified token material to the native credential bundle. It
// performs no storage and holds no global state. Device login, refresh and
// logout reuse the profile, the discovery, the token client and the mapping.

const (
	// GrokReferrer is the observed `referrer` query value of the native CLI.
	GrokReferrer = "grok-build"
	// GrokCallbackPath is the only path a loopback redirect may use.
	GrokCallbackPath = "/callback"
	// GrokAuthorizationPath is the authorization path the desktop receiver pins.
	GrokAuthorizationPath = "/oauth2/authorize"
	// GrokTokenAuthMethod is the token endpoint client authentication of the
	// public CLI client (token_endpoint_auth_method=none).
	GrokTokenAuthMethod = "none"

	grokMaxResponse   = 64 << 10
	grokHTTPTimeout   = 20 * time.Second
	grokClockSkew     = 5 * time.Minute
	grokMaxExpiresIn  = 30 * 24 * 60 * 60
	grokRandomBytes   = 32
	grokMaxClaimField = 512
)

// GrokScopes is the observed scope list of the native Grok Build CLI, in order.
var GrokScopes = []string{"openid", "profile", "email", "offline_access", "grok-cli:access", "api:access", "conversations:read", "conversations:write", "workspaces:read", "workspaces:write"}

// GrokOAuthProfile is the fixed official xAI OAuth profile.
type GrokOAuthProfile struct {
	Issuer   string
	ClientID string
	Referrer string
	Scopes   []string
}

// DefaultGrokOAuthProfile returns the observed profile of the public Grok Build
// CLI client. It never varies at runtime; tests intercept the HTTP transport
// instead of changing the profile.
func DefaultGrokOAuthProfile() GrokOAuthProfile {
	return GrokOAuthProfile{Issuer: GrokIssuer, ClientID: GrokClientID, Referrer: GrokReferrer, Scopes: slices.Clone(GrokScopes)}
}

// GrokOAuthReason is the closed, value-free reason of a Grok OAuth failure.
type GrokOAuthReason string

const (
	GrokReasonDiscovery      GrokOAuthReason = "discovery"
	GrokReasonJWKS           GrokOAuthReason = "jwks"
	GrokReasonRandom         GrokOAuthReason = "random"
	GrokReasonCallback       GrokOAuthReason = "callback"
	GrokReasonDenied         GrokOAuthReason = "access_denied"
	GrokReasonTokenRejected  GrokOAuthReason = "token_rejected"
	GrokReasonTokenUncertain GrokOAuthReason = "token_uncertain"
	GrokReasonTokenResponse  GrokOAuthReason = "token_response"
	GrokReasonIDTokenFormat  GrokOAuthReason = "id_token_format"
	GrokReasonSignature      GrokOAuthReason = "id_token_signature"
	GrokReasonIssuer         GrokOAuthReason = "id_token_issuer"
	GrokReasonAudience       GrokOAuthReason = "id_token_audience"
	GrokReasonNonce          GrokOAuthReason = "id_token_nonce"
	GrokReasonExpired        GrokOAuthReason = "id_token_expired"
	GrokReasonUserinfo       GrokOAuthReason = "userinfo"
	GrokReasonMapping        GrokOAuthReason = "mapping"
	GrokReasonResponse       GrokOAuthReason = "response"
)

// GrokOAuthError never carries a code, URL, verifier or token. Uncertain is true
// only when a token request may have reached the provider and consumed the code.
type GrokOAuthError struct {
	Reason    GrokOAuthReason
	Uncertain bool
}

func (e *GrokOAuthError) Error() string { return "grok oauth failed: " + string(e.Reason) }

func grokFail(reason GrokOAuthReason) error { return &GrokOAuthError{Reason: reason} }

// GrokOAuthReasonOf returns the closed reason of err and whether the token
// outcome is uncertain. A foreign error is reported as uncertain only when
// uncertain is the safe default for the caller.
func GrokOAuthReasonOf(err error) (GrokOAuthReason, bool) {
	var failure *GrokOAuthError
	if asGrokError(err, &failure) {
		return failure.Reason, failure.Uncertain
	}
	return "", false
}

func asGrokError(err error, target **GrokOAuthError) bool { return errors.As(err, target) }

// grokDecode decodes a bounded foreign JSON document. Unknown members are
// ignored, because the provider owns those schemas.
func grokDecode(data []byte, target any) error {
	if len(data) > grokMaxResponse || !utf8.Valid(data) {
		return grokFail(GrokReasonResponse)
	}
	return json.Unmarshal(data, target)
}

// GrokEndpoints are the discovery endpoints, validated against the issuer.
type GrokEndpoints struct {
	Authorization string
	Token         string
	JWKS          string
	Userinfo      string
}

type grokDiscoveryDocument struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	UserinfoEndpoint      string   `json:"userinfo_endpoint"`
	ResponseTypes         []string `json:"response_types_supported"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
	TokenAuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
	IDTokenAlgs           []string `json:"id_token_signing_alg_values_supported"`
}

// grokSameOrigin accepts only an https URL on the issuer's exact origin without
// credentials, query or fragment.
func grokSameOrigin(issuer, raw string) bool {
	base, err := url.Parse(issuer)
	if err != nil {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && base.Scheme == "https" && u.Host == base.Host && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.Path != "" && len(raw) <= 2048
}

// DiscoverGrokEndpoints reads the OIDC discovery document of the profile issuer
// and validates it. The issuer must match exactly, every endpoint must be an
// https URL on the issuer origin, PKCE must be S256-only, the token endpoint must
// accept the public client method `none` and ID tokens must be ES256.
func DiscoverGrokEndpoints(ctx context.Context, client *http.Client, profile GrokOAuthProfile) (GrokEndpoints, error) {
	status, body, err := grokRoundTrip(ctx, client, http.MethodGet, strings.TrimSuffix(profile.Issuer, "/")+"/.well-known/openid-configuration", nil, nil)
	if err != nil || status != http.StatusOK {
		return GrokEndpoints{}, grokFail(GrokReasonDiscovery)
	}
	var doc grokDiscoveryDocument
	if grokDecode(body, &doc) != nil {
		return GrokEndpoints{}, grokFail(GrokReasonDiscovery)
	}
	if doc.Issuer != profile.Issuer || !slices.Contains(doc.ResponseTypes, "code") ||
		!slices.Contains(doc.CodeChallengeMethods, "S256") || len(doc.CodeChallengeMethods) != 1 ||
		!slices.Contains(doc.TokenAuthMethods, GrokTokenAuthMethod) || !slices.Contains(doc.IDTokenAlgs, string(jose.ES256)) {
		return GrokEndpoints{}, grokFail(GrokReasonDiscovery)
	}
	endpoints := GrokEndpoints{Authorization: doc.AuthorizationEndpoint, Token: doc.TokenEndpoint, JWKS: doc.JWKSURI, Userinfo: doc.UserinfoEndpoint}
	for _, endpoint := range []string{endpoints.Authorization, endpoints.Token, endpoints.JWKS, endpoints.Userinfo} {
		if !grokSameOrigin(profile.Issuer, endpoint) {
			return GrokEndpoints{}, grokFail(GrokReasonDiscovery)
		}
	}
	return endpoints, nil
}

// grokRoundTrip performs one bounded request. Redirects are never followed and
// the response body is limited. A non-nil error means no usable response.
func grokRoundTrip(ctx context.Context, client *http.Client, method, target string, body []byte, header map[string]string) (int, []byte, error) {
	if client == nil {
		return 0, nil, io.ErrUnexpectedEOF
	}
	ctx, cancel := context.WithTimeout(ctx, grokHTTPTimeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Accept", "application/json")
	for name, value := range header {
		request.Header.Set(name, value)
	}
	local := *client
	local.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := local.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, grokMaxResponse+1))
	if err != nil || len(raw) > grokMaxResponse {
		clear(raw)
		return response.StatusCode, nil, io.ErrUnexpectedEOF
	}
	return response.StatusCode, raw, nil
}

// GrokAuthorization holds the per-login state, nonce and PKCE verifier. The
// verifier is memory-only; Clear overwrites it.
type GrokAuthorization struct {
	State     string
	Nonce     string
	Challenge string
	verifier  []byte
}

func grokRandomToken(source io.Reader) (string, error) {
	raw := make([]byte, grokRandomBytes)
	defer clear(raw)
	if _, err := io.ReadFull(source, raw); err != nil {
		return "", grokFail(GrokReasonRandom)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// NewGrokAuthorization generates an opaque state, an opaque nonce and an S256
// PKCE verifier from source (crypto/rand when nil). Each value is 43 characters
// from [A-Za-z0-9_-], inside the charset the desktop receiver accepts.
func NewGrokAuthorization(source io.Reader) (*GrokAuthorization, error) {
	if source == nil {
		source = rand.Reader
	}
	state, err := grokRandomToken(source)
	if err != nil {
		return nil, err
	}
	nonce, err := grokRandomToken(source)
	if err != nil {
		return nil, err
	}
	verifier, err := grokRandomToken(source)
	if err != nil {
		return nil, err
	}
	a := &GrokAuthorization{State: state, Nonce: nonce, verifier: []byte(verifier)}
	sum := sha256.Sum256(a.verifier)
	a.Challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return a, nil
}

// Verifier returns the PKCE verifier bytes. The caller must not retain them.
func (a *GrokAuthorization) Verifier() []byte { return a.verifier }

// Clear overwrites the PKCE verifier.
func (a *GrokAuthorization) Clear() {
	if a != nil {
		clear(a.verifier)
		a.verifier = nil
	}
}

// GrokRedirectURI is the loopback redirect URI for one bound port.
func GrokRedirectURI(port int) string {
	return "http://127.0.0.1:" + strconv.Itoa(port) + GrokCallbackPath
}

func grokQueryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

// AuthorizationURL builds the authorization URL in the exact observed shape and
// parameter order: response_type, client_id, redirect_uri, scope, code_challenge,
// code_challenge_method, state, nonce and referrer. Spaces are encoded as %20.
func (p GrokOAuthProfile) AuthorizationURL(endpoints GrokEndpoints, redirectURI string, a *GrokAuthorization) string {
	fields := [][2]string{
		{"response_type", "code"}, {"client_id", p.ClientID}, {"redirect_uri", redirectURI},
		{"scope", strings.Join(p.Scopes, " ")}, {"code_challenge", a.Challenge}, {"code_challenge_method", "S256"},
		{"state", a.State}, {"nonce", a.Nonce}, {"referrer", p.Referrer},
	}
	var out strings.Builder
	out.WriteString(endpoints.Authorization)
	for i, field := range fields {
		if i == 0 {
			out.WriteByte('?')
		} else {
			out.WriteByte('&')
		}
		out.WriteString(field[0] + "=" + grokQueryEscape(field[1]))
	}
	return out.String()
}

// GrokTokens is the token endpoint response of the authorization-code grant.
// Every formatter is redacted.
type GrokTokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresIn    time.Duration
}

func (GrokTokens) String() string     { return grokRedactedLabel + "-tokens{redacted}" }
func (t GrokTokens) GoString() string { return t.String() }
func (t GrokTokens) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(t.String()))
}

// Clear drops the references held by the tokens.
func (t *GrokTokens) Clear() { *t = GrokTokens{} }

type grokTokenResponse struct {
	AccessToken  string          `json:"access_token"`
	TokenType    string          `json:"token_type"`
	ExpiresIn    json.Number     `json:"expires_in"`
	RefreshToken string          `json:"refresh_token"`
	IDToken      string          `json:"id_token"`
	Scope        json.RawMessage `json:"scope,omitempty"`
}

// ExchangeGrokCode sends exactly one authorization-code token request as the
// public client (no client authentication). The caller must have written its
// durable exchange claim first. Only an explicit OAuth rejection (a 4xx answer)
// proves that the code was not consumed. Every other outcome that is not a
// complete, usable success (transport error, timeout, 3xx or 5xx answer, a
// success status other than 200, and a 200 whose body is truncated, malformed
// or incomplete) may have consumed the code and is Uncertain.
func ExchangeGrokCode(ctx context.Context, client *http.Client, profile GrokOAuthProfile, endpoints GrokEndpoints, code string, verifier []byte, redirectURI string) (GrokTokens, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {profile.ClientID}, "code": {code}, "code_verifier": {string(verifier)}, "redirect_uri": {redirectURI}}
	payload := []byte(form.Encode())
	defer clear(payload)
	status, body, err := grokRoundTrip(ctx, client, http.MethodPost, endpoints.Token, payload, map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	defer clear(body)
	if err != nil {
		return GrokTokens{}, &GrokOAuthError{Reason: GrokReasonTokenUncertain, Uncertain: true}
	}
	if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
		return GrokTokens{}, grokFail(GrokReasonTokenRejected)
	}
	if status != http.StatusOK {
		return GrokTokens{}, &GrokOAuthError{Reason: GrokReasonTokenUncertain, Uncertain: true}
	}
	// The provider answered success, so the code may be consumed: an unusable
	// body can never prove otherwise and is recovery, not a definitive failure.
	unusable := &GrokOAuthError{Reason: GrokReasonTokenResponse, Uncertain: true}
	var response grokTokenResponse
	if grokDecode(body, &response) != nil {
		return GrokTokens{}, unusable
	}
	seconds, err := response.ExpiresIn.Int64()
	if err != nil || !strings.EqualFold(response.TokenType, "bearer") || seconds <= 0 || seconds > grokMaxExpiresIn ||
		!grokSecret(response.AccessToken) || !grokSecret(response.RefreshToken) || !grokSecret(response.IDToken) {
		return GrokTokens{}, unusable
	}
	return GrokTokens{AccessToken: response.AccessToken, RefreshToken: response.RefreshToken, IDToken: response.IDToken, ExpiresIn: time.Duration(seconds) * time.Second}, nil
}

// FetchGrokJWKS reads the signing keys advertised by discovery.
func FetchGrokJWKS(ctx context.Context, client *http.Client, endpoints GrokEndpoints) (*jose.JSONWebKeySet, error) {
	status, body, err := grokRoundTrip(ctx, client, http.MethodGet, endpoints.JWKS, nil, nil)
	if err != nil || status != http.StatusOK {
		return nil, grokFail(GrokReasonJWKS)
	}
	var keys jose.JSONWebKeySet
	if grokDecode(body, &keys) != nil || len(keys.Keys) == 0 {
		return nil, grokFail(GrokReasonJWKS)
	}
	return &keys, nil
}

// GrokClaims are the verified identity claims used by the mapping. The principal
// and team fields are optional ID-token claims; the issuer's published
// claims_supported does not list them, so userinfo is the primary source.
type GrokClaims struct {
	Subject       string
	Email         string
	GivenName     string
	PrincipalType string
	PrincipalID   string
	TeamID        string
}

type grokAudience []string

func (a *grokAudience) UnmarshalJSON(raw []byte) error {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		*a = grokAudience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

type grokIDTokenClaims struct {
	Issuer        string       `json:"iss"`
	Subject       string       `json:"sub"`
	Audience      grokAudience `json:"aud"`
	AuthorizedBy  string       `json:"azp"`
	Expiry        *int64       `json:"exp"`
	IssuedAt      *int64       `json:"iat"`
	NotBefore     *int64       `json:"nbf"`
	Nonce         string       `json:"nonce"`
	Email         string       `json:"email"`
	GivenName     string       `json:"given_name"`
	PrincipalType string       `json:"principal_type"`
	PrincipalID   string       `json:"principal_id"`
	TeamID        string       `json:"team_id"`
}

// VerifyGrokIDToken validates an ES256 ID token: the signature against keys
// (only ES256, only a P-256 key selected by kid), then issuer, audience (the
// client id), nonce and expiry. It returns the verified claims. The raw token is
// never retained or echoed.
func VerifyGrokIDToken(raw string, keys *jose.JSONWebKeySet, profile GrokOAuthProfile, nonce string, now time.Time) (GrokClaims, error) {
	parsed, err := jose.ParseSigned(raw, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil || len(parsed.Signatures) != 1 || keys == nil {
		return GrokClaims{}, grokFail(GrokReasonIDTokenFormat)
	}
	header := parsed.Signatures[0].Header
	if header.Algorithm != string(jose.ES256) || header.KeyID == "" {
		return GrokClaims{}, grokFail(GrokReasonIDTokenFormat)
	}
	matches := keys.Key(header.KeyID)
	if len(matches) != 1 || !matches[0].Valid() || matches[0].Use != "" && matches[0].Use != "sig" {
		return GrokClaims{}, grokFail(GrokReasonSignature)
	}
	public, ok := matches[0].Key.(*ecdsa.PublicKey)
	if !ok || public.Curve != elliptic.P256() {
		return GrokClaims{}, grokFail(GrokReasonSignature)
	}
	payload, err := parsed.Verify(public)
	if err != nil {
		return GrokClaims{}, grokFail(GrokReasonSignature)
	}
	defer clear(payload)
	var claims grokIDTokenClaims
	if grokDecode(payload, &claims) != nil || claims.Expiry == nil {
		return GrokClaims{}, grokFail(GrokReasonIDTokenFormat)
	}
	switch {
	case claims.Issuer != profile.Issuer:
		return GrokClaims{}, grokFail(GrokReasonIssuer)
	case !slices.Contains(claims.Audience, profile.ClientID) || len(claims.Audience) > 1 && claims.AuthorizedBy != profile.ClientID:
		return GrokClaims{}, grokFail(GrokReasonAudience)
	case !now.Before(time.Unix(*claims.Expiry, 0)):
		return GrokClaims{}, grokFail(GrokReasonExpired)
	case claims.IssuedAt != nil && time.Unix(*claims.IssuedAt, 0).After(now.Add(grokClockSkew)), claims.NotBefore != nil && time.Unix(*claims.NotBefore, 0).After(now.Add(grokClockSkew)):
		return GrokClaims{}, grokFail(GrokReasonExpired)
	case nonce == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1:
		return GrokClaims{}, grokFail(GrokReasonNonce)
	case !grokControlFree(claims.Subject, maxGrokIdentityField, true):
		return GrokClaims{}, grokFail(GrokReasonIDTokenFormat)
	}
	return GrokClaims{Subject: claims.Subject, Email: claims.Email, GivenName: claims.GivenName, PrincipalType: claims.PrincipalType, PrincipalID: claims.PrincipalID, TeamID: claims.TeamID}, nil
}

type grokUserinfo struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	GivenName     string `json:"given_name"`
	PrincipalType string `json:"principal_type"`
	PrincipalID   string `json:"principal_id"`
	TeamID        string `json:"team_id"`
}

// FetchGrokUserinfo reads the userinfo endpoint with the access token. The
// subject must equal the verified ID-token subject.
func FetchGrokUserinfo(ctx context.Context, client *http.Client, endpoints GrokEndpoints, accessToken string, verified GrokClaims) (GrokClaims, error) {
	status, body, err := grokRoundTrip(ctx, client, http.MethodGet, endpoints.Userinfo, nil, map[string]string{"Authorization": "Bearer " + accessToken})
	defer clear(body)
	var info grokUserinfo
	if err != nil || status != http.StatusOK || grokDecode(body, &info) != nil || info.Subject != verified.Subject {
		return GrokClaims{}, grokFail(GrokReasonUserinfo)
	}
	return GrokClaims{Subject: info.Subject, Email: info.Email, GivenName: info.GivenName, PrincipalType: info.PrincipalType, PrincipalID: info.PrincipalID, TeamID: info.TeamID}, nil
}

func grokFirst(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func grokOptionalClaim(value string, max int) *string {
	if value == "" || !grokControlFree(value, max, false) {
		return nil
	}
	return &value
}

// BuildGrokBundle is the single mapping from verified token material to the
// native auth.json scope entry. UNVERIFIED ASSUMPTION: the real field source of
// each member was not captured from a real login (it needs an account-owner
// run). The mapping is therefore isolated here and pinned by tests:
//
//   - key and refresh_token are the access_token and refresh_token;
//   - create_time is now and expires_at is now plus expires_in;
//   - user_id is the verified ID-token subject (userinfo must agree);
//   - principal_type, principal_id and team_id come from userinfo, falling back to
//     the same-named verified ID-token claim; a missing principal fails closed;
//   - email and first_name come from userinfo, falling back to the ID token.
//
// The result is round-tripped through ParseGrokAuthFile, so only a bundle the
// existing library accepts is returned.
func BuildGrokBundle(tokens GrokTokens, idClaims, info GrokClaims, now time.Time) (GrokBundle, error) {
	created := now.UTC()
	entry := grokAuthEntry{
		Key: tokens.AccessToken, AuthMode: grokAuthMode,
		CreateTime:    created.Format(time.RFC3339Nano),
		UserID:        idClaims.Subject,
		PrincipalType: grokFirst(info.PrincipalType, idClaims.PrincipalType),
		PrincipalID:   grokFirst(info.PrincipalID, idClaims.PrincipalID),
		TeamID:        grokOptionalClaim(grokFirst(info.TeamID, idClaims.TeamID), maxGrokIdentityField),
		Email:         grokOptionalClaim(grokFirst(info.Email, idClaims.Email), maxGrokProfileField),
		FirstName:     grokOptionalClaim(grokFirst(info.GivenName, idClaims.GivenName), maxGrokProfileField),
		RefreshToken:  tokens.RefreshToken,
		ExpiresAt:     created.Add(tokens.ExpiresIn).Format(time.RFC3339Nano),
		OIDCIssuer:    GrokIssuer, OIDCClientID: GrokClientID,
	}
	raw, err := marshalGrokJSON(map[string]grokAuthEntry{GrokScopeKey: entry})
	if err != nil {
		return GrokBundle{}, grokFail(GrokReasonMapping)
	}
	defer clear(raw)
	bundle, err := ParseGrokAuthFile(raw)
	if err != nil {
		return GrokBundle{}, grokFail(GrokReasonMapping)
	}
	return bundle, nil
}
