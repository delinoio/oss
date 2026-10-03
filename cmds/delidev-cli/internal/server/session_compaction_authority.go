// SPDX-License-Identifier: Apache-2.0
package server

import (
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func (a *executionAuthority) compactionScope(tx *store.Tx, g store.ExecutionGrant, r store.Record, j domain.Job) (apiproxy.Scope, error) {
	denied := func() (apiproxy.Scope, error) { return apiproxy.Scope{}, executionDenied() }
	var i domain.SessionCompactionInput
	if g.ServerEpoch != a.epoch || j.State != domain.JobClaimed || j.InstanceID != g.InstanceID || j.AssignedDeviceID != g.DeviceID || j.MachineID != g.MachineID || domain.Decode(j.Input, &i) != nil || i.Validate() != nil || g.ExecutionID != i.ActionID || i.Assignment.SessionID != r.SessionID {
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
	_, machine, err := activeMachine(tx, g.MachineID)
	if err != nil {
		return denied()
	}
	if i.Assignment.Configuration.Harness == domain.Codex && (!machineCapabilityContains(machine.WorkerCapabilities, domain.NativeSessionCompactionV1) || !machineCapabilityContains(machine.WorkerCapabilities, domain.CodexSessionCompactionV1)) {
		return denied()
	}
	if i.Assignment.Configuration.Harness == domain.OpenCode && (!machineCapabilityContains(machine.WorkerCapabilities, domain.NativeSessionCompactionV1) || !machineCapabilityContains(machine.WorkerCapabilities, domain.OpenCodeSessionCompactionV1)) {
		return denied()
	}
	sr, s, err := sessionRecord(tx, r.SessionID)
	if err != nil || s.CompactionJobID != r.ID || s.Archive != domain.NotArchived || s.Recovery != domain.NoRecovery || !s.OwnsExecution(i.Assignment) || s.ActiveExecutionID != "" {
		return denied()
	}
	if _, err := checkedExecutionAssignment(tx, sr, s, machine, i.Assignment); err != nil {
		return denied()
	}
	if err := tx.RequireSessionBudget(sr.ID, s.EstimatedCostBudget); err != nil {
		return denied()
	}
	v := i.Assignment
	if v.Configuration.Subscription {
		_, account, err := accountFromTx(tx, v.AccountID, 0)
		if err != nil || v.Configuration.Harness != domain.Codex || v.Configuration.SubscriptionService != domain.SubscriptionChatGPT || account.SubscriptionService != v.Configuration.SubscriptionService || account.ProviderID != "" || account.Type != domain.SubscriptionAccount || account.Subscription == nil || account.Subscription.RecoveryRequired || account.Subscription.Lease == nil {
			return denied()
		}
		state, lease := account.Subscription, account.Subscription.Lease
		if lease.Action != domain.SubscriptionExecute || lease.OperationID != g.JobID || lease.MachineID != g.MachineID || lease.InstanceID != g.InstanceID || lease.DeviceID != g.DeviceID || lease.Epoch != a.service.subscriptionServerEpoch() || lease.Generation != state.Generation {
			return denied()
		}
		return apiproxy.Scope{ExecutionID: g.ExecutionID, SessionID: v.SessionID, AccountID: v.AccountID, ConnectionID: v.ConnectionID, SubscriptionService: v.Configuration.SubscriptionService, ModelID: v.Configuration.ModelID, NativeModel: v.Configuration.NativeModel, Harness: v.Configuration.Harness}, nil
	}
	pr, err := tx.Get(domain.ProviderKind, i.Assignment.Configuration.ProviderID)
	if err != nil {
		return denied()
	}
	p, err := store.Decode[domain.Provider](pr)
	if err != nil {
		return denied()
	}
	operations := executionAPIOperations(v, p.Protocol)
	if len(operations) == 0 {
		return denied()
	}
	sourceTurn := domain.NativeIdentity("")
	if v.Configuration.Harness == domain.Codex {
		sourceTurn = i.Completion.NativeTurnID
	}
	scope := apiproxy.Scope{CompactionSourceTurn: sourceTurn, ExecutionID: g.ExecutionID, SessionID: v.SessionID, AccountID: v.AccountID, ConnectionID: v.ConnectionID, ProviderID: v.Configuration.ProviderID, ModelID: v.Configuration.ModelID, NativeModel: v.Configuration.NativeModel, Provider: p, Harness: v.Configuration.Harness, Operations: operations}
	if scope.Validate() != nil {
		return denied()
	}
	return scope, nil
}
