// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// mutateAccount edits the fixture account in one store transaction.
func (f *grokFixture) mutateAccount(t *testing.T, edit func(*domain.Account)) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.account-edit", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		edit(&a)
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// grokRecordFor returns the server record of one operation, ignoring the settled
// record of an earlier operation on the same account.
func grokRecordFor(st *domain.SubscriptionState, operation string) *domain.ServerSubscriptionOperation {
	if o := st.ServerOperation; o != nil && string(o.ID) == operation {
		return o
	}
	return nil
}

// The refused-Take table: every Take variant against every state of the
// account's original operation, with the installation below the subscription
// baseline so each Take is refused by admission. Only a Take that addresses the
// account's own queued, uncanceled operation by its exact operation ID, action
// and Runner Device leaves a record; every other variant leaves the operation
// and its progress untouched.
func TestGrokTakeRefusalRecordsOnlyTheExactQueuedOperation(t *testing.T) {
	login := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN
	refresh := pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH
	logout := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT
	for _, test := range []struct {
		name string
		// queued is the original operation's lifecycle action; connected
		// prepares a signed-in account first.
		queued    pb.SubscriptionAction
		connected bool
		prepare   func(*grokFixture, *testing.T)
		// take is the action sent by the Take; foreign sends another operation ID.
		take    pb.SubscriptionAction
		foreign bool
		// recorded is whether the refusal annotates the original operation, and
		// state is the progress state afterwards.
		recorded bool
		state    pb.SubscriptionLoginState
	}{
		{name: "login take of the queued login", queued: login, take: login, recorded: true, state: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_UNSUPPORTED},
		{name: "refresh take of the queued login", queued: login, take: refresh, state: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING},
		{name: "logout take of the queued login", queued: login, take: logout, state: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING},
		{name: "login take of a foreign operation", queued: login, take: login, foreign: true, state: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING},
		{name: "refresh take of the queued refresh", queued: refresh, connected: true, take: refresh, recorded: true, state: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_UNSUPPORTED},
		{name: "logout take of the queued refresh", queued: refresh, connected: true, take: logout, state: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING},
		{name: "login take of the queued refresh", queued: refresh, connected: true, take: login, state: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING},
		{name: "logout take of the queued logout", queued: logout, connected: true, take: logout, recorded: true, state: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_UNSUPPORTED},
		{name: "refresh take of the queued logout", queued: logout, connected: true, take: refresh, state: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING},
		{name: "login take of the queued login on a connected account", queued: login, connected: true, take: login, state: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING},
		{name: "refresh take of the queued refresh on a disconnected account", queued: refresh, take: refresh, state: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING},
		{name: "login take of a canceled queued login", queued: login, take: login, prepare: func(f *grokFixture, t *testing.T) { f.cancelPending() }, state: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING},
		{name: "login take of a fenced account", queued: login, take: login, prepare: func(f *grokFixture, t *testing.T) {
			f.mutateAccount(t, func(a *domain.Account) { a.Subscription.RecoveryRequired = true })
		}, state: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGrokFixture(t)
			if test.connected {
				f.login("synthetic-principal-1")
			}
			op := f.queue(test.queued)
			if test.prepare != nil {
				test.prepare(f, t)
			}
			f.setGrokInstallation(t, func(installations *[]domain.Installation) { (*installations)[0].Version = "1.0.45" })
			sent := op
			if test.foreign {
				sent = &pb.RequestSubscriptionResponse{OperationId: string(domain.NewID())}
			}
			if _, err := f.take(sent, test.take); err == nil {
				t.Fatal("Take admitted an installation below the subscription baseline")
			}
			_, a := f.record()
			st := a.Subscription
			if o := grokRecordFor(st, op.OperationId); (o != nil) != test.recorded {
				t.Fatalf("recorded = %v, want %v: %+v", o != nil, test.recorded, o)
			}
			if st.Lease != nil {
				t.Fatal("a refused Take claimed a lease")
			}
			p := f.progressFor(op.OperationId)
			if p.State != test.state {
				t.Fatalf("progress state = %v, want %v", p.State, test.state)
			}
			if test.recorded {
				requireGrokProgress(t, p, test.state, domain.GrokVersion, domain.Unsupported, op.OperationId)
			} else if p.GrokDiagnostic != nil {
				t.Fatalf("an unrelated Take attached a diagnostic to the operation: %v", p.GrokDiagnostic)
			}
			f.assertGrokOperationValid(t)
		})
	}
}

// A claimed operation is never annotated by a later refused Take: its lease
// belongs to the Worker that claimed it, and its progress stays waiting.
func TestGrokTakeRefusalNeverAnnotatesAClaimedOperation(t *testing.T) {
	f := newGrokFixture(t)
	op := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if _, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err != nil {
		t.Fatal(err)
	}
	f.setGrokInstallation(t, func(installations *[]domain.Installation) { (*installations)[0].Version = "1.0.45" })
	if _, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err == nil {
		t.Fatal("Take admitted an installation below the subscription baseline")
	}
	if _, a := f.record(); a.Subscription.ServerOperation != nil || a.Subscription.Lease == nil {
		t.Fatal("a refused Take annotated or released a claimed operation")
	}
	if p := f.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING || p.GrokDiagnostic != nil {
		t.Fatalf("claimed operation progress: %v", p)
	}
}

// The refusal target is the exact queued original: a different Runner Device is
// rejected by the same predicate that the recording transaction applies.
func TestGrokTakeRefusalTargetRequiresEveryIdentifyingField(t *testing.T) {
	f := newGrokFixture(t)
	op := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	operation := domain.ID(op.OperationId)
	_, a := f.record()
	for _, test := range []struct {
		name      string
		operation domain.ID
		machine   domain.ID
		action    domain.SubscriptionAction
		want      bool
	}{
		{"exact", operation, f.input.MachineID, domain.SubscriptionLogin, true},
		{"operation", domain.NewID(), f.input.MachineID, domain.SubscriptionLogin, false},
		{"machine", operation, domain.NewID(), domain.SubscriptionLogin, false},
		{"action", operation, f.input.MachineID, domain.SubscriptionRefresh, false},
		{"empty action", operation, f.input.MachineID, "", false},
	} {
		err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
			if got := grokTakeRefusalTarget(tx, a, test.operation, test.machine, test.action) != nil; got != test.want {
				t.Errorf("%s: target = %v, want %v", test.name, got, test.want)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
