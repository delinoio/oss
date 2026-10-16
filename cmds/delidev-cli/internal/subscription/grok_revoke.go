// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"context"
	"net/http"
	"net/url"
)

// This file is the refresh-token revocation client of the server-owned Grok
// logout (RFC 7009). It reuses the fixed profile, the validated discovery
// endpoints and the bounded round trip of the login client and performs no
// storage. The request is the public-client form the native CLI uses for its
// other grants: `token`, `token_type_hint=refresh_token` and the public
// `client_id`, with no client secret and no authentication header.
//
// Revocation is idempotent, so unlike the token exchanges no durable claim is
// required for correctness: a repeated request for a revoked token is answered
// as already invalid. The exact parameters and answers of the real provider are
// unverified (the opt-in real-account run records them).

const (
	// GrokRevokeTokenTypeHint is the token_type_hint of the revocation request.
	GrokRevokeTokenTypeHint = "refresh_token"

	// GrokReasonRevocationEndpoint means discovery advertised no usable
	// revocation endpoint. Nothing was sent.
	GrokReasonRevocationEndpoint GrokOAuthReason = "revocation_endpoint"
	// GrokReasonRevocationRejected is a definite provider rejection (an HTTP 4xx
	// answer other than an already-invalid token). The token may still be valid.
	GrokReasonRevocationRejected GrokOAuthReason = "revocation_rejected"
	// GrokReasonRevocationUncertain is a revocation request that may have reached
	// the provider without a usable answer (transport error, timeout, 5xx,
	// unexpected status). The token may or may not be revoked.
	GrokReasonRevocationUncertain GrokOAuthReason = "revocation_uncertain"
)

// GrokRevocation is the closed outcome of a confirmed revocation.
type GrokRevocation string

const (
	// GrokRevoked is a 2xx answer: the provider revoked the token.
	GrokRevoked GrokRevocation = "revoked"
	// GrokRevokedAlreadyInvalid is the provider's `invalid_token` or
	// `invalid_grant` answer: the token was already unusable, which counts as
	// revoked.
	GrokRevokedAlreadyInvalid GrokRevocation = "already_invalid"
)

// Confirmed reports whether the outcome proves the refresh token is unusable.
func (r GrokRevocation) Confirmed() bool {
	return r == GrokRevoked || r == GrokRevokedAlreadyInvalid
}

// RevokeGrokRefreshToken sends exactly one revocation request. A 2xx answer and
// an already-invalid answer return a confirmed outcome. Any other result is an
// error and the token must be treated as possibly still valid: a definite 4xx
// rejection is a plain GrokOAuthError, and every other failure (transport error,
// timeout, truncated or oversized body, 3xx, 5xx) is Uncertain. A missing
// revocation endpoint fails before anything is sent.
func RevokeGrokRefreshToken(ctx context.Context, client *http.Client, profile GrokOAuthProfile, endpoints GrokEndpoints, refreshToken string) (GrokRevocation, error) {
	if endpoints.Revocation == "" {
		return "", grokFail(GrokReasonRevocationEndpoint)
	}
	form := url.Values{"token": {refreshToken}, "token_type_hint": {GrokRevokeTokenTypeHint}, "client_id": {profile.ClientID}}
	payload := []byte(form.Encode())
	defer clear(payload)
	status, body, err := grokRoundTrip(ctx, client, http.MethodPost, endpoints.Revocation, payload, map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	defer clear(body)
	switch {
	case err != nil:
		return "", &GrokOAuthError{Reason: GrokReasonRevocationUncertain, Uncertain: true}
	case status >= 200 && status < 300:
		return GrokRevoked, nil
	case status >= 400 && status < 500:
		var answer grokTokenError
		if grokDecode(body, &answer) == nil && (answer.Error == "invalid_token" || answer.Error == "invalid_grant") {
			return GrokRevokedAlreadyInvalid, nil
		}
		return "", grokFail(GrokReasonRevocationRejected)
	default:
		return "", &GrokOAuthError{Reason: GrokReasonRevocationUncertain, Uncertain: true}
	}
}
