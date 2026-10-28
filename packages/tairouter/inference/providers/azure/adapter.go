// SPDX-License-Identifier: Apache-2.0
// Protocol behavior adapted from opencodex at 88aaa754566d119b6b8fc898f8fb47602387d82e.
// See LICENSE.upstream for the original MIT notice.
package azure

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

const Wire inference.Wire = "azure-openai"
const MaxRequestBytes = 32 << 20

// OptionsHeader carries trusted per-connection policy, never an incoming client header.
const OptionsHeader = "X-Tairouter-Azure-Options"

type Options struct {
	MinMaxOutputTokens int64 `json:"min_max_output_tokens,omitempty"`
}
type optionsKey struct{}

func WithOptions(ctx context.Context, options Options) context.Context {
	return context.WithValue(ctx, optionsKey{}, options)
}

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

var _ inference.Adapter = (*Adapter)(nil)

// EffectiveOutputBudget is also used by spend admission. Absent, zero, negative and
// noninteger budgets are left unchanged, as in the pinned Azure adapter.
func EffectiveOutputBudget(budget json.RawMessage, floor int64) json.RawMessage {
	var value float64
	if floor > 0 && json.Unmarshal(budget, &value) == nil && value > 0 && math.Trunc(value) == value && value < float64(floor) {
		out, _ := json.Marshal(floor)
		return out
	}
	return budget
}

func (*Adapter) Build(ctx context.Context, in inference.AdapterInput) (inference.AdapterRequest, error) {
	if err := ctx.Err(); err != nil {
		return inference.AdapterRequest{}, err
	}
	if in.Operation != inference.Generate {
		return inference.AdapterRequest{}, inference.ErrUnsupported
	}
	if in.Target.Auth != inference.APIKey {
		return inference.AdapterRequest{}, &inference.Error{Status: 400, Code: "azure_key_auth_required"}
	}
	if strings.TrimSpace(in.Credential.Secret) == "" || strings.ContainsAny(in.Credential.Secret, "\r\n\x00") {
		return inference.AdapterRequest{}, inference.ErrNoCredential
	}
	if len(in.Body) > MaxRequestBytes {
		return inference.AdapterRequest{}, &inference.Error{Status: 400, Code: "azure_request_too_large"}
	}
	u, err := url.Parse(strings.TrimSpace(in.Target.BaseURL))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return inference.AdapterRequest{}, inference.ErrDestination
	}
	decoded, err := url.PathUnescape(u.String())
	if err != nil || strings.ContainsAny(decoded, "{}") {
		return inference.AdapterRequest{}, &inference.Error{Status: 400, Code: "azure_unresolved_resource"}
	}
	// The configured base is the v1 API root. No legacy deployments path or
	// api-version query is synthesized. Destination approval remains central.
	u.Path = strings.TrimRight(u.Path, "/") + "/responses"
	u.RawPath = ""
	data, err := inference.TranslateRequestJSON(in.Body, in.Wire, inference.Responses)
	if err != nil {
		return inference.AdapterRequest{}, err
	}
	var body map[string]json.RawMessage
	if err = json.Unmarshal(data, &body); err != nil {
		return inference.AdapterRequest{}, err
	}
	options, supplied := ctx.Value(optionsKey{}).(Options)
	if !supplied {
		for k, values := range in.Credential.Headers {
			if strings.EqualFold(k, OptionsHeader) && len(values) > 0 {
				if len(values) != 1 || len(values[0]) > 1024 || json.Unmarshal([]byte(values[0]), &options) != nil {
					return inference.AdapterRequest{}, &inference.Error{Status: 400, Code: "azure_invalid_options"}
				}
			}
		}
	}
	if budget, ok := body["max_output_tokens"]; ok {
		body["max_output_tokens"] = EffectiveOutputBudget(budget, options.MinMaxOutputTokens)
	}
	if in.Target.Model != "" {
		body["model"], _ = json.Marshal(in.Target.Model)
	}
	body["store"] = json.RawMessage("false")
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
	// Deletion markers prevent generic credentials from reappearing at the final merge.
	for _, k := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "X-Dsh-Auth-Token", "Host", OptionsHeader} {
		headers[k] = nil
	}
	headers.Set("Api-Key", in.Credential.Secret)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "application/json")
	if string(body["stream"]) == "true" {
		headers.Set("Accept", "text/event-stream")
	}
	return inference.AdapterRequest{Method: http.MethodPost, URL: u.String(), Headers: headers, Body: data, ResponseWire: inference.Responses}, nil
}
func (*Adapter) Decode(ctx context.Context, response *http.Response) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, &inference.Error{Status: 502, Code: "azure_missing_response"}
	}
	return response, nil
}
