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

// The complete transition table of one Grok lifecycle operation against the
// progress mapping. Every row is a state the account can persist between the
// queued request and the settled outcome. The provisional column is the server
// half of "Client classification of a progress response" in
// docs/cmds-delidev-grok-subscription-contract.md: a response is provisional
// only for a refused Take whose original operation is still queued, and then it
// is the only response that serves the Grok diagnostic with `unsupported`.
// subscription-progress.test.ts asserts the client half of the same rows.
func TestGrokProgressViewClassifiesEveryOperationState(t *testing.T) {
	const epoch domain.ID = "epoch-current"
	now := time.Now().UTC()
	owner := domain.Principal{Type: domain.OwnerDevice}
	operation := domain.NewID()
	diagnostic := domain.GrokErrorDiagnostic(domain.WithGrokDiagnostic("1.0.45", domain.GrokVersion, domain.Fail(domain.Unsupported, "x", "y")))
	diagnostic.CorrelationID = string(operation)
	pending := func(phase domain.SubscriptionPhase, canceled bool) *domain.SubscriptionOperation {
		return &domain.SubscriptionOperation{ID: operation, Action: domain.SubscriptionLogin, MachineID: domain.NewID(), Actor: owner, Phase: phase, Canceled: canceled}
	}
	record := func(state domain.SubscriptionLoginState, d *domain.GrokDiagnostic, expires time.Time) *domain.ServerSubscriptionOperation {
		return &domain.ServerSubscriptionOperation{ID: operation, Action: domain.SubscriptionLogin, Epoch: epoch, FinishID: domain.NewID(), Actor: owner, State: state, StartedAt: now.Add(-time.Minute), ExpiresAt: expires, GrokDiagnostic: d}
	}
	future, past := now.Add(time.Minute), now.Add(-time.Second)
	for _, test := range []struct {
		name        string
		state       *domain.SubscriptionState
		want        domain.SubscriptionLoginState
		canceled    bool
		diagnostic  bool
		denied      bool
		provisional bool
	}{
		{name: "queued without record", state: &domain.SubscriptionState{Pending: pending(domain.SubscriptionQueued, false)}, want: domain.SubscriptionPreparing},
		{name: "claimed without record", state: &domain.SubscriptionState{Pending: pending(domain.SubscriptionClaimed, false)}, want: domain.SubscriptionWaiting},
		{name: "claimed and canceled", state: &domain.SubscriptionState{Pending: pending(domain.SubscriptionClaimed, true)}, want: domain.SubscriptionWaiting, canceled: true},
		{name: "queued during recovery", state: &domain.SubscriptionState{RecoveryRequired: true, Pending: pending(domain.SubscriptionQueued, false)}, want: domain.SubscriptionRecovery},
		{name: "take refused", state: &domain.SubscriptionState{Pending: pending(domain.SubscriptionQueued, false), ServerOperation: record(domain.SubscriptionUnsupported, diagnostic, future)}, want: domain.SubscriptionUnsupported, diagnostic: true, provisional: true},
		{name: "take refused past the record window", state: &domain.SubscriptionState{Pending: pending(domain.SubscriptionQueued, false), ServerOperation: record(domain.SubscriptionUnsupported, diagnostic, past)}, want: domain.SubscriptionUnsupported, diagnostic: true, provisional: true},
		{name: "take refused then claimed", state: &domain.SubscriptionState{Pending: pending(domain.SubscriptionClaimed, false), ServerOperation: record(domain.SubscriptionUnsupported, diagnostic, future)}, want: domain.SubscriptionUnsupported},
		{name: "take refused then canceled flag", state: &domain.SubscriptionState{Pending: pending(domain.SubscriptionQueued, true), ServerOperation: record(domain.SubscriptionUnsupported, diagnostic, future)}, want: domain.SubscriptionUnsupported, canceled: true},
		{name: "take refused without a pending operation", state: &domain.SubscriptionState{ServerOperation: record(domain.SubscriptionUnsupported, diagnostic, future)}, want: domain.SubscriptionUnsupported},
		{name: "take refused during recovery", state: &domain.SubscriptionState{RecoveryRequired: true, Pending: pending(domain.SubscriptionQueued, false), ServerOperation: record(domain.SubscriptionUnsupported, diagnostic, future)}, want: domain.SubscriptionRecovery, diagnostic: true},
		{name: "finished failed", state: &domain.SubscriptionState{ServerOperation: record(domain.SubscriptionFailed, diagnostic, future)}, want: domain.SubscriptionFailed, diagnostic: true},
		{name: "finished succeeded", state: &domain.SubscriptionState{ServerOperation: record(domain.SubscriptionSucceeded, nil, future)}, want: domain.SubscriptionSucceeded},
		{name: "finished canceled", state: &domain.SubscriptionState{ServerOperation: record(domain.SubscriptionCanceled, nil, future)}, want: domain.SubscriptionCanceled, canceled: true},
		{name: "canceled record with a stale diagnostic", state: &domain.SubscriptionState{ServerOperation: record(domain.SubscriptionCanceled, diagnostic, future)}, want: domain.SubscriptionCanceled, canceled: true},
		{name: "active record before expiry", state: &domain.SubscriptionState{Pending: pending(domain.SubscriptionClaimed, false), ServerOperation: record(domain.SubscriptionWaiting, nil, future)}, want: domain.SubscriptionWaiting},
		{name: "active record after expiry", state: &domain.SubscriptionState{Pending: pending(domain.SubscriptionClaimed, false), ServerOperation: record(domain.SubscriptionPreparing, nil, past)}, want: domain.SubscriptionExpired},
		{name: "settled record under recovery", state: &domain.SubscriptionState{RecoveryRequired: true, ServerOperation: record(domain.SubscriptionFailed, diagnostic, future)}, want: domain.SubscriptionRecovery, diagnostic: true},
		{name: "another operation", state: &domain.SubscriptionState{Pending: &domain.SubscriptionOperation{ID: domain.NewID(), Action: domain.SubscriptionLogin, MachineID: domain.NewID(), Actor: owner, Phase: domain.SubscriptionQueued}}, denied: true},
		{name: "no operation", state: &domain.SubscriptionState{}, denied: true},
		{name: "no subscription", denied: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			view, actor, ok := grokProgressView(test.state, string(operation), false, epoch, now)
			if ok == test.denied {
				t.Fatalf("operation visibility = %v, denied=%v", ok, test.denied)
			}
			if test.denied {
				return
			}
			if actor != owner || view.State != test.want || view.Canceled != test.canceled || (view.Diagnostic != nil) != test.diagnostic || view.Provisional != test.provisional {
				t.Fatalf("unexpected view: actor=%+v view=%+v", actor, view)
			}
			// The wire marks a provisional response by serving `unsupported` with the
			// Grok diagnostic; no final response may carry that combination.
			if view.State == domain.SubscriptionUnsupported && (view.Diagnostic != nil) != view.Provisional {
				t.Fatalf("unsupported response is ambiguous: %+v", view)
			}
			if view.State == domain.SubscriptionCanceled && view.Diagnostic != nil {
				t.Fatal("a canceled operation carried a failure diagnostic")
			}
		})
	}
}

// A queued or claimed Grok operation has no ServerOperation, so the original
// actor's polling must report it instead of being denied.
func TestGrokProgressReportsAnActiveOperationBeforeItSettles(t *testing.T) {
	f := newGrokFixture(t)
	op := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if p := f.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING || p.Canceled || p.GrokDiagnostic != nil {
		t.Fatalf("queued operation progress: %v", p)
	}
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	if p := f.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING || p.Canceled || p.GrokDiagnostic != nil {
		t.Fatalf("claimed operation progress: %v", p)
	}
	f.cancelPending()
	if p := f.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING || !p.Canceled {
		t.Fatalf("canceled claimed operation progress: %v", p)
	}
	if _, err := f.finishWith(lease, domain.NewID(), nil, false, false, true); err != nil {
		t.Fatal(err)
	}
	if p := f.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_CANCELED || !p.Canceled || p.GrokDiagnostic != nil {
		t.Fatalf("finished canceled operation progress: %v", p)
	}
}

// Revoking the initiating client device cancels its queued operation through
// cancelQueuedSubscriptionInitiator, which must not keep the failure diagnostic
// that an earlier refused Take recorded.
func TestGrokInitiatorRevocationClearsTheRefusalDiagnostic(t *testing.T) {
	f := newGrokFixture(t)
	f.setGrokInstallation(t, func(installations *[]domain.Installation) { (*installations)[0].Version = "1.0.45" })
	op := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	device := domain.NewID()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.client-initiator", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Subscription.Pending.Actor = domain.Principal{Type: domain.ClientDevice, DeviceID: device}
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	a := f.seedRefusal(t, op.OperationId)
	if a.Subscription.ServerOperation == nil || a.Subscription.ServerOperation.GrokDiagnostic == nil {
		t.Fatal("the refusal was not recorded")
	}
	if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.revoke", nil, func(tx *store.Tx) (any, error) {
		return nil, cancelQueuedSubscriptionInitiator(tx, device, f.service.subscriptionServerEpoch())
	}); err != nil {
		t.Fatal(err)
	}
	a = f.assertGrokOperationValid(t)
	if a.Subscription.Pending != nil || a.Subscription.ServerOperation.State != domain.SubscriptionCanceled || a.Subscription.ServerOperation.GrokDiagnostic != nil {
		t.Fatalf("revocation left a stale diagnostic: %+v", a.Subscription.ServerOperation)
	}
}

func (f *grokFixture) seedRefusal(t *testing.T, operation string) domain.Account {
	t.Helper()
	r, a := f.record()
	_ = r
	d := domain.GrokErrorDiagnostic(grokAdmissionFailure(&domain.Installation{Harness: domain.GrokBuild, State: domain.InstallationDetected, Version: "1.0.45"}))
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.refusal", nil, func(tx *store.Tx) (any, error) {
		rec, acct, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		p := acct.Subscription.Pending
		settleGrokOperation(acct.Subscription, grokSettlement{Operation: p.ID, Action: p.Action, Actor: p.Actor, Epoch: f.service.subscriptionServerEpoch(), State: domain.SubscriptionUnsupported, Diagnostic: d}, time.Now().UTC())
		return tx.Put(domain.AccountKind, rec.ID, rec.Revision, "", "", acct)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	return a
}

// The domain invariant forbids a failure diagnostic on a canceled record.
func TestGrokCanceledRecordRejectsADiagnostic(t *testing.T) {
	f := newGrokFixture(t)
	op := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	a := f.seedRefusal(t, op.OperationId)
	a.Subscription.ServerOperation.State = domain.SubscriptionCanceled
	if a.Subscription.Validate(a) == nil {
		t.Fatal("a canceled operation with a failure diagnostic validated")
	}
	a.Subscription.ServerOperation.GrokDiagnostic = nil
	a.Subscription.Pending = nil
	if err := a.Subscription.Validate(a); err != nil {
		t.Fatal(err)
	}
}
