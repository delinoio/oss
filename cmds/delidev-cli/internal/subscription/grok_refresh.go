// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// GrokTokenEndpoint is the fixed token endpoint of the refresh_token grant. The
// refresh grant does not depend on discovery, so a refresh never waits on a
// metadata request that could be answered differently after the claim.
const GrokTokenEndpoint = GrokIssuer + "/oauth2/token"

// Refresh redeems one refresh token at the fixed token endpoint as a public
// client (grant_type=refresh_token, client_id and refresh_token only; no client
// secret, no Authorization header). It sends exactly one request and never
// retries or follows a redirect.
//
// The classification matches ExchangeCode. A definitive provider rejection
// (HTTP 400 or 401 with an OAuth error, for example invalid_grant) is
// GrokReasonTokenRejected: the provider refused the token, so nothing rotated.
// Every transport failure, cancellation, truncated or oversized body and
// unexpected status is GrokReasonTokenUncertain, and a malformed success body is
// GrokReasonTokenResponse; in both cases the provider may already have rotated
// the token, so the caller must never resend it.
//
// A successful response must carry an access token, a bearer token type and a
// positive expires_in. The refresh token and the ID token are optional: an
// omitted refresh_token means the provider did not rotate it (the result's
// RefreshToken is empty and the caller keeps the current one).
func (c GrokOAuthClient) Refresh(ctx context.Context, refreshToken []byte) (GrokTokens, error) {
	payload := grokForm([][2][]byte{
		{[]byte("grant_type"), []byte("refresh_token")},
		{[]byte("client_id"), []byte(GrokClientID)},
		{[]byte("refresh_token"), refreshToken},
	})
	defer clear(payload)
	status, raw, err := c.do(ctx, http.MethodPost, GrokTokenEndpoint, payload, "")
	defer clear(raw)
	if err != nil {
		return GrokTokens{}, err
	}
	var body struct {
		AccessToken  string      `json:"access_token"`
		RefreshToken string      `json:"refresh_token"`
		IDToken      string      `json:"id_token"`
		TokenType    string      `json:"token_type"`
		ExpiresIn    json.Number `json:"expires_in"`
		Error        string      `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	parsed := decoder.Decode(&body) == nil
	if status != http.StatusOK {
		if parsed && (status == http.StatusBadRequest || status == http.StatusUnauthorized) && body.Error != "" {
			return GrokTokens{}, grokFailure(GrokReasonTokenRejected)
		}
		return GrokTokens{}, grokFailure(GrokReasonTokenUncertain)
	}
	seconds, numErr := body.ExpiresIn.Int64()
	if !parsed || numErr != nil || seconds <= 0 || seconds > 86400*365 || !strings.EqualFold(body.TokenType, "bearer") ||
		!grokSecret(body.AccessToken) || body.RefreshToken != "" && !grokSecret(body.RefreshToken) || body.IDToken != "" && !grokSecret(body.IDToken) {
		return GrokTokens{}, grokFailure(GrokReasonTokenResponse)
	}
	return GrokTokens{AccessToken: body.AccessToken, RefreshToken: body.RefreshToken, IDToken: body.IDToken, ExpiresIn: time.Duration(seconds) * time.Second}, nil
}

// RefreshedClaims gathers the identity evidence of a refresh response with the
// same sources the login mapping uses. An ID token, when the provider returns
// one, is verified like the login's (signature, issuer, audience and time; a
// refresh carries no nonce to compare). The userinfo endpoint is always read
// with the new access token; without an ID token its subject is the only
// subject evidence and must be present.
func (c GrokOAuthClient) RefreshedClaims(ctx context.Context, endpoints GrokEndpoints, tokens GrokTokens) (GrokClaims, GrokUserInfo, error) {
	var claims GrokClaims
	if tokens.IDToken != "" {
		verified, err := c.validateIDToken(ctx, endpoints, tokens.IDToken, "", false)
		if err != nil {
			return GrokClaims{}, GrokUserInfo{}, err
		}
		claims = verified
	}
	info, subject, err := c.userInfo(ctx, endpoints, tokens.AccessToken, claims, claims.Subject == "")
	if err != nil {
		return GrokClaims{}, GrokUserInfo{}, err
	}
	if claims.Subject == "" {
		claims.Subject = subject
	}
	return claims, info, nil
}

// RefreshGrokBundle maps a verified refresh response to the next bundle of the
// same identity. The token fields come from the response and the identity from
// the login mapping (BuildGrokBundle); a refresh token the provider did not
// return stays the current one. Profile members the provider did not return this
// time are carried over from the current bundle, so a refresh never drops a
// field the native file already held.
//
// The refreshed identity must equal the bound one: a different user, principal
// type or principal ID is GrokReasonIdentity and no bundle is produced.
func RefreshGrokBundle(now time.Time, current GrokBundle, tokens GrokTokens, claims GrokClaims, info GrokUserInfo) (GrokBundle, error) {
	if current.entry.Key == "" || current.identity.UserID == "" {
		return GrokBundle{}, grokFailure(GrokReasonMapping)
	}
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = current.entry.RefreshToken
	}
	mapped, err := BuildGrokBundle(now, tokens, claims, info)
	if err != nil {
		return GrokBundle{}, err
	}
	if mapped.IdentityKey() != current.IdentityKey() {
		mapped.Clear()
		return GrokBundle{}, grokFailure(GrokReasonIdentity)
	}
	entry := mapped.entry
	if entry.Email == nil {
		entry.Email = current.entry.Email
	}
	if entry.FirstName == nil {
		entry.FirstName = current.entry.FirstName
	}
	if entry.ProfileImageAssetID == nil {
		entry.ProfileImageAssetID = current.entry.ProfileImageAssetID
	}
	if entry.TeamID == nil {
		entry.TeamID = current.entry.TeamID
	}
	if entry.CodingDataRetentionOptOut == nil {
		entry.CodingDataRetentionOptOut = current.entry.CodingDataRetentionOptOut
	}
	mapped.Clear()
	raw, err := marshalGrokJSON(map[string]any{GrokScopeKey: entry})
	if err != nil {
		return GrokBundle{}, grokFailure(GrokReasonMapping)
	}
	defer clear(raw)
	next, err := ParseGrokAuthFile(raw)
	if err != nil {
		return GrokBundle{}, grokFailure(GrokReasonMapping)
	}
	return next, nil
}

// RefreshTokenBytes returns a copy of the bundle's refresh token for the one
// refresh_token grant. The caller owns the copy and must clear it after use.
func (b GrokBundle) RefreshTokenBytes() []byte { return []byte(b.entry.RefreshToken) }
