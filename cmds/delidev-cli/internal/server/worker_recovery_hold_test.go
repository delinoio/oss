// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// TestAccountWorkerRecoveryHoldClassifiesEveryOriginalWorkerState pins the closed
// table of states that only the original Worker device can complete.
func TestAccountWorkerRecoveryHoldClassifiesEveryOriginalWorkerState(t *testing.T) {
	machine, device := domain.NewID(), domain.NewID()
	lease := func(action domain.SubscriptionAction) *domain.SubscriptionLease {
		return &domain.SubscriptionLease{ID: domain.NewID(), Action: action, MachineID: machine, DeviceID: device}
	}
	account := func(service domain.SubscriptionService, state domain.SubscriptionState) domain.Account {
		return domain.Account{Type: domain.SubscriptionAccount, SubscriptionService: service, Subscription: &state}
	}
	tests := map[string]struct {
		account domain.Account
		kind    workerRecoveryHoldKind
		held    bool
	}{
		"live execute lease, any provider": {account(domain.SubscriptionChatGPT, domain.SubscriptionState{Lease: lease(domain.SubscriptionExecute)}), holdLiveExecuteLease, true},
		"fenced chatgpt execute lease":     {account(domain.SubscriptionChatGPT, domain.SubscriptionState{RecoveryRequired: true, Lease: lease(domain.SubscriptionExecute)}), holdFencedLease, true},
		"fenced grok execute lease":        {account(domain.SubscriptionGrok, domain.SubscriptionState{RecoveryRequired: true, Lease: lease(domain.SubscriptionExecute)}), holdFencedLease, true},
		"fenced claude execute lease":      {account(domain.SubscriptionClaude, domain.SubscriptionState{RecoveryRequired: true, Lease: lease(domain.SubscriptionExecute)}), holdFencedLease, true},
		"fenced claude lifecycle lease":    {account(domain.SubscriptionClaude, domain.SubscriptionState{RecoveryRequired: true, Lease: lease(domain.SubscriptionRefresh)}), holdFencedLease, true},
		"committed recovery settlement": {account(domain.SubscriptionChatGPT, domain.SubscriptionState{RecoveryRequired: true, RecoverySettlement: &domain.SubscriptionRecoverySettlement{
			Request: domain.NewID(), Lease: domain.NewID(), Machine: machine, Device: device, Outcome: domain.RecoveryRestored}}), holdRecoverySettlement, true},
		"fenced chatgpt lifecycle lease is never offered back": {account(domain.SubscriptionChatGPT, domain.SubscriptionState{RecoveryRequired: true, Lease: lease(domain.SubscriptionRefresh)}), "", false},
		"live lifecycle lease":                                 {account(domain.SubscriptionChatGPT, domain.SubscriptionState{Lease: lease(domain.SubscriptionLogin)}), "", false},
		"fence without lease or settlement":                    {account(domain.SubscriptionChatGPT, domain.SubscriptionState{RecoveryRequired: true}), "", false},
		"no lease":                                             {account(domain.SubscriptionChatGPT, domain.SubscriptionState{}), "", false},
		"no subscription state":                                {domain.Account{Type: domain.SubscriptionAccount}, "", false},
	}
	for name, test := range tests {
		hold, held := accountWorkerRecoveryHold(domain.NewID(), test.account)
		if held != test.held || held && (hold.Kind != test.kind || hold.Machine != machine || hold.Device != device) {
			t.Fatalf("%s: got %+v held=%v, want kind %q held=%v", name, hold, held, test.kind, test.held)
		}
	}
}

func removeFixtureWorker(t *testing.T, f *codexRecoveryFixture) error {
	t.Helper()
	machine, err := f.service.Store.Get(context.Background(), domain.MachineKind, f.input.MachineID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.RemoveWorker(context.Background(), connect.NewRequest(removeWorkerRequest(rpc.Resource(machine))))
	return err
}

// TestWorkerRemovalWaitsForEveryOriginalWorkerState covers the live and the fenced
// lease, which a committed settlement alone did not.
func TestWorkerRemovalWaitsForEveryOriginalWorkerState(t *testing.T) {
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	retired := func() bool {
		machine, err := f.service.Store.Get(context.Background(), domain.MachineKind, f.input.MachineID)
		if err != nil {
			t.Fatal(err)
		}
		value, err := store.Decode[domain.Machine](machine)
		if err != nil {
			t.Fatal(err)
		}
		return value.Retired || value.Disabled
	}
	if err := removeFixtureWorker(t, f); connect.CodeOf(err) != connect.CodeAborted || retired() {
		t.Fatal("removal retired a Worker that still runs a live managed execution lease", err)
	}
	f.fence()
	if err := removeFixtureWorker(t, f); connect.CodeOf(err) != connect.CodeAborted || retired() {
		t.Fatal("removal retired a Worker whose fenced lease has not committed a settlement", err)
	}
	device, err := f.service.Store.Get(context.Background(), domain.DeviceKind, f.device)
	if err != nil {
		t.Fatal(err)
	}
	// Revocation is the emergency cut-off and is never refused by a held state.
	if _, err := f.service.RevokeDevice(context.Background(), connect.NewRequest(&pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.device), ExpectedRevision: device.Revision}})); err != nil {
		t.Fatal("revocation was refused while the Worker held a fenced lease", err)
	}
	if _, a := f.record(); a.Subscription.Lease == nil || !a.Subscription.RecoveryRequired {
		t.Fatal("revocation released the fenced lease")
	}
	// With the original device revoked, nothing is left for removal to strand, so a
	// permanently lost Worker can still be removed; the account stays fenced.
	if err := removeFixtureWorker(t, f); err != nil {
		t.Fatal("removal stayed blocked on a state whose original device is revoked", err)
	}
	if _, a := f.record(); a.Subscription.Lease == nil || !a.Subscription.RecoveryRequired {
		t.Fatal("removal released the fenced lease")
	}
}
