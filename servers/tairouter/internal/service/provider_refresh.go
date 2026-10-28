// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/delinoio/oss/packages/tairouter/providers"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

// refreshProviderCredential reloads the claimed version rather than refreshing a
// request snapshot. Claims survive process loss: an ambiguous rotating grant must
// never be replayed merely because a timeout or another replica released a lock.
func refreshProviderCredential(ctx context.Context, s *store.Store, lease store.Lease, connectionID string) (providerData, error) {
	return refreshProviderCredentialWithExecutor(ctx, s, lease, connectionID, nil)
}

func refreshProviderCredentialWithExecutor(ctx context.Context, s *store.Store, lease store.Lease, connectionID string, testExecutor providers.Executor) (providerData, error) {
	var d providerData
	raw, version, claim, err := s.ClaimPrivateRefresh(ctx, lease, string(providerKind), connectionID)
	if err != nil {
		return d, err
	}
	var sent atomic.Bool
	complete := false
	defer func() {
		if complete {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if sent.Load() {
			_ = s.MarkPrivateRefreshUncertain(cleanup, lease, string(providerKind), connectionID, claim)
		} else {
			_ = s.ReleasePrivateRefresh(cleanup, lease, string(providerKind), connectionID, claim)
		}
	}()
	if err = json.Unmarshal(raw, &d); err != nil {
		return providerData{}, err
	}
	if d.Metadata == nil || d.Metadata.State != v1.ConnectionState_CONNECTION_STATE_READY || d.Credential == nil || d.Credential.Scope != lease.Principal.Scope {
		return providerData{}, providers.ErrReauthenticate
	}
	executor := &providerExecutor{store: s, scope: lease.Principal.Scope, test: testExecutor, onSend: func() { sent.Store(true) }, gate: func(c context.Context, start func(context.Context) error) error {
		return s.WithPrivateRefreshDispatch(c, lease, string(providerKind), connectionID, claim, start)
	}}
	fresh, err := providers.NewAuth(executor).Refresh(ctx, lease.Principal.Scope, d.Metadata.ProviderId, *d.Credential)
	if err != nil {
		return providerData{}, err
	}
	// Vertex location is owner-selected configuration, not token-bound evidence.
	// Copy only that field from the claimed version; profiles and discovery must
	// be rebuilt for the new token, and no implicit OAuth region is introduced.
	if d.Metadata.ProviderId == "google-vertex" {
		if location, selected := d.Credential.Metadata["location"]; selected {
			if fresh.Metadata == nil {
				fresh.Metadata = make(map[string]string)
			}
			fresh.Metadata["location"] = location
		}
	}
	d.Credential = &fresh
	// Discovery evidence is bound to the old token/account generation. Obtain fresh
	// evidence before it can authorize an account-specific model again.
	d.Models = nil
	d.ModelsDiscoveredAt = time.Time{}
	d.CodexCatalog = nil
	catalog, renewed, discoveryErr := providers.NewAuth(executor).Discover(ctx, lease.Principal.Scope, providerConnection(d))
	if discoveryErr == nil {
		if renewed != nil {
			d.Credential = renewed
		}
		d.Models = &catalog
		d.ModelsDiscoveredAt = time.Now()
		d.CodexCatalog = catalog.CodexCatalog
	} else if d.Metadata.ProviderId == "zed" {
		return providerData{}, discoveryErr
	}
	raw, err = json.Marshal(d)
	if err != nil {
		return providerData{}, err
	}
	if err = s.CompletePrivateRefresh(ctx, lease, string(providerKind), connectionID, version, claim, raw); err != nil {
		return providerData{}, err
	}
	complete = true
	return d, nil
}
