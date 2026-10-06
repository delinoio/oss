// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Encoded only inside protected storage. None of these bytes enter receipts,
// resource documents, diagnostic output or frontend caches.
type oauthTokens struct {
	Access  []byte `json:"access"`
	Refresh []byte `json:"refresh,omitempty"`
}

func (v *oauthTokens) clear() { clear(v.Access); clear(v.Refresh) }

type oauthTokenResult struct {
	tokens  oauthTokens
	expires time.Time
}
type oauthTokenClient interface {
	Exchange(context.Context, oauthProfile, []byte, []byte, string) (oauthTokenResult, error)
	Refresh(context.Context, oauthProfile, []byte) (oauthTokenResult, error)
}
type ownedOAuthTokenClient struct {
	route     outbound.Resolver
	transport http.RoundTripper
}

func oauthCredentialProblem() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The OAuth token operation has an uncertain outcome.", "Inspect the original connection and provider authorization. Never resend an uncertain token exchange or refresh; reconnect explicitly after cleanup.")
}

// Percent-encode secret values directly into owned bytes. url.Values.Encode
// would create immutable strings containing the code or refresh token.
func oauthForm(fields map[string][]byte) []byte {
	var payload []byte
	const digits = "0123456789ABCDEF"
	for name, value := range fields {
		if len(payload) > 0 {
			payload = append(payload, '&')
		}
		payload = append(payload, name...)
		payload = append(payload, '=')
		for _, b := range value {
			if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b == '.' || b == '~' {
				payload = append(payload, b)
			} else {
				payload = append(payload, '%', digits[b>>4], digits[b&15])
			}
		}
	}
	return payload
}
func (c ownedOAuthTokenClient) Exchange(ctx context.Context, p oauthProfile, code, verifier []byte, callback string) (oauthTokenResult, error) {
	return c.token(ctx, p, map[string][]byte{"grant_type": []byte("authorization_code"), "client_id": []byte(p.registration.ClientID), "redirect_uri": []byte(callback), "code": code, "code_verifier": verifier})
}
func (c ownedOAuthTokenClient) Refresh(ctx context.Context, p oauthProfile, refresh []byte) (oauthTokenResult, error) {
	return c.token(ctx, p, map[string][]byte{"grant_type": []byte("refresh_token"), "client_id": []byte(p.registration.ClientID), "refresh_token": refresh})
}
func (c ownedOAuthTokenClient) token(ctx context.Context, p oauthProfile, fields map[string][]byte) (oauthTokenResult, error) {
	var result oauthTokenResult
	payload := oauthForm(fields)
	defer clear(payload)
	transport := c.transport
	if transport == nil {
		if c.route == nil {
			return result, oauthCredentialProblem()
		}
		base := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 20 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 20 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true, DisableCompression: true}
		defer base.CloseIdleConnections()
		transport = &outbound.Transport{Base: base, Resolve: c.route}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.token, io.NopCloser(bytes.NewReader(payload)))
	if err != nil {
		return result, oauthCredentialProblem()
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return result, oauthCredentialProblem()
	}
	defer response.Body.Close()
	raw := make([]byte, (64<<10)+1)
	defer clear(raw)
	n, err := io.ReadFull(response.Body, raw)
	if err != io.EOF && err != io.ErrUnexpectedEOF || n > 64<<10 {
		return result, oauthCredentialProblem()
	}
	parsed, err := decodeOAuthSecretObject(raw[:n], 0)
	if err != nil {
		return result, oauthCredentialProblem()
	}
	defer func() {
		for _, v := range parsed {
			clear(v)
		}
	}()
	if response.StatusCode != http.StatusOK {
		var code string
		if json.Unmarshal(parsed["error"], &code) == nil && (code == "invalid_grant" || code == "access_denied" || code == "expired_token") {
			return result, domain.Fail(domain.PermissionDenied, "The provider rejected this OAuth authorization.", "Reconnect explicitly after removing the original connection.")
		}
		return result, oauthCredentialProblem()
	}
	var kind string
	if json.Unmarshal(parsed["token_type"], &kind) != nil || kind != "Bearer" && kind != "bearer" {
		return result, oauthCredentialProblem()
	}
	seconds, err := strconv.ParseInt(string(parsed["expires_in"]), 10, 32)
	if err != nil || seconds <= 0 || seconds > 86400*365 {
		return result, oauthCredentialProblem()
	}
	result.tokens.Access, err = decodeOAuthASCIIKey(parsed["access_token"])
	if err != nil {
		return result, oauthCredentialProblem()
	}
	if v, ok := parsed["refresh_token"]; ok {
		result.tokens.Refresh, err = decodeOAuthASCIIKey(v)
		if err != nil {
			result.tokens.clear()
			return oauthTokenResult{}, oauthCredentialProblem()
		}
	}
	result.expires = time.Now().UTC().Truncate(time.Millisecond).Add(time.Duration(seconds) * time.Second)
	return result, nil
}
func encodeOAuthTokens(v oauthTokens) ([]byte, error) {
	if domain.ValidateAPIKey(v.Access, false) != nil || len(v.Refresh) > 0 && domain.ValidateAPIKey(v.Refresh, false) != nil {
		return nil, oauthCredentialProblem()
	}
	return json.Marshal(v)
}
func decodeOAuthTokens(raw []byte) (oauthTokens, error) {
	var v oauthTokens
	// The protected envelope uses bounded byte fields; it never passes through
	// a string-backed domain document decoder.
	if len(raw) > 32<<10 || json.Unmarshal(raw, &v) != nil || domain.ValidateAPIKey(v.Access, false) != nil || len(v.Refresh) > 0 && domain.ValidateAPIKey(v.Refresh, false) != nil {
		v.clear()
		return oauthTokens{}, oauthCredentialProblem()
	}
	return v, nil
}
