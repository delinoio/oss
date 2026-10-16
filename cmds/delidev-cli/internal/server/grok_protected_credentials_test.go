// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// putGrokBundle stores a sealed Grok bundle as a protected generation of the
// account and returns its reference.
func putGrokBundle(t *testing.T, f *subscriptionFixture, account domain.ID) credentials.Ref {
	t.Helper()
	generation := domain.NewID()
	sealed, err := subscription.SealGrokBundle(f.service.Identity.ServerID, account, generation, grokServerBundle(t, "synthetic-principal-1"))
	if err != nil {
		t.Fatal(err)
	}
	ref := credentials.Ref{Owner: account, ID: generation, Purpose: credentials.AccountLogin}
	if _, err := f.secrets.Put(context.Background(), ref, sealed); err != nil {
		t.Fatal(err)
	}
	return ref
}

func setGrokService(t *testing.T, f *subscriptionFixture, edit func(*domain.Account)) {
	t.Helper()
	_, err := f.service.Store.Mutate(failedLoginContext(), domain.NewID(), "fixture.grok.protected", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.SubscriptionService, a.Subscription = domain.SubscriptionGrok, nil
		if edit != nil {
			edit(&a)
		}
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Failed-login cleanup (capability 41) never selects a connected Grok account and
// never removes its protected bundle.
func TestFailedLoginCleanupNeverTouchesAConnectedGrokBundle(t *testing.T) {
	f := unreferencedInitialSubscription(t)
	excludeUnrelatedBatchFixture(t, f)
	ref := putGrokBundle(t, f, f.input.AccountID)
	setGrokService(t, f, func(a *domain.Account) {
		a.Connection = &domain.AccountConnection{ID: ref.ID, Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
		a.Health = domain.AccountReady
		a.Subscription = &domain.SubscriptionState{Generation: ref.ID, IdentityCommitment: strings.Repeat("a", 64)}
	})
	accepted := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
	if accepted.Job.Total != 0 {
		t.Fatalf("a connected Grok account was selected for failed-login cleanup: %v", accepted)
	}
	if _, err := f.secrets.Get(context.Background(), ref); err != nil {
		t.Fatal("failed-login cleanup removed a protected Grok bundle", err)
	}
}

// A failed Grok login that fenced the account for recovery is not a Codex failed
// login: it is neither selected by the batch nor consumed by the background
// controller, and its vault references stay.
func TestFailedLoginCleanupDoesNotAdoptAFencedGrokLogin(t *testing.T) {
	f := unreferencedInitialSubscription(t)
	excludeUnrelatedBatchFixture(t, f)
	ref := putGrokBundle(t, f, f.input.AccountID)
	var operation domain.ID
	setGrokService(t, f, func(a *domain.Account) {
		now := time.Now().UTC()
		operation = domain.NewID()
		a.Health = domain.AccountFailed
		a.Subscription = &domain.SubscriptionState{RecoveryRequired: true, ServerOperation: &domain.ServerSubscriptionOperation{ID: operation, Action: domain.SubscriptionLogin, Epoch: domain.NewID(), FinishID: domain.NewID(), Actor: domain.Principal{Type: domain.OwnerDevice}, State: domain.SubscriptionRecovery, ExchangeClaimed: true, NativeStarted: true, StartedAt: now, ExpiresAt: now.Add(time.Minute)},
			Pending: &domain.SubscriptionOperation{ID: operation, Action: domain.SubscriptionLogin, Actor: domain.Principal{Type: domain.OwnerDevice}, Phase: domain.SubscriptionClaimed}}
	})
	_, a := f.record()
	if failedCleanupCandidate(a) || failedServerLoginNeedsCleanup(a) || disconnectedSubscription(a) {
		t.Fatal("a fenced Grok login was adopted by the Codex failed-login cleanup")
	}
	accepted := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
	if accepted.Job.Total != 0 {
		t.Fatalf("batch selected a fenced Grok login: %v", accepted)
	}
	if err := f.service.recoverFailedServerLogin(failedLoginContext(), f.input.AccountID, operation); err == nil {
		t.Fatal("the background controller recovered a Grok login as a Codex login")
	}
	if _, err := f.secrets.Get(context.Background(), ref); err != nil {
		t.Fatal("cleanup removed a protected Grok bundle", err)
	}
}

// A disconnected Grok account keeps a protected reference of another purpose,
// but its retained AccountLogin generation is removed by the explicit batch
// deletion.
func TestFailedLoginCleanupBatchRemovesRetainedGrokBundleAndKeepsOtherPurposes(t *testing.T) {
	f := unreferencedInitialSubscription(t)
	excludeUnrelatedBatchFixture(t, f)
	ref := putGrokBundle(t, f, f.input.AccountID)
	protected := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountAPI}
	if _, err := f.secrets.Put(context.Background(), protected, []byte("synthetic-api-key")); err != nil {
		t.Fatal(err)
	}
	setGrokService(t, f, nil)
	accepted := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
	runFailedCleanup(t, f, accepted.Job.Id)
	result := readFailedCleanup(t, f, accepted.Job.Id, "")
	if result.Job.Deleted != 0 || result.Job.Retained != 1 {
		t.Fatal("status bypassed the protected-reference proof", result)
	}
	if _, err := f.secrets.Get(context.Background(), protected); err != nil {
		t.Fatal("batch cleanup deleted a protected reference of another purpose", err)
	}
	if err := f.secrets.Delete(context.Background(), protected); err != nil {
		t.Fatal(err)
	}
	accepted = acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
	runFailedCleanup(t, f, accepted.Job.Id)
	if result := readFailedCleanup(t, f, accepted.Job.Id, ""); result.Job.Deleted != 1 {
		t.Fatal("a disconnected Grok account with a retained generation was not deleted", result)
	}
	if _, err := f.secrets.Get(context.Background(), ref); err == nil {
		t.Fatal("explicit deletion retained the Grok bundle")
	}
}

// The retained logout-cleanup shape is consumed by explicit deletion only; the
// batch never creates that target.
func TestFailedLoginCleanupBatchNeverSelectsAGrokLogoutCleanupShape(t *testing.T) {
	d := newGrokDeletion(t)
	d.f.secrets.mu.Lock()
	d.f.secrets.deleteError = context.DeadlineExceeded
	d.f.secrets.mu.Unlock()
	if err := d.remove(context.Background(), d.request()); err == nil {
		t.Fatal("deletion was acknowledged with an unconfirmed credential removal")
	}
	if !serverLogoutRecoveryCandidate(d.account()) {
		t.Fatal("fixture does not hold the retained logout-cleanup shape")
	}
	excludeUnrelatedBatchFixture(t, d.f.subscriptionFixture)
	accepted := acceptFailedCleanup(t, d.f.subscriptionFixture, failedLoginContext(), domain.NewID())
	if accepted.Job.Total != 0 {
		t.Fatalf("the batch selected a Grok logout-cleanup account: %v", accepted)
	}
	if len(d.f.references()) != 1 {
		t.Fatal("the protected bundle reference vanished")
	}
}

// Diagnostics never read a Grok account's protected storage, whatever its state.
func TestDiagnosticsNeverReadAGrokBundleEvenDuringLogout(t *testing.T) {
	s, secrets := newDoctorFixture(t)
	a := doctorChatGPTAccount()
	a.SubscriptionService = domain.SubscriptionGrok
	doctorPending(&a, domain.SubscriptionLogout, true)
	id := domain.NewID()
	doctorPut(t, s, domain.AccountKind, id, 0, a)
	secrets.values[credentials.Ref{Owner: id, ID: a.Subscription.Generation, Purpose: credentials.AccountLogin}] = []byte("private-synthetic-grok-bundle")
	result, err := s.doctorCredential(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets.refs) != 0 || result.Result.State != domain.DiagnosticUnavailable || result.Result.Code != domain.Unsupported {
		t.Fatalf("diagnostics inspected a Grok bundle: %+v", result)
	}
	if strings.Contains(result.Result.Guidance, "private-synthetic") {
		t.Fatal("diagnostics echoed a bundle value")
	}
}

// A restored backup holding a queued Grok logout keeps the account quarantined:
// the scanner never starts the revocation, the logout cannot be requested again
// while the account is disconnected, and the protected bundle stays gated.
func TestGrokBackupRestoreNeverResumesAQueuedLogout(t *testing.T) {
	g := newGrokLogout(t)
	g.request()
	g.f.restoreGrokImage(t, func(a *domain.Account) {
		a.Subscription.Pending, a.Subscription.ServerOperation = nil, nil
		a.Health = domain.AccountReady
	})
	_, restored := g.f.record()
	st := restored.Subscription
	if restored.Connection != nil || !st.RecoveryRequired || st.Generation != g.generation || st.ServerOperation == nil || st.ServerOperation.Action != domain.SubscriptionLogout {
		t.Fatal("restore did not quarantine the queued Grok logout")
	}
	g.restart()
	if g.idp.RevokeRequests != 0 || g.revokes.Load() != 0 {
		t.Fatal("a restored image started a revocation")
	}
	if _, err := g.f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.Action = grokLogoutAction }); err == nil {
		t.Fatal("a logout was accepted for a quarantined restored account")
	}
	if refs := g.f.references(); len(refs) != 1 {
		t.Fatalf("restore changed the vault: %v", refs)
	}
}
