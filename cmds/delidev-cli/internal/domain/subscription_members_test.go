// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sharedFixture(t *testing.T, members int) (Account, []SubscriptionMember) {
	t.Helper()
	now := time.Now().UTC()
	generation, lease := NewID(), NewID()
	state := &SubscriptionState{Generation: generation, IdentityCommitment: strings.Repeat("a", 64)}
	state.Lease = &SubscriptionLease{ID: lease, OperationID: NewID(), Revision: 1, Action: SubscriptionExecute, MachineID: NewID(), DeviceID: NewID(), InstanceID: NewID(), Epoch: NewID(), Generation: generation, StartedAt: now, Shared: true}
	for i := 0; i < members; i++ {
		member := SubscriptionMember{LeaseID: NewID(), OperationID: NewID(), Revision: uint64(i + 1), State: SubscriptionMemberRunning, AdmittedAt: now}
		if i == 0 {
			member.LeaseID, member.OperationID = lease, state.Lease.OperationID
		}
		state.AdmitMember(member)
	}
	account := Account{Alias: "fixture", SubscriptionService: SubscriptionChatGPT, Type: SubscriptionAccount, Health: AccountReady, Connection: &AccountConnection{ID: NewID(), Authentication: SubscriptionAuth, ConnectedAt: now}, Subscription: state}
	return account, append([]SubscriptionMember(nil), state.Members...)
}

func TestSharedOwnerAllowsAnyMemberCount(t *testing.T) {
	for _, count := range []int{0, 1, 2, 5, 64} {
		account, _ := sharedFixture(t, count)
		if err := account.Validate(); err != nil {
			t.Fatalf("%d members: %v", count, err)
		}
		if got := account.Subscription.RunnableMembers(); got != count {
			t.Fatalf("%d members counted %d runnable", count, got)
		}
	}
}

func TestSharedOwnerRejectsInconsistentMembers(t *testing.T) {
	for name, change := range map[string]func(*SubscriptionState){
		"duplicate job":          func(s *SubscriptionState) { s.Members[2].OperationID = s.Members[1].OperationID },
		"duplicate lease":        func(s *SubscriptionState) { s.Members[2].LeaseID = s.Members[1].LeaseID },
		"owner id on later":      func(s *SubscriptionState) { s.Members[1].LeaseID = s.Lease.ID },
		"zero revision":          func(s *SubscriptionState) { s.Members[1].Revision = 0 },
		"unknown state":          func(s *SubscriptionState) { s.Members[1].State = "stopped" },
		"missing admission time": func(s *SubscriptionState) { s.Members[1].AdmittedAt = time.Time{} },
		"fork owner":             func(s *SubscriptionState) { s.Lease.ForkRevision = 3 },
		"lifecycle owner":        func(s *SubscriptionState) { s.Lease.Action = SubscriptionRefresh },
		"members without lease":  func(s *SubscriptionState) { s.Lease = nil },
		"singleton with members": func(s *SubscriptionState) { s.Lease.Shared = false },
	} {
		t.Run(name, func(t *testing.T) {
			account, _ := sharedFixture(t, 3)
			change(account.Subscription)
			if account.Validate() == nil {
				t.Fatal("accepted an inconsistent owner/member set")
			}
		})
	}
}

func TestMemberViewsCarryOnlyTheirOwnJob(t *testing.T) {
	account, members := sharedFixture(t, 3)
	state := account.Subscription
	owner, ok := state.OwnerLeaseFor(state.Lease.ID)
	if !ok || owner.Shared == false || owner.ID != state.Lease.ID {
		t.Fatal("the owner identifier must resolve to the stored owner")
	}
	view, member, ok := state.MemberLease(members[2].LeaseID)
	if !ok || member == nil || view.OperationID != members[2].OperationID || view.Revision != members[2].Revision || view.Shared || view.Generation != state.Generation || view.MachineID != state.Lease.MachineID {
		t.Fatal("a member view did not bind its own job to the owner's Runner and generation")
	}
	if _, ok := state.LeaseForOperation(members[1].OperationID); !ok {
		t.Fatal("a running member did not resolve by job")
	}
	state.RetainMember(members[1].LeaseID)
	if _, ok := state.LeaseForOperation(members[1].OperationID); ok {
		t.Fatal("a retained member kept execution authority")
	}
	if _, ok := state.LeaseForOperation(NewID()); ok {
		t.Fatal("an unknown job resolved")
	}
	if !state.ReleaseMember(members[1].LeaseID) || state.ReleaseMember(members[1].LeaseID) {
		t.Fatal("a member released twice")
	}
	if account.Validate() != nil {
		t.Fatal("release left an invalid set")
	}
}

func TestSingletonLeaseKeepsItsHistoricalRepresentation(t *testing.T) {
	account, _ := sharedFixture(t, 0)
	account.Subscription.Lease.Shared = false
	raw, err := json.Marshal(account.Subscription)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "members") || strings.Contains(string(raw), "shared") {
		t.Fatal("a singleton lease serialized the owner/member extension")
	}
	var decoded SubscriptionState
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.SharedOwner() || decoded.Validate(account) != nil {
		t.Fatal("a historical singleton lease no longer decodes or validates", err)
	}
	lease, ok := decoded.LeaseForOperation(decoded.Lease.OperationID)
	if !ok || lease.ID != decoded.Lease.ID {
		t.Fatal("a singleton lease owns exactly its recorded job")
	}
	if _, ok := decoded.LeaseForOperation(NewID()); ok {
		t.Fatal("a singleton lease resolved a foreign job")
	}
}
