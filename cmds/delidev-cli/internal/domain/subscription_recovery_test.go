// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
	"time"
)

func recoveryState(t *testing.T) (Account, *SubscriptionState) {
	t.Helper()
	now := time.Now().UTC()
	lease := &SubscriptionLease{ID: NewID(), OperationID: NewID(), Revision: 1, Action: SubscriptionExecute, MachineID: NewID(), InstanceID: NewID(), DeviceID: NewID(), Epoch: NewID(), Generation: NewID(), StartedAt: now}
	state := &SubscriptionState{Generation: lease.Generation, IdentityCommitment: strings.Repeat("a", 64), Lease: lease, RecoveryRequired: true}
	account := Account{Type: SubscriptionAccount, SubscriptionService: SubscriptionChatGPT, Connection: &AccountConnection{ID: NewID(), Authentication: SubscriptionAuth, ConnectedAt: now}, Health: AccountFailed, Subscription: state}
	return account, state
}

func TestRecoveryStateValidation(t *testing.T) {
	account, state := recoveryState(t)
	if err := state.Validate(account); err != nil {
		t.Fatal("the fenced baseline is invalid", err)
	}
	capture := &SubscriptionRecoveryCapture{ID: NewID(), Lease: state.Lease.ID, Generation: state.Generation, IdentityCommitment: state.IdentityCommitment, SyncedAt: time.Now().UTC()}
	state.RecoveryCapture = capture
	if err := state.Validate(account); err != nil {
		t.Fatal("a capture bound to the held lease is invalid", err)
	}
	if capture.OriginalID() == capture.ID || capture.OriginalID().Validate() != nil || RecoveryOriginalReference(capture.OriginalID()) != capture.ID {
		t.Fatal("original reference derivation is not a distinct reversible canonical identifier")
	}
	for name, mutate := range map[string]func(){
		"foreign lease": func() {
			state.RecoveryCapture = &SubscriptionRecoveryCapture{ID: capture.ID, Lease: NewID(), Generation: capture.Generation, IdentityCommitment: capture.IdentityCommitment, SyncedAt: capture.SyncedAt}
		},
		"replaced generation": func() {
			state.RecoveryCapture = &SubscriptionRecoveryCapture{ID: capture.ID, Lease: capture.Lease, Generation: NewID(), IdentityCommitment: capture.IdentityCommitment, SyncedAt: capture.SyncedAt}
		},
		"no lease":       func() { state.Lease = nil },
		"claude account": func() { account.SubscriptionService = SubscriptionClaude },
	} {
		account, state = recoveryState(t)
		state.RecoveryCapture = &SubscriptionRecoveryCapture{ID: capture.ID, Lease: state.Lease.ID, Generation: state.Generation, IdentityCommitment: state.IdentityCommitment, SyncedAt: capture.SyncedAt}
		mutate()
		if state.Validate(account) == nil {
			t.Fatal("invalid recovery capture was accepted:", name)
		}
	}
}

func TestRecoverySettlementAndBlockValidation(t *testing.T) {
	account, state := recoveryState(t)
	lease := state.Lease
	state.RecoveryBlock = &SubscriptionRecoveryBlock{Reason: RecoveryCredentialProofUnavailable, Request: NewID(), ObservedAt: time.Now().UTC()}
	if err := state.Validate(account); err != nil {
		t.Fatal("a closed block reason on a fenced lease is invalid", err)
	}
	state.RecoveryBlock.Reason = "free text reason"
	if state.Validate(account) == nil {
		t.Fatal("an open-ended block reason was accepted")
	}
	state.RecoveryBlock = nil
	// A committed restore keeps the fence until retirement, holds no lease and names the published generation.
	published := NewID()
	state.Lease, state.Generation = nil, published
	state.RecoverySettlement = &SubscriptionRecoverySettlement{Request: published, Lease: lease.ID, Machine: lease.MachineID, Device: lease.DeviceID, Outcome: RecoveryRestored, Generation: published, StartedAt: time.Now().UTC()}
	if err := state.Validate(account); err != nil {
		t.Fatal("a restored settlement checkpoint is invalid", err)
	}
	state.Generation = NewID()
	if state.Validate(account) == nil {
		t.Fatal("a settlement checkpoint with a different generation was accepted")
	}
	state.Generation = published
	state.RecoveryRequired = false
	if state.Validate(account) == nil {
		t.Fatal("a settlement checkpoint without the recovery fence was accepted")
	}
	// A disconnected settlement holds no connection.
	account, state = recoveryState(t)
	state.Lease = nil
	state.RecoverySettlement = &SubscriptionRecoverySettlement{Request: NewID(), Lease: lease.ID, Machine: lease.MachineID, Device: lease.DeviceID, Outcome: RecoveryDisconnected, StartedAt: time.Now().UTC()}
	if state.Validate(account) == nil {
		t.Fatal("a disconnected settlement with a live connection was accepted")
	}
	account.Connection = nil
	if err := state.Validate(account); err != nil {
		t.Fatal("a disconnected settlement checkpoint is invalid", err)
	}
}
