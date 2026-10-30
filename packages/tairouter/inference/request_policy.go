// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"net/http"
)

// ApplyRequestPolicy transforms an already translated model body. Preflight GETs
// and non-model requests with no declared ResponseWire are left untouched. It
// never changes credentials, destination, method or physical-send accounting.
func ApplyRequestPolicy(t Target, r AdapterRequest) (AdapterRequest, error) {
	if r.Method == "GET" || r.ResponseWire == "" || t.Wire != Chat && t.Wire != Responses && t.Wire != Anthropic {
		return r, nil
	}
	body, e := decode(r.Body)
	if e != nil {
		return r, e
	}
	if t.Policy != nil {
		if r.ResponseWire == Chat {
			body, e = t.Policy.TransformChatRequest(body)
		} else {
			body, e = t.Policy.TransformRequest(body)
		}
	}
	if e != nil {
		return r, &Error{400, "invalid_provider_policy_request", 0}
	}
	if e = applyFastBody(t, body); e != nil {
		return r, e
	}
	r.Headers = r.Headers.Clone()
	if r.Headers == nil {
		r.Headers = make(http.Header)
	}
	if e = applyFastHeaders(t, r.Headers); e != nil {
		return r, e
	}
	r.Body, e = json.Marshal(body)
	return r, e
}

type responseRewriterKey struct{}

// RewriteProviderResponse runs after raw capture and before public translation.
// Its request-local closure must not be shared across concurrent model legs.
func (h *Handler) RewriteProviderResponse(ctx context.Context, body map[string]any) (map[string]any, error) {
	if rewrite, ok := ctx.Value(responseRewriterKey{}).(func(map[string]any) (map[string]any, error)); ok {
		return rewrite(body)
	}
	return body, nil
}
