// SPDX-License-Identifier: Apache-2.0
package zed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

const PolicyHeader = "X-Tairouter-Zed-Policy"
const StateTTL = 24 * time.Hour
const MaxStateBytes = 4096

// ModelPolicy is trusted selected-route metadata. It contains no credentials,
// prompts or thread identifiers and is removed before the physical HTTP send.
type ModelPolicy struct {
	ReasoningEfforts      []string          `json:"reasoning_efforts"`
	ReasoningEffortMap    map[string]string `json:"reasoning_effort_map,omitempty"`
	ShowThinkingSummaries bool              `json:"show_thinking_summaries,omitempty"`
}

var (
	_ inference.ContextPreparer    = (*Adapter)(nil)
	_ inference.ContextualAdapter  = (*Adapter)(nil)
	_ inference.StatefulAdapter    = (*Adapter)(nil)
	_ inference.HTTPRecoveryPolicy = (*Adapter)(nil)
)

func optionsForInput(ctx context.Context, in inference.AdapterInput) (context.Context, error) {
	options := requestOptions(ctx)
	if value := in.Target.Headers.Get(PolicyHeader); value != "" {
		if len(value) > 16<<10 {
			return nil, requestError("zed_policy_too_large")
		}
		var policy ModelPolicy
		d := json.NewDecoder(strings.NewReader(value))
		d.DisallowUnknownFields()
		var extra any
		if d.Decode(&policy) != nil || d.Decode(&extra) != io.EOF {
			return nil, requestError("invalid_zed_policy")
		}
		if len(policy.ReasoningEfforts) > 16 || len(policy.ReasoningEffortMap) > 32 {
			return nil, requestError("invalid_zed_policy")
		}
		options.ReasoningEfforts = policy.ReasoningEfforts
		options.ReasoningEffortMap = policy.ReasoningEffortMap
		options.ShowThinkingSummaries = policy.ShowThinkingSummaries
	}
	return WithRequestOptions(ctx, options), nil
}

func inputBinding(in inference.AdapterInput) [32]byte {
	// Hash structured values rather than delimiter-concatenated caller strings.
	b, _ := json.Marshal([]any{in.Principal, in.Target.Provider, in.Target.BaseURL, in.Target.Model, in.Credential.AccountID, in.Credential.Secret, in.RequestID, in.Wire, in.Body})
	return sha256.Sum256(b)
}

type savedState struct {
	Version  int      `json:"version"`
	Provider Provider `json:"family"`
	ThreadID string   `json:"thread_id"`
}

// RestoreState only reads the executor-supplied, exact-owner/account/model store.
// Missing, expired or malformed continuation state never becomes a fresh thread.
func (a *Adapter) RestoreState(ctx context.Context, in inference.AdapterInput, store inference.AdapterStateStore) (context.Context, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var err error
	ctx, err = optionsForInput(ctx, in)
	if err != nil {
		return nil, err
	}
	if _, ok := ctx.Value(stateKey{}).(*requestState); !ok {
		ctx = a.PrepareContext(ctx)
	}
	options := requestOptions(ctx)
	if in.PreviousResponseID != "" {
		if store == nil {
			return nil, inference.ErrContinuation
		}
		data, err := store.Load(ctx, in.PreviousResponseID)
		if err != nil {
			return nil, err
		}
		if len(data) > MaxStateBytes {
			return nil, inference.ErrContinuation
		}
		var saved savedState
		d := json.NewDecoder(bytes.NewReader(data))
		d.DisallowUnknownFields()
		var extra any
		if d.Decode(&saved) != nil || d.Decode(&extra) != io.EOF || saved.Version != 1 || saved.Provider != a.provider || !validThread(saved.ThreadID) {
			return nil, inference.ErrContinuation
		}
		if options.ThreadID != "" && options.ThreadID != saved.ThreadID {
			return nil, inference.ErrContinuation
		}
		options.ThreadID = saved.ThreadID
	} else if options.ThreadID == "" {
		// Sanitized client identifiers are correlation hints, not storage authority.
		// Domain separation prevents different owners/accounts from sharing a Zed thread.
		for _, header := range []string{"thread-id", "session-id", "session_id"} {
			if hint := in.ClientHeaders.Get(header); hint != "" {
				if len(hint) > 2048 || strings.ContainsAny(hint, "\x00\r\n") {
					return nil, requestError("invalid_zed_thread_id")
				}
				identity, _ := json.Marshal([]any{"zed-thread-v1", in.Principal.OrganizationID, in.Principal.UserID, in.Principal.MembershipGeneration, in.Target.Provider, in.Target.BaseURL, in.Target.Model, in.Credential.AccountID, hint})
				digest := sha256.Sum256(identity)
				options.ThreadID = hex.EncodeToString(digest[:])
				break
			}
		}
	}
	if options.ThreadID != "" && !validThread(options.ThreadID) {
		return nil, requestError("invalid_zed_thread_id")
	}
	return WithRequestOptions(ctx, options), nil
}

func validThread(id string) bool {
	return id != "" && len(id) <= 512 && !strings.ContainsAny(id, "\x00\r\n")
}

func (a *Adapter) CaptureState(ctx context.Context, in inference.AdapterInput, responseID string, store inference.AdapterStateStore) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if responseID == "" {
		return nil
	}
	if len(responseID) > 2048 || strings.ContainsAny(responseID, "\x00\r\n") {
		return inference.ErrContinuation
	}
	state, _ := ctx.Value(stateKey{}).(*requestState)
	if state == nil {
		return inference.ErrContinuation
	}
	state.mu.Lock()
	thread, built, binding := state.threadID, state.built, state.binding
	state.mu.Unlock()
	if !built || binding != inputBinding(in) || !validThread(thread) || store == nil {
		return inference.ErrContinuation
	}
	data, err := json.Marshal(savedState{Version: 1, Provider: a.provider, ThreadID: thread})
	if err != nil {
		return err
	}
	return store.Save(ctx, responseID, data, StateTTL)
}

// DecodeWithInput binds restoration to the exact immutable attempt. The fallback
// supports generic hosts which call ContextualAdapter but omit ContextPreparer.
func (a *Adapter) DecodeWithInput(ctx context.Context, in inference.AdapterInput, response *http.Response) (*http.Response, error) {
	state, _ := ctx.Value(stateKey{}).(*requestState)
	if state == nil {
		ctx = a.PrepareContext(ctx)
		if _, err := a.Build(ctx, in); err != nil {
			return nil, err
		}
		state = ctx.Value(stateKey{}).(*requestState)
	}
	state.mu.Lock()
	valid := state.built && state.binding == inputBinding(in)
	state.mu.Unlock()
	if !valid {
		return nil, requestError("zed_response_context_mismatch")
	}
	return a.Decode(ctx, response)
}

// RecoverHTTP is advice only. The executor owns refresh I/O, current authority,
// response closure and the original send budget. Never replay a second expiry.
func (a *Adapter) RecoverHTTP(ctx context.Context, in inference.AdapterInput, failure inference.HTTPFailure) (inference.RecoveryDecision, error) {
	if err := ctx.Err(); err != nil {
		return inference.RecoveryDecision{}, err
	}
	if !NeedsTokenRefresh(&http.Response{StatusCode: failure.Status, Header: failure.Header}) {
		return inference.RecoveryDecision{Action: inference.RecoveryDefault}, nil
	}
	if failure.Refreshed {
		status := failure.Status
		if status < 400 || status > 599 {
			status = 502
		}
		return inference.RecoveryDecision{Action: inference.RecoveryStop, Error: &inference.Error{Status: status, Code: "zed_token_refresh_exhausted"}}, nil
	}
	return inference.RecoveryDecision{Action: inference.RecoveryRefresh}, nil
}
