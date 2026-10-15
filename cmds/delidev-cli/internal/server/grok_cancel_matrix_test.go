// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// cancelLogin applies CancelSubscription's cancellation to the Grok account in
// one store transaction. The RPC itself still refuses Grok accounts until Grok
// login can be requested, so the shared cancellation body is driven directly.
func (f *grokFixture) cancelLogin() error {
	f.t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.cancel-login", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		if err := cancelPendingLogin(&a, f.service.subscriptionServerEpoch(), time.Now().UTC()); err != nil {
			return nil, err
		}
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	return err
}

// The cancel table: CancelSubscription against every state of the account's
// original Grok operation. A queued login that was never claimed has no server
// record, so cancellation must create the canceled record that progress serves;
// otherwise the original actor would be denied instead of reading `canceled`.
func TestGrokCancelAgainstEveryOperationState(t *testing.T) {
	login := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN
	refresh := pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH
	preparing := pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING
	waiting := pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING
	canceled := pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_CANCELED
	for _, test := range []struct {
		name    string
		action  pb.SubscriptionAction
		refused bool // an earlier Take was refused by admission
		claimed bool // the Worker claimed the lease
		// Expected outcome of the cancel and of the persisted account.
		cancelErr domain.Code
		pending   bool // the pending operation remains
		flagged   bool // the pending operation carries the cancel flag
		record    bool // a server record exists
		state     pb.SubscriptionLoginState
		canceled  bool
	}{
		{name: "queued login without a record", action: login, record: true, state: canceled, canceled: true},
		{name: "queued login after a refused take", action: login, refused: true, record: true, state: canceled, canceled: true},
		{name: "claimed login", action: login, claimed: true, pending: true, flagged: true, state: waiting, canceled: true},
		{name: "queued refresh", action: refresh, cancelErr: domain.Unsupported, pending: true, state: preparing},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGrokFixture(t)
			if test.action == refresh {
				f.login("synthetic-principal-1")
			}
			if test.refused {
				f.setGrokInstallation(t, func(installations *[]domain.Installation) { (*installations)[0].Version = "1.0.45" })
			}
			op := f.queue(test.action)
			if test.refused {
				if _, err := f.take(op, test.action); err == nil {
					t.Fatal("Take admitted an installation below the subscription baseline")
				}
			}
			if test.claimed {
				if _, err := f.take(op, test.action); err != nil {
					t.Fatal(err)
				}
			}
			err := f.cancelLogin()
			if test.cancelErr == "" && err != nil || test.cancelErr != "" && (err == nil || domain.SafeError(err).Code != test.cancelErr) {
				t.Fatalf("cancel outcome: %v, want %q", err, test.cancelErr)
			}
			a := f.assertGrokOperationValid(t)
			st := a.Subscription
			if (st.Pending != nil) != test.pending || st.Pending != nil && st.Pending.Canceled != test.flagged {
				t.Fatalf("pending = %+v, want remaining=%v flagged=%v", st.Pending, test.pending, test.flagged)
			}
			o := grokRecordFor(st, op.OperationId)
			if (o != nil) != test.record {
				t.Fatalf("record = %+v, want present=%v", o, test.record)
			}
			if o != nil && (o.State != domain.SubscriptionCanceled || o.GrokDiagnostic != nil || o.ID != domain.ID(op.OperationId) || o.Active()) {
				t.Fatalf("canceled record = %+v", o)
			}
			p := f.progressFor(op.OperationId)
			if p.State != test.state || p.Canceled != test.canceled || p.GrokDiagnostic != nil || p.Generation != "" {
				t.Fatalf("progress after cancel = %v, want state=%v canceled=%v", p, test.state, test.canceled)
			}
			if test.record {
				// A second cancel has nothing pending, yet progress keeps reporting
				// the cancellation, and a late Take cannot claim the removed
				// operation.
				if err := f.cancelLogin(); err == nil {
					t.Fatal("a second cancel found a pending operation")
				}
				if _, err := f.take(op, test.action); err == nil {
					t.Fatal("Take claimed a canceled operation")
				}
				if p := f.progressFor(op.OperationId); p.State != canceled || !p.Canceled {
					t.Fatalf("progress after a repeated cancel = %v", p)
				}
			}
		})
	}
}

// Without any pending operation there is nothing to cancel and no progress.
func TestGrokCancelWithoutAnOperationIsRefused(t *testing.T) {
	f := newGrokFixture(t)
	f.mutateAccount(t, func(a *domain.Account) {
		if a.Subscription == nil {
			a.Subscription = &domain.SubscriptionState{}
		}
	})
	if err := f.cancelLogin(); err == nil || domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("cancel without an operation: %v", err)
	}
	if _, a := f.record(); a.Subscription.ServerOperation != nil {
		t.Fatal("a refused cancel created a record")
	}
}

// Revoking the initiating client device cancels its queued Grok operation with
// the same record, whether or not an earlier Take was refused.
func TestGrokInitiatorRevocationRecordsAnUnclaimedCancellation(t *testing.T) {
	for _, refused := range []bool{false, true} {
		name := "unclaimed"
		if refused {
			name = "after a refused take"
		}
		t.Run(name, func(t *testing.T) {
			f := newGrokFixture(t)
			if refused {
				f.setGrokInstallation(t, func(installations *[]domain.Installation) { (*installations)[0].Version = "1.0.45" })
			}
			op := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			device := domain.NewID()
			f.mutateAccount(t, func(a *domain.Account) {
				a.Subscription.Pending.Actor = domain.Principal{Type: domain.ClientDevice, DeviceID: device}
			})
			if refused {
				if _, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err == nil {
					t.Fatal("Take admitted an installation below the subscription baseline")
				}
			}
			if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.revoke", nil, func(tx *store.Tx) (any, error) {
				return nil, cancelQueuedSubscriptionInitiator(tx, device, f.service.subscriptionServerEpoch())
			}); err != nil {
				t.Fatal(err)
			}
			a := f.assertGrokOperationValid(t)
			o := a.Subscription.ServerOperation
			if a.Subscription.Pending != nil || o == nil || o.ID != domain.ID(op.OperationId) || o.State != domain.SubscriptionCanceled || o.GrokDiagnostic != nil || o.Actor.DeviceID != device {
				t.Fatalf("revocation left pending=%+v record=%+v", a.Subscription.Pending, o)
			}
			view, owner, ok := grokProgressView(a.Subscription, op.OperationId, false, f.service.subscriptionServerEpoch(), o.StartedAt)
			if !ok || owner.DeviceID != device || view.State != domain.SubscriptionCanceled || !view.Canceled || view.Diagnostic != nil {
				t.Fatalf("progress after revocation: ok=%v owner=%+v view=%+v", ok, owner, view)
			}
		})
	}
}

// Only a Grok account gets a record from a pre-claim cancellation; other
// services keep their existing behavior of settling an existing record only.
func TestCancelServerOperationCreatesRecordsOnlyForGrokAccounts(t *testing.T) {
	op := domain.NewID()
	owner := domain.Principal{Type: domain.OwnerDevice}
	epoch := domain.NewID()
	for _, test := range []struct {
		name    string
		service domain.SubscriptionService
		want    bool
	}{
		{"grok", domain.SubscriptionGrok, true},
		{"chatgpt", domain.SubscriptionChatGPT, false},
	} {
		account := &domain.Account{SubscriptionService: test.service, Subscription: &domain.SubscriptionState{Pending: &domain.SubscriptionOperation{ID: op, Action: domain.SubscriptionLogin, MachineID: domain.NewID(), Actor: owner, Phase: domain.SubscriptionQueued}}}
		cancelServerOperation(account, epoch, time.Now().UTC())
		if got := account.Subscription.ServerOperation != nil; got != test.want {
			t.Errorf("%s: record created = %v, want %v", test.name, got, test.want)
		}
	}
}
