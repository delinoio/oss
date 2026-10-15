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

// assertGrokOperationValid proves the persisted account still satisfies the
// domain invariants after a settlement wrote the operation record.
func (f *grokFixture) assertGrokOperationValid(t *testing.T) domain.Account {
	t.Helper()
	_, a := f.record()
	if err := a.Subscription.Validate(a); err != nil {
		t.Fatalf("settled Grok operation violates the subscription invariants: %v", err)
	}
	return a
}

// requireGrokProgress asserts one failed operation's presentation: the state,
// the original phase and code, the operation ID as correlation ID, and that the
// Codex and native diagnostics, login presentation and generation stay unset.
func requireGrokProgress(t *testing.T, p *pb.GetSubscriptionProgressResponse, state pb.SubscriptionLoginState, phase domain.GrokPhase, code domain.Code, operation string) {
	t.Helper()
	g := p.GrokDiagnostic
	if g == nil || g.Phase != grokDiagnosticPhases[phase] || g.Code != string(code) || g.CorrelationId != operation || g.SupportedVersion != domain.GrokSubscriptionBaselineVersion || g.Message == "" || g.Guidance == "" {
		t.Fatalf("progress lost the original diagnostic: %v", g)
	}
	if p.State != state || p.Diagnostic != nil || p.NativeDiagnostic != nil || p.Generation != "" || p.Url != "" || p.UserCode != "" {
		t.Fatalf("unexpected progress: %v", p)
	}
}

// A refused Take is the failure the user can act on, so progress must serve the
// original version diagnostic. The refusal leaves the operation claimable: a
// later Take at a supported installation supersedes it and a successful login
// reports the generation it committed.
func TestGrokTakeRefusalIsServedByProgressAndSupersededByAClaim(t *testing.T) {
	f := newGrokFixture(t)
	f.setGrokInstallation(t, func(installations *[]domain.Installation) { (*installations)[0].Version = "1.0.45" })
	op := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if _, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err == nil {
		t.Fatal("Take admitted an installation below the subscription baseline")
	}
	p := f.progressFor(op.OperationId)
	requireGrokProgress(t, p, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_UNSUPPORTED, domain.GrokVersion, domain.Unsupported, op.OperationId)
	if p.GrokDiagnostic.DetectedVersion != "1.0.45" {
		t.Fatalf("progress lost the installed version: %v", p.GrokDiagnostic)
	}
	a := f.assertGrokOperationValid(t)
	if a.Subscription.Pending == nil || a.Subscription.Pending.Phase != domain.SubscriptionQueued || a.Subscription.Lease != nil || a.Subscription.RecoveryRequired {
		t.Fatal("recording the refusal consumed or fenced the queued operation")
	}
	// A repeated refusal is idempotent and does not rewrite the account.
	before, _ := f.record()
	if _, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err == nil {
		t.Fatal("Take admitted an installation below the subscription baseline")
	}
	if after, _ := f.record(); after.Revision != before.Revision {
		t.Fatal("an identical refusal rewrote the account")
	}

	f.setGrokInstallation(t, func(installations *[]domain.Installation) {
		(*installations)[0].Version = domain.GrokSubscriptionBaselineVersion
	})
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	if a := f.assertGrokOperationValid(t); a.Subscription.ServerOperation != nil {
		t.Fatal("a claim kept the provisional refusal record")
	}
	// The provisional response is replaced by the claimed operation's own state,
	// which is what a client that kept polling adopts.
	if p := f.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING || p.GrokDiagnostic != nil {
		t.Fatalf("a claim did not supersede the provisional refusal: %v", p)
	}
	generation := domain.NewID()
	if _, err := f.finishWith(lease, generation, f.sealed(t, f.input.AccountID, generation, "synthetic-principal-1"), true, false, true); err != nil {
		t.Fatal(err)
	}
	p = f.progressFor(op.OperationId)
	if p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || p.Generation != string(generation) || p.GrokDiagnostic != nil || p.Diagnostic != nil || p.NativeDiagnostic != nil || p.Url != "" || p.UserCode != "" {
		t.Fatalf("a succeeded Grok login did not report its committed generation: %v", p)
	}
	f.assertGrokOperationValid(t)
}

// A refused Take for another operation, machine or a claimed lease never writes
// a record, so a Worker cannot attach a diagnostic to an operation it does not own.
func TestGrokTakeRefusalIgnoresOperationsItDoesNotOwn(t *testing.T) {
	f := newGrokFixture(t)
	f.setGrokInstallation(t, func(installations *[]domain.Installation) { (*installations)[0].Version = "1.0.45" })
	f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	foreign := &pb.RequestSubscriptionResponse{OperationId: string(domain.NewID())}
	if _, err := f.take(foreign, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err == nil {
		t.Fatal("Take admitted an operation that is not the account's original")
	}
	if a := f.assertGrokOperationValid(t); a.Subscription.ServerOperation != nil {
		t.Fatal("a refusal for a foreign operation wrote a progress record")
	}
}

// Every Finish outcome of a Worker-owned Grok operation reaches progress with the
// original diagnostic and the operation ID as correlation ID.
func TestGrokFinishSettlesProgressForEveryOutcome(t *testing.T) {
	for _, test := range []struct {
		name     string
		action   pb.SubscriptionAction
		prepare  bool
		finish   func(*grokFixture, *pb.TakeSubscriptionResponse) error
		state    pb.SubscriptionLoginState
		phase    domain.GrokPhase
		code     domain.Code
		recovery bool
	}{
		{"login native failure", pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, false, func(f *grokFixture, l *pb.TakeSubscriptionResponse) error {
			_, err := f.finishWith(l, domain.NewID(), nil, false, false, true)
			return err
		}, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED, domain.GrokLogin, domain.Unavailable, false},
		{"login native failure with unconfirmed cleanup keeps the failure", pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, false, func(f *grokFixture, l *pb.TakeSubscriptionResponse) error {
			_, err := f.finishWith(l, domain.NewID(), nil, false, false, false)
			return err
		}, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED, domain.GrokLogin, domain.Unavailable, true},
		{"login unconfirmed cleanup after success", pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, false, func(f *grokFixture, l *pb.TakeSubscriptionResponse) error {
			_, err := f.finishWith(l, domain.NewID(), nil, true, false, false)
			return err
		}, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED, domain.GrokCleanup, domain.RecoveryRequired, true},
		{"refresh native failure", pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, true, func(f *grokFixture, l *pb.TakeSubscriptionResponse) error {
			_, err := f.finishWith(l, domain.NewID(), nil, false, false, true)
			return err
		}, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED, domain.GrokRefresh, domain.Unavailable, true},
		{"logout native failure", pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT, true, func(f *grokFixture, l *pb.TakeSubscriptionResponse) error {
			_, err := f.finishWith(l, domain.NewID(), nil, false, false, true)
			return err
		}, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED, domain.GrokLogout, domain.Unavailable, true},
		{"logout unconfirmed cleanup", pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT, true, func(f *grokFixture, l *pb.TakeSubscriptionResponse) error {
			_, err := f.finishWith(l, domain.NewID(), nil, true, false, false)
			return err
		}, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED, domain.GrokCleanup, domain.RecoveryRequired, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGrokFixture(t)
			if test.prepare {
				f.login("synthetic-principal-1")
			}
			op := f.queue(test.action)
			lease, err := f.take(op, test.action)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.finish(f, lease); err != nil {
				t.Fatal(err)
			}
			p := f.progressFor(op.OperationId)
			requireGrokProgress(t, p, test.state, test.phase, test.code, op.OperationId)
			// The version label is the installed Grok Build observed at settlement.
			if p.GrokDiagnostic.DetectedVersion != domain.GrokSubscriptionBaselineVersion {
				t.Fatalf("settlement lost the installed version: %v", p.GrokDiagnostic)
			}
			a := f.assertGrokOperationValid(t)
			if a.Subscription.RecoveryRequired != test.recovery {
				t.Fatalf("recovery fence = %v, want %v", a.Subscription.RecoveryRequired, test.recovery)
			}
		})
	}
}

// A canceled operation settles as canceled and never invents a failure.
func TestGrokFinishOfACanceledLoginSettlesWithoutADiagnostic(t *testing.T) {
	f := newGrokFixture(t)
	op := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.cancel", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Subscription.Pending.Canceled = true
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	generation := domain.NewID()
	if _, err := f.finishWith(lease, generation, f.sealed(t, f.input.AccountID, generation, "synthetic-principal-1"), true, false, true); err != nil {
		t.Fatal(err)
	}
	p := f.progressFor(op.OperationId)
	if p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_CANCELED || p.GrokDiagnostic != nil || p.Generation != "" {
		t.Fatalf("a canceled login reported a failure or a generation: %v", p)
	}
	f.assertGrokOperationValid(t)
}

// A logout that completed reports success without any generation.
func TestGrokFinishedLogoutAndRefreshReportTheirOutcome(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	refresh := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	lease, err := f.take(refresh, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	rotated := domain.NewID()
	if _, err := f.finishWith(lease, rotated, f.grokRotatedEnvelope(rotated), true, true, true); err != nil {
		t.Fatal(err)
	}
	if p := f.progressFor(refresh.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || p.Generation != string(rotated) || p.GrokDiagnostic != nil {
		t.Fatalf("a refresh did not report its rotated generation: %v", p)
	}
	logout := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	lease, err = f.take(logout, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal(err)
	}
	if p := f.progressFor(logout.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || p.Generation != "" || p.GrokDiagnostic != nil {
		t.Fatalf("a logout reported a generation or failure: %v", p)
	}
	f.assertGrokOperationValid(t)
}

// When the publication committed but the superseded generations cannot be
// removed, the account is fenced and progress reports the cleanup failure
// instead of an unqualified success.
func TestGrokFinishCleanupFailureAfterPublicationIsReportedByProgress(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	op := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	f.secrets.afterPut = func() {
		f.secrets.mu.Lock()
		f.secrets.deleteError = domain.Fail(domain.Unavailable, "The vault removal failed.", "")
		f.secrets.mu.Unlock()
	}
	rotated := domain.NewID()
	if _, err := f.finishWith(lease, rotated, f.grokRotatedEnvelope(rotated), true, true, true); err == nil {
		t.Fatal("a failed removal of the superseded generation was reported as success")
	}
	p := f.progressFor(op.OperationId)
	requireGrokProgress(t, p, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED, domain.GrokCleanup, domain.Unavailable, op.OperationId)
	a := f.assertGrokOperationValid(t)
	if !a.Subscription.RecoveryRequired || a.Subscription.Generation != rotated {
		t.Fatal("the committed generation was not kept and fenced")
	}
}

// settleGrokOperation never overwrites a settled outcome, an active operation of
// another owner or a diagnostic whose text is not locally reconstructed.
func TestSettleGrokOperationKeepsTheFirstOutcome(t *testing.T) {
	now := time.Now().UTC()
	op := domain.NewID()
	owner := domain.Principal{Type: domain.OwnerDevice}
	failed := grokLocalDiagnostic("1.0.52", domain.GrokLogin, domain.Unavailable)
	st := &domain.SubscriptionState{}
	if !settleGrokOperation(st, grokSettlement{Operation: op, Action: domain.SubscriptionLogin, Actor: owner, Epoch: domain.NewID(), State: domain.SubscriptionFailed, Diagnostic: failed}, now) {
		t.Fatal("the first outcome was not recorded")
	}
	if st.ServerOperation.GrokDiagnostic == nil || st.ServerOperation.GrokDiagnostic.CorrelationID != string(op) {
		t.Fatal("the diagnostic lost its operation correlation ID")
	}
	if settleGrokOperation(st, grokSettlement{Operation: op, Action: domain.SubscriptionLogin, Actor: owner, Epoch: domain.NewID(), State: domain.SubscriptionSucceeded}, now) {
		t.Fatal("a settled outcome was overwritten")
	}
	tampered := *failed
	tampered.Message = "raw native stderr"
	other := &domain.SubscriptionState{}
	settleGrokOperation(other, grokSettlement{Operation: op, Action: domain.SubscriptionLogin, Actor: owner, Epoch: domain.NewID(), State: domain.SubscriptionFailed, Diagnostic: &tampered}, now)
	if other.ServerOperation == nil || other.ServerOperation.GrokDiagnostic != nil {
		t.Fatal("an unreconstructed diagnostic was retained")
	}
	active := &domain.SubscriptionState{ServerOperation: &domain.ServerSubscriptionOperation{ID: domain.NewID(), State: domain.SubscriptionWaiting}}
	if settleGrokOperation(active, grokSettlement{Operation: op, Action: domain.SubscriptionLogin, Actor: owner, Epoch: domain.NewID(), State: domain.SubscriptionFailed, Diagnostic: failed}, now) {
		t.Fatal("an active operation of another owner was overwritten")
	}
}
