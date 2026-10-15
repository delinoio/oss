// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// failDurableReads makes every read of the durable login operation (the store
// watcher and the pre-poll check) fail while the returned flag is set. It stands
// for a store that cannot answer, which proves nothing about a cancellation.
func failDurableReads(g *grokLoginFixture) *atomic.Bool {
	var failing atomic.Bool
	g.service.grokFaultHook = func(point grokFaultPoint) error {
		if point == grokFaultDurableRead && failing.Load() {
			return errors.New("injected durable read failure")
		}
		return nil
	}
	return &failing
}

// The durable-read table of a login. A durable state that cannot be read is not
// a cancellation: once the exchange is claimed the request may have redeemed the
// credential, so the login is fenced and never resent; before the claim nothing
// was sent, so ending it is a plain stop. A confirmed cancellation keeps its own
// rows (TestGrokDeviceLoginCancelAtEachPhaseHasADistinctOutcome and
// TestGrokDeviceLoginCancelCommittedJustBeforeAPollSendsNoFurtherPoll).
//
//	durable read          | claimed | observed by      | outcome
//	unreadable            | yes     | pre-poll check   | RECOVERY_REQUIRED, no further poll
//	unreadable            | yes     | watcher (wait)   | RECOVERY_REQUIRED, no further poll
//	unreadable            | no      | watcher (wait)   | CANCELED, nothing claimed or sent
//	unreadable            | no      | watcher (browser)| CANCELED, nothing claimed or sent
func TestGrokLoginUnreadableDurableStateIsNeverACancellation(t *testing.T) {
	requireFencedClaim := func(t *testing.T, g *grokDeviceFixture, run *grokRun) {
		t.Helper()
		p, a := g.settle(t, run, stateRecovery)
		st := a.Subscription
		if g.idp.DevicePolls() != 1 || !st.RecoveryRequired || st.Pending == nil || !st.ServerOperation.CodeConsumed || st.ServerOperation.State != domain.SubscriptionRecovery || p.GrokDiagnostic == nil || p.GrokDiagnostic.Code != string(domain.RecoveryRequired) {
			t.Fatalf("an unreadable durable state did not keep the claimed login in recovery (polls=%d)", g.idp.DevicePolls())
		}
		g.requireNoBundle(t, a)
		// The claimed device code is never resent or released automatically.
		if grokLoginNeedsRecovery(a) || g.service.recoverGrokLogin(ownerContext(), g.input.AccountID, st.ServerOperation.ID) == nil || g.idp.DevicePolls() != 1 || g.idp.DeviceRequests() != 1 {
			t.Fatal("recovery released or resent a claimed device login")
		}
	}
	t.Run("claimed, found by the pre-poll check", func(t *testing.T) {
		g := newGrokDeviceFixture(t)
		g.idp.DeviceScript = pendingScript(5)
		failing := failDurableReads(g.grokLoginFixture)
		g.clock.mu.Lock()
		g.clock.hook = func(_ context.Context, n int) error {
			if n == 2 {
				// The state becomes unreadable as the second interval elapses.
				failing.Store(true)
			}
			return nil
		}
		g.clock.mu.Unlock()
		requireFencedClaim(t, g, g.startDevice(t))
	})
	t.Run("claimed, found by the watcher while waiting", func(t *testing.T) {
		g := newGrokDeviceFixture(t)
		g.idp.DeviceScript = pendingScript(5)
		failing := failDurableReads(g.grokLoginFixture)
		entered, release := make(chan struct{}), make(chan struct{})
		g.clock.blockAt(2, entered, release)
		run := g.startDevice(t)
		awaitServerFixture(t, entered)
		failing.Store(true)
		requireFencedClaim(t, g, run)
	})
	t.Run("not claimed, found by the watcher while waiting", func(t *testing.T) {
		g := newGrokDeviceFixture(t)
		failing := failDurableReads(g.grokLoginFixture)
		entered, release := make(chan struct{}), make(chan struct{})
		g.clock.blockAt(1, entered, release)
		run := g.startDevice(t)
		awaitServerFixture(t, entered)
		failing.Store(true)
		_, a := g.settle(t, run, stateCanceled)
		if g.idp.DevicePolls() != 0 || a.Subscription.ServerOperation.CodeConsumed || a.Subscription.RecoveryRequired {
			t.Fatal("an unreadable state before the claim must end the login without a claim or a fence")
		}
		g.requireNoBundle(t, a)
	})
	t.Run("browser, not claimed, found by the watcher", func(t *testing.T) {
		g := newGrokLoginFixture(t)
		failing := failDurableReads(g)
		run := g.begin(t)
		failing.Store(true)
		g.awaitDone(t, run)
		_, a := g.record()
		p := g.progress(run)
		if p.State != stateCanceled || a.Subscription.ServerOperation.CodeConsumed || a.Subscription.RecoveryRequired || g.idp.TokenRequests() != 0 || g.refs(t) != 0 {
			t.Fatal("an unreadable state before the callback must end the login without a claim or a token request")
		}
	})
}
