// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// The Grok login helpers that read the initiating actor must keep a real denial
// (a revoked, mismatched or unknown device) apart from an operational read or
// decode failure. Only a denial may settle CANCELED; a failed read says nothing
// about the operator's intent and settles FAILED with the sanitized code.

type actorBreak string

const (
	// actorRevoked is a real denial: the device record says it was revoked.
	actorRevoked actorBreak = "revoked"
	// actorCorrupt is an operational failure: the device record cannot be decoded.
	actorCorrupt actorBreak = "corrupt"
)

// clientInitiated rewrites the queued login so that a paired client device, not
// the owner, is its initiator. The device record is returned for later damage.
func (g *grokLoginRun) clientInitiated() domain.ID {
	g.t.Helper()
	device := domain.NewID()
	_, err := g.f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.client-initiator", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Initiator", Type: domain.ClientDevice, PairedAt: time.Now().UTC()})
	})
	if err != nil {
		g.t.Fatal(err)
	}
	actor := domain.Principal{Type: domain.ClientDevice, DeviceID: device}
	g.f.mutateAccount(g.t, func(a *domain.Account) {
		a.Subscription.Pending.Actor = actor
		a.Subscription.ServerOperation.Actor = actor
	})
	return device
}

// damage breaks the initiator's device record in the requested way.
func (g *grokLoginRun) damage(device domain.ID, how actorBreak) {
	g.t.Helper()
	switch how {
	case actorRevoked:
		_, err := g.f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.client-revoke", nil, func(tx *store.Tx) (any, error) {
			r, err := tx.Get(domain.DeviceKind, device)
			if err != nil {
				return nil, err
			}
			d, err := store.Decode[domain.Device](r)
			if err != nil {
				return nil, err
			}
			d.Revoked = true
			return tx.Put(domain.DeviceKind, r.ID, r.Revision, "", "", d)
		})
		if err != nil {
			g.t.Fatal(err)
		}
	case actorCorrupt:
		db, err := sql.Open("sqlite", filepath.Join(g.f.service.Store.Root(), "state.sqlite"))
		if err != nil {
			g.t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec("UPDATE entities SET body=? WHERE id=?", []byte("{"), string(device)); err != nil {
			g.t.Fatal(err)
		}
	default:
		g.t.Fatalf("unknown damage %q", how)
	}
}

func (g *grokLoginRun) requireLogged(event string) {
	g.t.Helper()
	g.logs.mu.Lock()
	logs := g.logs.buf.String()
	g.logs.mu.Unlock()
	if !strings.Contains(logs, event) || !strings.Contains(logs, g.op) {
		g.t.Fatalf("%s was not logged with the operation correlation id", event)
	}
}

func (g *grokLoginRun) requireNotLogged(event string) {
	g.t.Helper()
	g.logs.mu.Lock()
	logs := g.logs.buf.String()
	g.logs.mu.Unlock()
	if strings.Contains(logs, event) {
		g.t.Fatalf("%s was logged for a real denial", event)
	}
}

// operation returns the stored server record of the login.
func (g *grokLoginRun) operation() domain.ServerSubscriptionOperation {
	g.t.Helper()
	return *g.account().Subscription.ServerOperation
}

// waitStored polls the stored operation: a client-initiated login cannot be
// polled through progress once its initiator is damaged.
func (g *grokLoginRun) waitStored(want domain.SubscriptionLoginState) {
	g.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for g.operation().State != want {
		if time.Now().After(deadline) {
			g.t.Fatalf("the stored operation stayed %v, want %v", g.operation().State, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSubscriptionActorCheckSeparatesDenialFromReadFailure(t *testing.T) {
	g := newGrokLogin(t)
	live := g.clientInitiated()
	revoked := g.clientInitiated()
	g.damage(revoked, actorRevoked)
	corrupt := g.clientInitiated()
	g.damage(corrupt, actorCorrupt)
	worker := domain.NewID()
	if _, err := g.f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.worker-device", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.DeviceKind, worker, 0, "", "", domain.Device{Name: "Worker", Type: domain.WorkerDevice, MachineID: g.f.input.MachineID, PairedAt: time.Now().UTC()})
	}); err != nil {
		t.Fatal(err)
	}
	client := func(id domain.ID) domain.Principal { return domain.Principal{Type: domain.ClientDevice, DeviceID: id} }
	for _, test := range []struct {
		name      string
		actor     domain.Principal
		cancelled bool
		valid     bool
		readError bool
	}{
		{name: "owner", actor: domain.Principal{Type: domain.OwnerDevice}, valid: true},
		{name: "live client", actor: client(live), valid: true},
		{name: "revoked client is a denial", actor: client(revoked)},
		{name: "unknown device is a denial", actor: client(domain.NewID())},
		{name: "malformed device id is a denial", actor: domain.Principal{Type: domain.ClientDevice}},
		{name: "device of another kind is a denial", actor: client(g.f.input.AccountID)},
		{name: "type mismatch is a denial", actor: domain.Principal{Type: domain.WorkerDevice, DeviceID: live}},
		{name: "machine mismatch is a denial", actor: domain.Principal{Type: domain.WorkerDevice, DeviceID: worker, MachineID: domain.NewID()}},
		{name: "decode failure is a read failure", actor: client(corrupt), readError: true},
		{name: "storage failure is a read failure", actor: client(live), cancelled: true, readError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var valid bool
			var checkErr, validErr error
			if err := g.f.service.Store.Read(ctx, func(tx *store.Tx) error {
				if test.cancelled {
					cancel()
				}
				valid, checkErr = subscriptionActorCheck(tx, test.actor)
				validErr = subscriptionActorValid(tx, test.actor)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if valid != test.valid || (checkErr != nil) != test.readError {
				t.Fatalf("valid = %v, err = %v", valid, checkErr)
			}
			// The legacy predicate keeps treating every non-live actor as denied.
			if (validErr == nil) != test.valid || (validErr != nil && !subscriptionIsDenied(validErr)) {
				t.Fatalf("subscriptionActorValid = %v", validErr)
			}
			if checkErr != nil && subscriptionIsDenied(checkErr) {
				t.Fatal("a read failure was reported as the denial")
			}
		})
	}
}

func TestGrokLoginWatcherActorDenialSettlesCanceledButActorReadFailureSettlesFailed(t *testing.T) {
	for _, test := range []struct {
		name  string
		how   actorBreak
		state domain.SubscriptionLoginState
	}{
		{name: "revoked initiator", how: actorRevoked, state: domain.SubscriptionCanceled},
		{name: "unreadable initiator", how: actorCorrupt, state: domain.SubscriptionFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := newGrokLogin(t)
			device := g.clientInitiated()
			g.run()
			g.waitStored(domain.SubscriptionWaiting)
			// The damage lands after the claim and the publication, so only the
			// cancellation watcher can observe it.
			g.damage(device, test.how)
			g.await()
			op := g.operation()
			if op.State != test.state || g.account().Subscription.Pending != nil || g.idp.TokenRequests != 0 || !g.listenerClosed() {
				t.Fatalf("state = %v, token requests = %d", op.State, g.idp.TokenRequests)
			}
			switch test.state {
			case domain.SubscriptionCanceled:
				if op.GrokDiagnostic != nil {
					t.Fatalf("a cancellation kept a diagnostic: %v", op.GrokDiagnostic)
				}
				g.requireNotLogged("grok_login_watcher_read_failed")
			default:
				if op.GrokDiagnostic == nil || op.GrokDiagnostic.CorrelationID != g.op || op.GrokDiagnostic.Code == domain.RecoveryRequired {
					t.Fatalf("diagnostic = %v", op.GrokDiagnostic)
				}
				g.requireLogged("grok_login_watcher_read_failed")
			}
		})
	}
}

func TestGrokLoginClaimActorDenialSettlesCanceledButActorReadFailureSettlesFailed(t *testing.T) {
	for _, test := range []struct {
		name  string
		how   actorBreak
		state domain.SubscriptionLoginState
	}{
		{name: "revoked initiator", how: actorRevoked, state: domain.SubscriptionCanceled},
		{name: "unreadable initiator", how: actorCorrupt, state: domain.SubscriptionFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := newGrokLogin(t)
			device := g.clientInitiated()
			g.damage(device, test.how)
			g.run()
			g.await()
			a := g.account()
			op := a.Subscription.ServerOperation
			if op.State != test.state || op.NativeStarted || op.ExchangeClaimed || a.Subscription.Pending != nil || a.Subscription.RecoveryRequired || a.Connection != nil || !g.vaultEmpty() || g.idp.TokenRequests != 0 {
				t.Fatalf("state = %v, native started = %v", op.State, op.NativeStarted)
			}
			if test.state == domain.SubscriptionFailed {
				if op.GrokDiagnostic == nil || op.GrokDiagnostic.CorrelationID != g.op || op.GrokDiagnostic.Code == domain.RecoveryRequired {
					t.Fatalf("diagnostic = %v", op.GrokDiagnostic)
				}
				g.requireLogged("grok_login_actor_read_failed")
			} else {
				if op.GrokDiagnostic != nil {
					t.Fatalf("a cancellation kept a diagnostic: %v", op.GrokDiagnostic)
				}
				g.requireNotLogged("grok_login_actor_read_failed")
			}
		})
	}
}

// claimedForTest moves the queued login to the state the later helpers expect:
// started, waiting and, optionally, with the exchange claimed.
func (g *grokLoginRun) claimedForTest(exchange bool) domain.ServerSubscriptionOperation {
	g.t.Helper()
	g.f.mutateAccount(g.t, func(a *domain.Account) {
		st := a.Subscription
		st.ServerOperation.NativeStarted = true
		st.ServerOperation.State = domain.SubscriptionWaiting
		st.ServerOperation.ExchangeClaimed = exchange
		st.Pending.Phase = domain.SubscriptionClaimed
	})
	return g.operation()
}

func (g *grokLoginRun) ownerContext() context.Context {
	return domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
}

func TestGrokLoginPresentationAndExchangeClaimKeepActorReadFailuresApartFromDenials(t *testing.T) {
	for _, step := range []struct {
		name string
		call func(g *grokLoginRun, o domain.ServerSubscriptionOperation) error
		// exchange is the stored claim state the step starts from.
		exchange bool
	}{
		{name: "presentation", call: func(g *grokLoginRun, o domain.ServerSubscriptionOperation) error {
			return g.f.service.publishGrokPresentation(g.ownerContext(), g.f.input.AccountID, o, subscriptionProgress{URL: "https://accounts.x.ai/oauth2/authorize", Until: o.ExpiresAt})
		}},
		{name: "exchange claim", call: func(g *grokLoginRun, o domain.ServerSubscriptionOperation) error {
			return g.f.service.claimGrokExchange(g.ownerContext(), g.f.input.AccountID, o)
		}},
		{name: "native claim", call: func(g *grokLoginRun, o domain.ServerSubscriptionOperation) error {
			return g.f.service.claimGrokCLI(g.ownerContext(), g.f.input.AccountID, o)
		}},
	} {
		for _, how := range []actorBreak{actorRevoked, actorCorrupt} {
			t.Run(step.name+" "+string(how), func(t *testing.T) {
				g := newGrokLogin(t)
				device := g.clientInitiated()
				o := g.claimedForTest(false)
				if step.name == "native claim" {
					g.f.mutateAccount(t, func(a *domain.Account) { a.Subscription.ServerOperation.State = domain.SubscriptionPreparing })
					o = g.operation()
				}
				g.damage(device, how)
				err := step.call(g, o)
				if err == nil {
					t.Fatal("the step succeeded for a broken initiator")
				}
				denied := subscriptionIsDenied(err)
				if denied != (how == actorRevoked) {
					t.Fatalf("denied = %v for %q: %v", denied, how, err)
				}
				if how == actorCorrupt && domain.SafeError(err).Code == domain.RecoveryRequired {
					t.Fatalf("a read failure carries the denial code: %v", err)
				}
				if g.operation().ExchangeClaimed {
					t.Fatal("a refused step recorded the exchange claim")
				}
			})
		}
	}
}

func TestGrokLoginClaimRefusalCodeKeepsOperationalFailures(t *testing.T) {
	if got := grokClaimRefusalCode(subscriptionDenied()); got != domain.Conflict {
		t.Fatalf("denial code = %q", got)
	}
	if got := grokClaimRefusalCode(domain.Fail(domain.Unavailable, "State storage failed.", "Retry.")); got != domain.Unavailable {
		t.Fatalf("read failure code = %q", got)
	}
}

func TestGrokLoginSettlementKeepsActorReadFailureApartFromRevocation(t *testing.T) {
	for _, test := range []struct {
		name     string
		how      actorBreak
		claimed  bool
		outcome  domain.SubscriptionLoginState
		expected domain.SubscriptionLoginState
		code     domain.Code
	}{
		{name: "revoked before the claim", how: actorRevoked, outcome: domain.SubscriptionFailed, expected: domain.SubscriptionCanceled},
		{name: "unreadable before the claim", how: actorCorrupt, outcome: domain.SubscriptionFailed, expected: domain.SubscriptionFailed, code: domain.Unavailable},
		{name: "revoked after the claim", how: actorRevoked, claimed: true, outcome: domain.SubscriptionFailed, expected: domain.SubscriptionRecovery},
		{name: "unreadable after the claim keeps the definitive failure", how: actorCorrupt, claimed: true, outcome: domain.SubscriptionFailed, expected: domain.SubscriptionFailed, code: domain.Unavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := newGrokLogin(t)
			device := g.clientInitiated()
			o := g.claimedForTest(test.claimed)
			g.damage(device, test.how)
			out := grokLoginOutcome{state: test.outcome, reason: "callback", code: test.code}
			if err := g.f.service.finishGrokLogin(g.ownerContext(), g.f.input.AccountID, o, &out, false, false); err != nil {
				t.Fatal(err)
			}
			a := g.account()
			if got := a.Subscription.ServerOperation.State; got != test.expected || out.state != test.expected {
				t.Fatalf("state = %v, outcome = %v, want %v", got, out.state, test.expected)
			}
			if test.expected == domain.SubscriptionFailed {
				d := a.Subscription.ServerOperation.GrokDiagnostic
				if d == nil || d.Code != test.code || d.CorrelationID != g.op {
					t.Fatalf("diagnostic = %v", d)
				}
				g.requireLogged("grok_login_actor_read_failed")
			}
			if a.Connection != nil || !g.vaultEmpty() {
				t.Fatal("a broken initiator published credentials")
			}
		})
	}
}
