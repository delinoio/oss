// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	"google.golang.org/protobuf/proto"
)

// A stale family refusal admits one physical model send, not a usage HTTP probe.
// The permit remains PostgreSQL-owned across instances and credential changes.
type nativeFamilyReceipt struct {
	Account, Version, Family string
	Permit                   store.NativeHealthPermit
	Bindings                 []store.NativeHealthBinding
	Finished                 bool
}

func nativeFamilyKey(id, version, family string) store.NativeHealthKey {
	return store.NativeHealthKey{AccountID: id, CredentialVersion: version, Model: family, Lane: store.NativeHealthFamily}
}
func (a *inferenceAdapter) nativeFamilyEligible(ctx context.Context, p inference.Principal, c store.SelectionCandidate, model string) (bool, error) {
	family := nativeModelFamily(model)
	if family == "" {
		return true, nil
	}
	lease, err := a.current(p)
	if err != nil {
		return false, err
	}
	health, err := a.s.GetNativeHealth(ctx, lease, nativeFamilyKey(c.ID, c.Version, family))
	if err != nil {
		return false, inferenceError(err)
	}
	return !health.CooldownUntil.After(health.Now) && health.ProbeID == "", nil
}
func (a *inferenceAdapter) dispatchNativeFamily(ctx context.Context, p inference.Principal, start func(context.Context) error) (bool, error) {
	a.mu.Lock()
	target := a.activeTarget
	id, version := a.activeAccount, a.activeVersion
	d := a.connections[id]
	state := a.native[selectionKey(target)]
	selection := a.selections[selectionKey(target)]
	a.mu.Unlock()
	if target.Auth != inference.OAuth || d.Metadata == nil || (d.Metadata.ProviderId != "anthropic" && d.Metadata.ProviderId != "anthropic2") {
		return false, nil
	}
	family := nativeModelFamily(target.Model)
	if family == "" {
		return false, nil
	}
	lease, err := a.current(p)
	if err != nil {
		return true, err
	}
	if state.ConfigGroup == "" {
		state.ConfigGroup, state.Version = selection.request.GroupID, selection.request.Version
	}
	bindings, err := a.nativeFamilyBindings(ctx, p, state)
	if err != nil {
		return true, err
	}
	key := nativeFamilyKey(id, version, family)
	health, err := a.s.GetNativeHealth(ctx, lease, key)
	if err != nil {
		return true, inferenceError(err)
	}
	permit, err := a.s.AcquireNativeHealthPermitBound(ctx, lease, key, health.ProbeEligible, bindings)
	if errors.Is(err, store.ErrBusy) {
		return true, nativeHealthWithheld(health.Now, health.CooldownUntil)
	}
	if err != nil {
		return true, inferenceError(err)
	}
	receipt := &nativeFamilyReceipt{Account: id, Version: version, Family: family, Permit: permit, Bindings: bindings}
	state.Family = receipt
	a.mu.Lock()
	if a.native == nil {
		a.native = map[string]nativeSelectionState{}
	}
	a.native[selectionKey(target)] = state
	a.mu.Unlock()
	sent := false
	err = a.s.WithNativeHealthDispatch(ctx, lease, []store.NativeHealthPermit{permit}, func(locked context.Context) error { sent = true; return start(locked) })
	if !sent {
		_ = a.releaseTerminalNativeHealthPermit(ctx, lease, permit)
	}
	return true, inferenceError(err)
}
func (a *inferenceAdapter) observeNativeFamily(ctx context.Context, p inference.Principal, target inference.Target, c inference.Credential, status int, h http.Header) error {
	a.mu.Lock()
	d := a.connections[c.AccountID]
	state := a.native[selectionKey(target)]
	receipt := state.Family
	finished := receipt != nil && receipt.Finished
	selection := a.selections[selectionKey(target)]
	a.mu.Unlock()
	if target.Auth != inference.OAuth || d.Metadata == nil || (d.Metadata.ProviderId != "anthropic" && d.Metadata.ProviderId != "anthropic2") {
		return nil
	}
	lease, err := a.current(p)
	if err != nil {
		return err
	}
	var bindings []store.NativeHealthBinding
	if receipt != nil {
		bindings = receipt.Bindings
	} else {
		if state.ConfigGroup == "" {
			state.ConfigGroup, state.Version = selection.request.GroupID, selection.request.Version
		}
		bindings, err = a.nativeFamilyBindings(ctx, p, state)
		if err != nil {
			return err
		}
	}
	rejected := status == 429 && strings.TrimSpace(h.Get("anthropic-ratelimit-unified-7d_oi-status")) == "rejected"
	// The pinned header is attributed only to Fable, even on another model's request.
	if rejected {
		reset := nativeResetHeader(h, "anthropic-ratelimit-unified-7d_oi-reset")
		var resetAt *time.Time
		if reset != "" {
			resetAt = nativeReset(reset)
		}
		reject := func(current store.NativeHealth) (store.NativeHealth, error) {
			current.CooldownSince = current.Now
			current.CooldownUntil = current.Now.Add(30 * time.Minute)
			if resetAt != nil && !resetAt.IsZero() && resetAt.Before(current.CooldownUntil) {
				current.CooldownUntil = *resetAt
			}
			current.ProbeEligible = true
			return current, nil
		}
		if receipt != nil && !finished && receipt.Account == c.AccountID && receipt.Version == c.CredentialVersion && receipt.Family == "fable" {
			err = a.s.FinishNativeHealthPermit(ctx, lease, receipt.Permit, reject)
			a.mu.Lock()
			receipt.Finished = err == nil
			a.mu.Unlock()
			// A newly received refusal remains authoritative even if an earlier
			// successful sibling advanced the admission revision. Only success needs
			// the captured revision to protect a newer refusal from being cleared.
			if errors.Is(err, store.ErrConflict) {
				err = a.s.UpdateNativeHealthBound(ctx, lease, nativeFamilyKey(c.AccountID, c.CredentialVersion, "fable"), bindings, reject)
			}
			if errors.Is(err, store.ErrExpired) {
				return nil
			}
			return inferenceError(err)
		}
		err = a.s.UpdateNativeHealthBound(ctx, lease, nativeFamilyKey(c.AccountID, c.CredentialVersion, "fable"), bindings, reject)
		if err != nil {
			return inferenceError(err)
		}
	}

	if receipt == nil || finished || receipt.Account != c.AccountID || receipt.Version != c.CredentialVersion {
		return nil
	}
	err = a.s.FinishNativeHealthPermit(ctx, lease, receipt.Permit, func(current store.NativeHealth) (store.NativeHealth, error) {
		if status >= 200 && status < 300 {
			current.CooldownUntil = time.Time{}
			current.CooldownSince = time.Time{}
			current.ProbeEligible = false
		}
		return current, nil
	})
	a.mu.Lock()
	receipt.Finished = err == nil
	a.mu.Unlock()
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrExpired) {
		return nil
	}
	return inferenceError(err)
}

// Capture the same bytes used for the selection fingerprint and their durable
// row versions. The permit validates these versions again under the physical
// write transaction and completion transaction, closing the read/send race.
func (a *inferenceAdapter) nativeFamilyBindings(ctx context.Context, p inference.Principal, s nativeSelectionState) ([]store.NativeHealthBinding, error) {
	parts := strings.Split(s.ConfigGroup, "/")
	if (len(parts) != 2 && len(parts) != 4) || parts[0] != "route" || (len(parts) == 4 && parts[2] != "pool") {
		return nil, inference.ErrNoCredential
	}
	raw, version, e := a.s.GetPrivateVersion(ctx, scopeOf(p), string(routeKind), parts[1])
	if e != nil {
		return nil, inferenceError(e)
	}
	var route v1.Route
	if proto.Unmarshal(raw, &route) != nil {
		return nil, inference.ErrUnavailable
	}
	if !route.Enabled {
		return nil, inference.ErrNoCredential
	}
	sort.SliceStable(route.Targets, func(i, j int) bool { return route.Targets[i].Priority < route.Targets[j].Priority })
	bindings := []store.NativeHealthBinding{{Kind: string(routeKind), ID: parts[1], Version: version}}
	fingerprint := selectionVersion(&route)
	if len(parts) == 4 {
		raw, version, e = a.s.GetPrivateVersion(ctx, scopeOf(p), string(poolKind), parts[3])
		if e != nil {
			return nil, inferenceError(e)
		}
		var pool v1.Pool
		if json.Unmarshal(raw, &pool) != nil {
			return nil, inference.ErrUnavailable
		}
		bindings = append(bindings, store.NativeHealthBinding{Kind: string(poolKind), ID: parts[3], Version: version})
		fingerprint = selectionVersion(struct {
			Route *v1.Route
			Pool  *v1.Pool
		}{&route, &pool})
	}
	if fingerprint != s.Version {
		return nil, inference.ErrNoCredential
	}
	return bindings, nil
}

// Called only after the model transport has terminated without response headers.
// A failed detached cleanup remains owned by the durable admission-release queue.
func (a *inferenceAdapter) finishNativeFamilyTransport(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential) error {
	a.mu.Lock()
	receipt := a.native[selectionKey(t)].Family
	if receipt == nil || receipt.Finished || receipt.Account != c.AccountID || receipt.Version != c.CredentialVersion {
		a.mu.Unlock()
		return nil
	}
	permit := receipt.Permit
	a.mu.Unlock()
	l, e := a.current(p)
	if e != nil {
		return e
	}
	e = a.releaseTerminalNativeHealthPermit(ctx, l, permit)
	if e == nil {
		a.mu.Lock()
		receipt.Finished = true
		a.mu.Unlock()
	}
	return e
}
