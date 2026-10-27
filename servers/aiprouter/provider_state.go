// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/store"
	"time"
)

var _ inference.ProviderStates = (*inferenceAdapter)(nil)
var _ inference.ProviderStateLookup = (*inferenceAdapter)(nil)
var _ inference.ProviderStateUpdater = (*inferenceAdapter)(nil)

func statePolicy(namespace string) (store.ProviderStatePolicy, error) {
	p, ok := providerStatePolicy(namespace)
	if !ok {
		return store.ProviderStatePolicy{}, inference.ErrUnsupported
	}
	return store.ProviderStatePolicy{TTL: p.TTL, Entries: p.Entries, MaxEntryBytes: p.MaxEntryBytes, MaxTotalBytes: p.MaxTotalBytes}, nil
}
func statePrincipal(p inference.Principal) store.Principal {
	return store.Principal{Scope: scopeOf(p), KeyID: p.KeyID, AccountID: p.AccountID, CredentialVersion: p.CredentialVersion}
}
func (a *inferenceAdapter) LookupProviderState(ctx context.Context, p inference.Principal, namespace, id string) (inference.ProviderState, bool, error) {
	if _, e := statePolicy(namespace); e != nil {
		return inference.ProviderState{}, false, e
	}
	value, e := a.s.GetProviderState(ctx, statePrincipal(p), namespace, id)
	// Store distinguishes authoritative absence from denied ownership under one gate.
	if errors.Is(e, store.ErrNotFound) {
		return inference.ProviderState{}, false, nil
	}
	if e != nil {
		return inference.ProviderState{}, false, inferenceError(e)
	}
	return inference.ProviderState{Data: value.Data, ExpiresAt: value.ExpiresAt}, true, nil
}
func (a *inferenceAdapter) LoadProviderState(ctx context.Context, p inference.Principal, namespace, id string) (inference.ProviderState, error) {
	value, found, e := a.LookupProviderState(ctx, p, namespace, id)
	if e != nil {
		return value, e
	}
	if !found {
		return value, inference.ErrContinuation
	}
	return value, nil
}
func (a *inferenceAdapter) SaveProviderState(ctx context.Context, p inference.Principal, namespace, id string, value inference.ProviderState) error {
	policy, e := statePolicy(namespace)
	if e != nil {
		return e
	}
	if len(id) > 512 || id == "" || int64(len(value.Data)) > policy.MaxEntryBytes {
		return &inference.Error{Status: 400, Code: "invalid_provider_state"}
	}
	return inferenceError(a.s.PutProviderState(ctx, statePrincipal(p), namespace, id, store.ProviderState{Data: value.Data, ExpiresAt: value.ExpiresAt}, policy))
}
func (a *inferenceAdapter) UpdateProviderState(ctx context.Context, p inference.Principal, namespace, id string, expires time.Time, merge func([]byte) ([]byte, error)) error {
	policy, e := statePolicy(namespace)
	if e != nil {
		return e
	}
	ttl := time.Until(expires)
	if len(id) > 512 || id == "" || merge == nil || ttl <= 0 || ttl > policy.TTL {
		return &inference.Error{Status: 400, Code: "invalid_provider_state"}
	}
	policy.TTL = ttl
	// The callback runs inside the store's owner/key transaction. No network is done here.
	var mergeError error
	err := a.s.UpdateProviderState(ctx, statePrincipal(p), namespace, id, policy, func(current []byte) ([]byte, error) { next, e := merge(current); mergeError = e; return next, e })
	if mergeError != nil {
		return mergeError
	}
	return inferenceError(err)
}
