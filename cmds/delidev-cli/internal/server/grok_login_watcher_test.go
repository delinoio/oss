// SPDX-License-Identifier: Apache-2.0
package server

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/grokidptest"
)

// injectWatcherFault makes every following cancellation-watcher read fail with
// an operational storage error until the returned function restores it.
func injectWatcherFault(g *grokLoginRun) (restore func()) {
	var failing atomic.Bool
	g.f.service.grokLoginWatchFault = func() error {
		if failing.Load() {
			return domain.Fail(domain.Unavailable, "State storage failed.", "Retry.")
		}
		return nil
	}
	failing.Store(true)
	return func() { failing.Store(false) }
}

func (g *grokLoginRun) requireWatcherFailureLogged() {
	g.t.Helper()
	g.logs.mu.Lock()
	logs := g.logs.buf.String()
	g.logs.mu.Unlock()
	if !strings.Contains(logs, "grok_login_watcher_read_failed") || !strings.Contains(logs, g.op) {
		g.t.Fatal("the watcher failure was not logged with the operation correlation id")
	}
}

func TestGrokBrowserLoginWatcherReadFailureSettlesFailedNotCanceled(t *testing.T) {
	g := newGrokLogin(t)
	g.waiting()
	restore := injectWatcherFault(g)
	// The fault is one-shot for the settlement: the watcher has already stopped
	// the flow by the time the durable state is read again.
	g.f.service.grokLoginBeforeSettle = restore
	p := g.waitState(grokFailedSt)
	g.await()
	requireGrokProgressOnly(t, p, grokFailedSt, domain.Unavailable, g.op)
	if p.Canceled {
		t.Fatal("a storage failure was reported as a cancellation")
	}
	a := g.account()
	if a.Subscription.Pending != nil || a.Subscription.RecoveryRequired || a.Subscription.ServerOperation.ExchangeClaimed || a.Connection != nil || !g.vaultEmpty() || g.idp.TokenRequests != 0 || !g.listenerClosed() {
		t.Fatal("a watcher failure left a pending operation, fence, claim, credential, token request or listener")
	}
	g.requireWatcherFailureLogged()
	g.noLeak()
}

func TestGrokBrowserLoginWatcherReadFailureAfterTheClaimIsRecovery(t *testing.T) {
	g := newGrokLogin(t)
	entered, release := make(chan struct{}), make(chan struct{})
	g.idp.BeforeToken = func() { close(entered); <-release }
	g.waiting()
	g.browse(nil)
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the token request was never sent")
	}
	restore := injectWatcherFault(g)
	g.f.service.grokLoginBeforeSettle = restore
	// The next poll fails while the token request is in flight.
	time.Sleep(3 * grokLoginPollInterval)
	close(release)
	g.waitState(grokRecovery)
	g.await()
	a := g.account()
	if !a.Subscription.RecoveryRequired || a.Connection != nil || !g.vaultEmpty() || g.idp.TokenRequests != 1 {
		t.Fatal("a watcher failure after the claim dropped the recovery fence or resent the code")
	}
	g.noLeak()
}

func TestGrokBrowserLoginWatcherReadFailureDoesNotHideACancellation(t *testing.T) {
	g := newGrokLogin(t)
	g.waiting()
	restore := injectWatcherFault(g)
	// The operator's cancellation lands after the watcher failed and before the
	// settlement: the durable state still wins as CANCELED.
	g.f.service.grokLoginBeforeSettle = func() {
		restore()
		(&grokDeviceRun{grokLoginRun: g}).cancelRPC()
	}
	g.waitState(grokCanceled)
	g.await()
	if p := g.progress(); p.State != grokCanceled || !p.Canceled || p.GrokDiagnostic != nil {
		t.Fatalf("progress = %v", p)
	}
	if a := g.account(); a.Subscription.Pending != nil || a.Subscription.RecoveryRequired || g.idp.TokenRequests != 0 {
		t.Fatal("a cancellation after a watcher failure left state behind")
	}
}

func TestGrokDeviceLoginWatcherReadFailureSettlesFailedNotCanceled(t *testing.T) {
	g := newGrokDeviceRun(t)
	g.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending, grokidptest.DevicePending}
	entered, _ := g.holdWait(2)
	g.run()
	g.waitEntered(entered)
	restore := injectWatcherFault(g.grokLoginRun)
	g.f.service.grokLoginBeforeSettle = restore
	p := g.waitState(grokFailedSt)
	g.await()
	requireGrokProgressOnly(t, p, grokFailedSt, domain.Unavailable, g.op)
	a := g.requireNothingStored(false)
	if a.Subscription.Pending != nil || a.Subscription.RecoveryRequired || len(g.idp.DevicePollForms) != 1 {
		t.Fatalf("polls = %d", len(g.idp.DevicePollForms))
	}
	g.requireWatcherFailureLogged()
	g.noDeviceLeak()
}

// A persistent storage fault that also defeats the settlement keeps the
// existing recovery fence instead of reporting a terminal state.
func TestGrokBrowserLoginWatcherReadFailureWithFailedSettlementKeepsTheFence(t *testing.T) {
	g := newGrokLogin(t)
	g.waiting()
	injectWatcherFault(g)
	g.f.service.grokLoginBeforeSettle = func() {
		g.f.mutateAccount(t, func(a *domain.Account) { a.Subscription.ServerOperation.Epoch = domain.NewID() })
	}
	g.await()
	if a := g.account(); !a.Subscription.RecoveryRequired {
		t.Fatal("a failed settlement did not fence the account for recovery")
	}
}
