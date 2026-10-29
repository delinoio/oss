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
	e = a.s.PutAccountBoundState(ctx, l, core.Responses, "live/"+p.KeyID+"/"+id, binding.AccountID, binding.ServedCredentialVersion, b)
	if errors.Is(e, store.ErrForbidden) || errors.Is(e, store.ErrNotFound) || errors.Is(e, store.ErrExpired) {
		return inference.ErrContinuation
	}
	return inferenceError(e)
}
func (a *inferenceAdapter) ResolveBinding(ctx context.Context, p inference.Principal, id string) (inference.LiveBinding, inference.Credential, error) {
	var stored storedLiveBinding
	b, e := a.s.GetState(ctx, scopeOf(p), core.Responses, "live/"+p.KeyID+"/"+id)
	if e != nil {
		return stored.Binding, inference.Credential{}, inference.ErrContinuation
	}
	if json.Unmarshal(b, &stored) != nil || stored.Version != liveBindingEnvelopeVersion || stored.Binding.ServedCredentialVersion == "" || !stored.Binding.ExpiresAt.After(time.Now()) {
		return stored.Binding, inference.Credential{}, inference.ErrContinuation
	}
	_, version, e := a.s.GetPrivateVersion(ctx, scopeOf(p), string(providerKind), stored.Binding.AccountID)
	if e != nil || version != stored.Binding.ServedCredentialVersion {
		return stored.Binding, inference.Credential{}, inference.ErrContinuation
	}
	credential, e := a.SelectBound(ctx, p, stored.Binding.Target, stored.Binding.AccountID)
	if e != nil || !matchesServedCredential(stored.Binding, credential) {
		return stored.Binding, inference.Credential{}, inference.ErrContinuation
	}
	return stored.Binding, credential, nil
}

func matchesServedCredential(binding inference.LiveBinding, credential inference.Credential) bool {
	return binding.ServedCredentialVersion != "" && binding.AccountID == credential.AccountID && binding.ServedCredentialVersion == credential.CredentialVersion
}
