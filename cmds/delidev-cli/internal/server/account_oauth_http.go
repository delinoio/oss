// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const openRouterExchangeURL = "https://openrouter.ai/api/v1/auth/keys"

type directOAuthExchange struct{}

func (directOAuthExchange) Exchange(ctx context.Context, code, verifier []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	payload, err := json.Marshal(struct {
		Code     string `json:"code"`
		Verifier string `json:"code_verifier"`
		Method   string `json:"code_challenge_method"`
	}{string(code), string(verifier), "S256"})
	if err != nil {
		return nil, oauthProblem()
	}
	defer clear(payload)
	// A fresh transport with keep-alives disabled cannot replay on a reused
	// connection. An opaque reader also supplies no GetBody retry authority.
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 20 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 20 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterExchangeURL, io.NopCloser(bytes.NewReader(payload)))
	if err != nil {
		return nil, oauthProblem()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return nil, oauthProblem()
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	defer clear(body)
	if err != nil || len(body) > 64<<10 || response.StatusCode != http.StatusOK {
		return nil, oauthProblem()
	}
	var fields map[string]json.RawMessage
	if domain.Decode(body, &fields) != nil {
		return nil, oauthProblem()
	}
	defer func() {
		for _, value := range fields {
			clear(value)
		}
	}()
	var key string
	if json.Unmarshal(fields["key"], &key) != nil || domain.ValidateAPIKey([]byte(key), false) != nil {
		return nil, oauthProblem()
	}
	return []byte(key), nil
}
