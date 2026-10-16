// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// This file is the refresh_token grant client of the server-owned Grok refresh.
// It reuses the fixed profile, the validated discovery endpoints, the bounded
// round trip and the bundle library of the login client, and performs no
// storage. The caller must have written its durable refresh claim before it
// calls RefreshGrokTokens, and must never call it twice for one claim: the
// provider may rotate the refresh token on every refresh, so a rotated token
// that was not recorded cannot be recovered and the old token is never resent.

const (
	// GrokRefreshGrantType is the token endpoint grant of the refresh request.
	GrokRefreshGrantType = "refresh_token"

	// GrokReasonInvalidGrant is the provider's definitive answer that the stored
	// refresh token is no longer accepted. The token was not consumed.
	GrokReasonInvalidGrant GrokOAuthReason = "invalid_grant"
	// GrokReasonIdentity is a refresh answer that names another identity than the
	// bound one.
	GrokReasonIdentity GrokOAuthReason = "identity"
)

// GrokRefreshTokens is the validated token endpoint answer of a refresh. The
// refresh token is empty when the provider did not rotate it, and the ID token
// is empty when the provider did not issue one. Every formatter is redacted.
type GrokRefreshTokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresIn    time.Duration
}

func (GrokRefreshTokens) String() string     { return grokRedactedLabel + "-refresh-tokens{redacted}" }
func (t GrokRefreshTokens) GoString() string { return t.String() }
func (t GrokRefreshTokens) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(t.String()))
}

// Rotated reports whether the provider issued a new refresh token.
func (t GrokRefreshTokens) Rotated() bool { return t.RefreshToken != "" }

// Clear drops the references held by the tokens.
func (t *GrokRefreshTokens) Clear() { *t = GrokRefreshTokens{} }

type grokRefreshResponse struct {
	AccessToken  string      `json:"access_token"`
	TokenType    string      `json:"token_type"`
	ExpiresIn    json.Number `json:"expires_in"`
	RefreshToken string      `json:"refresh_token"`
	IDToken      string      `json:"id_token"`
}

type grokTokenError struct {
	Error string `json:"error"`
}

func grokRefreshUncertain(reason GrokOAuthReason) error {
	return &GrokOAuthError{Reason: reason, Uncertain: true}
}

// RefreshGrokTokens sends exactly one refresh_token grant as the public client
// (no client authentication). A 4xx answer is a definitive rejection that did
// not consume the token (`invalid_grant` is named separately). Every other
// failure is Uncertain, because the provider may have rotated the refresh token
// without this caller recording it: a transport error, a timeout, a truncated
// or oversized body, a 5xx answer and an unusable success answer.
func RefreshGrokTokens(ctx context.Context, client *http.Client, profile GrokOAuthProfile, endpoints GrokEndpoints, refreshToken string) (GrokRefreshTokens, error) {
	form := url.Values{"grant_type": {GrokRefreshGrantType}, "client_id": {profile.ClientID}, "refresh_token": {refreshToken}}
	payload := []byte(form.Encode())
	defer clear(payload)
	status, body, err := grokRoundTrip(ctx, client, http.MethodPost, endpoints.Token, payload, map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	defer clear(body)
	switch {
	case err != nil:
		return GrokRefreshTokens{}, grokRefreshUncertain(GrokReasonTokenUncertain)
	case status == http.StatusOK:
		return parseGrokRefresh(body)
	case status >= 400 && status < 500:
		var answer grokTokenError
		if grokDecode(body, &answer) == nil && answer.Error == "invalid_grant" {
			return GrokRefreshTokens{}, grokFail(GrokReasonInvalidGrant)
		}
		return GrokRefreshTokens{}, grokFail(GrokReasonTokenRejected)
	default:
		return GrokRefreshTokens{}, grokRefreshUncertain(GrokReasonTokenUncertain)
	}
}

// parseGrokRefresh validates a successful refresh answer. Unlike the login
// answer, the refresh token and the ID token are optional.
func parseGrokRefresh(body []byte) (GrokRefreshTokens, error) {
	var response grokRefreshResponse
	if grokDecode(body, &response) != nil {
		return GrokRefreshTokens{}, grokRefreshUncertain(GrokReasonTokenResponse)
	}
	seconds, err := response.ExpiresIn.Int64()
	if err != nil || !strings.EqualFold(response.TokenType, "bearer") || seconds <= 0 || seconds > grokMaxExpiresIn ||
		!grokSecret(response.AccessToken) ||
		response.RefreshToken != "" && !grokSecret(response.RefreshToken) ||
		response.IDToken != "" && !grokSecret(response.IDToken) {
		return GrokRefreshTokens{}, grokRefreshUncertain(GrokReasonTokenResponse)
	}
	return GrokRefreshTokens{AccessToken: response.AccessToken, RefreshToken: response.RefreshToken, IDToken: response.IDToken, ExpiresIn: time.Duration(seconds) * time.Second}, nil
}

// CompleteGrokRefresh turns a refresh answer into the successor of the bound
// bundle. The identity of the answer must equal the bound identity: an ID token,
// when present, must verify (signature, issuer, audience, expiry; no nonce
// exists for a refresh) and carry the bound subject, and the userinfo read with
// the new access token must carry the bound subject, principal type and
// principal ID. The successor keeps every other member of the bound entry,
// takes the new access token and, when rotated, the new refresh token, and moves
// its creation time and expiry forward. GrokRefreshed then proves a changed
// credential with a later expiry for the same identity; a bundle that does not
// pass it is never returned.
func CompleteGrokRefresh(ctx context.Context, client *http.Client, profile GrokOAuthProfile, endpoints GrokEndpoints, keys *jose.JSONWebKeySet, bound GrokBundle, tokens GrokRefreshTokens, now time.Time) (GrokBundle, error) {
	if bound.entry.Key == "" || bound.identity.UserID == "" {
		return GrokBundle{}, grokFail(GrokReasonMapping)
	}
	verified := GrokClaims{Subject: bound.identity.UserID}
	if tokens.IDToken != "" {
		claims, err := verifyGrokIDToken(tokens.IDToken, keys, profile, "", now)
		if err != nil {
			return GrokBundle{}, err
		}
		if claims.Subject != bound.identity.UserID {
			return GrokBundle{}, grokFail(GrokReasonIdentity)
		}
		verified = claims
	}
	info, err := FetchGrokUserinfo(ctx, client, endpoints, tokens.AccessToken, verified)
	if err != nil {
		return GrokBundle{}, err
	}
	principalType, principalID := grokFirst(info.PrincipalType, verified.PrincipalType), grokFirst(info.PrincipalID, verified.PrincipalID)
	if principalType != bound.identity.PrincipalType || principalID != bound.identity.PrincipalID {
		return GrokBundle{}, grokFail(GrokReasonIdentity)
	}
	created := now.UTC()
	entry := bound.entry
	entry.Key = tokens.AccessToken
	entry.CreateTime = created.Format(time.RFC3339Nano)
	entry.ExpiresAt = created.Add(tokens.ExpiresIn).Format(time.RFC3339Nano)
	if tokens.Rotated() {
		entry.RefreshToken = tokens.RefreshToken
	}
	raw, err := marshalGrokJSON(map[string]grokAuthEntry{GrokScopeKey: entry})
	if err != nil {
		return GrokBundle{}, grokFail(GrokReasonMapping)
	}
	defer clear(raw)
	next, err := ParseGrokAuthFile(raw)
	if err != nil {
		return GrokBundle{}, grokFail(GrokReasonMapping)
	}
	if GrokRefreshed(bound, next) != nil {
		next.Clear()
		return GrokBundle{}, grokFail(GrokReasonMapping)
	}
	return next, nil
}

// RefreshToken returns the bound refresh token for the one refresh request. The
// caller must not retain, log or persist it.
func (b GrokBundle) RefreshToken() string { return b.entry.RefreshToken }
