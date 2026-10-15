// SPDX-License-Identifier: Apache-2.0
package server

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// The Finish result table: every action against the Worker's reported outcome
// (native success, refresh confirmation, cleanup confirmation) and a requested
// cancellation. The first failure stays authoritative: a reported native failure
// keeps its login, refresh or logout diagnostic even when native cleanup is also
// unconfirmed (which only fences the account), and an unconfirmed cleanup is the
// diagnostic only after a success or a cancellation.
func TestGrokFinishResultTableKeepsTheFirstFailure(t *testing.T) {
	login := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN
	refresh := pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH
	logout := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT
	const (
		succeeded = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED
		failed    = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED
		canceled  = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_CANCELED
		recovery  = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED
	)
	for _, test := range []struct {
		name                              string
		action                            pb.SubscriptionAction
		success, refreshed, cleanup, stop bool
		state                             pb.SubscriptionLoginState
		phase                             domain.GrokPhase
		code                              domain.Code
		fenced                            bool
	}{
		{name: "login success", action: login, success: true, cleanup: true, state: succeeded},
		{name: "login success, cleanup unconfirmed", action: login, success: true, state: recovery, phase: domain.GrokCleanup, code: domain.RecoveryRequired, fenced: true},
		{name: "login failure", action: login, cleanup: true, state: failed, phase: domain.GrokLogin, code: domain.Unavailable},
		{name: "login failure, cleanup unconfirmed", action: login, state: recovery, phase: domain.GrokLogin, code: domain.Unavailable, fenced: true},
		{name: "login canceled", action: login, success: true, cleanup: true, stop: true, state: canceled},
		{name: "login canceled, cleanup unconfirmed", action: login, success: true, stop: true, state: recovery, phase: domain.GrokCleanup, code: domain.RecoveryRequired, fenced: true},
		{name: "refresh success", action: refresh, success: true, refreshed: true, cleanup: true, state: succeeded},
		{name: "refresh success, cleanup unconfirmed", action: refresh, success: true, refreshed: true, state: recovery, phase: domain.GrokCleanup, code: domain.RecoveryRequired, fenced: true},
		{name: "refresh unconfirmed", action: refresh, success: true, cleanup: true, state: recovery, phase: domain.GrokRefresh, code: domain.Unavailable, fenced: true},
		{name: "refresh failure", action: refresh, cleanup: true, state: recovery, phase: domain.GrokRefresh, code: domain.Unavailable, fenced: true},
		{name: "refresh failure, cleanup unconfirmed", action: refresh, state: recovery, phase: domain.GrokRefresh, code: domain.Unavailable, fenced: true},
		{name: "logout success", action: logout, success: true, cleanup: true, state: succeeded},
		{name: "logout success, cleanup unconfirmed", action: logout, success: true, state: recovery, phase: domain.GrokCleanup, code: domain.RecoveryRequired, fenced: true},
		{name: "logout failure", action: logout, cleanup: true, state: recovery, phase: domain.GrokLogout, code: domain.Unavailable, fenced: true},
		{name: "logout failure, cleanup unconfirmed", action: logout, state: recovery, phase: domain.GrokLogout, code: domain.Unavailable, fenced: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGrokFixture(t)
			if test.action != login {
				f.login("synthetic-principal-1")
			}
			op := f.queue(test.action)
			lease, err := f.take(op, test.action)
			if err != nil {
				t.Fatal(err)
			}
			if test.stop {
				f.cancelPending()
			}
			logs := f.captureLogs()
			generation := domain.NewID()
			var bundle []byte
			usable := test.success && test.cleanup && !test.stop && (test.action != refresh || test.refreshed)
			switch {
			case usable && test.action == login:
				bundle = f.sealed(t, f.input.AccountID, generation, "synthetic-principal-1")
			case usable && test.action == refresh:
				bundle = f.grokRotatedEnvelope(generation)
			}
			if _, err := f.finishWith(lease, generation, bundle, test.success, test.refreshed, test.cleanup); err != nil {
				t.Fatal(err)
			}
			// Every Finish settles its original operation once, and the line follows
			// the committed record: it carries the diagnostic only for a failure.
			settled := logs.requireLogged(t, "grok_subscription_operation_settled", 1)[0]
			if settled["diagnostic_attached"] != (test.phase != "") {
				t.Fatalf("settlement line = %v, want diagnostic_attached=%v", settled, test.phase != "")
			}
			p := f.progressFor(op.OperationId)
			if test.phase == "" {
				if p.State != test.state || p.GrokDiagnostic != nil {
					t.Fatalf("progress = %v, want %v without a diagnostic", p, test.state)
				}
			} else {
				requireGrokProgress(t, p, test.state, test.phase, test.code, op.OperationId)
			}
			a := f.assertGrokOperationValid(t)
			if a.Subscription.RecoveryRequired != test.fenced {
				t.Fatalf("recovery fence = %v, want %v", a.Subscription.RecoveryRequired, test.fenced)
			}
		})
	}
}

// A failed removal of superseded generations after Finish settled: the outcome
// keeps whichever failure came first. A succeeded or canceled outcome becomes the
// cleanup diagnostic; a native failure keeps its own diagnostic, and the
// recovery fence reports the outstanding cleanup.
func TestGrokFinishVaultCleanupFailureNeverReplacesTheFirstFailure(t *testing.T) {
	login := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN
	logout := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT
	for _, test := range []struct {
		name    string
		action  pb.SubscriptionAction
		success bool
		stop    bool
		phase   domain.GrokPhase
		code    domain.Code
	}{
		{name: "logout success", action: logout, success: true, phase: domain.GrokCleanup, code: domain.Unavailable},
		{name: "logout failure", action: logout, phase: domain.GrokLogout, code: domain.Unavailable},
		{name: "login failure", action: login, phase: domain.GrokLogin, code: domain.Unavailable},
		{name: "login canceled", action: login, success: true, stop: true, phase: domain.GrokCleanup, code: domain.Unavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGrokFixture(t)
			if test.action != login {
				f.login("synthetic-principal-1")
			}
			op := f.queue(test.action)
			lease, err := f.take(op, test.action)
			if err != nil {
				t.Fatal(err)
			}
			if test.stop {
				f.cancelPending()
			}
			// An unreferenced generation that the cleanup must remove, whose
			// removal then fails.
			orphan := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
			if _, err := f.secrets.Put(t.Context(), orphan, []byte("orphan")); err != nil {
				t.Fatal(err)
			}
			f.secrets.mu.Lock()
			f.secrets.deleteError = domain.Fail(domain.Unavailable, "The vault removal failed.", "")
			f.secrets.mu.Unlock()
			logs := f.captureLogs()
			if _, err := f.finishWith(lease, domain.NewID(), nil, test.success, false, true); err == nil {
				t.Fatal("a failed removal of an unreferenced generation was reported as success")
			}
			// The cleanup diagnostic is logged as recorded only when it replaced the
			// outcome; a native failure keeps its own diagnostic and the cleanup
			// failure is reported as not recorded.
			replaced := 0
			if test.phase == domain.GrokCleanup {
				replaced = 1
			}
			logs.requireLogged(t, "grok_subscription_cleanup_failure_recorded", replaced)
			if kept := logs.requireLogged(t, "grok_subscription_cleanup_failure_not_recorded", 1-replaced); replaced == 0 && kept[0]["reason"] != string(grokRecordKept) {
				t.Fatalf("not-recorded reason = %v", kept[0]["reason"])
			}
			requireGrokProgress(t, f.progressFor(op.OperationId), pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED, test.phase, test.code, op.OperationId)
			f.assertGrokOperationValid(t)
		})
	}
}
