// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"context"
	"net/http"
)

// GrokRevocationEndpoint is the fixed RFC 7009 revocation endpoint of the xAI
// issuer, advertised as `revocation_endpoint` by its OIDC metadata. Like the
// refresh grant, it does not depend on discovery, so a logout never waits on a
// metadata request that could be answered differently after the claim.
const GrokRevocationEndpoint = GrokIssuer + "/oauth2/revoke"

// GrokRevocation is the closed outcome of one revocation request.
type GrokRevocation string

const (
	// GrokRevoked reports that the provider acknowledged the request (HTTP 200).
	// RFC 7009 answers 200 both for a revoked token and for a submitted token that
	// was already invalid, so a 200 proves the token unusable either way.
	GrokRevoked GrokRevocation = "revoked"
)

// Settled reports whether the outcome proves the refresh token unusable.
func (r GrokRevocation) Settled() bool { return r == GrokRevoked }

// Revoke asks the provider to revoke one refresh token as a public client
// (token, token_type_hint=refresh_token and client_id only; no client secret, no
// Authorization header). It sends exactly one request and never retries or
// follows a redirect.
//
// Only a 200 response is GrokRevoked. RFC 7009 (section 2.2) answers 200 for an
// invalid submitted token too, so an error response means the request itself was
// refused (for example a client identity or validation mismatch) and proves
// nothing about the token: a 400 or 401 body, even invalid_grant or
// invalid_token, is not a proof that the token is unusable elsewhere. Every 4xx
// response is a definite refusal (GrokReasonRevokeRejected): the provider did not
// revoke the token, and the caller keeps the stored credential. A transport
// failure, timeout, cancellation, oversized body, 5xx response or unexpected
// status is GrokReasonRevokeUncertain: the token may or may not be revoked. The
// error carries only a closed reason, never a token or a body.
func (c GrokOAuthClient) Revoke(ctx context.Context, refreshToken []byte) (GrokRevocation, error) {
	payload := grokForm([][2][]byte{
		{[]byte("token"), refreshToken},
		{[]byte("token_type_hint"), []byte("refresh_token")},
		{[]byte("client_id"), []byte(GrokClientID)},
	})
	defer clear(payload)
	status, raw, err := c.do(ctx, http.MethodPost, GrokRevocationEndpoint, payload, "")
	defer clear(raw)
	if err != nil {
		return "", grokFailure(GrokReasonRevokeUncertain)
	}
	switch {
	case status == http.StatusOK:
		return GrokRevoked, nil
	case status >= 400 && status < 500:
		return "", grokFailure(GrokReasonRevokeRejected)
	default:
		return "", grokFailure(GrokReasonRevokeUncertain)
	}
}
