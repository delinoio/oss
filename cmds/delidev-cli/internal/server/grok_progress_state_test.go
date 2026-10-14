// SPDX-License-Identifier: Apache-2.0
package server

import (
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// The complete transition table of one Grok lifecycle operation against the
// progress mapping. Every row is a state the account can persist between the
// queued request and the settled outcome.
func TestGrokProgressViewCoversEveryOperationState(t *testing.T) {
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
		name       string
		state      *domain.SubscriptionState
		want       domain.SubscriptionLoginState
		canceled   bool
		diagnostic bool
		denied     bool
	}{
		{name: "queued without record", state: &domain.SubscriptionState{Pending: pending(domain.SubscriptionQueued, false)}, want: domain.SubscriptionPreparing},
		{name: "claimed without record", state: &domain.SubscriptionState{Pending: pending(domain.SubscriptionClaimed, false)}, want: domain.SubscriptionWaiting},
		{name: "claimed and canceled", state: &domain.SubscriptionState{Pending: pending(domain.SubscriptionClaimed, true)}, want: domain.SubscriptionWaiting, canceled: true},
		{name: "queued during recovery", state: &domain.SubscriptionState{RecoveryRequired: true, Pending: pending(domain.SubscriptionQueued, false)}, want: domain.SubscriptionRecovery},
		{name: "take refused", state: &domain.SubscriptionState{Pending: pending(domain.SubscriptionQueued, false), ServerOperation: record(domain.SubscriptionUnsupported, diagnostic, future)}, want: domain.SubscriptionUnsupported, diagnostic: true},
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
			if actor != owner || view.State != test.want || view.Canceled != test.canceled || (view.Diagnostic != nil) != test.diagnostic {
				t.Fatalf("unexpected view: actor=%+v view=%+v", actor, view)
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
