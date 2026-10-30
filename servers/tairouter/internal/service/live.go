// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
	"time"
)

type storedLiveBinding struct {
	Version int
	Binding inference.LiveBinding
}

const liveBindingEnvelopeVersion = 1

func (a *inferenceAdapter) SaveBinding(ctx context.Context, p inference.Principal, id string, binding inference.LiveBinding) error {
	if e := a.BeforeSend(ctx, p); e != nil {
		return e
	}
	if binding.AccountID == "" || binding.ServedCredentialVersion == "" || !binding.ExpiresAt.After(time.Now()) || binding.ExpiresAt.After(time.Now().Add(24*time.Hour)) {
		return inference.ErrContinuation
	}
	l, e := a.current(p)
	if e != nil {
		return e
	}
	b, e := json.Marshal(storedLiveBinding{Version: liveBindingEnvelopeVersion, Binding: binding})
	if e != nil {
		return e
	}
	e = a.s.PutLiveAlias(ctx, l, "live/"+p.KeyID+"/"+id, binding.AccountID, binding.ServedCredentialVersion, b, binding.ExpiresAt)
	if errors.Is(e, store.ErrForbidden) || errors.Is(e, store.ErrNotFound) || errors.Is(e, store.ErrExpired) {
		return inference.ErrContinuation
	}
	return inferenceError(e)
}
func (a *inferenceAdapter) ResolveBinding(ctx context.Context, p inference.Principal, id string) (inference.LiveBinding, inference.Credential, error) {
	var stored storedLiveBinding
	b, e := a.s.GetState(ctx, scopeOf(p), core.LiveAlias, "live/"+p.KeyID+"/"+id)
	if e != nil {
		return stored.Binding, inference.Credential{}, inference.ErrContinuation
	}
	if json.Unmarshal(b, &stored) != nil || stored.Version != liveBindingEnvelopeVersion || stored.Binding.ServedCredentialVersion == "" || !stored.Binding.ExpiresAt.After(time.Now()) {
		return stored.Binding, inference.Credential{}, inference.ErrContinuation
	}
	d, version, e := a.providerVersion(ctx, scopeOf(p), stored.Binding.AccountID)
	if e != nil || version != stored.Binding.ServedCredentialVersion {
		return stored.Binding, inference.Credential{}, inference.ErrContinuation
	}
	// Compiled policy has private fields that JSON cannot restore. Rebuild it from
	// the bound account, then verify the persisted route before selecting it.
	saved := stored.Binding.Target
	target, e := selectedTargetForContext(ctx, d, saved)
	if e != nil || saved.Provider != stored.Binding.AccountID || !sameSavedLiveLane(saved, target) {
		return stored.Binding, inference.Credential{}, inference.ErrContinuation
	}
	stored.Binding.Target = target
	credential, e := a.SelectBound(ctx, p, target, stored.Binding.AccountID)
	if e != nil || !matchesServedCredential(stored.Binding, credential) {
		return stored.Binding, inference.Credential{}, inference.ErrContinuation
	}
	return stored.Binding, credential, nil
}

func sameSavedLiveLane(saved, rebuilt inference.Target) bool {
	if saved.Policy == nil || rebuilt.Policy == nil {
		return false
	}
	a, b := saved.Policy, rebuilt.Policy
	policyMatches := a.ProviderID == b.ProviderID && a.Model == b.Model && a.BaseURL == b.BaseURL && a.Endpoint == b.Endpoint && a.Wire == b.Wire && a.Inbound == b.Inbound && a.Auth == b.Auth && a.TransportMatchedRegistry == b.TransportMatchedRegistry
	return policyMatches && saved.Provider == rebuilt.Provider && saved.BaseURL == rebuilt.BaseURL && saved.Model == rebuilt.Model && saved.Wire == rebuilt.Wire && saved.Auth == rebuilt.Auth && saved.Endpoint == rebuilt.Endpoint && saved.Fast == rebuilt.Fast && selectionVersion(saved.FastPolicy) == selectionVersion(rebuilt.FastPolicy) && saved.ForceMessagesTranslation == rebuilt.ForceMessagesTranslation && selectionVersion(saved.ReasoningRetention) == selectionVersion(rebuilt.ReasoningRetention)
}

func matchesServedCredential(binding inference.LiveBinding, credential inference.Credential) bool {
	return binding.ServedCredentialVersion != "" && binding.AccountID == credential.AccountID && binding.ServedCredentialVersion == credential.CredentialVersion
}
