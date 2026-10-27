// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/delinoio/oss/packages/aiprouter/inference/reasoningreplay"
)

type reasoningReplayKey struct{}

func replayPolicy(t Target, b object) reasoningreplay.Policy {
	p := reasoningreplay.DefaultPolicy(t.Model, len(arr(b["tools"])) > 0)
	if t.Policy != nil {
		meta, _ := decode(t.Policy.Metadata())
		includes := func(v any) bool {
			for _, value := range arr(v) {
				s := str(value)
				if s == t.Model || s == strings.SplitN(t.Model, ":", 2)[0] {
					return true
				}
			}
			return false
		}
		if values, ok := meta["preserveReasoningContentModels"]; ok {
			p.Preserve = includes(values)
		}
		if values, ok := meta["requiresReasoningPlaceholderModels"]; ok {
			p.Placeholder = includes(values)
		}
		p.Details = includes(meta["reasoningDetailsModels"])
	}
	return p
}
func (h *Handler) prepareReasoningReplay(ctx context.Context, p Principal, t Target, c Credential, body object) (context.Context, object, error) {
	if t.Wire != Chat {
		return ctx, body, nil
	}
	policy := replayPolicy(t, body)
	if !policy.Preserve || policy.Details {
		return ctx, body, nil
	}
	scope := scopedAdapterState{h: h, p: p, t: t, c: c}
	conversation := AffinitySession(ctx)
	if conversation == "" {
		for _, raw := range arr(body["messages"]) {
			message := obj(raw)
			if message["role"] == "assistant" && len(arr(message["tool_calls"])) > 0 && str(message["reasoning_content"]) == "" && str(message["reasoning"]) == "" {
				return ctx, nil, ErrContinuation
			}
		}
		return ctx, body, nil
	}
	digest := sha256.Sum256([]byte(scope.credentialBinding()))
	hooks := reasoningreplay.Hooks{
		Lookup: func(ctx context.Context, key string) (reasoningreplay.State, bool, error) {
			if e := h.guard(ctx, p); e != nil {
				return reasoningreplay.State{}, false, e
			}
			backend, ok := h.c.ProviderStates.(ProviderStateLookup)
			if !ok {
				return reasoningreplay.State{}, false, ErrContinuation
			}
			state, found, e := backend.LookupProviderState(ctx, scope.statePrincipal(), reasoningreplay.Namespace, scope.key(key))
			return reasoningreplay.State{Data: state.Data, ExpiresAt: state.ExpiresAt}, found, e
		},
		Save: func(ctx context.Context, key string, state reasoningreplay.State) error {
			if e := h.guard(ctx, p); e != nil {
				return e
			}
			if h.c.ProviderStates == nil {
				return ErrContinuation
			}
			return h.c.ProviderStates.SaveProviderState(ctx, scope.statePrincipal(), reasoningreplay.Namespace, scope.key(key), ProviderState{Data: state.Data, ExpiresAt: state.ExpiresAt})
		},
	}
	replay, e := reasoningreplay.New(hooks, reasoningreplay.Scope{OrganizationID: p.OrganizationID, UserID: p.UserID, ConversationID: conversation, Provider: t.Provider, AccountID: c.AccountID, Model: t.Model, Destination: t.BaseURL + "|" + t.Endpoint, Adapter: string(t.Wire), StateBinding: hex.EncodeToString(digest[:])})
	if e != nil {
		return ctx, nil, ErrContinuation
	}
	messages, e := replay.RepairChat(ctx, arr(body["messages"]), policy, 32<<20)
	if e != nil {
		return ctx, nil, replayError(e)
	}
	out := clone(body)
	out["messages"] = messages
	return context.WithValue(ctx, reasoningReplayKey{}, replay), out, nil
}
func replayError(e error) error {
	switch e {
	case reasoningreplay.ErrMissing, reasoningreplay.ErrScope, reasoningreplay.ErrState:
		return ErrContinuation
	case reasoningreplay.ErrBudget:
		return &Error{400, "reasoning_replay_budget_exceeded", 0}
	}
	return e
}

// CaptureReasoningReplay observes raw upstream Chat before response repair/stripping.
func (h *Handler) CaptureReasoningReplay(ctx context.Context, body object) error {
	if r, ok := ctx.Value(reasoningReplayKey{}).(*reasoningreplay.Replay); ok {
		return replayError(r.CaptureChat(ctx, body))
	}
	return nil
}

// NewReasoningStreamCapture observes raw native Chat events before translation.
// Call Finish on every exit; only Finish(success=true) may publish completed state.
func (h *Handler) NewReasoningStreamCapture(ctx context.Context) *reasoningreplay.StreamCapture {
	r, _ := ctx.Value(reasoningReplayKey{}).(*reasoningreplay.Replay)
	return reasoningreplay.NewStreamCapture(r, r != nil)
}
