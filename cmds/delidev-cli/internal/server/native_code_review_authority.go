// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"time"
)

func (a *executionAuthority) nativeReviewScope(tx *store.Tx, g store.ExecutionGrant, r store.Record, j domain.Job) (apiproxy.Scope, error) {
	denied := func() (apiproxy.Scope, error) { return apiproxy.Scope{}, executionDenied() }
	input, err := nativeReviewAuthority(tx, r, j)
	if err != nil || g.ServerEpoch != a.epoch || j.State != domain.JobClaimed || j.InstanceID != g.InstanceID || j.AssignedDeviceID != g.DeviceID || j.MachineID != g.MachineID || g.ExecutionID != input.ActionID {
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
	v := input.Source
	if v.Configuration.Subscription {
		_, account, err := accountFromTx(tx, v.AccountID, 0)
		if err != nil || account.Subscription == nil || account.Subscription.Lease == nil {
			return denied()
		}
		state, lease := account.Subscription, account.Subscription.Lease
		if lease.Action != domain.SubscriptionExecute || lease.OperationID != g.JobID || lease.MachineID != g.MachineID || lease.InstanceID != g.InstanceID || lease.DeviceID != g.DeviceID || lease.Epoch != a.service.subscriptionServerEpoch() || lease.Generation != state.Generation || state.Generation != input.SubscriptionGeneration {
			return denied()
		}
		return apiproxy.Scope{ExecutionID: input.ActionID, SessionID: v.SessionID, AccountID: v.AccountID, ConnectionID: v.ConnectionID, SubscriptionService: v.Configuration.SubscriptionService, ModelID: v.Configuration.ModelID, NativeModel: v.Configuration.NativeModel, Harness: domain.Codex, Purpose: domain.NativeCodeReviewUsage}, nil
	}
	_, account, err := executionAccountFromTx(tx, v.AccountID, v.ConnectionID)
	if err != nil || !account.Enabled || account.Type != domain.APIAccount || account.Health != domain.AccountReady || account.Removal != nil || account.ConfirmedExhausted {
		return denied()
	}
	pr, err := tx.Get(domain.ProviderKind, v.Configuration.ProviderID)
	if err != nil {
		return denied()
	}
	provider, err := store.Decode[domain.Provider](pr)
	if err == nil {
		provider, err = providers.ResolveAccountProfile(provider, account)
	}
	if err != nil || provider.Protocol != domain.OpenAIResponses {
		return denied()
	}
	scope := apiproxy.Scope{ContextRevision: input.ContextRevision, ExecutionID: input.ActionID, SessionID: v.SessionID, AccountID: v.AccountID, ConnectionID: v.ConnectionID, ProviderID: v.Configuration.ProviderID, ModelID: v.Configuration.ModelID, NativeModel: v.Configuration.NativeModel, Harness: domain.Codex, Purpose: domain.NativeCodeReviewUsage, Provider: provider, Operations: []apiproxy.Operation{apiproxy.ResponseCreate}}
	if scope.Validate() != nil {
		return denied()
	}
	return scope, nil
}
