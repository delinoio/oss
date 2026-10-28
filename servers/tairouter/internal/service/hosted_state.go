// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"time"
)

// Helper protocol state uses the helper's account and credential digest. Parent
// model continuation identifiers cannot satisfy a helper state lookup.
type hostedState struct {
	auth  *inferenceAdapter
	input inference.AdapterInput
}

func (s hostedState) key(id string) string {
	return selectionVersion([]any{s.input.Principal, s.input.Target.Provider, s.input.Target.BaseURL, s.input.Target.Model, s.input.Credential.AccountID, s.input.Credential.Secret, id})
}
func (s hostedState) Load(ctx context.Context, id string) ([]byte, error) {
	b, found, e := s.Lookup(ctx, id)
	if e == nil && !found {
		e = inference.ErrContinuation
	}
	return b, e
}
func (s hostedState) Lookup(ctx context.Context, id string) ([]byte, bool, error) {
	v, found, e := s.auth.LookupProviderState(ctx, s.input.Principal, string(s.input.Target.Wire), s.key(id))
	return v.Data, found, e
}
func (s hostedState) Save(ctx context.Context, id string, data []byte, ttl time.Duration) error {
	return s.auth.SaveProviderState(ctx, s.input.Principal, string(s.input.Target.Wire), s.key(id), inference.ProviderState{Data: data, ExpiresAt: time.Now().Add(ttl)})
}
func (s hostedState) Update(ctx context.Context, id string, ttl time.Duration, merge func([]byte) ([]byte, error)) error {
	return s.auth.UpdateProviderState(ctx, s.input.Principal, string(s.input.Target.Wire), s.key(id), time.Now().Add(ttl), merge)
}
