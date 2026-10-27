// SPDX-License-Identifier: Apache-2.0
// Replay behavior is adapted from opencodex; see LICENSE-MIT.

// Package reasoningreplay restores raw tool-call reasoning through encrypted
// temporary state. It does not validate signed tokens or keep a resident cache.
package reasoningreplay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	Namespace  = "tool-reasoning-replay"
	TTL        = time.Hour
	MaxEntries = 64
	MaxBytes   = 256 << 10
)

var (
	ErrScope   = errors.New("reasoning_replay_scope_required")
	ErrMissing = errors.New("reasoning_replay_unavailable")
	ErrState   = errors.New("invalid_reasoning_replay_state")
	ErrBudget  = errors.New("reasoning_replay_budget_exceeded")
)

// Scope must describe the selected physical route, not a pool or model alias.
// StateBinding is a non-secret credential-generation identity. Rebind on every
// retry/rotation. Destination includes any account-affecting route configuration.
type Scope struct {
	OrganizationID, UserID, ConversationID string
	Provider, AccountID, Model             string
	Destination, Adapter, StateBinding     string
}

type State struct {
	Data      []byte
	ExpiresAt time.Time
}

// Hooks must be bound to the authenticated principal and Namespace. Lookup must
// distinguish authoritative absence from authorization/storage errors. Save must
// encrypt state in backup-excluded PostgreSQL, atomically fence revocation and
// membership/account generations, and enforce TTL/MaxEntries/MaxBytes oldest-first
// per org/user across keys and conversations. Neither hook may log raw contents.
// Hooks must support concurrent callers; Replay itself holds no mutable state.
type Hooks struct {
	Lookup func(context.Context, string) (State, bool, error)
	Save   func(context.Context, string, State) error
}

type Replay struct {
	hooks Hooks
	scope Scope
	now   func() time.Time
}

func New(hooks Hooks, scope Scope) (*Replay, error) {
	for _, s := range []string{scope.OrganizationID, scope.UserID, scope.ConversationID, scope.Provider, scope.AccountID, scope.Model, scope.Destination, scope.Adapter, scope.StateBinding} {
		if strings.TrimSpace(s) == "" || !utf8.ValidString(s) {
			return nil, ErrScope
		}
	}
	if hooks.Lookup == nil || hooks.Save == nil {
		return nil, ErrState
	}
	return &Replay{hooks: hooks, scope: scope, now: time.Now}, nil
}

func (r *Replay) key(callID string) (string, error) {
	if r == nil || strings.TrimSpace(callID) == "" || !utf8.ValidString(callID) {
		return "", ErrScope
	}
	// JSON tuple encoding avoids delimiter collisions in caller-controlled IDs.
	b, _ := json.Marshal([]string{Namespace, r.scope.OrganizationID, r.scope.UserID, r.scope.ConversationID, r.scope.Provider, r.scope.AccountID, r.scope.Model, r.scope.Destination, r.scope.Adapter, r.scope.StateBinding, callID})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Capture records complete raw reasoning. Empty deltas never erase a candidate.
// Oversized optional captures are skipped, matching the pinned upstream bound.
func (r *Replay) Capture(ctx context.Context, callID, text string) error {
	key, err := r.key(callID)
	if err != nil {
		return err
	}
	if text == "" || len(text) > MaxBytes {
		return nil
	}
	if !utf8.ValidString(text) {
		return ErrState
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.hooks.Save(ctx, key, State{Data: []byte(text), ExpiresAt: r.now().Add(TTL)})
}

// Peek is non-consuming: retries can reuse a live record without extending TTL.
// Missing, expired, evicted, and scope-mismatched records return ErrMissing.
func (r *Replay) Peek(ctx context.Context, callID string) (string, error) {
	key, err := r.key(callID)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	state, found, err := r.hooks.Lookup(ctx, key)
	if err != nil {
		return "", err
	}
	if !found || !state.ExpiresAt.After(r.now()) {
		return "", ErrMissing
	}
	if len(state.Data) == 0 || len(state.Data) > MaxBytes || !utf8.Valid(state.Data) {
		return "", ErrState
	}
	return string(state.Data), nil
}

// ForCalls joins unique blocks in call order. Parallel calls often share a block.
// Unlike the upstream optional memory cache, required missing SQL context fails
// explicitly under the AIPRouter continuation contract; it is never fabricated.
func (r *Replay) ForCalls(ctx context.Context, callIDs []string) (string, error) {
	seen := make(map[string]bool)
	parts := make([]string, 0)
	bytes := 0
	for _, id := range callIDs {
		text, err := r.Peek(ctx, id)
		if err != nil {
			return "", err
		}
		if seen[text] {
			continue
		}
		bytes += len(text)
		if len(parts) > 0 {
			bytes++
		}
		if bytes > MaxBytes {
			return "", ErrBudget
		}
		seen[text] = true
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n"), nil
}
