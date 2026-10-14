// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bytes"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const logoutAction = pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT

// logout requests a logout and runs it to its settled outcome in this goroutine.
func (g *grokLoginFixture) logout(t *testing.T) *pb.RequestSubscriptionResponse {
	t.Helper()
	op := g.serverStart(logoutAction)
	g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	return op
}

// crashLogoutAt runs a requested logout and ends it at the named point, as a
// process crash would.
func (g *grokLoginFixture) crashLogoutAt(t *testing.T, point grokFaultPoint) *pb.RequestSubscriptionResponse {
	t.Helper()
	op := g.serverStart(logoutAction)
	g.service.grokFaultHook = func(p grokFaultPoint) error {
		if p == point {
			panic(grokCrash{})
		}
		return nil
	}
	crashed := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(grokCrash); !ok {
					panic(r)
				}
				crashed = true
			}
		}()
		g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	}()
	g.service.grokFaultHook = nil
	if !crashed {
		t.Fatalf("the logout never reached %s", point)
	}
	return op
}

func (g *grokLoginFixture) requireDisconnected(t *testing.T) {
	t.Helper()
	_, a := g.record()
	st := a.Subscription
	o := st.ServerOperation
	if a.Connection != nil || a.Health != domain.AccountDisconnected || st.Generation != "" || st.IdentityCommitment != "" || st.RevokedGeneration != "" || st.RecoveryRequired || st.Pending != nil || st.Lease != nil || o.Action != domain.SubscriptionLogout || o.State != domain.SubscriptionSucceeded || o.NativeStarted || o.GrokRevocation != domain.GrokRevocationConfirmed {
		t.Fatal("logout did not settle as a disconnected account")
	}
	if g.refs(t) != 0 {
		t.Fatal("logout left protected generations in the vault")
	}
}

// requireRetained proves that the protected bundle and its account references
// are intact and the account is fenced for recovery.
func (g *grokLoginFixture) requireRetained(t *testing.T, before domain.Account, phase domain.GrokRevocationPhase) {
	t.Helper()
	_, a := g.record()
	st := a.Subscription
	if a.Connection == nil || !st.RecoveryRequired || a.Health != domain.AccountFailed || st.Generation != before.Subscription.Generation || st.ServerOperation.State != domain.SubscriptionRecovery || st.ServerOperation.GrokRevocation != phase || st.Pending == nil {
		t.Fatalf("the account is not fenced with its credential: %+v", st.ServerOperation)
	}
	if g.refs(t) != 1 {
		t.Fatal("the protected bundle was deleted without a proven revocation")
	}
	bundle := g.storedBundle(t, before.Subscription.Generation)
	bundle.Clear()
}

func TestGrokLogoutRevokesTheRefreshTokenThenDeletesTheBundle(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	stored := g.storedBundle(t, before.Subscription.Generation)
	token := refreshTokenOf(t, stored)
	stored.Clear()
	op := g.serverStart(logoutAction)
	// Acceptance alone revokes readiness; nothing is sent or deleted yet.
	_, accepted := g.record()
	if accepted.Health != domain.AccountRevoked || !accepted.GrokLoggingOut() || g.idp.RevokeRequests() != 0 || g.refs(t) != 1 {
		t.Fatal("the accepted logout did not revoke readiness first")
	}
	g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	if g.idp.RevokeRequests() != 1 {
		t.Fatalf("expected exactly one revocation request, got %d", g.idp.RevokeRequests())
	}
	form := g.idp.RevokeForms()[0]
	if len(form) != 3 || form.Get("token") != token || form.Get("token_type_hint") != "refresh_token" || form.Get("client_id") != subscription.GrokClientID || g.idp.RevokeHeaders()[0].Get("Authorization") != "" || form.Get("client_secret") != "" {
		t.Fatal("the revocation request is not a public-client refresh-token revocation")
	}
	g.requireDisconnected(t)
	if g.idp.CurrentRefreshToken() != "" {
		t.Fatal("the provider still honors the revoked refresh token")
	}
	if p := g.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || p.GrokDiagnostic != nil {
		t.Fatalf("progress does not report the settled logout: %v", p.State)
	}
	g.requireNoLogoutLeak(t, op, token, true)
	// The same account can connect again.
	g.service.logger.Info("relogin")
	if _, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: g.mutation(), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN})); err != nil {
		t.Fatal("a logged-out account could not request a new login", err)
	}
}

func (g *grokLoginFixture) mutation() *pb.Mutation {
	r, _ := g.record()
	return &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}
}

// requireNoLogoutLeak scans the structured logs and the durable metadata for the
// token, identity and path values of the logout and asserts the closed phases.
func (g *grokLoginFixture) requireNoLogoutLeak(t *testing.T, op *pb.RequestSubscriptionResponse, token string, cleanup bool) {
	t.Helper()
	logs := g.logs.String()
	if !strings.Contains(logs, `"phase":"LOGOUT"`) || cleanup && !strings.Contains(logs, `"phase":"CLEANUP"`) || !strings.Contains(logs, `"correlation_id":"`+op.OperationId+`"`) {
		t.Fatal("logs lack the LOGOUT or CLEANUP phase or the operation correlation id")
	}
	secrets := []string{"fake-access-token", token, "eyJ", "fake-user-1", "fixture@example.invalid", "Fixture", g.service.Store.Root(), "oauth2/revoke", "refresh_token", "client_id"}
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		if !strings.Contains(line, `"phase":"LOGOUT"`) && !strings.Contains(line, `"phase":"CLEANUP"`) {
			continue
		}
		if !strings.Contains(line, `"operation_id"`) {
			t.Fatal("a LOGOUT or CLEANUP record lacks the operation id")
		}
		for _, secret := range secrets {
			if strings.Contains(line, secret) {
				t.Fatalf("a secret, identity or path value reached a LOGOUT or CLEANUP record: %.8s", secret)
			}
		}
	}
	for _, name := range []string{"state.sqlite", "state.sqlite-wal", "state.sqlite-shm"} {
		data, err := os.ReadFile(filepath.Join(g.service.Store.Root(), name))
		if err != nil {
			continue
		}
		for _, secret := range []string{"fake-access-token", token, "refresh_token="} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatalf("a token reached durable metadata: %.8s", secret)
			}
		}
	}
}

func TestGrokLogoutUnprovenRevocationLeavesRecoveryAndKeepsTheBundle(t *testing.T) {
	for name, scenario := range map[string]struct {
		status int
		body   string
	}{
		"definite-4xx": {http.StatusBadRequest, `{"error":"invalid_client"}`},
		// RFC 7009 answers 200 for an invalid token, so an error response is a
		// refused request and never proof that the token is unusable elsewhere.
		"invalid-grant":  {http.StatusBadRequest, `{"error":"invalid_grant"}`},
		"invalid-token":  {http.StatusUnauthorized, `{"error":"invalid_token"}`},
		"forbidden":      {http.StatusForbidden, ``},
		"server-error":   {http.StatusInternalServerError, ``},
		"unavailable":    {http.StatusServiceUnavailable, ``},
		"bad-bad-answer": {http.StatusBadRequest, `not json`},
	} {
		t.Run(name, func(t *testing.T) {
			g := newGrokLoginFixture(t)
			before := g.connected(t)
			g.idp.RevokeStatus, g.idp.RevokeBody = scenario.status, scenario.body
			op := g.logout(t)
			if g.idp.RevokeRequests() != 1 {
				t.Fatal("a failed revocation must not be retried")
			}
			g.requireRetained(t, before, domain.GrokRevocationClaimed)
			p := g.progressFor(op.OperationId)
			if p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED || p.GrokDiagnostic == nil || p.GrokDiagnostic.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGOUT {
				t.Fatalf("progress lacks the LOGOUT diagnostic: %v", p.State)
			}
			// The claimed revocation is never resolved or resent automatically.
			if grokLogoutRecoveryKind(func() domain.Account { _, a := g.record(); return a }()) != grokLogoutNone {
				t.Fatal("an unproven revocation is automatically recoverable")
			}
			g.restart(t)
			if g.idp.RevokeRequests() != 1 {
				t.Fatal("a restart resent the revocation")
			}
			g.requireNoLogoutLeak(t, op, "fake-refresh-token", false)
		})
	}
}

func TestGrokLogoutTimeoutOrShutdownDuringRevocationIsUncertain(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	g.idp.RevokeGate = make(chan struct{})
	g.idp.RevokeEntered = make(chan struct{}, 1)
	op := g.serverStart(logoutAction)
	ctx, cancel := context.WithCancel(ownerContext())
	done := make(chan struct{})
	go func() { defer close(done); g.service.runServerSubscription(ctx, g.input.AccountID) }()
	select {
	case <-g.idp.RevokeEntered:
	case <-time.After(30 * time.Second):
		t.Fatal("the revocation request never started")
	}
	cancel()
	awaitServerFixture(t, done)
	close(g.idp.RevokeGate)
	g.requireRetained(t, before, domain.GrokRevocationClaimed)
	if g.idp.RevokeRequests() != 1 || g.progressFor(op.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED {
		t.Fatal("an interrupted revocation was not left uncertain")
	}
}

func TestGrokLogoutCrashPointsNeverResendOrDeleteWithoutRecoveryState(t *testing.T) {
	for _, scenario := range []struct {
		point    grokFaultPoint
		phase    domain.GrokRevocationPhase
		resolves bool
		requests int
	}{
		{grokFaultAfterRevokeClaim, domain.GrokRevocationClaimed, false, 0},
		{grokFaultAfterRevoke, domain.GrokRevocationClaimed, false, 1},
		{grokFaultAfterRevocationRecorded, domain.GrokRevocationConfirmed, true, 1},
		{grokFaultAfterLogoutCleanup, domain.GrokRevocationConfirmed, true, 1},
	} {
		t.Run(string(scenario.point), func(t *testing.T) {
			g := newGrokLoginFixture(t)
			before := g.connected(t)
			op := g.crashLogoutAt(t, scenario.point)
			g.restart(t)
			_, a := g.record()
			if !a.Subscription.RecoveryRequired || a.Subscription.ServerOperation.GrokRevocation != scenario.phase {
				t.Fatal("a crash did not leave the account fenced at the proven phase")
			}
			if scenario.point != grokFaultAfterLogoutCleanup {
				g.requireRetained(t, before, scenario.phase)
			}
			recoverable := grokLogoutRecoveryKind(a) == grokLogoutRevoked
			if recoverable != scenario.resolves {
				t.Fatalf("recoverable = %v", recoverable)
			}
			if !scenario.resolves {
				if g.idp.RevokeRequests() != scenario.requests || g.refs(t) != 1 {
					t.Fatal("an unproven revocation was resent or its bundle deleted")
				}
				return
			}
			if err := g.service.recoverGrokLogout(ownerContext(), g.input.AccountID, domain.ID(op.OperationId)); err != nil {
				t.Fatal(err)
			}
			if g.idp.RevokeRequests() != 1 {
				t.Fatal("the cleanup recovery revoked the token again")
			}
			g.requireDisconnected(t)
			if p := g.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
				t.Fatalf("progress does not report the recovered logout: %v", p.State)
			}
		})
	}
}

func TestGrokLogoutCleanupFailureIsFencedRetriedAndNeverRevokesTwice(t *testing.T) {
	g := newGrokLoginFixture(t)
	g.connected(t)
	g.secrets.mu.Lock()
	g.secrets.deleteError = domain.Fail(domain.Unavailable, "The vault removal failed.", "")
	g.secrets.mu.Unlock()
	op := g.logout(t)
	_, a := g.record()
	st := a.Subscription
	if !st.RecoveryRequired || a.Connection == nil || st.ServerOperation.GrokRevocation != domain.GrokRevocationConfirmed || st.RevokedGeneration != st.Generation || grokLogoutRecoveryKind(a) != grokLogoutRevoked {
		t.Fatal("a failed cleanup did not keep the account fenced with its revocation proven")
	}
	if !strings.Contains(g.logs.String(), `"phase":"CLEANUP"`) || !strings.Contains(g.logs.String(), "grok_logout_cleanup_pending") {
		t.Fatal("the pending cleanup was not logged with the CLEANUP phase")
	}
	// Deletion is blocked while the cleanup is unconfirmed.
	if err := g.deleteAccount(a); err == nil {
		t.Fatal("an account with unconfirmed cleanup was deleted")
	}
	if err := g.service.recoverGrokLogout(ownerContext(), g.input.AccountID, domain.ID(op.OperationId)); err == nil {
		t.Fatal("a failing vault removal was reported as recovered")
	}
	g.secrets.mu.Lock()
	g.secrets.deleteError = nil
	g.secrets.mu.Unlock()
	if err := g.service.recoverGrokLogout(ownerContext(), g.input.AccountID, domain.ID(op.OperationId)); err != nil {
		t.Fatal(err)
	}
	if g.idp.RevokeRequests() != 1 {
		t.Fatalf("the cleanup retry revoked %d times", g.idp.RevokeRequests())
	}
	g.requireDisconnected(t)
}

func (g *grokLoginFixture) deleteAccount(a domain.Account) error {
	r, _ := g.record()
	return g.deleteFailedLogin(&pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: &pb.Mutation{Id: string(r.ID), ExpectedRevision: r.Revision, RequestId: string(domain.NewID())}})
}

func TestGrokLogoutBeforeTheClaimRestartKeepsTheCredentialAndAllowsANewLogout(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	op := g.serverStart(logoutAction)
	g.restart(t)
	_, a := g.record()
	if grokLogoutRecoveryKind(a) != grokLogoutUnclaimed {
		t.Fatal("an unclaimed interrupted logout is not recoverable")
	}
	if err := g.service.recoverGrokLogout(ownerContext(), g.input.AccountID, domain.ID(op.OperationId)); err != nil {
		t.Fatal(err)
	}
	_, a = g.record()
	if a.Subscription.RecoveryRequired || a.Subscription.Pending != nil || a.Connection == nil || a.Subscription.Generation != before.Subscription.Generation || a.Subscription.ServerOperation.State != domain.SubscriptionFailed || g.idp.RevokeRequests() != 0 || g.refs(t) != 1 {
		t.Fatal("the unclaimed logout was not freed with its credential intact")
	}
	if p := g.progressFor(op.OperationId); p.GrokDiagnostic == nil || p.GrokDiagnostic.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGOUT {
		t.Fatal("the recovered logout lacks its diagnostic")
	}
	g.logout(t)
	g.requireDisconnected(t)
}

func TestGrokLogoutVaultFailureBeforeTheClaimSendsNothing(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	g.secrets.mu.Lock()
	g.secrets.values = map[credentials.Ref][]byte{}
	g.secrets.mu.Unlock()
	g.logout(t)
	_, a := g.record()
	if g.idp.RevokeRequests() != 0 || a.Subscription.ServerOperation.State != domain.SubscriptionFailed || a.Subscription.RecoveryRequired || a.Subscription.Pending != nil || a.Connection == nil || a.Subscription.Generation != before.Subscription.Generation {
		t.Fatal("a vault failure before the claim sent a request or fenced the account")
	}
}

// A Worker lease is released only by a Finish that confirmed the original
// execution exit and the private-home cleanup, so the logout waits for it.
func TestGrokLogoutWaitsForTheOriginalExecutionLease(t *testing.T) {
	g := newGrokLoginFixture(t)
	g.connected(t)
	g.mutateServerOperation(func(a *domain.Account) {
		a.Subscription.Lease = &domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 1, Action: domain.SubscriptionExecute, MachineID: g.input.MachineID, InstanceID: g.instance, DeviceID: domain.NewID(), Epoch: g.service.subscriptionServerEpoch(), Generation: a.Subscription.Generation, StartedAt: time.Now().UTC()}
		a.Subscription.OwnerMachineID = g.input.MachineID
	})
	op := g.serverStart(logoutAction)
	g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	_, a := g.record()
	if g.idp.RevokeRequests() != 0 || a.Subscription.ServerOperation.NativeStarted || a.Subscription.Pending == nil || a.Subscription.Pending.Phase != domain.SubscriptionQueued || a.Health != domain.AccountRevoked || g.refs(t) != 1 {
		t.Fatal("the logout did not wait for the original execution")
	}
	// New execution is refused with a typed unavailable problem meanwhile.
	if _, err := g.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE); err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Unavailable {
		t.Fatalf("new execution during logout was not refused as unavailable: %v", err)
	}
	// The original execution exits and its Worker confirmed cleanup.
	g.mutateServerOperation(func(a *domain.Account) { a.Subscription.Lease = nil })
	g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	if g.idp.RevokeRequests() != 1 {
		t.Fatal("the logout did not revoke after the execution exited")
	}
	g.requireDisconnected(t)
}

func TestGrokExecuteTakeIsOnlyUnavailableWhileLoggingOut(t *testing.T) {
	g := newGrokLoginFixture(t)
	g.connected(t)
	op := g.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if _, err := g.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE); err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Unsupported {
		t.Fatalf("execution outside a logout lost its generic refusal: %v", err)
	}
}

func TestGrokLogoutSerializesWithOtherOperationsOnOneAccountOnly(t *testing.T) {
	g := newGrokLoginFixture(t)
	g.connected(t)
	g.serverStart(logoutAction)
	for _, action := range []pb.SubscriptionAction{pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, logoutAction} {
		if _, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: g.mutation(), Action: action})); err == nil {
			t.Fatalf("%v was accepted while a logout holds the account", action)
		}
	}
	// A second account is independent of the first one's logout.
	other := domain.NewID()
	_, err := g.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.second", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.AccountKind, other, 0, "", "", domain.Account{Alias: "second", Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionGrok, Health: domain.AccountDisconnected})
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := g.service.Store.Get(context.Background(), domain.AccountKind, other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(other), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN})); err != nil {
		t.Fatal("a separate account was blocked by another account's logout", err)
	}
}

// newUnreferencedGrokLoginFixture moves the fixture to a fresh Grok account that
// no project, agent or model references, so deletion rules can be exercised.
func newUnreferencedGrokLoginFixture(t *testing.T) *grokLoginFixture {
	t.Helper()
	g := newGrokLoginFixture(t)
	_, account := g.record()
	id := domain.NewID()
	if _, err := g.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.unreferenced", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.AccountKind, id, 0, "", "", account)
	}); err != nil {
		t.Fatal(err)
	}
	g.input.AccountID = id
	return g
}

func TestGrokDeleteRequiresAConfirmedLogoutFirst(t *testing.T) {
	g := newUnreferencedGrokLoginFixture(t)
	g.connected(t)
	_, connected := g.record()
	if err := g.deleteAccount(connected); err == nil {
		t.Fatal("a connected Grok account was deleted without logout")
	}
	g.logout(t)
	g.requireDisconnected(t)
	_, a := g.record()
	if err := g.deleteAccount(a); err != nil {
		t.Fatal("a logged-out account could not be deleted", err)
	}
	if _, err := g.service.Store.Get(context.Background(), domain.AccountKind, g.input.AccountID); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("the account remains after deletion")
	}
}

func TestGrokDeleteIsBlockedByUnprovenAndUnconfirmedLogouts(t *testing.T) {
	g := newUnreferencedGrokLoginFixture(t)
	g.connected(t)
	g.idp.RevokeStatus = http.StatusInternalServerError
	g.logout(t)
	_, a := g.record()
	if err := g.deleteAccount(a); err == nil {
		t.Fatal("an account whose revocation is unproven was deleted")
	}
	if _, err := g.service.Store.Get(context.Background(), domain.AccountKind, g.input.AccountID); err != nil || g.refs(t) != 1 {
		t.Fatal("a blocked deletion changed the account or its bundle")
	}
}

// A Worker-routed logout lease may delete the bundle only after the provider
// revoked it, is retried only by its original owner, and a different Worker is
// refused.
func TestGrokWorkerLogoutRequiresRevocationAndRetriesOnlyOnTheOriginalOwner(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	// Without a proven revocation the lease is refused before any bundle moves.
	if _, err := f.take(f.queue(logoutAction), logoutAction); err == nil {
		t.Fatal("a logout lease was granted before the provider revoked the token")
	}
	f.markRevoked(t)
	lease, err := f.take(f.queue(logoutAction), logoutAction)
	if err != nil {
		t.Fatal(err)
	}
	// The Worker could not remove auth.json or auth.json.lock: the lease stays
	// fenced and the bundle stays.
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, false, false); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil || a.Subscription.Generation != generation || a.Connection == nil {
		t.Fatal("an unconfirmed Worker cleanup did not keep the lease fenced")
	}
	if stored, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}); err != nil || len(stored) == 0 {
		t.Fatal("an unconfirmed Worker cleanup deleted the bundle")
	}
	// Another Worker instance is refused, and so is a different Runner Device.
	other := lease
	for name, request := range map[string]*pb.FinishSubscriptionRequest{
		"other-instance": {Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: other.LeaseRevision}, LeaseId: other.LeaseId, GenerationId: other.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(domain.NewID()), CleanupConfirmed: true, Succeeded: true},
		"other-machine":  {Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: other.LeaseRevision}, LeaseId: other.LeaseId, GenerationId: other.GenerationId, MachineId: string(domain.NewID()), InstanceId: string(f.instance), CleanupConfirmed: true, Succeeded: true},
	} {
		if _, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, request)); err == nil {
			t.Fatalf("%s completed a lease it does not own", name)
		}
	}
	if _, a = f.record(); !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil || a.Connection == nil {
		t.Fatal("a refused Worker changed the fenced lease")
	}
	// The original owner retries and confirms the cleanup.
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal("the original owner could not retry the fenced logout", err)
	}
	_, a = f.record()
	if a.Connection != nil || a.Health != domain.AccountDisconnected || a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Subscription.Generation != "" || a.Subscription.RevokedGeneration != "" {
		t.Fatal("the retried logout did not settle")
	}
	if refs, err := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID); err != nil || len(refs) != 0 {
		t.Fatal("the retried logout left protected generations")
	}
}

// Another fence on a non-logout lease is never cleared by a retry.
func TestGrokFencedRefreshLeaseIsNotRetryable(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, true, true); err == nil {
		t.Fatal("a fenced refresh lease was released by a retry")
	}
}

func TestGrokWorkerLogoutWithoutRevocationFencesInsteadOfDeleting(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	f.markRevoked(t)
	lease, err := f.take(f.queue(logoutAction), logoutAction)
	if err != nil {
		t.Fatal(err)
	}
	// The revocation record disappears before Finish (for example a restored image).
	f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.unrevoke", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Subscription.RevokedGeneration = ""
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if !a.Subscription.RecoveryRequired || a.Subscription.Generation != generation || a.Connection == nil {
		t.Fatal("a logout without a proven revocation was published")
	}
	if stored, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}); err != nil || len(stored) == 0 {
		t.Fatal("a logout without a proven revocation deleted the bundle")
	}
}

// Grok bundles are protected credentials for the batch cleanup: a connected
// account and a logout whose revocation is unproven are never selected, and the
// bundle is not touched; a settled logout is an ordinary disconnected account.
func TestGrokCleanupTreatsTheBundleAsProtected(t *testing.T) {
	g := newGrokLoginFixture(t)
	excludeUnrelatedBatchFixture(t, g.subscriptionFixture)
	before := g.connected(t)
	accepted := acceptFailedCleanup(t, g.subscriptionFixture, failedLoginContext(), domain.NewID())
	if accepted.Job.Total != 0 || g.refs(t) != 1 {
		t.Fatal("a connected Grok account was selected for cleanup")
	}
	g.idp.RevokeStatus = http.StatusInternalServerError
	g.logout(t)
	g.requireRetained(t, before, domain.GrokRevocationClaimed)
	accepted = acceptFailedCleanup(t, g.subscriptionFixture, failedLoginContext(), domain.NewID())
	if accepted.Job.Total != 0 || g.refs(t) != 1 {
		t.Fatal("a Grok account with an unproven revocation was selected for cleanup")
	}
}

func TestGrokCleanupSettledLogoutIsAnOrdinaryDisconnectedAccount(t *testing.T) {
	g := newGrokLoginFixture(t)
	g.connected(t)
	g.logout(t)
	g.requireDisconnected(t)
	_, a := g.record()
	if !disconnectedSubscription(a) {
		t.Fatal("a settled Grok logout is not an ordinary disconnected account")
	}
}

// A Grok login that never produced a credential is an ordinary disconnected
// account; a claimed login that is still unsettled holds a recovery fence and is
// excluded from every cleanup selection.
func TestGrokCleanupFailedLoginSelection(t *testing.T) {
	now := time.Now().UTC()
	operation := domain.NewID()
	failed := domain.Account{Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionGrok, Health: domain.AccountDisconnected, Subscription: &domain.SubscriptionState{
		ServerOperation: &domain.ServerSubscriptionOperation{ID: operation, Epoch: domain.NewID(), FinishID: domain.NewID(), Action: domain.SubscriptionLogin, Actor: domain.Principal{Type: domain.OwnerDevice}, State: domain.SubscriptionFailed, StartedAt: now, ExpiresAt: now.Add(15 * time.Minute)},
	}}
	if err := failed.Subscription.Validate(failed); err != nil || !disconnectedSubscription(failed) {
		t.Fatalf("a failed Grok login is not an ordinary disconnected account: %v", err)
	}
	claimed := failed
	claimed.Health = domain.AccountFailed
	claimed.Subscription = &domain.SubscriptionState{RecoveryRequired: true,
		Pending:         &domain.SubscriptionOperation{ID: operation, Action: domain.SubscriptionLogin, Actor: domain.Principal{Type: domain.OwnerDevice}, Phase: domain.SubscriptionClaimed},
		ServerOperation: &domain.ServerSubscriptionOperation{ID: operation, Epoch: domain.NewID(), FinishID: domain.NewID(), Action: domain.SubscriptionLogin, Actor: domain.Principal{Type: domain.OwnerDevice}, State: domain.SubscriptionRecovery, NativeStarted: true, CodeConsumed: true, CodeSubmissionID: domain.NewID(), StartedAt: now, ExpiresAt: now.Add(15 * time.Minute)},
	}
	if disconnectedSubscription(claimed) || failedCleanupCandidate(claimed) || grokLoginNeedsRecovery(claimed) {
		t.Fatal("a claimed Grok login was selected for cleanup")
	}
}

// A Runner Device cannot carry a Grok lifecycle request: RequestSubscription with
// a machine_id enters the Codex gate, which refuses Grok accounts before any
// mutation. A machine-scoped logout therefore never creates a Worker-routed
// operation that the revoked-generation guard of Take would reject.
func TestGrokMachineScopedRequestsAreRefusedBeforeAnyMutation(t *testing.T) {
	for _, action := range []pb.SubscriptionAction{
		pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN,
		pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH,
		logoutAction,
	} {
		t.Run(action.String(), func(t *testing.T) {
			g := newGrokLoginFixture(t)
			g.connected(t)
			before, account := g.record()
			_, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(before.ID), ExpectedRevision: before.Revision}, Action: action, MachineId: string(g.input.MachineID)}))
			if err == nil {
				t.Fatal("a machine-scoped Grok request was accepted")
			}
			after, current := g.record()
			if after.Revision != before.Revision || current.Health != account.Health || current.Subscription.Pending != nil || current.Subscription.ServerOperation.Action != domain.SubscriptionLogin {
				t.Fatal("a refused machine-scoped Grok request changed the account")
			}
		})
	}
}
