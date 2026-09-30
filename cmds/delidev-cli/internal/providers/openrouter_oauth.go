// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const oauthResponseLimit = 64 << 10

// The verifier and authorization URL belong only to the original live attempt.
// Entropy is injected by tests; the server coordinator supplies crypto/rand.Reader.
func NewOpenRouterAuthorization(callback string, entropy io.Reader) ([]byte, string, error) {
	if err := domain.ValidateOAuthCallback(callback); err != nil {
		return nil, "", err
	}
	if entropy == nil {
		entropy = rand.Reader
	}
	seed := make([]byte, 32)
	defer clear(seed)
	if _, err := io.ReadFull(entropy, seed); err != nil {
		return nil, "", domain.Fail(domain.Unavailable, "OAuth authorization could not be initialized.", "Retry the deliberate start action before opening a browser.")
	}
	verifier := []byte(base64.RawURLEncoding.EncodeToString(seed))
	challenge := sha256.Sum256(verifier)
	query := url.Values{"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}, "key_label": {"DeliDev"}}
	if callback != "" {
		query.Set("callback_url", callback)
	}
	return verifier, "https://openrouter.ai/auth?" + query.Encode(), nil
}

func OAuthRecoveryProblem() error {
	return domain.Fail(domain.RecoveryRequired, "OpenRouter may have created an API key, but local connection could not be confirmed.", "Inspect the OpenRouter keys dashboard before explicitly starting over or connecting manually. Local cancellation does not revoke a provider key.")
}

// ExchangeOpenRouterCode sends one bounded POST only. Durable dispatch ownership
// is the caller's responsibility. No response error is safe authority to retry.
func ExchangeOpenRouterCode(ctx context.Context, code, verifier []byte) ([]byte, error) {
	transport := &http.Transport{
		Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true, DisableCompression: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return exchangeOpenRouterCode(ctx, client, code, verifier)
}

func exchangeOpenRouterCode(ctx context.Context, client *http.Client, code, verifier []byte) ([]byte, error) {
	if err := domain.ValidateOAuthCode(code); err != nil {
		return nil, err
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		return nil, domain.Fail(domain.InvalidArgument, "The PKCE verifier is invalid.", "Use the original server-generated verifier.")
	}
	for _, b := range verifier {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '.' || b == '_' || b == '~') {
			return nil, domain.Fail(domain.InvalidArgument, "The PKCE verifier is invalid.", "Use the original server-generated verifier.")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	body, err := json.Marshal(struct {
		Code     string `json:"code"`
		Verifier string `json:"code_verifier"`
		Method   string `json:"code_challenge_method"`
	}{string(code), string(verifier), "S256"})
	if err != nil {
		return nil, OAuthRecoveryProblem()
	}
	defer clear(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://openrouter.ai/api/v1/auth/keys", bytes.NewReader(body))
	if err != nil {
		return nil, OAuthRecoveryProblem()
	}
	req.Header.Set("Content-Type", "application/json")
	// Avoid retaining another body closure; the non-idempotent POST has no retry.
	req.GetBody = nil
	response, err := client.Do(req)
	if err != nil {
		return nil, OAuthRecoveryProblem()
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, OAuthRecoveryProblem()
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, oauthResponseLimit+1))
	defer clear(raw)
	if err != nil || len(raw) > oauthResponseLimit {
		return nil, OAuthRecoveryProblem()
	}
	fields, err := object(raw)
	if err != nil {
		return nil, OAuthRecoveryProblem()
	}
	defer func() {
		for _, field := range fields {
			clear(field)
		}
	}()
	var result struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(fields["key"], &result.Key) != nil {
		return nil, OAuthRecoveryProblem()
	}
	key := []byte(result.Key)
	result.Key = ""
	if domain.ValidateAPIKey(key, false) != nil {
		clear(key)
		return nil, OAuthRecoveryProblem()
	}
	return key, nil
}
