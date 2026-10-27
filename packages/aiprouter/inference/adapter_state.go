// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

type ProviderState struct {
	Data      []byte
	ExpiresAt time.Time
}

// ProviderStates stores encrypted temporary provider state. Implementations must atomically
// enforce live membership/generation and key revocation, TTL, and oldest-first limits per
// org/user/namespace. Cursor namespace: 15min,64 entries,16MiB total. No backup inclusion.
type ProviderStates interface {
	LoadProviderState(context.Context, Principal, string, string) (ProviderState, error)
	SaveProviderState(context.Context, Principal, string, string, ProviderState) error
}

// AdapterStateStore is already bound to principal, provider, exact account, model and
// credential digest. IDs must be public conversation IDs, never raw credential values.
type AdapterStateStore interface {
	Load(context.Context, string) ([]byte, error)
	Save(context.Context, string, []byte, time.Duration) error
}
type scopedAdapterState struct {
	h *Handler
	p Principal
	t Target
	c Credential
}

func (s scopedAdapterState) key(id string) string {
	sum := sha256.Sum256([]byte(s.p.OrganizationID + "\x00" + s.p.UserID + "\x00" + s.p.KeyID + "\x00" + strconv.FormatInt(s.p.MembershipGeneration, 10) + "\x00" + s.t.Provider + "\x00" + s.t.BaseURL + "\x00" + s.t.Model + "\x00" + s.c.AccountID + "\x00" + s.credentialBinding() + "\x00" + id))
	return hex.EncodeToString(sum[:])
}
func (s scopedAdapterState) Load(ctx context.Context, id string) ([]byte, error) {
	if s.h.c.ProviderStates == nil {
		return nil, ErrContinuation
	}
	if e := s.h.guard(ctx, s.p); e != nil {
		return nil, e
	}
	state, e := s.h.c.ProviderStates.LoadProviderState(ctx, s.statePrincipal(), string(s.t.Wire), s.key(id))
	if e != nil || !state.ExpiresAt.After(time.Now()) {
		return nil, ErrContinuation
	}
	return state.Data, nil
}
func (s scopedAdapterState) Save(ctx context.Context, id string, data []byte, ttl time.Duration) error {
	if s.h.c.ProviderStates == nil {
		return ErrContinuation
	}
	maxBytes := 32 << 20
	maxTTL := 24 * time.Hour
	if s.t.Wire == Wire("cursor-agent") {
		maxBytes = 16 << 20
		maxTTL = 15 * time.Minute
	}
	if len(data) > maxBytes || ttl <= 0 || ttl > maxTTL {
		return &Error{400, "invalid_provider_state", 0}
	}
	if e := s.h.guard(ctx, s.p); e != nil {
		return e
	}
	return s.h.c.ProviderStates.SaveProviderState(ctx, s.statePrincipal(), string(s.t.Wire), s.key(id), ProviderState{Data: data, ExpiresAt: time.Now().Add(ttl)})
}

// AdapterStateLookup distinguishes absent state from failed authorization/storage.
// Adapters may type-assert it on AdapterStateStore for optional session continuity.
type AdapterStateLookup interface {
	Lookup(context.Context, string) ([]byte, bool, error)
}

// ProviderStateLookup must return found=false only after authoritative ownership
// validation. It must never turn database errors or revoked ownership into absence.
type ProviderStateLookup interface {
	LookupProviderState(context.Context, Principal, string, string) (ProviderState, bool, error)
}

func (s scopedAdapterState) Lookup(ctx context.Context, id string) ([]byte, bool, error) {
	if s.h.c.ProviderStates == nil {
		return nil, false, ErrUnavailable
	}
	if e := s.h.guard(ctx, s.p); e != nil {
		return nil, false, e
	}
	backend, ok := s.h.c.ProviderStates.(ProviderStateLookup)
	if !ok {
		return nil, false, ErrUnsupported
	}
	state, found, e := backend.LookupProviderState(ctx, s.statePrincipal(), string(s.t.Wire), s.key(id))
	if e != nil {
		return nil, false, e
	}
	if !found || !state.ExpiresAt.After(time.Now()) {
		return nil, false, nil
	}
	return state.Data, true, nil
}

// AdapterStateUpdater atomically merges bounded state after authoritative validation.
// merge is pure; nil current means absent/expired. It must not perform network I/O.
type AdapterStateUpdater interface {
	Update(context.Context, string, time.Duration, func([]byte) ([]byte, error)) error
}

// ProviderStateUpdater serializes read/merge/write with deletion and current owner
// generation in one storage transaction. Expired state is passed as nil.
type ProviderStateUpdater interface {
	UpdateProviderState(context.Context, Principal, string, string, time.Time, func([]byte) ([]byte, error)) error
}

func (s scopedAdapterState) Update(ctx context.Context, id string, ttl time.Duration, merge func([]byte) ([]byte, error)) error {
	if e := s.h.guard(ctx, s.p); e != nil {
		return e
	}
	if ttl <= 0 || ttl > 24*time.Hour || merge == nil {
		return &Error{400, "invalid_provider_state", 0}
	}
	backend, ok := s.h.c.ProviderStates.(ProviderStateUpdater)
	if !ok {
		return ErrUnsupported
	}
	maxBytes := 32 << 20
	if s.t.Wire == Wire("cursor-agent") {
		maxBytes = 16 << 20
		if ttl > 15*time.Minute {
			return &Error{400, "invalid_provider_state", 0}
		}
	}
	return backend.UpdateProviderState(ctx, s.statePrincipal(), string(s.t.Wire), s.key(id), time.Now().Add(ttl), func(current []byte) ([]byte, error) {
		next, e := merge(current)
		if e != nil {
			return nil, e
		}
		if len(next) > maxBytes {
			return nil, &Error{400, "invalid_provider_state", 0}
		}
		return next, nil
	})
}

func (s scopedAdapterState) credentialBinding() string {
	if s.c.StateBinding != "" {
		return s.c.StateBinding
	}
	// OAuth token renewal preserves the connected account identity. The credential
	// service must reject refreshes that change that upstream identity.
	if s.t.Auth == OAuth {
		return s.c.AccountID
	}
	return s.c.Secret
}

func (s scopedAdapterState) statePrincipal() Principal {
	p := s.p
	p.AccountID = s.c.AccountID
	p.CredentialVersion = s.c.CredentialVersion
	return p
}
