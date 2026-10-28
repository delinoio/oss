// SPDX-License-Identifier: Apache-2.0
// Protocol behavior adapted from opencodex at 88aaa754566d119b6b8fc898f8fb47602387d82e.
// See LICENSE.upstream for the original MIT notice.
package dsh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

const Wire inference.Wire = "dsh-account"
const MessagesURL = "https://api.deepseek.com/anthropic/v1/messages"
const MaxRequestBytes = 32 << 20

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

var _ inference.Adapter = (*Adapter)(nil)
var _ inference.HTTPRecoveryPolicy = (*Adapter)(nil)

// ResolveMessagesURL validates configuration without ever using it as a token destination.
func ResolveMessagesURL(base string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return MessagesURL, nil
	}
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "api.deepseek.com") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return "", inference.ErrDestination
	}
	switch strings.TrimRight(u.Path, "/") {
	case "", "/anthropic", "/anthropic/v1", "/anthropic/v1/messages":
		return MessagesURL, nil
	}
	return "", inference.ErrDestination
}

// ValidateHeaders applies to operator configuration before synthesized credential headers.
func ValidateHeaders(headers http.Header) error {
	for k := range headers {
		switch strings.ToLower(k) {
		case "x-dsh-auth-token", "anthropic-version", "authorization", "x-api-key", "host":
			return &inference.Error{Status: 400, Code: "dsh_reserved_header"}
		}
	}
	return nil
}

func (*Adapter) Build(ctx context.Context, in inference.AdapterInput) (inference.AdapterRequest, error) {
	if err := ctx.Err(); err != nil {
		return inference.AdapterRequest{}, err
	}
	if in.Operation != inference.Generate {
		return inference.AdapterRequest{}, inference.ErrUnsupported
	}
	if len(in.Body) > MaxRequestBytes {
		return inference.AdapterRequest{}, &inference.Error{Status: 400, Code: "dsh_request_too_large"}
	}
	endpoint, err := ResolveMessagesURL(in.Target.BaseURL)
	if err != nil {
		return inference.AdapterRequest{}, err
	}
	if err = ValidateHeaders(in.Target.Headers); err != nil {
		return inference.AdapterRequest{}, err
	}
	token := strings.TrimSpace(in.Credential.Secret)
	if token == "" || strings.ContainsAny(token, "\r\n\x00") {
		return inference.AdapterRequest{}, inference.ErrNoCredential
	}
	data, err := inference.TranslateRequestJSON(in.Body, in.Wire, inference.Anthropic)
	if err != nil {
		return inference.AdapterRequest{}, err
	}
	var body map[string]json.RawMessage
	if err = json.Unmarshal(data, &body); err != nil {
		return inference.AdapterRequest{}, err
	}
	if in.Target.Model != "" {
		body["model"], _ = json.Marshal(in.Target.Model)
	}
	data, err = json.Marshal(body)
	if err != nil {
		return inference.AdapterRequest{}, err
	}
	headers := make(http.Header)
	for _, source := range []http.Header{in.Target.Headers, in.Credential.Headers} {
		for k, values := range source {
			headers[http.CanonicalHeaderKey(k)] = append([]string(nil), values...)
		}
	}
	// Nil values are deletion markers in the central executor's final header merge.
	// Keep these markers until that merge no longer adds generic credential headers.
	for _, k := range []string{"Authorization", "X-Api-Key", "Api-Key", "X-Goog-Api-Key", "Host", "Anthropic-Beta", "X-Claude-Code-Session-Id", "X-Client-Request-Id"} {
		headers[k] = nil
	}
	headers.Set("X-Dsh-Auth-Token", token)
	headers.Set("Anthropic-Version", "2023-06-01")
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "application/json")
	if string(body["stream"]) == "true" {
		headers.Set("Accept", "text/event-stream")
	}
	return inference.AdapterRequest{Method: http.MethodPost, URL: endpoint, Headers: headers, Body: data, ResponseWire: inference.Anthropic}, nil
}

func (*Adapter) Decode(ctx context.Context, response *http.Response) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, &inference.Error{Status: 502, Code: "dsh_missing_response"}
	}
	return response, nil
}

// RecoverHTTP never refreshes an imported account token. The central credential owner
// must fence any reimport-state write against the selected credential generation.
func (*Adapter) RecoverHTTP(ctx context.Context, _ inference.AdapterInput, failure inference.HTTPFailure) (inference.RecoveryDecision, error) {
	if err := ctx.Err(); err != nil {
		return inference.RecoveryDecision{}, err
	}
	if failure.Status == http.StatusUnauthorized {
		return inference.RecoveryDecision{Action: inference.RecoveryStop, Error: &inference.Error{Status: 401, Code: "dsh_reimport_required"}}, nil
	}
	return inference.RecoveryDecision{Action: inference.RecoveryDefault}, nil
}
