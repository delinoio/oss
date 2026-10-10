// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"time"
)

// Registration supplies original native configuration only. inferenceScope
// rejects this job before acquiring any provider request or inference lease.
func (a *executionAuthority) directoryScope(tx *store.Tx, g store.ExecutionGrant, r store.Record, j domain.Job) (apiproxy.Scope, error) {
	denied := func() (apiproxy.Scope, error) { return apiproxy.Scope{}, executionDenied() }
	input, _, _, err := directoryClaimSource(tx, r, j)
	if err != nil || g.ServerEpoch != a.epoch || j.State != domain.JobClaimed || j.InstanceID != g.InstanceID || j.AssignedDeviceID != g.DeviceID || j.MachineID != g.MachineID || g.ExecutionID != input.GenerationID {
		return denied()
	}
	canceled, err := tx.JobCancellationRequested(r.ID)
	if err != nil || canceled {
		return denied()
	}
	instance, seen, err := tx.WorkerInstance(g.MachineID)
	if err != nil || instance != g.InstanceID || time.Since(seen) > domain.WorkerConnectionTimeout || seen.After(time.Now().UTC().Add(time.Second)) {
		return denied()
	}
	dr, err := tx.Get(domain.DeviceKind, g.DeviceID)
	if err != nil {
		return denied()
	}
	device, err := store.Decode[domain.Device](dr)
	if err != nil || device.Revoked || device.Type != domain.WorkerDevice || device.MachineID != g.MachineID {
		return denied()
	}
	v := input.Assignment
	scope := apiproxy.Scope{ContextRevision: input.ContextRevision, ExecutionID: g.ExecutionID, SessionID: v.SessionID, AccountID: v.AccountID, ConnectionID: v.ConnectionID, ModelID: v.Configuration.ModelID, NativeModel: v.Configuration.NativeModel, Harness: v.Configuration.Harness}
	if v.Configuration.Subscription {
		_, account, err := accountFromTx(tx, v.AccountID, 0)
		if err != nil || account.Subscription == nil || account.Subscription.RecoveryRequired || account.Subscription.Lease == nil || account.Connection == nil || account.Connection.ID != v.ConnectionID || account.SubscriptionService != v.Configuration.SubscriptionService {
			return denied()
		}
		state, lease := account.Subscription, account.Subscription.Lease
		if lease.Action != domain.SubscriptionExecute || lease.OperationID != g.JobID || lease.MachineID != g.MachineID || lease.InstanceID != g.InstanceID || lease.DeviceID != g.DeviceID || lease.Epoch != a.service.subscriptionServerEpoch() || lease.Generation != state.Generation {
			return denied()
		}
		scope.SubscriptionService = v.Configuration.SubscriptionService
		return scope, nil
	}
	pr, err := tx.Get(domain.ProviderKind, v.Configuration.ProviderID)
	if err != nil {
		return denied()
	}
	provider, err := store.Decode[domain.Provider](pr)
	if err != nil {
		return denied()
	}
	_, account, err := executionAccountFromTx(tx, v.AccountID, v.ConnectionID)
	if err != nil {
		return denied()
	}
	provider, err = providers.ResolveAccountProfile(provider, account)
	if err != nil {
		return denied()
	}
	scope.ProviderID = v.Configuration.ProviderID
	scope.Provider = provider
	// Existing proxy configuration requires its closed protocol adapter. These
	// descriptors are not request authority: this typed job has no inferenceScope.
	scope.Operations = executionAPIOperations(v, provider.Protocol)
	if scope.Validate() != nil {
		return denied()
	}
	return scope, nil
}
