// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// These tests make every Take, Finish, abort and loss interleaving of a Grok
// account lease explicit. The contract document lists the same rows.

type hookGrokSecrets struct {
	*accountTestSecrets
	beforeGet func()
}

func (v *hookGrokSecrets) Get(ctx context.Context, ref credentials.Ref) ([]byte, error) {
	if hook := v.beforeGet; hook != nil {
		v.beforeGet = nil
		hook()
	}
	return v.accountTestSecrets.Get(ctx, ref)
}

func (f *grokFixture) cancelPending() {
	f.t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.cancel", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Subscription.Pending.Canceled = true
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		f.t.Error(err)
	}
}

func (f *grokFixture) references() map[domain.ID]bool {
	f.t.Helper()
	refs, err := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID)
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[domain.ID]bool{}
	for _, ref := range refs {
		out[ref.ID] = true
	}
	return out
}

// A Take that fails after its lease committed delivers nothing and fences the
// account, because the Worker never learned the lease it must Finish.
func TestGrokTakeFailuresAfterTheLeaseCommitFenceWithoutDelivery(t *testing.T) {
	for name, disturb := range map[string]func(f *grokFixture, vault *hookGrokSecrets, generation domain.ID){
		"invalid-stored-envelope": func(f *grokFixture, vault *hookGrokSecrets, generation domain.ID) {
			vault.mu.Lock()
			vault.values[credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}] = []byte(`{"version":1}`)
			vault.mu.Unlock()
		},
		"missing-stored-generation": func(f *grokFixture, vault *hookGrokSecrets, generation domain.ID) {
			vault.mu.Lock()
			delete(vault.values, credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin})
			vault.mu.Unlock()
		},
		"canceled-during-the-vault-read": func(f *grokFixture, vault *hookGrokSecrets, generation domain.ID) {
			vault.beforeGet = f.cancelPending
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newGrokFixture(t)
			vault := &hookGrokSecrets{accountTestSecrets: f.secrets}
			f.service.accountSecrets = vault
			generation := f.login("synthetic-principal-1")
			disturb(f, vault, generation)
			response, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
			if err == nil || response != nil {
				t.Fatal("a Take that failed after its lease committed delivered a response")
			}
			if _, a := f.record(); !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil || a.Subscription.Generation != generation {
				t.Fatal("the failed Take did not keep the lease fenced under the current generation")
			}
		})
	}
}

// Every combination of the Worker's report maps to one explicit account outcome.
// A lease is released only by a fully confirmed outcome (or a failed login that
// confirmed cleanup and produced no credential); everything else keeps the lease
// fenced under the generation the account already referenced.
func TestGrokFinishReportsMapToExplicitOutcomes(t *testing.T) {
	login, refresh, logout := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT
	const (
		released = "released"
		fenced   = "fenced"
	)
	for _, c := range []struct {
		name                     string
		action                   pb.SubscriptionAction
		success, confirmed, done bool
		lease                    string
		published                bool // the account references the Finish's new generation
		connected                bool
		references               int
	}{
		{"login-confirmed", login, true, false, true, released, true, true, 1},
		{"login-failed-with-cleanup", login, false, false, true, released, false, false, 0},
		{"login-succeeded-without-cleanup", login, true, false, false, fenced, false, false, 0},
		{"refresh-confirmed", refresh, true, true, true, released, true, true, 1},
		{"refresh-unconfirmed-by-native-process", refresh, true, false, true, fenced, false, true, 1},
		{"refresh-failed-with-cleanup", refresh, false, true, true, fenced, false, true, 1},
		{"refresh-without-cleanup", refresh, true, true, false, fenced, false, true, 1},
		{"logout-confirmed", logout, true, false, true, released, false, false, 0},
		{"logout-failed-with-cleanup", logout, false, false, true, fenced, false, true, 1},
		{"logout-without-cleanup", logout, true, false, false, fenced, false, true, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newGrokFixture(t)
			var current domain.ID
			if c.action != login {
				current = f.login("synthetic-principal-1")
			}
			if c.action == logout {
				f.markRevoked(t)
			}
			lease, err := f.take(f.queue(c.action), c.action)
			if err != nil {
				t.Fatal(err)
			}
			next := domain.NewID()
			var bundle []byte
			switch c.action {
			case login:
				bundle = f.sealed(t, f.input.AccountID, next, "synthetic-principal-1")
			case refresh:
				bundle = f.grokRotatedEnvelope(next)
			}
			if _, err := f.finishWith(lease, next, bundle, c.success, c.confirmed, c.done); err != nil {
				t.Fatal(err)
			}
			_, a := f.record()
			st := a.Subscription
			if (st.Lease == nil) != (c.lease == released) || st.RecoveryRequired != (c.lease == fenced) || (a.Connection != nil) != c.connected {
				t.Fatalf("lease, fence or connection outcome differs from the contract (lease=%v fenced=%v connected=%v)", st.Lease != nil, st.RecoveryRequired, a.Connection != nil)
			}
			want := current
			if c.published {
				want = next
			} else if c.action == logout && c.lease == released {
				want = ""
			}
			if st.Generation != want {
				t.Fatal("the account references the wrong generation")
			}
			refs := f.references()
			if len(refs) != c.references || want != "" && !refs[want] || next != want && refs[next] {
				t.Fatal("the vault holds a generation the account does not reference, or lacks the one it references")
			}
		})
	}
}

// A cancellation that wins while the new generation is being staged never lets
// that generation become current, and the staged credential is removed.
func TestGrokFinishCanceledDuringStagingRemovesTheStagedGeneration(t *testing.T) {
	for _, action := range []pb.SubscriptionAction{pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH} {
		t.Run(action.String(), func(t *testing.T) {
			f := newGrokFixture(t)
			var current domain.ID
			if action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH {
				current = f.login("synthetic-principal-1")
			}
			lease, err := f.take(f.queue(action), action)
			if err != nil {
				t.Fatal(err)
			}
			next := domain.NewID()
			bundle := f.sealed(t, f.input.AccountID, next, "synthetic-principal-1")
			if action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH {
				bundle = f.grokRotatedEnvelope(next)
			}
			f.secrets.afterPut = f.cancelPending
			if _, err := f.finishWith(lease, next, bundle, true, true, true); err != nil {
				t.Fatal(err)
			}
			_, a := f.record()
			if a.Subscription.Generation != current || a.Connection != nil != (current != "") {
				t.Fatal("a canceled operation published its generation")
			}
			if refs := f.references(); refs[next] || current != "" && !refs[current] {
				t.Fatal("the staged generation survived the cancellation or the current one was removed")
			}
		})
	}
}

// A cleanup failure after the publication committed is surfaced and fences the
// account, but the published generation is never retired as if it were unpublished.
func TestGrokFinishCleanupFailureAfterPublicationKeepsThePublishedGeneration(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
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
	_, a := f.record()
	if a.Subscription.Generation != rotated || a.Subscription.Lease != nil || !a.Subscription.RecoveryRequired {
		t.Fatal("the committed publication was not kept or the failed removal was not fenced")
	}
	if refs := f.references(); !refs[rotated] || !refs[generation] {
		t.Fatal("the published generation was retired, or the superseded generation vanished despite the failed removal")
	}
}

// The retried Finish of an already published operation is a receipt replay: it
// repeats no vault write or removal and leaves the account untouched.
func TestGrokFinishReplayRepeatsNoVaultWork(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	rotated := domain.NewID()
	bundle := f.grokRotatedEnvelope(rotated)
	if _, err := f.finishWith(lease, rotated, bundle, true, true, true); err != nil {
		t.Fatal(err)
	}
	record, _ := f.record()
	puts, deletes, held := f.secrets.counts()
	replay, err := f.finishWith(lease, rotated, bundle, true, true, true)
	if err != nil || !replay.Replayed {
		t.Fatal("the retried Finish was not a receipt replay")
	}
	if p, d, h := f.secrets.counts(); p != puts || d != deletes || h != held {
		t.Fatal("a replayed Finish touched the vault")
	}
	if again, a := f.record(); again.Revision != record.Revision || a.Subscription.Generation != rotated || a.Subscription.RecoveryRequired {
		t.Fatal("a replayed Finish changed the account")
	}
}

// A Finish that does not match the held lease is refused before it can act, and
// the genuine owner can still finish afterwards.
func TestGrokFinishWithAStaleLeaseIsDeniedWithoutFencing(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	rotated := domain.NewID()
	bundle := f.grokRotatedEnvelope(rotated)
	for name, stale := range map[string]*pb.TakeSubscriptionResponse{
		"revision":   {LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, LeaseRevision: lease.LeaseRevision + 1},
		"generation": {LeaseId: lease.LeaseId, GenerationId: string(domain.NewID()), LeaseRevision: lease.LeaseRevision},
		"lease":      {LeaseId: string(domain.NewID()), GenerationId: lease.GenerationId, LeaseRevision: lease.LeaseRevision},
	} {
		if _, err := f.finishWith(stale, rotated, bundle, true, true, true); err == nil {
			t.Fatalf("a stale %s Finish was accepted", name)
		}
		if _, a := f.record(); a.Subscription.RecoveryRequired || a.Subscription.Lease == nil || a.Subscription.Generation != generation {
			t.Fatalf("a stale %s Finish fenced or released the lease", name)
		}
	}
	if refs := f.references(); len(refs) != 1 || !refs[generation] {
		t.Fatal("a refused Finish staged a generation")
	}
	if _, err := f.finishWith(lease, rotated, bundle, true, true, true); err != nil {
		t.Fatal("the genuine owner could not finish after refused attempts")
	}
}

// A Worker instance that is lost while it holds a lease (a crash or restart
// whose native credential file may still exist) fences the lease. A later Finish
// from the same lease is refused, so no second generation can appear and no
// credential is redistributed.
func TestGrokLostWorkerFencesTheLeaseAndRefusesLaterFinish(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.retainLostSubscriptionLeases(f.input.MachineID, f.instance, false); err != nil {
		t.Fatal(err)
	}
	if _, a := f.record(); !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil || a.Subscription.Generation != generation {
		t.Fatal("the lost Worker's lease was not fenced under its generation")
	}
	rotated := domain.NewID()
	if _, err := f.finishWith(lease, rotated, f.grokRotatedEnvelope(rotated), true, true, true); err == nil {
		t.Fatal("a fenced lease accepted a Finish")
	}
	if _, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); err == nil {
		t.Fatal("a fenced account redistributed its credential")
	}
	if refs := f.references(); len(refs) != 1 || !refs[generation] {
		t.Fatal("a refused Finish or Take changed the vault")
	}
}
