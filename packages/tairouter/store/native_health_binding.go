// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

type NativeHealthBinding struct{ Kind, ID, Version string }

func encodeHealthBindings(b []NativeHealthBinding) []byte {
	if b == nil {
		return []byte(`[]`)
	}
	raw, _ := json.Marshal(b)
	return raw
}
func (s *Store) checkNativeHealthBindings(ctx context.Context, tx *sql.Tx, l Lease, bindings []NativeHealthBinding) error {
	if len(bindings) > 64 {
		return ErrConflict
	}
	sc := l.Principal.Scope
	seen := map[string]string{}
	var unique []NativeHealthBinding
	for _, b := range bindings {
		key := b.Kind + "/" + b.ID
		if (b.Kind != "route" && b.Kind != "pool") || b.ID == "" || b.Version == "" {
			return ErrConflict
		}
		if version, exists := seen[key]; exists {
			if version != b.Version {
				return ErrConflict
			}
			continue
		}
		seen[key] = b.Version
		if len(seen) > 8 {
			return ErrConflict
		}
		unique = append(unique, b)
	}
	for _, b := range unique {
		var valid bool
		if e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM private_data WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4 AND version=$5)", sc.OrganizationID, sc.UserID, b.Kind, b.ID, b.Version).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return ErrForbidden
		}
	}
	return nil
}
func (s *Store) UpdateNativeHealthBound(ctx context.Context, l Lease, k NativeHealthKey, bindings []NativeHealthBinding, merge func(NativeHealth) (NativeHealth, error)) error {
	bindings = append(append([]NativeHealthBinding(nil), dispatchBindings(ctx)...), bindings...)
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.healthAuthority(ctx, tx, l, k); e != nil {
			return e
		}
		if e := s.checkNativeHealthBindings(ctx, tx, l, bindings); e != nil {
			return e
		}
		h, e := s.readHealth(ctx, tx, l, k)
		if e != nil {
			return e
		}
		return s.mergeHealth(ctx, tx, l, k, h, merge, false)
	})
}

// PrivateBinding is the common private-record version fence used by routing.
type PrivateBinding = NativeHealthBinding

type dispatchBindingsContextKey struct{}

// WithDispatchBindings adds immutable configuration fences to existing send gates.
// It does not acquire locks, so native health/family gates can consume it without
// nested organization transactions. Outer bindings cannot be removed by wrapping.
func WithDispatchBindings(ctx context.Context, bindings []NativeHealthBinding) context.Context {
	all := append([]NativeHealthBinding(nil), dispatchBindings(ctx)...)
	all = append(all, bindings...)
	return context.WithValue(ctx, dispatchBindingsContextKey{}, all)
}
func dispatchBindings(ctx context.Context) []NativeHealthBinding {
	b, _ := ctx.Value(dispatchBindingsContextKey{}).([]NativeHealthBinding)
	return b
}

func (s *Store) WithAccountBoundDispatch(ctx context.Context, l Lease, account, credentialVersion string, bindings []PrivateBinding, start func(context.Context) error) error {
	return s.WithAccountDispatch(WithDispatchBindings(ctx, bindings), l, account, credentialVersion, start)
}
