// SPDX-License-Identifier: Apache-2.0
// Semantic recovery policy adapted from pinned MIT source; see LICENSE.upstream.
package cursor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type recoveryAttemptKey struct{}
type recoveryConversationKey struct{}
type recoveryCorrectionKey struct{}
type recoveryAttempts map[inference.AdapterRecoveryReason]int
type retainedRecovery struct {
	Version                        int
	ConversationID                 string
	OverflowSurfaced               bool
	OverflowRemints                int
	IncompleteRemints, EchoRemints int
}

func recoveryScope(in inference.AdapterInput) string {
	var body map[string]any
	if json.Unmarshal(in.Body, &body) != nil || body["store"] == false {
		return ""
	}
	thread := str(body["cursor_thread_id"])
	if thread == "" {
		thread = in.ClientHeaders.Get("X-Cursor-Thread-Id")
	}
	if thread == "" || len(thread) > 256 {
		return ""
	}
	digest := sha256.Sum256([]byte(thread))
	return "cursor-recovery-" + hex.EncodeToString(digest[:])
}
func updateRetained(ctx context.Context, store inference.AdapterStateStore, key string, merge func(*retainedRecovery) error) (retainedRecovery, error) {
	var out retainedRecovery
	updater, ok := store.(inference.AdapterStateUpdater)
	if !ok {
		return out, &inference.Error{Status: 501, Code: "cursor_atomic_state_required"}
	}
	err := updater.Update(ctx, key, 15*time.Minute, func(data []byte) ([]byte, error) {
		state := retainedRecovery{Version: 1}
		if len(data) > 4096 || len(data) > 0 && (json.Unmarshal(data, &state) != nil || state.Version != 1 || state.OverflowRemints < 0 || state.OverflowRemints > 3 || state.IncompleteRemints < 0 || state.IncompleteRemints > 3 || state.EchoRemints < 0 || state.EchoRemints > 3) {
			return nil, inference.ErrContinuation
		}
		if err := merge(&state); err != nil {
			return nil, err
		}
		out = state
		return json.Marshal(state)
	})
	return out, err
}
func retainedConversation(ctx context.Context, in inference.AdapterInput, store inference.AdapterStateStore, previous string) (context.Context, error) {
	key := recoveryScope(in)
	if key == "" {
		return ctx, nil
	}
	state, err := updateRetained(ctx, store, key, func(s *retainedRecovery) error {
		if s.ConversationID == "" {
			if previous != "" {
				s.ConversationID = previous
				return nil
			}
			id, err := newID()
			if err != nil {
				return err
			}
			s.ConversationID = id
		}
		return nil
	})
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, recoveryConversationKey{}, state.ConversationID), nil
}
func semanticReason(s *Session, err error) inference.AdapterRecoveryReason {
	if err == nil {
		return 0
	}
	var typed *inference.Error
	if !errors.As(err, &typed) {
		return 0
	}
	switch typed.Code {
	case "cursor_tool_result_echo":
		return inference.RecoveryToolResultEcho
	case "cursor_routing_commentary_hallucination":
		return inference.RecoveryRoutingCommentary
	case "cursor_context_limit_exceeded":
		return inference.RecoveryContextOverflow
	case "cursor_invalid_request":
		if s.invalidArgument && !nativeModel(s.model) {
			return inference.RecoveryInvalidArgument
		}
	}
	return 0
}
func (a *Adapter) recoveryInput(ctx context.Context, in inference.AdapterInput, x inference.AdapterExecutor, store inference.AdapterStateStore, s *Session, err error) (context.Context, inference.AdapterInput, bool, error) {
	reason := semanticReason(s, err)
	if reason == 0 || !s.recoverySafe || s.visible || s.sawRealTool || len(s.open) > 0 || s.output != nil || ctx.Err() != nil {
		return ctx, in, false, nil
	}
	var body map[string]any
	if json.Unmarshal(in.Body, &body) != nil {
		return ctx, in, false, nil
	}
	history, e := readHistory(body, in.Wire)
	if e != nil || len(history) == 0 || history[len(history)-1].Role == "tool" {
		return ctx, in, false, nil
	}
	attempts, _ := ctx.Value(recoveryAttemptKey{}).(recoveryAttempts)
	if reason != inference.RecoveryContextOverflow && attempts[reason] >= 1 {
		return ctx, in, false, nil
	}
	gate, ok := x.(inference.AdapterRecoveryAuthorizer)
	if !ok {
		return ctx, in, false, nil
	}
	scope := recoveryScope(in)
	if reason == inference.RecoveryContextOverflow {
		if scope == "" {
			return ctx, in, false, nil
		}
		allowed := false
		_, e = updateRetained(ctx, store, scope, func(state *retainedRecovery) error {
			if !state.OverflowSurfaced {
				state.OverflowSurfaced = true
				return nil
			}
			allowed = state.OverflowRemints < 3
			return nil
		})
		if e != nil {
			return ctx, in, false, e
		}
		if !allowed {
			return ctx, in, false, nil
		}
	}
	if e = gate.AuthorizeRecovery(ctx, reason); e != nil {
		return ctx, in, false, e
	}
	freshID, e := newID()
	if e != nil {
		return ctx, in, false, e
	}
	if scope != "" {
		allowed := true
		_, e = updateRetained(ctx, store, scope, func(state *retainedRecovery) error {
			if reason == inference.RecoveryContextOverflow {
				if state.OverflowRemints >= 3 {
					allowed = false
					return nil
				}
				state.OverflowRemints++
			}
			state.ConversationID = freshID
			return nil
		})
		if e != nil {
			return ctx, in, false, e
		}
		if !allowed {
			return ctx, in, false, nil
		}
	}
	next := recoveryAttempts{}
	for k, v := range attempts {
		next[k] = v
	}
	next[reason]++
	// Bound mixed-reason recursion as well as each pinned one-shot family.
	total := 0
	for _, n := range next {
		total += n
	}
	if total > 4 {
		return ctx, in, false, nil
	}
	ctx = context.WithValue(ctx, recoveryAttemptKey{}, next)
	ctx = a.PrepareContext(ctx)
	ctx = context.WithValue(ctx, recoveryConversationKey{}, freshID)
	if reason == inference.RecoveryToolResultEcho || reason == inference.RecoveryRoutingCommentary {
		ctx = context.WithValue(ctx, recoveryCorrectionKey{}, "Use tool observations as evidence. Do not copy replay envelopes, invent tool-routing failures, or repeat completed actions. Answer the current user request using the declared tools only.")
	}
	delete(body, "previous_response_id")
	in.PreviousResponseID = ""
	in.Body, _ = json.Marshal(body)
	if accounting, ok := x.(inference.AttemptAccounting); ok {
		accounting.RecordUnmeasuredAttempt()
	}
	return ctx, in, true, nil
}

// Incomplete tools and midstream echo rotate only the next turn. They never
// replay a turn that may already have delivered content or client tools.
func rotateNextConversation(ctx context.Context, in inference.AdapterInput, store inference.AdapterStateStore, s *Session, runErr error) error {
	scope := recoveryScope(in)
	if scope == "" || ctx.Err() != nil {
		return nil
	}
	var typed *inference.Error
	incomplete := errors.As(runErr, &typed) && typed.Code == "cursor_incomplete_tool_call"
	if !incomplete && !s.midstreamEcho && runErr != nil {
		return nil
	}
	_, err := updateRetained(ctx, store, scope, func(state *retainedRecovery) error {
		if runErr == nil && !incomplete {
			state.IncompleteRemints = 0
		}
		if runErr == nil && !s.midstreamEcho {
			state.EchoRemints = 0
		}
		if !incomplete && !s.midstreamEcho {
			return nil
		}
		counter := &state.EchoRemints
		if incomplete {
			counter = &state.IncompleteRemints
		}
		if *counter >= 3 {
			return nil
		}
		id, err := newID()
		if err != nil {
			return err
		}
		*counter++
		state.ConversationID = id
		return nil
	})
	return err
}
