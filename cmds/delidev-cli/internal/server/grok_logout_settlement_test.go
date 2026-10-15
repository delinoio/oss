// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// The final disconnect committed but reported a lost commit acknowledgement. The
// account is already disconnected with its generation and revocation marker
// cleared, so the fallback must reconcile it as the committed outcome instead of
// rewriting it to RECOVERY_REQUIRED with a pending placeholder.
func TestGrokLogoutAmbiguousDisconnectCommitIsReconciledNotFenced(t *testing.T) {
	g := newGrokLoginFixture(t)
	g.connected(t)
	g.failAt(grokFaultAfterLogoutDisconnect)
	op := g.logout(t)
	g.service.grokFaultHook = nil
	g.requireDisconnected(t)
	if p := g.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || p.GrokDiagnostic != nil {
		t.Fatalf("progress does not report the committed logout: %v", p.State)
	}
	if g.idp.RevokeRequests() != 1 {
		t.Fatal("the token was revoked more than once")
	}
	// Nothing blocks the next login, deletion or logout request.
	if _, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: g.mutation(), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN})); err != nil {
		t.Fatal("a reconciled logout still blocks a new login", err)
	}
}

// The revocation claim committed but reported a lost commit acknowledgement. The
// durable claim decides: the logout is uncertain and fenced, so no later logout
// can overwrite the claim and send the same refresh token again.
func TestGrokLogoutAmbiguousClaimCommitStaysClaimedAndFenced(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	g.failAt(grokFaultAfterRevokeClaimCommit)
	op := g.logout(t)
	g.service.grokFaultHook = nil
	if g.idp.RevokeRequests() != 0 {
		t.Fatal("a revocation was sent after a claim error")
	}
	g.requireRetained(t, before, domain.GrokRevocationClaimed)
	p := g.progressFor(op.OperationId)
	if p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED || p.GrokDiagnostic == nil || p.GrokDiagnostic.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGOUT || p.GrokDiagnostic.Code != string(domain.RecoveryRequired) {
		t.Fatalf("progress lacks the LOGOUT recovery diagnostic: %v", p.State)
	}
	_, a := g.record()
	if grokLogoutRecoveryKind(a) != grokLogoutNone {
		t.Fatal("a claimed revocation is automatically recoverable")
	}
	// The record describes what was stored, not the stale unclaimed failure.
	finished := ""
	for _, line := range strings.Split(g.logs.String(), "\n") {
		if strings.Contains(line, `"msg":"grok_logout_finished"`) {
			finished = line
		}
	}
	if !strings.Contains(finished, `"state":"recovery`) && !strings.Contains(finished, "RECOVERY") || !strings.Contains(finished, `"claimed":true`) {
		t.Fatalf("the finish record does not describe the reconciled outcome: %s", finished)
	}
	// A new logout cannot overwrite the claim while the fence stands.
	if _, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: g.mutation(), Action: logoutAction})); err == nil {
		t.Fatal("a second logout was accepted over a claimed revocation")
	}
	g.restart(t)
	if g.idp.RevokeRequests() != 0 {
		t.Fatal("a restart sent the claimed revocation")
	}
}

// A logout lease whose commit acknowledgement was lost is fenced so that the
// scanner settles it; nothing was sent and the credential is intact.
func TestGrokLogoutLeaseWithALostAcknowledgementIsFencedForTheScanner(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	op := g.serverStart(logoutAction)
	g.failAt(grokFaultAfterLease)
	g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	g.service.grokFaultHook = nil
	_, a := g.record()
	if !a.Subscription.RecoveryRequired || grokLogoutRecoveryKind(a) != grokLogoutUnclaimed || g.idp.RevokeRequests() != 0 {
		t.Fatal("an unowned logout lease is not fenced for recovery")
	}
	if err := g.service.recoverGrokLogout(ownerContext(), g.input.AccountID, domain.ID(op.OperationId)); err != nil {
		t.Fatal(err)
	}
	_, a = g.record()
	if a.Subscription.RecoveryRequired || a.Subscription.Pending != nil || a.Connection == nil || a.Subscription.Generation != before.Subscription.Generation || g.refs(t) != 1 {
		t.Fatal("the fenced logout lease was not settled with its credential intact")
	}
}

// A logout that failed before its revocation claim leaves the account connected
// and revoked. A refresh would publish a generation that cannot restore readiness,
// so only another logout may follow, and it settles normally.
func TestGrokFailedLogoutAllowsOnlyAnotherLogout(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	g.secrets.mu.Lock()
	saved := g.secrets.values
	g.secrets.values = map[credentials.Ref][]byte{}
	g.secrets.mu.Unlock()
	g.logout(t)
	_, a := g.record()
	if a.Health != domain.AccountRevoked || a.Subscription.Pending != nil || a.Connection == nil || a.Subscription.ServerOperation.State != domain.SubscriptionFailed {
		t.Fatal("the failed logout did not leave the account connected and revoked")
	}
	before2 := g.idp.TokenRequests()
	if _, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: g.mutation(), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH})); err == nil {
		t.Fatal("a refresh was accepted while the account is revoked by a failed logout")
	}
	if _, a = g.record(); a.Subscription.Pending != nil || a.Subscription.Generation != before.Subscription.Generation || g.idp.TokenRequests() != before2 {
		t.Fatal("a refused refresh changed the account or reached the provider")
	}
	g.secrets.mu.Lock()
	g.secrets.values = saved
	g.secrets.mu.Unlock()
	g.logout(t)
	g.requireDisconnected(t)
}

// An uncertain revocation settles the health to failed while the logout is still
// unsettled. New execution keeps the typed logging-out answer instead of falling
// back to the generic unsupported refusal.
func TestGrokExecuteTakeStaysTypedWhileAFencedLogoutIsUnsettled(t *testing.T) {
	g := newGrokLoginFixture(t)
	g.connected(t)
	g.idp.RevokeStatus, g.idp.RevokeBody = http.StatusBadRequest, `{"error":"invalid_grant"}`
	g.logout(t)
	if _, a := g.record(); a.Health != domain.AccountFailed || !a.Subscription.RecoveryRequired || !a.GrokLoggingOut() {
		t.Fatal("the unproven revocation did not leave an identifiable fenced logout")
	}
	op := &pb.RequestSubscriptionResponse{OperationId: string(domain.NewID())}
	if _, err := g.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE); err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Unavailable {
		t.Fatalf("execution during a fenced logout lost its typed refusal: %v", err)
	}
}

// A restart fences a logout that was still waiting for an existing Worker lease.
// The lease's own Finish is refused while that fence stands, so the unclaimed
// logout must settle without touching the lease, which then finishes normally.
func TestGrokRestartedLogoutWaitingForALeaseIsReleasedWithoutTouchingTheLease(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	lease := &domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Action: domain.SubscriptionExecute, MachineID: g.input.MachineID, InstanceID: domain.NewID(), DeviceID: domain.NewID(), Epoch: g.service.subscriptionServerEpoch(), Generation: before.Subscription.Generation, StartedAt: time.Now().UTC()}
	_, err := g.service.Store.Mutate(ownerContext(), domain.NewID(), "fixture.grok.lease", lease, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, g.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Subscription.Lease = lease
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	op := g.serverStart(logoutAction)
	g.restart(t)
	_, a := g.record()
	if !a.Subscription.RecoveryRequired || grokLogoutRecoveryKind(a) != grokLogoutUnclaimed {
		t.Fatal("a restarted logout waiting for a lease is not recoverable")
	}
	if err := g.service.recoverGrokLogout(ownerContext(), g.input.AccountID, domain.ID(op.OperationId)); err != nil {
		t.Fatal(err)
	}
	_, a = g.record()
	st := a.Subscription
	if st.RecoveryRequired || st.Pending != nil || st.Lease == nil || st.Lease.ID != lease.ID || st.ServerOperation.State != domain.SubscriptionFailed || a.Health != domain.AccountRevoked || g.idp.RevokeRequests() != 0 || st.Generation != before.Subscription.Generation {
		t.Fatal("the restarted logout was not released with its Worker lease and credential intact")
	}
	// A confirmed revocation still waits for the lease to go: the cleanup deletes
	// the bundle the lease holds.
	a.Subscription.RecoveryRequired, a.Subscription.Pending = true, &domain.SubscriptionOperation{ID: st.ServerOperation.ID, Action: domain.SubscriptionLogout, Actor: st.ServerOperation.Actor, Phase: domain.SubscriptionQueued}
	a.Subscription.ServerOperation.State, a.Subscription.ServerOperation.GrokRevocation = domain.SubscriptionRecovery, domain.GrokRevocationConfirmed
	a.Subscription.RevokedGeneration = st.Generation
	if grokLogoutRecoveryKind(a) != grokLogoutNone {
		t.Fatal("a revoked logout was recoverable while a lease still holds the bundle")
	}
}

// The Worker-lease logout deletes the revoked bundle before the account stops
// owning it. A removal that fails keeps the lease and the generation as the
// durable cleanup owner, so the exact Worker retries and completes instead of the
// revoked bundle being stranded behind a disconnected account.
func TestGrokWorkerLogoutKeepsCleanupOwnershipUntilTheVaultRemovalIsConfirmed(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	f.markRevoked(t)
	lease, err := f.take(f.queue(logoutAction), logoutAction)
	if err != nil {
		t.Fatal(err)
	}
	f.secrets.mu.Lock()
	f.secrets.deleteError = domain.Fail(domain.Unavailable, "The vault removal failed.", "")
	f.secrets.mu.Unlock()
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, false, true); err == nil {
		t.Fatal("a Finish whose vault removal failed was acknowledged")
	}
	_, a := f.record()
	st := a.Subscription
	if a.Connection == nil || st.Lease == nil || st.Generation != generation || !st.RecoveryRequired || st.RevokedGeneration != generation {
		t.Fatal("a failed vault removal gave up the lease or the generation that own the cleanup")
	}
	f.secrets.mu.Lock()
	f.secrets.deleteError = nil
	f.secrets.mu.Unlock()
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal("the original Worker could not retry the cleanup", err)
	}
	_, a = f.record()
	if a.Connection != nil || a.Health != domain.AccountDisconnected || a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Subscription.Generation != "" {
		t.Fatal("the retried Worker logout did not settle")
	}
	if refs, err := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID); err != nil || len(refs) != 0 {
		t.Fatal("the retried Worker logout left protected generations")
	}
}

// The Worker-lease logout removes the revoked bundle before its commit. A second
// vault enumeration after the commit could only fail on an account that is already
// disconnected and refence it with nothing left to recover, so it does not run.
func TestGrokWorkerLogoutDoesNotRefenceADisconnectedAccountAfterTheCommit(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	f.markRevoked(t)
	lease, err := f.take(f.queue(logoutAction), logoutAction)
	if err != nil {
		t.Fatal(err)
	}
	f.secrets.mu.Lock()
	before := f.secrets.enumerations
	f.secrets.mu.Unlock()
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal(err)
	}
	f.secrets.mu.Lock()
	enumerations := f.secrets.enumerations - before
	f.secrets.mu.Unlock()
	if enumerations != 1 {
		t.Fatalf("the vault was enumerated %d times; only the pre-commit cleanup may enumerate", enumerations)
	}
	_, a := f.record()
	if a.Connection != nil || a.Subscription.RecoveryRequired || a.Health != domain.AccountDisconnected {
		t.Fatal("the completed Worker logout was not left disconnected and unfenced")
	}
}
