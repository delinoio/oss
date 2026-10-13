// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"testing"
	"time"
)

func accessCreditFixture() ServerCreditOperation {
	return ServerCreditOperation{AccessOnly: true, ID: NewID(), Epoch: NewID(), AttemptID: NewID(), AttemptEpoch: NewID(), FinishID: NewID(), ConnectionID: NewID(), Generation: NewID(), Actor: Principal{Type: OwnerDevice}, Phase: SubscriptionObservationQueued, RequestedAt: time.Now().UTC(), NextCredit: true, CreditsObservationID: NewID()}
}

func automaticProofFixture(o ServerCreditOperation) *ServerAutomaticCreditProof {
	return &ServerAutomaticCreditProof{EpisodeID: NewID(), Epoch: o.Epoch, LeaseID: NewID(), OperationID: NewID(), MachineID: NewID(), DeviceID: NewID(), InstanceID: NewID(), LeaseRevision: 3, Consent: AutomaticResetCreditConsent{Actor: o.Actor, ConnectionID: o.ConnectionID, Generation: o.Generation, ConfirmedAt: time.Now().UTC()}, Block: SubscriptionQuotaBlock{SessionID: NewID(), ExecutionID: NewID(), NativeThreadID: NativeIdentity(NewID()), NativeTurnID: NativeIdentity(NewID()), Reason: CodexUsageLimitExceeded}}
}

func TestAccessOnlyServerCreditValidatesFrozenAutomaticProof(t *testing.T) {
	o := accessCreditFixture()
	if o.Validate() != nil {
		t.Fatal("manual access-only credit rejected")
	}
	o.Automatic = automaticProofFixture(o)
	if err := o.Validate(); err != nil {
		t.Fatal("frozen automatic proof rejected", err)
	}
	for name, mutate := range map[string]func(*ServerCreditOperation){
		"historical-lane": func(o *ServerCreditOperation) { o.AccessOnly = false },
		"foreign-actor": func(o *ServerCreditOperation) {
			o.Automatic.Consent.Actor = Principal{Type: ClientDevice, DeviceID: NewID()}
		},
		"foreign-generation":   func(o *ServerCreditOperation) { o.Automatic.Consent.Generation = NewID() },
		"foreign-connection":   func(o *ServerCreditOperation) { o.Automatic.Consent.ConnectionID = NewID() },
		"foreign-epoch":        func(o *ServerCreditOperation) { o.Automatic.Epoch = NewID() },
		"missing-consent-time": func(o *ServerCreditOperation) { o.Automatic.Consent.ConfirmedAt = time.Time{} },
		"missing-lease":        func(o *ServerCreditOperation) { o.Automatic.LeaseRevision = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			c := accessCreditFixture()
			c.Automatic = automaticProofFixture(c)
			mutate(&c)
			if c.Validate() == nil {
				t.Fatal("invalid frozen proof accepted")
			}
		})
	}
}

func TestAccessOnlyServerCreditCoexistsWithExecuteButNotNativeLifecycle(t *testing.T) {
	account := Account{Type: SubscriptionAccount, SubscriptionService: SubscriptionChatGPT, Connection: &AccountConnection{ID: NewID(), Authentication: SubscriptionAuth}}
	generation := NewID()
	credit := accessCreditFixture()
	credit.Generation = generation
	lease := &SubscriptionLease{ID: NewID(), OperationID: NewID(), Revision: 2, Action: SubscriptionExecute, MachineID: NewID(), InstanceID: NewID(), DeviceID: NewID(), Epoch: NewID(), Generation: generation, StartedAt: time.Now().UTC()}
	state := SubscriptionState{Generation: generation, IdentityCommitment: "0123456789012345678901234567890123456789012345678901234567890123", Lease: lease, ServerCredit: &credit, OwnerMachineID: lease.MachineID}
	account.Subscription = &state
	if state.ExclusiveServerObservationActive() || !state.ServerObservationActive() || !state.AccessOnlyCredit() {
		t.Fatal("access-only credit is exclusive or invisible to lifecycle fences")
	}
	if err := state.Validate(account); err != nil {
		t.Fatal("access-only credit beside Execute rejected", err)
	}
	state.Lease = &SubscriptionLease{ID: lease.ID, OperationID: lease.OperationID, Revision: 2, Action: SubscriptionRefresh, MachineID: lease.MachineID, InstanceID: lease.InstanceID, DeviceID: lease.DeviceID, Epoch: lease.Epoch, Generation: generation, StartedAt: lease.StartedAt}
	if state.Validate(account) == nil {
		t.Fatal("access-only credit coexisted with a credential-writing lease")
	}
	historical := credit
	historical.AccessOnly = false
	state.Lease, state.ServerCredit = lease, &historical
	if state.Validate(account) == nil || !state.ExclusiveServerObservationActive() {
		t.Fatal("historical managed credit stopped being exclusive")
	}
}

func TestAutomaticEpisodeServerProofMustMatchEpisode(t *testing.T) {
	generation, lease := NewID(), NewID()
	o := accessCreditFixture()
	o.Generation = generation
	proof := automaticProofFixture(o)
	proof.LeaseID = lease
	e := AutomaticResetCreditEpisode{ID: proof.EpisodeID, LeaseID: lease, Generation: generation, ExecutionID: proof.Block.ExecutionID, SessionID: proof.Block.SessionID, NativeThreadID: proof.Block.NativeThreadID, NativeTurnID: proof.Block.NativeTurnID, Block: proof.Block.Reason, ObservedAt: time.Now().UTC(), ServerProof: proof}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	e.ServerFinishGeneration = NewID()
	if e.Validate() != nil {
		t.Fatal("finish generation rejected")
	}
	e.ServerProof.Block.Reason = CodexRateLimitExceeded
	if e.Validate() == nil {
		t.Fatal("proof drifted from its episode")
	}
	e.ServerProof = nil
	if e.Validate() == nil {
		t.Fatal("finish generation without a frozen proof accepted")
	}
}

func TestServerQuotaAutomaticEpisodeRequiresAccessOnly(t *testing.T) {
	q := ServerQuotaOperation{AccessOnly: true, AutomaticEpisodeID: NewID(), ID: NewID(), Epoch: NewID(), FinishID: NewID(), ConnectionID: NewID(), Generation: NewID(), Actor: Principal{Type: OwnerDevice}, Phase: SubscriptionObservationQueued, RequestedAt: time.Now().UTC()}
	if q.Validate() != nil {
		t.Fatal("automatic access-only refresh rejected")
	}
	q.AccessOnly = false
	if q.Validate() == nil {
		t.Fatal("automatic refresh accepted on the managed-writer lane")
	}
}
