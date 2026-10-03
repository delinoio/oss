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
	pr, err := tx.Get(domain.ProviderKind, i.Assignment.Configuration.ProviderID)
	if err != nil {
		return denied()
	}
	p, err := store.Decode[domain.Provider](pr)
	if err != nil || p.Protocol != domain.AnthropicMessages {
		return denied()
	}
	v := i.Assignment
	scope := apiproxy.Scope{ExecutionID: g.ExecutionID, SessionID: v.SessionID, AccountID: v.AccountID, ConnectionID: v.ConnectionID, ProviderID: v.Configuration.ProviderID, ModelID: v.Configuration.ModelID, NativeModel: v.Configuration.NativeModel, Provider: p, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}
	if scope.Validate() != nil {
		return denied()
	}
	return scope, nil
}
