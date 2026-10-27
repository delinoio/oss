// SPDX-License-Identifier: Apache-2.0
// Protocol behavior adapted from opencodex; see LICENSE.upstream and NOTICE.
package mimofree

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/inference/policy"
	"github.com/delinoio/oss/packages/aiprouter/providers"
)

const (
	Wire         inference.Wire = "mimo-free"
	ChatURL                     = "https://api.xiaomimimo.com/api/free-ai/openai/chat"
	BootstrapURL                = "https://api.xiaomimimo.com/api/free-ai/bootstrap"
	SystemMarker                = "You are MiMoCode, an interactive CLI tool that helps users with software engineering tasks."
	userAgent                   = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

type Adapter struct {
	mu      sync.Mutex
	flights map[string]*flight
}

func New() *Adapter { return &Adapter{} }

var _ inference.Adapter = (*Adapter)(nil)
var _ inference.ExecutingAdapter = (*Adapter)(nil)

func canonical(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || strings.Contains(raw, "#") {
		return false
	}
	return u.Scheme == "https" && strings.EqualFold(u.Hostname(), "api.xiaomimimo.com") && (u.Port() == "" || u.Port() == "443") && strings.TrimRight(u.Path, "/") == "/api/free-ai/openai/chat"
}

// Build constructs the public body only. Execute installs the scoped bootstrap token.
func (a *Adapter) Build(ctx context.Context, in inference.AdapterInput) (inference.AdapterRequest, error) {
	if !canonical(in.Target.BaseURL) {
		return inference.AdapterRequest{}, inference.ErrDestination
	}
	if in.Operation != inference.Generate {
		return inference.AdapterRequest{}, inference.ErrUnsupported
	}
	raw, err := inference.TranslateRequestJSON(in.Body, in.Wire, inference.Chat)
	if err != nil {
		return inference.AdapterRequest{}, err
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil || body == nil {
		return inference.AdapterRequest{}, invalid("invalid_mimo_request")
	}
	if in.Wire == inference.Chat {
		tools, _ := body["tools"].([]any)
		additional, _ := body["additional_tools"].([]any)
		tools = append(tools, additional...)
		if len(tools) > 0 {
			catalog, _ := json.Marshal(tools)
			catalog, err = inference.TranslateToolsJSON(catalog, inference.Chat, inference.Chat)
			if err != nil {
				return inference.AdapterRequest{}, err
			}
			if err = json.Unmarshal(catalog, &tools); err != nil {
				return inference.AdapterRequest{}, err
			}
			body["tools"] = tools
			delete(body, "additional_tools")
		}
	}
	preset, ok := providers.Lookup("mimo-free")
	if !ok {
		return inference.AdapterRequest{}, inference.ErrUnsupported
	}
	inbound := providers.InboundResponses
	switch in.Wire {
	case inference.Chat:
		inbound = providers.InboundChat
	case inference.Anthropic:
		inbound = providers.InboundAnthropic
	}
	resolved, err := policy.Resolve(policy.Config{ProviderID: "mimo-free", Model: in.Target.Model, BaseURL: ChatURL, Wire: providers.Wire(Wire), Inbound: inbound, Registry: preset.Metadata})
	if err != nil {
		return inference.AdapterRequest{}, err
	}
	if in.Target.Policy != nil {
		resolved = *in.Target.Policy
	}
	body, err = resolved.TransformChatRequest(body)
	if err != nil {
		return inference.AdapterRequest{}, err
	}
	messages, ok := body["messages"].([]any)
	if !ok {
		return inference.AdapterRequest{}, invalid("invalid_mimo_messages")
	}
	marked := false
	for _, item := range messages {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		text, _ := m["content"].(string)
		if m["role"] == "system" && strings.Contains(text, SystemMarker) {
			marked = true
		}
	}
	if !marked {
		body["messages"] = append([]any{map[string]any{"role": "system", "content": SystemMarker}}, messages...)
	}
	body["model"] = in.Target.Model
	raw, err = json.Marshal(body)
	if err != nil {
		return inference.AdapterRequest{}, err
	}
	accept := "application/json"
	if body["stream"] == true {
		accept = "text/event-stream"
	}
	return inference.AdapterRequest{Method: http.MethodPost, URL: ChatURL, Body: raw, ResponseWire: inference.Chat, Headers: http.Header{
		"Content-Type": {"application/json"}, "Accept": {accept}, "User-Agent": {userAgent}, "X-Mimo-Source": {"mimocode-cli-free"},
	}}, nil
}
func (*Adapter) Decode(_ context.Context, r *http.Response) (*http.Response, error) { return r, nil }
func invalid(code string) error                                                     { return &inference.Error{Status: 502, Code: code} }
