// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// loginToResult runs one complete login up to the native child's exit and waits
// for the runner to settle. The operation is not confirmed.
func (f *usageFixture) loginToResult(observed string) string {
	f.t.Helper()
	started, err := f.start(observed)
	if err != nil {
		f.t.Fatal(err)
	}
	done := f.runLogin()
	close(f.native.login.ready)
	f.awaitUsageState(started.OperationId, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	if err := f.submit(started.OperationId, usageFixtureCode); err != nil {
		f.t.Fatal(err)
	}
	select {
	case <-f.native.login.codes:
	case <-time.After(10 * time.Second):
		f.t.Fatal("approval code never reached the original child")
	}
	f.native.store.set(usageNativeRecord(usageFixtureAccess, usageFixtureRefresh, time.Now().Add(time.Hour)))
	close(f.native.login.finish)
	awaitServerFixture(f.t, done)
	return started.OperationId
}

func TestClaudeUsageUnreleasedLeaseNeverOffersConfirmationOrKeepsAnIdentity(t *testing.T) {
	f := newUsageFixture(t)
	f.native.closeErr = errors.New("lease release failed")
	operation := f.loginToResult("")
	usage := f.usage()
	o := usage.Operation
	if o == nil || string(o.ID) != operation || o.State != domain.ClaudeLimitLoginRecovery || o.Identity != nil || o.CredentialID != "" || !usage.CleanupPending {
		t.Fatalf("unproven lease release must enter cleanup recovery without an identity: %+v %+v", o, usage)
	}
	if len(f.usageRefs()) != 0 {
		t.Fatal("a credential was staged although native cleanup was unproven")
	}
	// The cleanup transition stores a valid state.
	f.native.closeErr = nil
	if err := f.service.completeClaudeUsageCleanup(f.owner(), f.input.AccountID, false); err != nil {
		t.Fatal(err)
	}
	if o := f.usage().Operation; o == nil || o.State != domain.ClaudeLimitLoginFailed || o.Identity != nil || o.CredentialID != "" {
		t.Fatalf("cleanup left an invalid operation: %+v", o)
	}
}

func TestClaudeUsageCleanupRetiresACredentialStagedByARecoveringOperation(t *testing.T) {
	f := newUsageFixture(t)
	staged := domain.NewID()
	ref := credentials.Ref{Owner: f.input.AccountID, ID: staged, Purpose: credentials.ClaudeUsage}
	f.secrets.mu.Lock()
	f.secrets.values[ref] = []byte("staged")
	f.secrets.mu.Unlock()
	now := time.Now().UTC()
	f.mutateUsage(func(s *domain.ClaudeLimitState) {
		s.Operation = &domain.ClaudeLimitOperation{ID: domain.NewID(), Epoch: f.service.subscriptionServerEpoch(), Generation: domain.NewID(), Actor: domain.Principal{Type: domain.OwnerDevice}, State: domain.ClaudeLimitLoginRecovery, CredentialID: staged, StartedAt: now, ExpiresAt: now.Add(time.Minute)}
		s.CleanupPending = true
	})
	for i := 0; i < 2; i++ {
		if err := f.service.completeClaudeUsageCleanup(f.owner(), f.input.AccountID, false); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.usageRefs()) != 0 {
		t.Fatal("the staged credential stayed in the vault")
	}
	if usage := f.usage(); usage == nil || usage.Operation == nil || usage.Operation.State != domain.ClaudeLimitLoginFailed || usage.Operation.CredentialID != "" || len(usage.RetiredCredentials) != 0 {
		t.Fatalf("recovery cleanup %+v", usage)
	}
}

func TestClaudeUsageUncertainVaultWriteRetainsTheReferenceAsARemovalObligation(t *testing.T) {
	f := newUsageFixture(t)
	f.secrets.putError = errors.New("write acknowledgment lost")
	f.loginToResult("")
	f.secrets.putError = nil
	usage := f.usage()
	if usage.Operation == nil || usage.Operation.State != domain.ClaudeLimitLoginFailed || usage.Operation.Identity != nil || usage.Operation.CredentialID != "" {
		t.Fatalf("operation %+v", usage.Operation)
	}
	refs := f.usageRefs()
	if len(refs) != 1 || len(usage.RetiredCredentials) != 1 || usage.RetiredCredentials[0] != refs[0].ID {
		t.Fatalf("the possibly stored reference must be owed a deletion: refs=%v retired=%v", refs, usage.RetiredCredentials)
	}
	if err := f.service.completeClaudeUsageCleanup(f.owner(), f.input.AccountID, false); err != nil {
		t.Fatal(err)
	}
	if len(f.usageRefs()) != 0 || len(f.usage().RetiredCredentials) != 0 {
		t.Fatal("the maintenance cleanup did not remove the reference")
	}
}

func TestClaudeUsageRecoveryMarkerRetiresStagedReferences(t *testing.T) {
	f := newUsageFixture(t)
	staged, operationCredential := domain.NewID(), domain.NewID()
	now := time.Now().UTC()
	operation := domain.NewID()
	f.mutateUsage(func(s *domain.ClaudeLimitState) {
		s.Operation = &domain.ClaudeLimitOperation{ID: operation, Epoch: f.service.subscriptionServerEpoch(), Generation: operation, Actor: domain.Principal{Type: domain.OwnerDevice}, State: domain.ClaudeLimitLoginConfirming, CredentialID: operationCredential, Identity: &domain.ClaudeLimitIdentity{Email: "alex@example.com", Plan: "max", Commitment: strings.Repeat("d", 64)}, StartedAt: now, ExpiresAt: now.Add(time.Minute)}
	})
	f.service.markClaudeUsageRecovery(f.input.AccountID, operation, staged)
	usage := f.usage()
	o := usage.Operation
	if o == nil || o.State != domain.ClaudeLimitLoginRecovery || o.Identity != nil || o.CredentialID != "" || !usage.CleanupPending {
		t.Fatalf("recovery marker %+v", usage)
	}
	if len(usage.RetiredCredentials) != 2 {
		t.Fatalf("both references must be owed a deletion: %v", usage.RetiredCredentials)
	}
}

func TestClaudeUsageStaleDisconnectNeverInterruptsAnotherClientsLogin(t *testing.T) {
	f := newUsageFixture(t)
	generation := f.link()
	f.native = newFakeUsageNative()
	started, err := f.start(generation)
	if err != nil {
		t.Fatal(err)
	}
	done := f.runLogin()
	close(f.native.login.ready)
	f.awaitUsageState(started.OperationId, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	// A stale request carries the wrong usage generation.
	if _, err := f.disconnect(string(domain.NewID())); err == nil {
		t.Fatal("a stale disconnect was accepted")
	}
	if f.native.login.closeCalls.Load() != 0 {
		t.Fatal("a rejected disconnect interrupted the live login")
	}
	if progress, err := f.progress(started.OperationId, started.OperationId); err != nil || progress.State != pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING || progress.Canceled {
		t.Fatalf("the live login was disturbed: %+v %v", progress, err)
	}
	if _, err := f.cancel(started.OperationId); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, done)
}

func TestClaudeUsageReplayedDisconnectNeverCancelsANewConnectionsLogin(t *testing.T) {
	f := newUsageFixture(t)
	generation := f.link()
	r, _ := f.record()
	request := &pb.DisconnectClaudeUsageRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, UsageGeneration: generation}
	if _, err := f.client.DisconnectClaudeUsage(context.Background(), subscriptionRequest(f.service.Identity.Token, request)); err != nil {
		t.Fatal(err)
	}
	// A new connection starts its login after the first disconnect completed.
	started, err := f.start("")
	if err != nil {
		t.Fatal(err)
	}
	f.native = newFakeUsageNative()
	done := f.runLogin()
	close(f.native.login.ready)
	f.awaitUsageState(started.OperationId, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	replay, err := f.client.DisconnectClaudeUsage(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil || !replay.Msg.Replayed {
		t.Fatalf("replay %v %v", replay, err)
	}
	if progress, err := f.progress(started.OperationId, started.OperationId); err != nil || progress.State != pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING || progress.Canceled {
		t.Fatalf("a replayed disconnect canceled the new login: %+v %v", progress, err)
	}
	if _, err := f.cancel(started.OperationId); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, done)
}

func TestClaudeUsageDisconnectReceiptsAreBoundToTheInitiatingActor(t *testing.T) {
	f := newUsageFixture(t)
	generation := f.link()
	r, _ := f.record()
	request := &pb.DisconnectClaudeUsageRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, UsageGeneration: generation}
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if _, err := f.service.DisconnectClaudeUsage(owner, connect.NewRequest(request)); err != nil {
		t.Fatal(err)
	}
	other := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: domain.NewID()})
	if response, err := f.service.DisconnectClaudeUsage(other, connect.NewRequest(request)); err == nil {
		t.Fatalf("another paired client replayed the first actor's receipt: %+v", response)
	}
	if replay, err := f.service.DisconnectClaudeUsage(owner, connect.NewRequest(request)); err != nil || !replay.Msg.Replayed {
		t.Fatalf("the initiating actor must still replay: %v", err)
	}
}

func TestClaudeUsageLoginRevokedBeforeRegistrationOpensNoNativeResource(t *testing.T) {
	f := newUsageFixture(t)
	started, err := f.start("")
	if err != nil {
		t.Fatal(err)
	}
	f.service.afterClaudeUsageClaim = func() {
		// A disconnect/logout commits between the claim and the registration.
		f.mutateUsage(func(s *domain.ClaudeLimitState) { revokeClaudeUsage(s) })
	}
	done := f.runLogin()
	awaitServerFixture(t, done)
	if f.native.opens.Load() != 0 {
		t.Fatal("native resources were opened for a revoked login")
	}
	if usage := f.usage(); usage != nil && usage.Operation != nil {
		t.Fatalf("the revoked operation was resurrected: %+v", usage.Operation)
	}
	_ = started
}

func TestClaudeUsageReplayedCodeClaimRestoresTheBytesForTheOriginalChild(t *testing.T) {
	f := newUsageFixture(t)
	operation := domain.NewID()
	now := time.Now().UTC()
	f.mutateUsage(func(s *domain.ClaudeLimitState) {
		s.Operation = &domain.ClaudeLimitOperation{ID: operation, Epoch: f.service.subscriptionServerEpoch(), Generation: operation, Actor: domain.Principal{Type: domain.OwnerDevice}, State: domain.ClaudeLimitLoginWaiting, NativeStarted: true, StartedAt: now, ExpiresAt: now.Add(time.Minute)}
	})
	r, _ := f.record()
	mutation := &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	submit := func() *pb.SubmitClaudeUsageCodeResponse {
		t.Helper()
		response, err := f.service.SubmitClaudeUsageCode(owner, connect.NewRequest(&pb.SubmitClaudeUsageCodeRequest{Mutation: mutation, OperationId: string(operation), UsageGeneration: string(operation), Code: []byte(usageFixtureCode)}))
		if err != nil {
			t.Fatal(err)
		}
		return response.Msg
	}
	held := func() string {
		defer f.service.claudeUsage.lock()()
		return string(f.service.claudeUsage.codes[operation])
	}
	submit()
	if held() != usageFixtureCode {
		t.Fatal("the first claim did not retain the bytes")
	}
	// The commit succeeded but the acknowledgment and the bytes were lost.
	f.service.clearClaudeUsageInput(operation)
	if held() != "" {
		t.Fatal("fixture failed to drop the bytes")
	}
	submit()
	if held() != usageFixtureCode {
		t.Fatal("an exact replay of an unconsumed claim must restore the approval code")
	}
	// A consumed claim is never restored.
	f.service.clearClaudeUsageInput(operation)
	f.mutateUsage(func(s *domain.ClaudeLimitState) { s.Operation.CodeConsumed = true })
	submit()
	if held() != "" {
		t.Fatal("a consumed claim restored its approval code")
	}
}

// seedActiveRead writes an in-flight read marker bound to the given actor.
func (f *usageFixture) seedActiveRead(actor domain.Principal) domain.ID {
	f.t.Helper()
	_, a := f.record()
	read := domain.NewID()
	started := time.Now().UTC()
	f.mutateUsage(func(s *domain.ClaudeLimitState) {
		s.Read = domain.ClaudeLimitRead{Phase: domain.ClaudeLimitReadFetching, ID: read, Epoch: f.service.subscriptionServerEpoch(), StartedAt: &started, ConnectionID: a.Connection.ID, Generation: s.Generation, Actor: actor, LastAttemptAt: &started}
	})
	return read
}

func TestClaudeUsageReadWhoseActorWasRevokedBeforeStartIsSettled(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	f.seedActiveRead(domain.Principal{Type: domain.ClientDevice, DeviceID: domain.NewID()})
	f.service.runClaudeUsageRead(f.owner(), f.input.AccountID)
	if f.usage().Read.Active() {
		t.Fatalf("the unsettled marker would keep the account reading: %+v", f.usage().Read)
	}
	if f.transport.count() != 0 {
		t.Fatal("a usage request was sent for a revoked actor")
	}
	if outcome := f.refreshOnce(true); outcome != claudeUsageReadAccepted {
		t.Fatalf("a later refresh was refused: %s", outcome)
	}
}

func TestClaudeUsageReadWhoseActorWasRevokedDuringTheGetIsSettledWithoutPublication(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	f.seedActiveRead(domain.Principal{Type: domain.OwnerDevice})
	f.transport.delay = make(chan struct{})
	done := make(chan struct{})
	go func() { defer close(done); f.service.runClaudeUsageRead(f.owner(), f.input.AccountID) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.transport.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	f.mutateUsage(func(s *domain.ClaudeLimitState) {
		s.Read.Actor = domain.Principal{Type: domain.ClientDevice, DeviceID: domain.NewID()}
	})
	close(f.transport.delay)
	awaitServerFixture(t, done)
	usage := f.usage()
	if usage.Read.Active() || usage.LastSuccess != nil || len(usage.Windows) != 0 {
		t.Fatalf("a revoked actor's read must be settled without publication: %+v", usage)
	}
}

func TestClaudeUsageProfileReleaseFailureAfterRefreshRequiresCleanup(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	f.seedBundle(time.Now().Add(30 * time.Second))
	f.rotating()
	f.native.closeErr = errors.New("lease release failed")
	if outcome := f.refreshOnce(true); outcome != claudeUsageReadAccepted {
		t.Fatal(outcome)
	}
	if usage := f.usage(); !usage.CleanupPending || usage.Readable() {
		t.Fatalf("a profile that may stay locked must not look readable: %+v", usage)
	}
}

func TestClaudeUsageFailedStagedRotationDeletionBecomesARemovalObligation(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	old := f.seedBundle(time.Now().Add(30 * time.Second))
	f.rotating()
	f.secrets.afterPut = func() {
		// The switch cannot commit and the staged reference cannot be deleted.
		f.secrets.mu.Lock()
		f.secrets.deleteError = errors.New("vault unavailable")
		f.secrets.mu.Unlock()
		f.mutateUsage(func(s *domain.ClaudeLimitState) { s.CredentialID = domain.NewID() })
	}
	f.refreshOnce(true)
	f.secrets.afterPut = nil
	usage := f.usage()
	var staged domain.ID
	for _, ref := range f.usageRefs() {
		if ref.ID != old {
			staged = ref.ID
		}
	}
	if staged == "" || !contains(usage.RetiredCredentials, staged) {
		t.Fatalf("the staged reference %s is not owed a deletion: %v", staged, usage.RetiredCredentials)
	}
	f.secrets.mu.Lock()
	f.secrets.deleteError = nil
	f.secrets.mu.Unlock()
	if err := f.service.completeClaudeUsageCleanup(f.owner(), f.input.AccountID, false); err != nil {
		t.Fatal(err)
	}
	for _, ref := range f.usageRefs() {
		if ref.ID == staged {
			t.Fatal("the maintenance cleanup did not remove the staged reference")
		}
	}
}

func contains(ids []domain.ID, id domain.ID) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

type fakeLease struct{ err error }

func (l fakeLease) Close() error { return l.err }

func TestFailedUsageOpenKeepsALeaseReleaseFailureVisible(t *testing.T) {
	cause := errors.New("discovery failed")
	if err := failClaudeUsageOpen(fakeLease{}, cause); err != cause || claudeUsageLeaseLeaked(err) {
		t.Fatalf("a released lease must return the original failure, got %v", err)
	}
	err := failClaudeUsageOpen(fakeLease{err: errors.New("close failed")}, cause)
	if !claudeUsageLeaseLeaked(err) || !errors.Is(err, cause) {
		t.Fatalf("an unreleased lease must stay visible and keep its cause: %v", err)
	}
}

func TestClaudeUsageLoginWithAnUnreleasedFailedOpenEntersCleanupRecovery(t *testing.T) {
	f := newUsageFixture(t)
	f.service.claudeUsageOpen = func(context.Context, nativeScope, domain.ID, *slog.Logger) (claudeUsageNative, error) {
		return nil, failClaudeUsageOpen(fakeLease{err: errors.New("close failed")}, domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureInstallation, 0))
	}
	started, err := f.start("")
	if err != nil {
		t.Fatal(err)
	}
	done := f.runLogin()
	awaitServerFixture(t, done)
	usage := f.usage()
	if usage.Operation == nil || string(usage.Operation.ID) != started.OperationId || usage.Operation.State != domain.ClaudeLimitLoginRecovery || !usage.CleanupPending {
		t.Fatalf("an unreleased profile lease must not look like an ordinary failure: %+v", usage)
	}
}

func TestClaudeUsageRetirementWaitsForLiveWorkInsteadOfRacingItsProfile(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	stopped := make(chan struct{})
	run, ok := f.service.registerClaudeUsageRun(f.input.AccountID, func() { close(stopped) })
	if !ok {
		t.Fatal("run not registered")
	}
	// The caller holds the gate, so a run that is still stopping keeps the
	// profile: removal is retried rather than racing the native child.
	if err := f.service.retireClaudeUsage(f.owner(), f.input.AccountID); err == nil {
		t.Fatal("retirement proceeded against a live usage run")
	}
	<-stopped
	f.service.finishClaudeUsageRun(f.input.AccountID, run)
	if err := f.service.retireClaudeUsage(f.owner(), f.input.AccountID); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeUsageStopWorkJoinsTheRunOutsideTheGate(t *testing.T) {
	f := newUsageFixture(t)
	canceled := make(chan struct{})
	run, ok := f.service.registerClaudeUsageRun(f.input.AccountID, func() { close(canceled) })
	if !ok {
		t.Fatal("run not registered")
	}
	go func() {
		<-canceled
		time.Sleep(50 * time.Millisecond)
		f.service.finishClaudeUsageRun(f.input.AccountID, run)
	}()
	if err := f.service.stopClaudeUsageWork(f.owner(), f.input.AccountID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-run.done:
	default:
		t.Fatal("the run was not joined")
	}
}

func TestClaudeUsageScannerQueuesDueReadsFromItsSingleAccountScan(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	ctx, stop := context.WithCancel(f.owner())
	done := make(chan struct{})
	go func() { defer close(done); f.service.runClaudeUsage(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for f.usage().LastSuccess == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	awaitServerFixture(t, done)
	if usage := f.usage(); usage.LastSuccess == nil || usage.Read.Active() || f.transport.count() != 1 {
		t.Fatalf("the due read was not accepted, run and settled exactly once: requests=%d %+v", f.transport.count(), usage)
	}
}

func TestClaudeUsageCancelOfAConfirmingLoginReportsIncompleteCleanup(t *testing.T) {
	f := newUsageFixture(t)
	operation := f.loginToResult("")
	f.secrets.mu.Lock()
	f.secrets.deleteError = errors.New("vault unavailable")
	f.secrets.mu.Unlock()
	r, _ := f.record()
	request := &pb.CancelClaudeUsageRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: operation, UsageGeneration: operation}
	if _, err := f.client.CancelClaudeUsage(context.Background(), subscriptionRequest(f.service.Identity.Token, request)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("an incomplete cancellation cleanup was acknowledged: %v", err)
	}
	if usage := f.usage(); len(usage.RetiredCredentials) != 1 || usage.Operation.State != domain.ClaudeLimitLoginCanceled {
		t.Fatalf("the cancellation must stay durable with its obligation: %+v", usage)
	}
	f.secrets.mu.Lock()
	f.secrets.deleteError = nil
	f.secrets.mu.Unlock()
	replay, err := f.client.CancelClaudeUsage(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil || !replay.Msg.Replayed {
		t.Fatalf("the original cancel did not recover: %v", err)
	}
	if len(f.usageRefs()) != 0 || len(f.usage().RetiredCredentials) != 0 {
		t.Fatal("the staged reference remained after the retried cancel")
	}
}

func TestClaudeUsageLoginJoinsAStaleRunRegistrationInsteadOfFailing(t *testing.T) {
	f := newUsageFixture(t)
	stale, ok := f.service.registerClaudeUsageRun(f.input.AccountID, func() {})
	if !ok {
		t.Fatal("run not registered")
	}
	started, err := f.start("")
	if err != nil {
		t.Fatal(err)
	}
	done := f.runLogin()
	time.AfterFunc(150*time.Millisecond, func() { f.service.finishClaudeUsageRun(f.input.AccountID, stale) })
	close(f.native.login.ready)
	f.awaitUsageState(started.OperationId, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	if _, err := f.cancel(started.OperationId); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, done)
	if o := f.usage().Operation; o.Failure == domain.ClaudeLimitFailureProfileUnsafe {
		t.Fatalf("a valid login failed on a stale registration: %+v", o)
	}
}

func TestClaudeUsageUncertainLoginStartupIsCleanupRecovery(t *testing.T) {
	f := newUsageFixture(t)
	f.native.startErr = domain.Fail(domain.RecoveryRequired, "Claude Code request delivery is uncertain.", "Inspect the original native operation.")
	started, err := f.start("")
	if err != nil {
		t.Fatal(err)
	}
	done := f.runLogin()
	awaitServerFixture(t, done)
	usage := f.usage()
	if usage.Operation == nil || string(usage.Operation.ID) != started.OperationId || usage.Operation.State != domain.ClaudeLimitLoginRecovery || !usage.CleanupPending {
		t.Fatalf("a child that may still write credentials must be fenced: %+v", usage)
	}
}

func TestClaudeUsageRefreshWithPopulatedStorageFencesTheConnectionForCleanup(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	f.seedBundle(time.Now().Add(30 * time.Second))
	f.native.store.set([]byte("unexpected native bytes"))
	if outcome := f.refreshOnce(true); outcome != claudeUsageReadAccepted {
		t.Fatal(outcome)
	}
	usage := f.usage()
	if usage.Failure != domain.ClaudeLimitFailureProfileUnsafe || !usage.CleanupPending || usage.Readable() {
		t.Fatalf("the connection must stop repeating the doomed refresh: %+v", usage)
	}
	if len(f.native.refreshInputs) != 0 {
		t.Fatal("a refresh child started although the storage was populated")
	}
	if outcome := f.refreshOnce(true); outcome != claudeUsageReadIneligible {
		t.Fatalf("a later trigger repeated the refresh check: %s", outcome)
	}
	if err := f.service.completeClaudeUsageCleanup(f.owner(), f.input.AccountID, false); err != nil {
		t.Fatal(err)
	}
	if present, _ := f.native.store.Present(context.Background()); present || f.usage().CleanupPending {
		t.Fatal("the cleanup obligation did not empty the exclusive usage profile")
	}
}

func TestClaudeUsageRefreshPublishesChangedPlanAndOrganizationName(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	f.seedBundle(time.Now().Add(30 * time.Second))
	f.rotating()
	f.native.identity.Plan = "team"
	f.native.identity.Organization = "Renamed Org"
	if outcome := f.refreshOnce(true); outcome != claudeUsageReadAccepted {
		t.Fatal(outcome)
	}
	usage := f.usage()
	if usage.Identity.Plan != "team" || usage.Identity.Organization != "Renamed Org" || usage.Identity.Email != "alex@example.com" || usage.ReconnectRequired {
		t.Fatalf("the refreshed display metadata was not published: %+v", usage.Identity)
	}
}
