// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"time"
)

type storedLiveBinding struct {
	Binding           inference.LiveBinding
	CredentialVersion string
}

func (a *inferenceAdapter) SaveBinding(ctx context.Context, p inference.Principal, id string, binding inference.LiveBinding) error {
	if e := a.BeforeSend(ctx, p); e != nil {
		return e
	}
	if binding.AccountID == "" || !binding.ExpiresAt.After(time.Now()) || binding.ExpiresAt.After(time.Now().Add(24*time.Hour)) {
		return inference.ErrContinuation
	}
	_, version, e := a.s.GetPrivateVersion(ctx, scopeOf(p), string(providerKind), binding.AccountID)
	if e != nil {
		return inferenceError(e)
	}
	b, e := json.Marshal(storedLiveBinding{binding, version})
	if e != nil {
		return e
	}
	return a.s.PutState(ctx, scopeOf(p), core.Responses, "live/"+p.KeyID+"/"+id, b)
}
func (a *inferenceAdapter) ResolveBinding(ctx context.Context, p inference.Principal, id string) (inference.LiveBinding, inference.Credential, error) {
	var stored storedLiveBinding
	b, e := a.s.GetState(ctx, scopeOf(p), core.Responses, "live/"+p.KeyID+"/"+id)
	if e != nil {
		return stored.Binding, inference.Credential{}, inference.ErrContinuation
	}
	if json.Unmarshal(b, &stored) != nil || !stored.Binding.ExpiresAt.After(time.Now()) {
		return stored.Binding, inference.Credential{}, inference.ErrContinuation
	}
	_, version, e := a.s.GetPrivateVersion(ctx, scopeOf(p), string(providerKind), stored.Binding.AccountID)
	if e != nil || version != stored.CredentialVersion {
		return stored.Binding, inference.Credential{}, inference.ErrContinuation
	}
	credential, e := a.SelectBound(ctx, p, stored.Binding.Target, stored.Binding.AccountID)
	if e != nil || credential.AccountID != stored.Binding.AccountID {
		return stored.Binding, inference.Credential{}, inference.ErrContinuation
	}
	return stored.Binding, credential, nil
}
