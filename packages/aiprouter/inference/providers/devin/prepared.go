// SPDX-License-Identifier: Apache-2.0
package devin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

// BuildPrepared supplements Adapter.Build with an immutable account catalog and
// request-local decode state. The executor must bind options to the same owner,
// membership generation, account and tenant as in.Credential before this call.
func (a *Adapter) BuildPrepared(ctx context.Context, in inference.AdapterInput, options Options) (inference.AdapterRequest, *ResponseContext, error) {
	var body object
	if len(in.Body) > maxFrame || json.Unmarshal(in.Body, &body) != nil {
		return inference.AdapterRequest{}, nil, badRequest()
	}
	var state *ResponseContext
	if in.Operation == inference.Generate {
		var err error
		state, err = planTools(body)
		if err != nil {
			return inference.AdapterRequest{}, nil, err
		}
		state.Inbound = in.Wire
		model := in.Target.Model
		if model == "" {
			model = str(body["model"])
		}
		effort := str(body["reasoning_effort"])
		if effort == "" {
			effort = str(obj(body["reasoning"])["effort"])
		}
		selected, err := ResolveModel(options.Catalog, model, effort)
		if err != nil {
			return inference.AdapterRequest{}, nil, err
		}
		state.Model = selected.UID
	}
	req, err := a.build(ctx, in, options)
	return req, state, err
}

type responseContextKey struct{}

// DecodePrepared restores tool identity and orders Messages reasoning before
// visible output. It still returns Chat SSE, as declared by ResponseWire.
func (a *Adapter) DecodePrepared(ctx context.Context, response *http.Response, state *ResponseContext) (*http.Response, error) {
	return a.Decode(context.WithValue(ctx, responseContextKey{}, state), response)
}

type Signature struct{ Value, Type string }

func EncodeSignature(s Signature) string {
	if s.Type != "" && !strings.Contains(s.Type, ":") {
		return "devin-sig1:" + s.Type + ":" + s.Value
	}
	return s.Value
}
func DecodeSignature(stored string) Signature {
	if strings.HasPrefix(stored, "devin-sig1:") {
		typ, value, ok := strings.Cut(strings.TrimPrefix(stored, "devin-sig1:"), ":")
		if ok && typ != "" {
			return Signature{Value: value, Type: typ}
		}
	}
	return Signature{Value: stored}
}
