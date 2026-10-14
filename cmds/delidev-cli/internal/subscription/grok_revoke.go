// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"bytes"
	"context"
	"encoding/json"
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
	// GrokRevoked reports that the provider acknowledged the revocation (HTTP 200).
	GrokRevoked GrokRevocation = "revoked"
	// GrokAlreadyInvalid reports that the provider already refused the token as
	// invalid, expired or revoked. The token is unusable, so the outcome counts as
	// revoked.
	GrokAlreadyInvalid GrokRevocation = "already-invalid"
)

// Settled reports whether the outcome proves the refresh token unusable.
func (r GrokRevocation) Settled() bool { return r == GrokRevoked || r == GrokAlreadyInvalid }

// Revoke asks the provider to revoke one refresh token as a public client
// (token, token_type_hint=refresh_token and client_id only; no client secret, no
// Authorization header). It sends exactly one request and never retries or
// follows a redirect.
//
// A 200 response is GrokRevoked (RFC 7009 answers 200 for an unknown token as
// well). A 400 or 401 response whose OAuth error is invalid_token or
// invalid_grant is GrokAlreadyInvalid. Any other 4xx response is a definite
// refusal (GrokReasonRevokeRejected): the provider did not revoke the token.
// A transport failure, timeout, cancellation, oversized body, 5xx response or
// unexpected status is GrokReasonRevokeUncertain: the token may or may not be
// revoked. The error carries only a closed reason, never a token or a body.
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
	case status == http.StatusBadRequest || status == http.StatusUnauthorized:
		var body struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(bytes.NewReader(raw)).Decode(&body) == nil && (body.Error == "invalid_token" || body.Error == "invalid_grant") {
			return GrokAlreadyInvalid, nil
		}
		return "", grokFailure(GrokReasonRevokeRejected)
	case status >= 400 && status < 500:
		return "", grokFailure(GrokReasonRevokeRejected)
	default:
		return "", grokFailure(GrokReasonRevokeUncertain)
	}
}
