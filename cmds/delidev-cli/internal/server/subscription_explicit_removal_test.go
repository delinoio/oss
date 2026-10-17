// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// retainedGenerationCount is more than the Worker Finish retirement budget, so
// every explicit-removal test proves the removal is not capped.
const retainedGenerationCount = workerReferenceRetirementBudget + 1

func putGenerations(t *testing.T, vault *accountTestSecrets, owner domain.ID, count int) []credentials.Ref {
	t.Helper()
	refs := make([]credentials.Ref, 0, count)
	for i := 0; i < count; i++ {
		ref := credentials.Ref{Owner: owner, ID: domain.NewID(), Purpose: credentials.AccountLogin}
		if _, err := vault.Put(context.Background(), ref, []byte("synthetic-retained-generation")); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, ref)
	}
	return refs
}

// plantRetainedCredential writes the credential file of one native harness into
// the account's permanent auth profile, as a completed routine operation leaves
// it. It returns the file path.
func plantRetainedCredential(t *testing.T, s *Service, account domain.ID, kind domain.Harness, content []byte) string {
	t.Helper()
	scope := s.scopeFor(account)
	if scope.Profiles == nil {
		t.Fatal("native profile registry unavailable")
	}
	lease, err := scope.Profiles.Acquire(context.Background(), nativeprofile.Key{Harness: kind, Purpose: nativeprofile.PurposeAuth, Account: account})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	directory, name := "codex", "auth.json"
	if kind == domain.ClaudeCode {
		directory, name = "claude", ".credentials.json"
	}
	if err := security.PrivateDir(filepath.Join(lease.Root, directory)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lease.Root, directory, name)
	if err := subscription.WriteCredentialFile(path, content); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRemoveAccountLoginReferencesRemovesEveryGeneration(t *testing.T) {
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	owner, other := domain.NewID(), domain.NewID()
	owned := putGenerations(t, vault, owner, retainedGenerationCount+10)
	unrelated := putGenerations(t, vault, other, 3)
	removed, err := removeAccountLoginReferences(context.Background(), vault, owner)
	if err != nil || removed != len(owned) {
		t.Fatalf("removed %d of %d: %v", removed, len(owned), err)
	}
	if refs, err := vault.UnremovedReferences(context.Background(), owner); err != nil || len(refs) != 0 {
		t.Fatal("generations remain", len(refs), err)
	}
	for _, ref := range unrelated {
		if _, err := vault.Get(context.Background(), ref); err != nil {
			t.Fatal("an unrelated account's credential was removed", err)
		}
	}
	// Replay is idempotent: removed references are tombstoned and not enumerated.
	if removed, err := removeAccountLoginReferences(context.Background(), vault, owner); err != nil || removed != 0 {
		t.Fatal("replay was not idempotent", removed, err)
	}
}

func TestRemoveAccountLoginReferencesValidatesOwnerAndPurposeBeforeDeleting(t *testing.T) {
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	owner := domain.NewID()
	owned := putGenerations(t, vault, owner, 5)
	protected := credentials.Ref{Owner: owner, ID: domain.NewID(), Purpose: credentials.AccountAPI}
	if _, err := vault.Put(context.Background(), protected, []byte("synthetic-api-key")); err != nil {
		t.Fatal(err)
	}
	if _, err := removeAccountLoginReferences(context.Background(), vault, owner); err == nil {
		t.Fatal("a reference of another purpose was accepted")
	}
	if vault.deletes != 0 {
		t.Fatal("removal deleted before validating every reference")
	}
	for _, ref := range append(owned, protected) {
		if _, err := vault.Get(context.Background(), ref); err != nil {
			t.Fatal("a credential was removed by a denied removal", err)
		}
	}
}

func TestRemoveAccountLoginReferencesLeavesClaudeUsageToItsOwnLifecycle(t *testing.T) {
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	owner := domain.NewID()
	logins := putGenerations(t, vault, owner, 3)
	usage := credentials.Ref{Owner: owner, ID: domain.NewID(), Purpose: credentials.ClaudeUsage}
	if _, err := vault.Put(context.Background(), usage, []byte("synthetic-usage-bundle")); err != nil {
		t.Fatal(err)
	}
	removed, err := removeAccountLoginReferences(context.Background(), vault, owner)
	if err != nil || removed != len(logins) {
		t.Fatal("the login removal did not remove exactly the login generations", removed, err)
	}
	if _, err := vault.Get(context.Background(), usage); err != nil {
		t.Fatal("the login removal deleted a usage reference", err)
	}
	// Any other protected purpose still denies the whole removal before deleting.
	foreign := credentials.Ref{Owner: owner, ID: domain.NewID(), Purpose: credentials.AccountAPI}
	if _, err := vault.Put(context.Background(), foreign, []byte("synthetic-api-key")); err != nil {
		t.Fatal(err)
	}
	extra := putGenerations(t, vault, owner, 1)
	before := vault.deletes
	if _, err := removeAccountLoginReferences(context.Background(), vault, owner); err == nil || vault.deletes != before {
		t.Fatal("a foreign reference was not denied before deletion", err)
	}
	if _, err := vault.Get(context.Background(), extra[0]); err != nil {
		t.Fatal("a denied removal deleted a login generation", err)
	}
}

// cancelingSecrets cancels the removal context after a fixed number of deletes,
// proving the removal observes cancellation between items.
type cancelingSecrets struct {
	*accountTestSecrets
	cancel context.CancelFunc
	after  int
}

func (c *cancelingSecrets) Delete(ctx context.Context, ref credentials.Ref) error {
	err := c.accountTestSecrets.Delete(ctx, ref)
	c.mu.Lock()
	reached := c.deletes == c.after
	c.mu.Unlock()
	if reached {
		c.cancel()
	}
	return err
}

func TestRemoveAccountLoginReferencesObservesCancellationPerItem(t *testing.T) {
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	owner := domain.NewID()
	putGenerations(t, vault, owner, retainedGenerationCount)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	canceling := &cancelingSecrets{accountTestSecrets: vault, cancel: cancel, after: 10}
	removed, err := removeAccountLoginReferences(ctx, canceling, owner)
	if err == nil || domain.SafeError(err).Code != domain.Canceled || removed != 10 {
		t.Fatalf("cancellation was not observed per item: removed=%d err=%v", removed, err)
	}
	remaining, err := vault.UnremovedReferences(context.Background(), owner)
	if err != nil || len(remaining) != retainedGenerationCount-10 {
		t.Fatal("canceled removal lost or removed the wrong generations", len(remaining), err)
	}
	// The retry resumes and finishes: nothing was reported as success.
	if removed, err := removeAccountLoginReferences(context.Background(), vault, owner); err != nil || removed != retainedGenerationCount-10 {
		t.Fatal("retry did not complete the removal", removed, err)
	}
}

func TestRemoveAccountLoginReferencesFailureRetainsTheObligation(t *testing.T) {
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	owner := domain.NewID()
	putGenerations(t, vault, owner, 4)
	vault.deleteError = errors.New("vault unavailable")
	if _, err := removeAccountLoginReferences(context.Background(), vault, owner); err == nil {
		t.Fatal("a failed deletion was reported as success")
	}
	vault.deleteError = nil
	if removed, err := removeAccountLoginReferences(context.Background(), vault, owner); err != nil || removed != 4 {
		t.Fatal("retry did not remove the retained generations", removed, err)
	}
}

func TestWorkerFinishReferenceRetirementIsBudgetedNotRefused(t *testing.T) {
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	owner := domain.NewID()
	refs := putGenerations(t, vault, owner, workerReferenceRetirementBudget+44)
	keep, protected := refs[0], refs[1]
	// Retained generations never make the Worker Finish refuse its work.
	if err := cleanupSubscriptionReferences(context.Background(), vault, owner, keep.ID, protected.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := vault.UnremovedReferences(context.Background(), owner)
	if err != nil || len(remaining) != len(refs)-workerReferenceRetirementBudget {
		t.Fatal("one Finish retired more or fewer than its budget", len(remaining), err)
	}
	if _, err := vault.Get(context.Background(), keep); err != nil {
		t.Fatal("the current generation was retired", err)
	}
	if _, err := vault.Get(context.Background(), protected); err != nil {
		t.Fatal("a protected generation was retired", err)
	}
	// A later Finish retires the next budget; only the kept generations remain.
	if err := cleanupSubscriptionReferences(context.Background(), vault, owner, keep.ID, protected.ID); err != nil {
		t.Fatal(err)
	}
	if remaining, err := vault.UnremovedReferences(context.Background(), owner); err != nil || len(remaining) != 2 {
		t.Fatal("the next Finish did not retire the rest", len(remaining), err)
	}
	// A reference of another purpose still denies before the first deletion.
	foreign := credentials.Ref{Owner: owner, ID: domain.NewID(), Purpose: credentials.AccountAPI}
	if _, err := vault.Put(context.Background(), foreign, []byte("synthetic-api-key")); err != nil {
		t.Fatal(err)
	}
	before := vault.deletes
	if err := cleanupSubscriptionReferences(context.Background(), vault, owner, keep.ID); err == nil || vault.deletes != before {
		t.Fatal("a foreign reference was not denied before deletion")
	}
}

func TestServerLogoutRemovesEveryRetainedGenerationAndCredentialFile(t *testing.T) {
	f := unreferencedInitialSubscription(t)
	f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	login := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("explicit-removal-fixture", "first", time.Now().UTC())}
	done := f.serverRun(login)
	awaitServerFixture(t, login.started)
	close(login.finish)
	awaitServerFixture(t, done)
	_, connected := f.record()
	if connected.Connection == nil || connected.Subscription.Generation == "" {
		t.Fatal("fixture login did not connect")
	}
	// Routine login retained nothing to delete.
	if f.secrets.deletes != 0 {
		t.Fatal("routine login deleted a protected reference")
	}
	putGenerations(t, f.secrets, f.input.AccountID, retainedGenerationCount)
	unrelatedOwner := domain.NewID()
	unrelated := putGenerations(t, f.secrets, unrelatedOwner, 2)
	path := plantRetainedCredential(t, f.service, f.input.AccountID, domain.Codex, []byte("synthetic-retained-credential"))

	var logs syncBuffer
	f.service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	awaitServerFixture(t, f.serverRun(&serverLoginFixture{}))
	_, a := f.record()
	if a.Connection != nil || a.Subscription.Generation != "" || a.Subscription.RecoveryRequired || a.Health != domain.AccountDisconnected || a.Subscription.ServerOperation.ID != domain.ID(op.OperationId) {
		t.Fatal("explicit logout did not settle", a)
	}
	if f.accountRefs() != 0 {
		t.Fatalf("explicit logout left %d generations", f.accountRefs())
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("explicit logout left the retained credential file", err)
	}
	for _, ref := range unrelated {
		if _, err := f.secrets.Get(context.Background(), ref); err != nil {
			t.Fatal("explicit logout touched an unrelated account", err)
		}
	}
	records := logs.records(t, "server_credential_removal")
	if len(records) == 0 || records[len(records)-1]["result"] != string(credentialRemovalConfirmed) {
		t.Fatal("explicit removal logged no confirmed outcome", records)
	}
	if bytes.Contains([]byte(logs.String()), []byte("synthetic-retained")) {
		t.Fatal("removal logs exposed credential content")
	}
}

func TestExplicitAccountDeletionRemovesRetainedCredentialsAndReplaysExactly(t *testing.T) {
	f := unreferencedInitialSubscription(t)
	putGenerations(t, f.secrets, f.input.AccountID, retainedGenerationCount)
	unrelated := putGenerations(t, f.secrets, domain.NewID(), 2)
	path := plantRetainedCredential(t, f.service, f.input.AccountID, domain.Codex, []byte("synthetic-retained-credential"))
	// A vault failure part way through retains the obligation: the account and its
	// request stay pending and nothing is reported as deleted.
	f.secrets.deleteError = errors.New("vault unavailable")
	request := f.failedLoginDeletion()
	if err := f.deleteFailedLogin(request); err == nil {
		t.Fatal("deletion reported success while removal failed")
	}
	if _, a := f.record(); a.Alias == "" {
		t.Fatal("account vanished")
	}
	f.secrets.deleteError = nil
	if err := f.deleteFailedLogin(request); err != nil {
		t.Fatal("retry of the same request did not complete", err)
	}
	if f.accountRefs() != 0 {
		t.Fatal("deletion left retained generations")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("deletion left the retained credential file", err)
	}
	for _, ref := range unrelated {
		if _, err := f.secrets.Get(context.Background(), ref); err != nil {
			t.Fatal("deletion touched an unrelated account", err)
		}
	}
	deletes := f.secrets.deletes
	if err := f.deleteFailedLogin(request); err != nil {
		t.Fatal("exact receipt replay failed", err)
	}
	if f.secrets.deletes != deletes {
		t.Fatal("receipt replay removed credentials again")
	}
}

func TestManagedClaudeLogoutRemovesRetainedCredentialFileAndEveryGeneration(t *testing.T) {
	f := newManagedClaudeFixture(t)
	f.connect()
	putGenerations(t, f.secrets, f.input.AccountID, retainedGenerationCount)
	unrelated := putGenerations(t, f.secrets, domain.NewID(), 2)
	path := plantRetainedCredential(t, f.service, f.input.AccountID, domain.ClaudeCode, []byte("synthetic-retained-claude-credential"))
	if _, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, f.run())
	_, a := f.record()
	if a.Connection != nil || a.Subscription.Generation != "" || a.Subscription.ServerOperation.CleanupPhase != "" || managedClaudeNeedsRecovery(a) {
		t.Fatal("Claude logout did not settle and clear its removal checkpoint", a)
	}
	if f.accountRefs() != 0 {
		t.Fatalf("Claude logout left %d generations", f.accountRefs())
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("Claude logout left the retained credential file", err)
	}
	for _, ref := range unrelated {
		if _, err := f.secrets.Get(context.Background(), ref); err != nil {
			t.Fatal("Claude logout touched an unrelated account", err)
		}
	}
}

func TestManagedClaudeLogoutRemovalFailureKeepsTheCheckpoint(t *testing.T) {
	f := newManagedClaudeFixture(t)
	f.connect()
	putGenerations(t, f.secrets, f.input.AccountID, 3)
	path := plantRetainedCredential(t, f.service, f.input.AccountID, domain.ClaudeCode, []byte("synthetic-retained-claude-credential"))
	if _, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); err != nil {
		t.Fatal(err)
	}
	f.secrets.deleteError = errors.New("vault unavailable")
	awaitServerFixture(t, f.run())
	_, a := f.record()
	if a.Subscription.ServerOperation.CleanupPhase != domain.SubscriptionCredentialCleanupPending || !managedClaudeNeedsRecovery(a) {
		t.Fatal("a failed explicit removal lost its obligation")
	}
	f.secrets.deleteError = nil
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.recoverManagedClaude(ctx, f.input.AccountID, a.Subscription.ServerOperation.ID); err != nil {
		t.Fatal(err)
	}
	if f.accountRefs() != 0 {
		t.Fatal("retry did not remove every generation")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("retry did not remove the credential file", err)
	}
}

func TestProfileCredentialAdmissionAcceptsOnlySafeRetainedFiles(t *testing.T) {
	f := unreferencedInitialSubscription(t)
	account := f.input.AccountID
	path := plantRetainedCredential(t, f.service, account, domain.Codex, []byte("synthetic-retained-credential"))
	acquire := func() *nativeprofile.Lease {
		t.Helper()
		lease, err := f.service.scopeFor(account).Profiles.Acquire(context.Background(), nativeprofile.Key{Harness: domain.Codex, Purpose: nativeprofile.PurposeAuth, Account: account})
		if err != nil {
			t.Fatal(err)
		}
		return lease
	}
	admit := func() (bool, error) {
		lease := acquire()
		defer lease.Close()
		return profileCredentialAdmission(f.service.scopeFor(account), lease, domain.NewID(), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	}
	if retained, err := admit(); err != nil || !retained {
		t.Fatal("a retained regular private file was not admitted", retained, err)
	}
	// An interrupted credential write is rejected with the closed profile cause.
	pending := filepath.Join(filepath.Dir(path), security.OwnedTemporaryPrefix(path)+"interrupted")
	if err := os.WriteFile(pending, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := admit(); err == nil {
		t.Fatal("a pending temporary was admitted")
	} else if reason, _ := nativeprofile.Reasoned(err); reason != nativeprofile.ReasonCredentialPresent {
		t.Fatal("pending temporary lost its closed cause", err)
	}
	if err := os.Remove(pending); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := admit(); err == nil {
			t.Fatal("a world-readable credential file was admitted")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), path); err != nil {
			t.Skip("symlinks unavailable")
		}
		if _, err := admit(); err == nil {
			t.Fatal("a symlinked credential file was admitted")
		}
	}
}

func TestOpenServerSubscriptionFailureRetainsTheCredentialFile(t *testing.T) {
	f := unreferencedInitialSubscription(t)
	account := f.input.AccountID
	path := plantRetainedCredential(t, f.service, account, domain.Codex, []byte("synthetic-retained-credential"))
	// No Codex executable is installed in the fixture runtime, so the opener
	// fails after admitting the retained file. The failure must not retire it.
	t.Setenv("PATH", t.TempDir())
	_, err := openServerSubscription(context.Background(), f.service.scopeFor(account), domain.NewID(), nil, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	if err == nil {
		t.Skip("a native Codex installation was discovered; the opener failure path is not reachable")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(got, []byte("synthetic-retained-credential")) {
		t.Fatal("the opener failure retired the retained credential file", readErr)
	}
	// The profile lease was released: the next operation can acquire it again.
	lease, err := f.service.scopeFor(account).Profiles.Acquire(context.Background(), nativeprofile.Key{Harness: domain.Codex, Purpose: nativeprofile.PurposeAuth, Account: account})
	if err != nil {
		t.Fatal("the opener failure leaked its profile lease", err)
	}
	_ = lease.Close()
}

// Routine server login and refresh publish the validated generation and retain
// every earlier one. An injected vault Delete failure is irrelevant: no Delete
// call exists on these paths, so neither operation fails nor sets a fence.
func TestServerLoginAndRefreshRetainGenerationsDespiteVaultDeleteFailure(t *testing.T) {
	f := unreferencedInitialSubscription(t)
	f.secrets.deleteError = errors.New("vault deletion unavailable")
	f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	login := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("retained-routine-fixture", "first", time.Now().UTC())}
	done := f.serverRun(login)
	awaitServerFixture(t, login.started)
	close(login.finish)
	awaitServerFixture(t, done)
	_, a := f.record()
	first := a.Subscription.Generation
	if a.Connection == nil || first == "" || a.Health != domain.AccountReady || a.Subscription.RecoveryRequired || a.Subscription.ServerOperation.CleanupPhase != "" {
		t.Fatal("a Delete failure affected routine login", a)
	}
	f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	refresh := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("retained-routine-fixture", "second", time.Now().UTC())}
	close(refresh.finish)
	done = f.serverRun(refresh)
	awaitServerFixture(t, done)
	_, a = f.record()
	second := a.Subscription.Generation
	if second == first || a.Connection == nil || a.Health != domain.AccountReady || a.Subscription.RecoveryRequired || a.Subscription.Pending != nil || a.Subscription.ServerOperation.CleanupPhase != "" {
		t.Fatal("a Delete failure affected routine refresh", a)
	}
	for _, generation := range []domain.ID{first, second} {
		if _, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}); err != nil {
			t.Fatal("a generation was removed by a routine operation", err)
		}
	}
	if f.secrets.deletes != 0 {
		t.Fatal("routine operations called the vault Delete")
	}
}

// Native join failures still fail the owning operation; only the deletion
// obligation was removed.
func TestServerRefreshNativeJoinFailureStillFencesTheAccount(t *testing.T) {
	f := unreferencedInitialSubscription(t)
	f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	login := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("join-failure-fixture", "first", time.Now().UTC())}
	done := f.serverRun(login)
	awaitServerFixture(t, login.started)
	close(login.finish)
	awaitServerFixture(t, done)
	f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	refresh := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("join-failure-fixture", "second", time.Now().UTC()), closeError: errors.New("native child did not join")}
	close(refresh.finish)
	done = f.serverRun(refresh)
	awaitServerFixture(t, done)
	_, a := f.record()
	if !a.Subscription.RecoveryRequired || a.Health != domain.AccountFailed {
		t.Fatal("a native join failure was reported as success", a)
	}
}

func TestGrokLogoutRemovesMoreThanTheWorkerBudgetOfGenerations(t *testing.T) {
	g := newGrokLogout(t)
	putGenerations(t, g.f.secrets, g.f.input.AccountID, retainedGenerationCount)
	unrelated := putGenerations(t, g.f.secrets, domain.NewID(), 2)
	if p := g.settle(); p.State != grokSucceeds {
		t.Fatalf("progress = %v", p)
	}
	if refs := g.f.references(); len(refs) != 0 {
		t.Fatalf("the explicit Grok logout left %d generations", len(refs))
	}
	for _, ref := range unrelated {
		if _, err := g.f.secrets.Get(context.Background(), ref); err != nil {
			t.Fatal("the explicit Grok logout touched an unrelated account", err)
		}
	}
}

func TestStageServerAuthenticationNeverTreatsARetainedFileAsAuthority(t *testing.T) {
	f := unreferencedInitialSubscription(t)
	account := f.input.AccountID
	path := plantRetainedCredential(t, f.service, account, domain.Codex, []byte("synthetic-retained-credential"))
	home := filepath.Dir(filepath.Dir(path))
	lease, err := f.service.scopeFor(account).Profiles.Acquire(context.Background(), nativeprofile.Key{Harness: domain.Codex, Purpose: nativeprofile.PurposeAuth, Account: account})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	// A refresh writes exactly the published generation over the retained file.
	published := subscriptionTestBundle("staged-fixture", "published", time.Now().UTC())
	if err := stageServerAuthentication(home, published, true, domain.NewID(), logger, lease); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, published) {
		t.Fatal("refresh did not replace the retained file with the published generation", err)
	}
	// A new login has no published generation, so the retained file is removed
	// before the native child can mistake it for the new login's result.
	if err := stageServerAuthentication(home, nil, true, domain.NewID(), logger, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("a new login left a retained credential file readable as its result", err)
	}
	// With nothing retained a new login stages nothing.
	if err := stageServerAuthentication(home, nil, false, domain.NewID(), logger, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("a new login staged a credential file")
	}
	// An invalid bundle is rejected before any file is written.
	if err := stageServerAuthentication(home, []byte("not-a-bundle"), false, domain.NewID(), logger, lease); err == nil {
		t.Fatal("an invalid bundle was staged")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("an invalid bundle left a credential file")
	}
}
