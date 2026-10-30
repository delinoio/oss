// SPDX-License-Identifier: Apache-2.0
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/inference/hosted"
	"github.com/delinoio/oss/packages/tairouter/inference/policy"
	"github.com/delinoio/oss/packages/tairouter/inference/providers/google"
)

// Standard wires use the same lossless translators as core. Specialized provider
// protocols remain delegated to their original adapter, never the hosted wrapper.
type hostedStandardAdapter struct{}

func (hostedStandardAdapter) Build(ctx context.Context, in inference.AdapterInput) (inference.AdapterRequest, error) {
	body := in.Body
	var e error
	if in.Operation == inference.Generate {
		body, e = inference.TranslateRequestJSON(in.Body, in.Wire, in.Target.Wire)
	}
	if e != nil {
		return inference.AdapterRequest{}, e
	}
	var obj map[string]any
	if json.Unmarshal(body, &obj) != nil {
		return inference.AdapterRequest{}, inference.ErrUnsupported
	}
	obj["model"] = in.Target.Model
	if in.Operation == inference.Generate && in.Target.Policy != nil {
		obj, e = in.Target.Policy.TransformRequest(obj)
		if e != nil {
			return inference.AdapterRequest{}, e
		}
	}
	body, e = json.Marshal(obj)
	if e != nil {
		return inference.AdapterRequest{}, e
	}
	path := "/chat/completions"
	switch in.Target.Wire {
	case inference.Responses:
		path = "/responses"
	case inference.Anthropic:
		path = "/messages"
	}
	switch in.Operation {
	case inference.Compact:
		path = "/responses/compact"
	case inference.CountTokens:
		path = "/messages/count_tokens"
	case inference.ImageGeneration, inference.ImageEdit, inference.Search:
		path = "/" + string(in.Operation)
	}
	h := in.Target.Headers.Clone()
	if h == nil {
		h = http.Header{}
	}
	for k, v := range in.Credential.Headers {
		h[k] = append([]string(nil), v...)
	}
	h.Set("Content-Type", "application/json")
	if in.Target.Auth != inference.Anonymous {
		if in.Target.Wire == inference.Anthropic && in.Target.Auth == inference.APIKey {
			h.Set("X-Api-Key", in.Credential.Secret)
		} else {
			h.Set("Authorization", "Bearer "+in.Credential.Secret)
		}
	}
	if in.Target.Wire == inference.Anthropic {
		h.Set("Anthropic-Version", "2023-06-01")
	}
	endpoint := strings.TrimRight(in.Target.BaseURL, "/") + path
	if in.Operation == inference.Generate && in.Target.Endpoint != "" {
		endpoint = in.Target.Endpoint
	}
	return inference.AdapterRequest{Method: http.MethodPost, URL: endpoint, Headers: h, Body: body, ResponseWire: in.Target.Wire}, nil
}
func (hostedStandardAdapter) Decode(_ context.Context, r *http.Response) (*http.Response, error) {
	return r, nil
}

func hostedSources(v any, out *[]hosted.Source, seen map[string]bool) {
	switch x := v.(type) {
	case []any:
		for _, v := range x {
			hostedSources(v, out, seen)
		}
	case map[string]any:
		if u, _ := x["url"].(string); (strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")) && !seen[u] {
			seen[u] = true
			*out = append(*out, hosted.Source{URL: u, Title: hostedString(x, "title")})
		}
		for _, v := range x {
			hostedSources(v, out, seen)
		}
	}
}
func (o *hostedOperation) model(ctx context.Context, args map[string]any) (hosted.OperationResult, error) {
	if o.request.Kind == hosted.Vision {
		ctx = context.WithValue(ctx, hostedVisionDepth{}, true)
	}
	timeout := 60 * time.Second
	if o.request.Kind == hosted.Vision {
		timeout = 45 * time.Second
		if v := o.settings.Vision.TimeoutMs; v != nil {
			timeout = time.Duration(*v) * time.Millisecond
		}
	} else if v := o.settings.Search.TimeoutMs; v != nil {
		timeout = time.Duration(*v) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	query := hostedString(args, "query")
	if o.request.Kind == hosted.Search && strings.TrimSpace(query) == "" {
		return hosted.OperationResult{}, inference.ErrUnsupported
	}
	if o.backend == "exa" {
		body, _ := json.Marshal(map[string]any{"query": query, "numResults": 5, "contents": map[string]any{"text": map[string]any{"maxCharacters": 1000}}})
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, o.transport.base+"/search", bytes.NewReader(body))
		if e != nil {
			return hosted.OperationResult{}, e
		}
		req.Header.Set("X-Api-Key", o.credential.Secret)
		req.Header.Set("Content-Type", "application/json")
		b, _, e := o.transport.exchange(ctx, req, 32<<20)
		if e != nil {
			return hosted.OperationResult{}, e
		}
		var obj map[string]any
		if json.Unmarshal(b, &obj) != nil {
			return hosted.OperationResult{}, inference.ErrUnavailable
		}
		result := hosted.OperationResult{}
		hostedSources(obj["results"], &result.Sources, map[string]bool{})
		result.Output = string(b)
		if len(result.Sources) == 0 {
			result.Failed = true
			result.Output = "Search returned no results."
		}
		return result, nil
	}
	wire := inference.Chat
	op := inference.Generate
	body := map[string]any{"model": o.target.Model, "stream": false, "messages": []any{map[string]any{"role": "user", "content": query}}}
	if o.request.Kind == hosted.Vision {
		if args["image_url"] == nil {
			return hosted.OperationResult{}, inference.ErrUnsupported
		}
		body["messages"] = []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "Describe this image accurately. Include visible text and details needed to answer the user's question."}, map[string]any{"type": "image_url", "image_url": args["image_url"]}}}}
	} else {
		declaration := map[string]any{}
		_ = json.Unmarshal(o.request.Declaration, &declaration)
		switch o.backend {
		case "devin":
			op = inference.Search
			body = map[string]any{"query": query}
		case "anthropic":
			wire = inference.Anthropic
			declaration["type"] = "web_search_20250305"
			declaration["name"] = "web_search"
			body = map[string]any{"model": o.target.Model, "max_tokens": 4096, "stream": false, "messages": []any{map[string]any{"role": "user", "content": query}}, "tools": []any{declaration}}
		case "gemini":
			// Grounding is injected into the provider envelope after its adapter builds it.
		default:
			wire = inference.Responses
			declaration["type"] = "web_search"
			tools := []any{declaration}
			if x := o.settings.Search.XSearch; x != nil && x.Enabled {
				t := map[string]any{"type": "x_search"}
				if len(x.AllowedXHandles) > 0 {
					t["allowed_x_handles"] = x.AllowedXHandles
				}
				if len(x.ExcludedXHandles) > 0 {
					t["excluded_x_handles"] = x.ExcludedXHandles
				}
				if x.FromDate != nil {
					t["from_date"] = *x.FromDate
				}
				if x.ToDate != nil {
					t["to_date"] = *x.ToDate
				}
				tools = append(tools, t)
			}
			effort := "minimal"
			if o.settings.Search.Reasoning != nil {
				effort = strings.ToLower(strings.TrimPrefix(o.settings.Search.GetReasoning().String(), "REASONING_EFFORT_"))
			}
			body = map[string]any{"model": o.target.Model, "input": []any{map[string]any{"role": "user", "content": query}}, "instructions": "Search the web and give a concise factual answer with source URLs.", "tools": tools, "include": []string{"web_search_call.action.sources"}, "reasoning": map[string]any{"effort": effort}, "stream": true, "store": false}
		}
	}
	raw, e := json.Marshal(body)
	if e != nil {
		return hosted.OperationResult{}, e
	}
	adapter := o.resolver.adapters[o.target.Wire]
	if o.backend == "gemini" && o.request.Kind == hosted.Search {
		adapter = o.resolver.adapters[google.WireAntigravity]
	}
	if adapter == nil {
		adapter = hostedStandardAdapter{}
	}
	in := inference.AdapterInput{Principal: o.request.Principal, RequestID: o.request.RequestID, Target: o.target, Credential: o.credential, Operation: op, Wire: wire, Body: raw}
	if p, ok := adapter.(inference.ContextPreparer); ok {
		ctx = p.PrepareContext(ctx)
	}
	state := hostedState{auth: o.resolver.auth, input: in}
	if p, ok := adapter.(inference.StatefulAdapter); ok {
		ctx, e = p.RestoreState(ctx, in, state)
		if e != nil {
			return hosted.OperationResult{}, e
		}
	}
	if p, ok := adapter.(inference.PreparingAdapter); ok {
		in.Body, e = p.Prepare(ctx, in, o.transport)
		if e != nil {
			return hosted.OperationResult{}, e
		}
	}
	var response *http.Response
	var responseWire inference.Wire
	if p, ok := adapter.(inference.ExecutingAdapter); ok {
		response, responseWire, e = p.Execute(ctx, in, o.transport, state)
	} else {
		var req inference.AdapterRequest
		req, e = adapter.Build(ctx, in)
		if e == nil && o.backend == "gemini" {
			var envelope map[string]any
			if json.Unmarshal(req.Body, &envelope) != nil {
				return hosted.OperationResult{}, inference.ErrUnavailable
			}
			native, ok := envelope["request"].(map[string]any)
			if !ok {
				return hosted.OperationResult{}, inference.ErrUnavailable
			}
			native["tools"] = []any{map[string]any{"google_search": map[string]any{}}}
			req.Body, e = json.Marshal(envelope)
		}
		if e == nil {
			response, e = o.transport.Do(ctx, req)
			responseWire = req.ResponseWire
		}
		if e == nil {
			if p, ok := adapter.(inference.ContextualAdapter); ok {
				response, e = p.DecodeWithInput(ctx, in, response)
			} else {
				response, e = adapter.Decode(ctx, response)
			}
		}
	}
	if e != nil {
		var validation hostedValidationRefusal
		if errors.As(e, &validation) && o.request.Kind == hosted.Search && o.backend == "gemini" {
			return o.recoverValidation(ctx, args, e)
		}
		return hosted.OperationResult{}, e
	}
	if response == nil || response.Body == nil {
		return hosted.OperationResult{}, inference.ErrUnavailable
	}
	if op == inference.Search {
		b, e := hostedRead(response.Body, 32<<20)
		if e != nil {
			return hosted.OperationResult{}, e
		}
		result := hosted.OperationResult{Output: string(b)}
		var obj any
		_ = json.Unmarshal(b, &obj)
		hostedSources(obj, &result.Sources, map[string]bool{})
		return result, nil
	}
	b, e := (hosted.StandardCodec{}).Collect(ctx, response, responseWire, 32<<20)
	if e != nil {
		return hosted.OperationResult{}, e
	}
	var obj map[string]any
	if json.Unmarshal(b, &obj) != nil {
		return hosted.OperationResult{}, inference.ErrUnavailable
	}
	result := hosted.OperationResult{}
	hostedSources(obj, &result.Sources, map[string]bool{})
	if choices, ok := obj["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if msg, ok := choice["message"].(map[string]any); ok {
				result.Output = hostedString(msg, "content")
				if parts, ok := msg["content"].([]any); ok {
					for _, part := range parts {
						if block, ok := part.(map[string]any); ok {
							if block["type"] == "text" || block["type"] == "output_text" {
								result.Output += hostedString(block, "text")
							}
						}
					}
				}
			}
		}
	}
	if o.request.Kind == hosted.Vision {
		chars := []rune(result.Output)
		if len(chars) > 2000 {
			result.Output = string(chars[:2000])
		}
	}
	if result.Output == "" {
		result.Failed = true
		result.Output = "The helper returned no text."
	}
	return result, nil
}

func (hostedStandardAdapter) DecodeWithInput(_ context.Context, in inference.AdapterInput, r *http.Response) (*http.Response, error) {
	if in.Target.Wire == inference.Chat {
		return policy.WrapChatResponse(r, in.Target.Model, policy.FreeformTools(in.Body)), nil
	}
	return r, nil
}
