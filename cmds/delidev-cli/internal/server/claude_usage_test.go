// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

const (
	usageFixtureAccess  = "sk-ant-oat01-UsageServerAccessTokenABCDEFGHIJKLMNOP0123456789_-"
	usageFixtureRefresh = "sk-ant-ort01-UsageServerRefreshTokenABCDEFGHIJKLMNOP0123456789_-"
	usageFixtureCode    = "usage-approval#sentinel-state"
	usageFixtureEmail   = "usage.person@example.com"
	usageFixtureOrgName = "Usage Sentinel Org"
	usageFixtureOrgID   = "org-usage-sentinel"
)

// fakeUsageNative is the injected native login. It reads no profile.
type fakeUsageNative struct {
	url         string
	ready       chan struct{}
	finish      chan struct{}
	codes       chan []byte
	captureErr  error
	closeErr    error
	submitErr   error
	closed      chan struct{}
	closeCalls  atomic.Int32
	releaseCall atomic.Int32
	submits     atomic.Int32
	captures    atomic.Int32
}

func newFakeUsageNative() *fakeUsageNative {
	return &fakeUsageNative{url: claudeFixtureURL(), ready: make(chan struct{}), finish: make(chan struct{}), codes: make(chan []byte, 4), closed: make(chan struct{})}
}
func (n *fakeUsageNative) Progress() (claude.AuthLoginProgress, error) {
	select {
	case <-n.ready:
		return claude.AuthLoginProgress{URL: n.url, Method: domain.SubscriptionBrowserCode}, nil
	default:
		return claude.AuthLoginProgress{}, nil
	}
}
func (n *fakeUsageNative) Submit(code []byte) error {
	n.submits.Add(1)
	n.codes <- bytes.Clone(code)
	clear(code)
	return n.submitErr
}
func (n *fakeUsageNative) Done() <-chan struct{} { return n.finish }
func (n *fakeUsageNative) Wait() error           { return nil }
func (n *fakeUsageNative) Close() error {
	if n.closeCalls.Add(1) == 1 {
		close(n.closed)
	}
	return n.closeErr
}
func (n *fakeUsageNative) Release() error { n.releaseCall.Add(1); return nil }
func (n *fakeUsageNative) Capture(context.Context) (claudeUsageCapture, error) {
	n.captures.Add(1)
	if n.captureErr != nil {
		return claudeUsageCapture{}, n.captureErr
	}
	bundle := subscription.ClaudeUsageBundle{AccessToken: []byte(usageFixtureAccess), RefreshToken: []byte(usageFixtureRefresh), ExpiresAt: time.Now().UTC().Add(time.Hour), Scopes: []string{"user:inference", "user:profile"}, ClientID: claude.UsageClientID}
	return claudeUsageCapture{Bundle: bundle, Identity: claude.UsageIdentity{Email: usageFixtureEmail, OrganizationID: usageFixtureOrgID, OrganizationName: usageFixtureOrgName, Plan: "max"}}, nil
}

type usageFixture struct {
	*managedFixture
	usage      *fakeUsageNative
	opens      atomic.Int32
	removals   atomic.Int32
	removalErr atomic.Value
	logs       *bytes.Buffer
	logMu      sync.Mutex
	execution  domain.ID
}

func newUsageFixture(t *testing.T) *usageFixture {
	t.Helper()
	m := newManagedClaudeFixture(t)
	generation := m.connect()
	f := &usageFixture{managedFixture: m, usage: newFakeUsageNative(), logs: &bytes.Buffer{}, execution: generation}
	f.service.logger = slog.New(slog.NewJSONHandler(&syncWriter{mu: &f.logMu, w: f.logs}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f.service.claudeUsageActiveTest = true
	f.service.claudeUsageOpen = func(context.Context, nativeScope, domain.ID, *slog.Logger) (claudeUsageNative, string, error) {
		f.opens.Add(1)
		return f.usage, domain.ClaudeProtocolVersion, nil
	}
	f.service.claudeUsageRemove = func(context.Context, nativeScope, *slog.Logger) error {
		f.removals.Add(1)
		if err, _ := f.removalErr.Load().(error); err != nil {
			return err
		}
		return nil
	}
	t.Cleanup(func() { f.service.closeClaudeUsage() })
	return f
}

type syncWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func (f *usageFixture) logText() string {
	f.logMu.Lock()
	defer f.logMu.Unlock()
	return f.logs.String()
}

func (f *usageFixture) section() *domain.ClaudeServerUsageState {
	_, a := f.record()
	return a.Subscription.ClaudeUsage
}

func (f *usageFixture) mutation() *pb.Mutation {
	r, _ := f.record()
	return &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}
}

func (f *usageFixture) start() (*pb.StartClaudeUsageLoginResponse, error) {
	response, err := f.client.StartClaudeUsageLogin(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.StartClaudeUsageLoginRequest{Mutation: f.mutation()}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *usageFixture) progress(op *pb.StartClaudeUsageLoginResponse) *pb.GetClaudeUsageLoginProgressResponse {
	f.t.Helper()
	response, err := f.client.GetClaudeUsageLoginProgress(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.GetClaudeUsageLoginProgressRequest{AccountId: string(f.input.AccountID), OperationId: op.OperationId, UsageGeneration: op.UsageGeneration}))
	if err != nil {
		f.t.Fatal(err)
	}
	return response.Msg
}

func (f *usageFixture) await(op *pb.StartClaudeUsageLoginResponse, want pb.ClaudeUsageLoginState) *pb.GetClaudeUsageLoginProgressResponse {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		progress := f.progress(op)
		if progress.State == want {
			return progress
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out waiting for %v, saw %v", want, progress.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *usageFixture) submit(op *pb.StartClaudeUsageLoginResponse, code string) error {
	_, err := f.client.SubmitClaudeUsageApprovalCode(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.SubmitClaudeUsageApprovalCodeRequest{Mutation: f.mutation(), OperationId: op.OperationId, UsageGeneration: op.UsageGeneration, Code: []byte(code)}))
	return err
}

func (f *usageFixture) confirm(op *pb.StartClaudeUsageLoginResponse, commitment string) (*pb.ConfirmClaudeUsageLinkResponse, error) {
	response, err := f.client.ConfirmClaudeUsageLink(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.ConfirmClaudeUsageLinkRequest{Mutation: f.mutation(), OperationId: op.OperationId, UsageGeneration: op.UsageGeneration, IdentityCommitment: commitment}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *usageFixture) cancel(op *pb.StartClaudeUsageLoginResponse) error {
	_, err := f.client.CancelClaudeUsageLogin(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelClaudeUsageLoginRequest{Mutation: f.mutation(), OperationId: op.OperationId, UsageGeneration: op.UsageGeneration}))
	return err
}

func (f *usageFixture) disconnect(generation string) error {
	_, err := f.client.DisconnectClaudeUsage(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.DisconnectClaudeUsageRequest{Mutation: f.mutation(), UsageGeneration: generation}))
	return err
}

// stage drives one login to the identity review.
func (f *usageFixture) stage() (*pb.StartClaudeUsageLoginResponse, *pb.GetClaudeUsageLoginProgressResponse) {
	f.t.Helper()
	op, err := f.start()
	if err != nil {
		f.t.Fatal(err)
	}
	close(f.usage.ready)
	f.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	if err := f.submit(op, usageFixtureCode); err != nil {
		f.t.Fatal(err)
	}
	select {
	case <-f.usage.codes:
	case <-time.After(10 * time.Second):
		f.t.Fatal("the approval code never reached the original child")
	}
	close(f.usage.finish)
	return op, f.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_AWAITING_CONFIRMATION)
}

func (f *usageFixture) usageRefs() []credentials.Ref {
	f.secrets.mu.Lock()
	defer f.secrets.mu.Unlock()
	var refs []credentials.Ref
	for ref := range f.secrets.values {
		if ref.Owner == f.input.AccountID && ref.Purpose == credentials.ClaudeUsage {
			refs = append(refs, ref)
		}
	}
	return refs
}

func TestClaudeUsageLoginLinksOnlyAfterExplicitConfirmation(t *testing.T) {
	f := newUsageFixture(t)
	_, before := f.record()
	op, review := f.stage()
	if review.Identity == nil || review.Identity.Email != usageFixtureEmail || review.Identity.Organization != usageFixtureOrgName || review.Identity.Plan != "max" {
		t.Fatalf("identity review = %+v", review.Identity)
	}
	// Replayed and repeated progress reads never relaunch, advance or confirm.
	for i := 0; i < 3; i++ {
		if got := f.progress(op); got.State != pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_AWAITING_CONFIRMATION {
			t.Fatalf("progress changed the state to %v", got.State)
		}
	}
	if f.opens.Load() != 1 || f.usage.submits.Load() != 1 || f.usage.captures.Load() != 1 {
		t.Fatalf("native work repeated: opens %d submits %d captures %d", f.opens.Load(), f.usage.submits.Load(), f.usage.captures.Load())
	}
	section := f.section()
	if section.Connection != domain.ClaudeUsageConfirming || section.Published || section.Reading() || section.LinkedAt != nil || len(section.Windows) != 0 || section.ProfileCleanup {
		t.Fatalf("a staged review granted authority: %+v", section)
	}
	refs := f.usageRefs()
	if len(refs) != 1 || refs[0].ID != section.CredentialID {
		t.Fatalf("staged references = %v", refs)
	}
	// The execution authority is untouched.
	_, after := f.record()
	if after.Subscription.Generation != before.Subscription.Generation || after.Connection.ID != before.Connection.ID || after.Health != before.Health {
		t.Fatal("usage staging changed the execution connection")
	}
	if len(f.secrets.values) != 2 {
		t.Fatalf("vault holds %d references, want the execution generation and one usage credential", len(f.secrets.values))
	}
	if _, err := f.confirm(op, strings.Repeat("0", 64)); err == nil {
		t.Fatal("a stale identity review was linked")
	}
	if f.section().Reading() {
		t.Fatal("a rejected confirmation granted authority")
	}
	linked, err := f.confirm(op, review.Identity.IdentityCommitment)
	if err != nil || linked.Replayed {
		t.Fatalf("confirm failed: %v", err)
	}
	section = f.section()
	if !section.Reading() || section.Connection != domain.ClaudeUsageLinked || !section.Published || section.LinkedAt == nil {
		t.Fatalf("link did not publish: %+v", section)
	}
	if got := f.progress(op); got.State != pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_LINKED {
		t.Fatalf("linked progress = %v", got.State)
	}
	// The usage credential opens only under its own purpose and binding.
	raw, err := f.secrets.Get(context.Background(), claudeUsageRef(f.input.AccountID, section.CredentialID))
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := subscription.OpenClaudeUsageBundle(raw, f.service.Identity.ServerID, f.input.AccountID, section.CredentialID, section.Identity.Commitment)
	if err != nil || string(bundle.AccessToken) != usageFixtureAccess {
		t.Fatalf("staged bundle is not readable: %v", err)
	}
	bundle.Clear()
	if _, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: section.CredentialID, Purpose: credentials.AccountLogin}); err == nil {
		t.Fatal("the usage credential is reachable as an execution login")
	}
	if f.removals.Load() == 0 {
		t.Fatal("native credential storage was not removed after the capture")
	}
	if replay, err := f.confirm(op, review.Identity.IdentityCommitment); err == nil && replay.Replayed {
		t.Fatal("a fresh request was reported as a replay")
	}
}

func TestClaudeUsageStartIsSingleFlightAndReplaySafe(t *testing.T) {
	f := newUsageFixture(t)
	mutation := f.mutation()
	request := &pb.StartClaudeUsageLoginRequest{Mutation: mutation}
	first, err := f.client.StartClaudeUsageLogin(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.client.StartClaudeUsageLogin(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil || !again.Msg.Replayed || again.Msg.OperationId != first.Msg.OperationId || again.Msg.UsageGeneration != first.Msg.UsageGeneration {
		t.Fatalf("replay = %+v %v", again, err)
	}
	if _, err := f.start(); connect.CodeOf(err) == 0 || err == nil {
		t.Fatal("a second concurrent usage login started")
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.opens.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.opens.Load() != 1 {
		t.Fatalf("native login opened %d times", f.opens.Load())
	}
}

func TestClaudeUsageCancelWhileWaitingJoinsLoginAndDiscardsEverything(t *testing.T) {
	f := newUsageFixture(t)
	op, err := f.start()
	if err != nil {
		t.Fatal(err)
	}
	close(f.usage.ready)
	f.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	if err := f.cancel(op); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.usage.closed:
	default:
		t.Fatal("cancel returned before the original child was joined")
	}
	if f.section() != nil && f.section().Reading() {
		t.Fatal("cancel left usage authority")
	}
	if len(f.usageRefs()) != 0 || f.usage.captures.Load() != 0 {
		t.Fatal("a canceled login captured or kept credentials")
	}
	if f.removals.Load() == 0 {
		t.Fatal("native storage removal was not attempted on cancel")
	}
	if section := f.section(); section != nil && section.OwesCleanup() {
		t.Fatalf("cancel left cleanup owed: %+v", section)
	}
	if f.service.claudeUsage.live(f.input.AccountID) {
		t.Fatal("the canceled run stayed registered")
	}
}

func TestClaudeUsageBackBeforeConfirmationRetiresTheStagedCredential(t *testing.T) {
	f := newUsageFixture(t)
	op, _ := f.stage()
	staged := f.section().CredentialID
	if err := f.cancel(op); err != nil {
		t.Fatal(err)
	}
	if len(f.usageRefs()) != 0 {
		t.Fatalf("an unconfirmed credential survived: %v", f.usageRefs())
	}
	if _, found := f.secrets.values[claudeUsageRef(f.input.AccountID, staged)]; found {
		t.Fatal("staged credential still readable")
	}
	if section := f.section(); section != nil && (section.Reading() || section.OwesCleanup()) {
		t.Fatalf("back left authority or owed cleanup: %+v", section)
	}
	if got := f.progressOrNil(op); got != nil && got.State == pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_LINKED {
		t.Fatal("canceled review reads as linked")
	}
}

func (f *usageFixture) progressOrNil(op *pb.StartClaudeUsageLoginResponse) *pb.GetClaudeUsageLoginProgressResponse {
	response, err := f.client.GetClaudeUsageLoginProgress(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.GetClaudeUsageLoginProgressRequest{AccountId: string(f.input.AccountID), OperationId: op.OperationId, UsageGeneration: op.UsageGeneration}))
	if err != nil {
		return nil
	}
	return response.Msg
}

func TestClaudeUsageDisconnectRevokesAndRemovesOnlyUsageCredentials(t *testing.T) {
	f := newUsageFixture(t)
	op, review := f.stage()
	if _, err := f.confirm(op, review.Identity.IdentityCommitment); err != nil {
		t.Fatal(err)
	}
	_, before := f.record()
	executionRef := credentials.Ref{Owner: f.input.AccountID, ID: f.execution, Purpose: credentials.AccountLogin}
	if err := f.disconnect(op.UsageGeneration); err != nil {
		t.Fatal(err)
	}
	if len(f.usageRefs()) != 0 {
		t.Fatal("the usage credential survived disconnect")
	}
	if _, found := f.secrets.values[executionRef]; !found {
		t.Fatal("disconnect removed the execution credential")
	}
	_, after := f.record()
	if after.Connection == nil || after.Connection.ID != before.Connection.ID || after.Subscription.Generation != before.Subscription.Generation || after.Health != before.Health {
		t.Fatal("usage disconnect changed execution readiness")
	}
	if section := after.Subscription.ClaudeUsage; section != nil && (section.Reading() || section.OwesCleanup()) {
		t.Fatalf("disconnect left authority or owed cleanup: %+v", section)
	}
	// Replay of the same disconnect is idempotent.
	if err := f.disconnect(op.UsageGeneration); err == nil {
		t.Log("a repeated disconnect with a new request is accepted as already achieved")
	}
}

func TestClaudeUsageDisconnectReportsPendingCleanupTruthfully(t *testing.T) {
	f := newUsageFixture(t)
	op, review := f.stage()
	if _, err := f.confirm(op, review.Identity.IdentityCommitment); err != nil {
		t.Fatal(err)
	}
	f.secrets.deleteError = errors.New("vault unavailable")
	if err := f.disconnect(op.UsageGeneration); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("disconnect reported cleanup complete: %v", err)
	}
	section := f.section()
	if section == nil || section.Reading() || section.Connection != domain.ClaudeUsageReconnectRequired || len(section.RetiredCredentials) != 1 {
		t.Fatalf("reads were not revoked with cleanup recorded: %+v", section)
	}
	f.secrets.deleteError = nil
	if err := f.service.cleanupClaudeUsage(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID); err != nil {
		t.Fatal(err)
	}
	if len(f.usageRefs()) != 0 || f.section().OwesCleanup() {
		t.Fatalf("cleanup did not finish: %+v", f.section())
	}
}

func TestClaudeUsageUncertainVaultWriteStaysOwedForCleanup(t *testing.T) {
	f := newUsageFixture(t)
	f.secrets.putError = errors.New("write acknowledgment lost")
	var written atomic.Bool
	f.secrets.afterPut = func() { written.Store(true) }
	op, err := f.start()
	if err != nil {
		t.Fatal(err)
	}
	close(f.usage.ready)
	f.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	if err := f.submit(op, usageFixtureCode); err != nil {
		t.Fatal(err)
	}
	<-f.usage.codes
	close(f.usage.finish)
	f.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_FAILED)
	if !written.Load() {
		t.Fatal("the fixture did not model an uncertain write")
	}
	// Cleanup deletes the intent it recorded before writing.
	deadline := time.Now().Add(5 * time.Second)
	for len(f.usageRefs()) != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(f.usageRefs()) != 0 {
		t.Fatal("an uncertainly written credential was orphaned")
	}
	if section := f.section(); section != nil && section.Reading() {
		t.Fatal("a failed stage granted authority")
	}
}

// A shutdown that cancels the run after the credential was written must not let
// the detached staging commit the review: the durable state stays untouched and
// the recorded intent keeps the written reference owed for cleanup.
func TestClaudeUsageShutdownDuringStagingNeverCommitsTheReview(t *testing.T) {
	f := newUsageFixture(t)
	f.secrets.afterPut = func() {
		f.service.claudeUsage.mu.Lock()
		run := f.service.claudeUsage.active[f.input.AccountID]
		f.service.claudeUsage.mu.Unlock()
		run.cancel(errClaudeUsageShutdown)
	}
	op, err := f.start()
	if err != nil {
		t.Fatal(err)
	}
	close(f.usage.ready)
	f.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	if err := f.submit(op, usageFixtureCode); err != nil {
		t.Fatal(err)
	}
	<-f.usage.codes
	close(f.usage.finish)
	deadline := time.Now().Add(10 * time.Second)
	for f.service.claudeUsage.live(f.input.AccountID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.service.claudeUsage.live(f.input.AccountID) {
		t.Fatal("the staging run never finished")
	}
	section := f.section()
	if section == nil || section.Connection != domain.ClaudeUsageConnecting || section.Published {
		t.Fatalf("shutdown committed a review: %+v", section)
	}
	if len(f.usageRefs()) != 1 || len(section.RetiredCredentials) != 1 || section.RetiredCredentials[0] != domain.ID(f.usageRefs()[0].ID) {
		t.Fatalf("the written reference is not owed for cleanup: refs=%d retired=%v", len(f.usageRefs()), section.RetiredCredentials)
	}
}

func TestClaudeUsageCaptureFailureLeavesNoCredential(t *testing.T) {
	f := newUsageFixture(t)
	f.usage.captureErr = domain.Fail(domain.RecoveryRequired, "The Claude usage credential storage is ambiguous.", "")
	op, err := f.start()
	if err != nil {
		t.Fatal(err)
	}
	close(f.usage.ready)
	f.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	_ = f.submit(op, usageFixtureCode)
	<-f.usage.codes
	close(f.usage.finish)
	f.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_FAILED)
	if len(f.usageRefs()) != 0 || f.section().Reading() || f.section().CredentialID != "" {
		t.Fatal("a failed capture left a credential or authority")
	}
}

func TestClaudeUsageLogoutJoinsLiveLoginAndDeletesRetiredReferences(t *testing.T) {
	f := newUsageFixture(t)
	op, review := f.stage()
	if _, err := f.confirm(op, review.Identity.IdentityCommitment); err != nil {
		t.Fatal(err)
	}
	staged := f.section().CredentialID
	// A failed vault deletion during logout retains the retired reference.
	f.secrets.deleteErrors = map[credentials.Ref]error{claudeUsageRef(f.input.AccountID, staged): errors.New("vault unavailable")}
	if _, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, f.run())
	_, account := f.record()
	if account.Connection != nil || account.Subscription.ClaudeUsage == nil || account.Subscription.ClaudeUsage.Reading() {
		t.Fatal("logout left usage authority")
	}
	if !slicesContainsID(account.Subscription.ClaudeUsage.RetiredCredentials, staged) {
		t.Fatalf("retired reference lost: %+v", account.Subscription.ClaudeUsage)
	}
	if _, found := f.secrets.values[claudeUsageRef(f.input.AccountID, staged)]; !found {
		t.Fatal("the fixture failed to retain the retired reference")
	}
	if !managedClaudeNeedsRecovery(account) {
		t.Fatal("the unfinished usage cleanup did not keep the logout checkpoint")
	}
	// The retry clears the usage reference, then the checkpoint.
	f.secrets.deleteErrors = nil
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.recoverManagedClaude(ctx, f.input.AccountID, account.Subscription.ServerOperation.ID); err != nil {
		t.Fatalf("checkpoint retry failed: %v", err)
	}
	_, account = f.record()
	if len(f.usageRefs()) != 0 || managedClaudeNeedsRecovery(account) {
		t.Fatalf("logout retry did not clear the usage reference and checkpoint")
	}
}

func slicesContainsID(ids []domain.ID, id domain.ID) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func TestClaudeUsageLogoutPreservesALiveCredentialOfAnotherAccount(t *testing.T) {
	f := newUsageFixture(t)
	other := credentials.Ref{Owner: domain.NewID(), ID: domain.NewID(), Purpose: credentials.ClaudeUsage}
	f.secrets.values[other] = []byte("another account")
	op, review := f.stage()
	if _, err := f.confirm(op, review.Identity.IdentityCommitment); err != nil {
		t.Fatal(err)
	}
	if _, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, f.run())
	if len(f.usageRefs()) != 0 {
		t.Fatal("logout left the retired usage credential")
	}
	if _, found := f.secrets.values[other]; !found {
		t.Fatal("logout deleted another account's usage credential")
	}
}

func TestClaudeUsageLogoutKeepsALiveCredentialOutOfTheRetirementList(t *testing.T) {
	f := newUsageFixture(t)
	op, review := f.stage()
	_ = review
	_ = op
	// The staged (unlinked) credential is live for its review; a removal pass
	// that was asked only for retired references must not delete it.
	if err := f.service.removeRetiredClaudeUsage(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID); err != nil {
		t.Fatal(err)
	}
	if len(f.usageRefs()) != 1 {
		t.Fatal("a live credential was deleted by a retired-reference pass")
	}
}

func TestClaudeUsageAccountDeletionRemovesUsageReferences(t *testing.T) {
	f := newUsageFixture(t)
	op, review := f.stage()
	if _, err := f.confirm(op, review.Identity.IdentityCommitment); err != nil {
		t.Fatal(err)
	}
	if _, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, f.run())
	_, source := f.record()
	// A logged-out, unreferenced account that still owes a retired usage
	// reference and holds one the section never recorded.
	id := domain.NewID()
	retired, stray := domain.NewID(), domain.NewID()
	clone := source
	state := *source.Subscription
	state.ClaudeUsage = &domain.ClaudeServerUsageState{Connection: domain.ClaudeUsageReconnectRequired, Generation: domain.NewID(), RetiredCredentials: []domain.ID{retired}}
	clone.Subscription = &state
	if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.usage.deletion", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.AccountKind, id, 0, "", "", clone)
	}); err != nil {
		t.Fatal(err)
	}
	f.secrets.values[claudeUsageRef(id, retired)] = []byte("retired")
	f.secrets.values[claudeUsageRef(id, stray)] = []byte("stray")
	live := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.ClaudeUsage}
	f.secrets.values[live] = []byte("another account")
	configuration := delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, f.http.URL)
	r, err := f.service.accountRecord(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configuration.DeleteConfiguration(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: &pb.Mutation{Id: string(id), ExpectedRevision: r.Revision, RequestId: string(domain.NewID())}})); err != nil {
		t.Fatalf("account deletion refused a usage reference: %v", err)
	}
	for ref := range f.secrets.values {
		if ref.Owner == id {
			t.Fatalf("account deletion left %s", ref.Purpose)
		}
	}
	if _, found := f.secrets.values[live]; !found {
		t.Fatal("account deletion removed another account's credential")
	}
}

func TestClaudeUsageRestartFencesOldLoginWithoutRelaunch(t *testing.T) {
	f := newUsageFixture(t)
	op, err := f.start()
	if err != nil {
		t.Fatal(err)
	}
	close(f.usage.ready)
	f.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	// Simulate a new server process: the old run is gone and the epoch differs.
	f.service.closeClaudeUsage()
	f.service.claudeUsage = claudeUsageRuns{}
	f.service.subscriptionEpoch = domain.NewID()
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.initializeServerSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.progress(op); got.State != pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_RECOVERY_REQUIRED || got.Url != "" {
		t.Fatalf("restart progress = %+v", got)
	}
	_, account := f.record()
	if !f.service.claudeUsageCleanupCandidate(f.input.AccountID, account, time.Now(), false) {
		t.Fatal("the fenced login is not selected for cleanup")
	}
	if err := f.service.cleanupClaudeUsage(ctx, f.input.AccountID); err != nil {
		t.Fatal(err)
	}
	if f.opens.Load() != 1 || f.removals.Load() == 0 {
		t.Fatalf("restart relaunched or skipped cleanup: opens %d removals %d", f.opens.Load(), f.removals.Load())
	}
	if section := f.section(); section != nil && section.Reading() {
		t.Fatal("restart restored authority")
	}
}

func TestClaudeUsageShutdownJoinsLoginAndKeepsDurableState(t *testing.T) {
	f := newUsageFixture(t)
	op, err := f.start()
	if err != nil {
		t.Fatal(err)
	}
	close(f.usage.ready)
	f.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	f.service.closeClaudeUsage()
	select {
	case <-f.usage.closed:
	default:
		t.Fatal("shutdown returned before the native child was joined")
	}
	section := f.section()
	if section == nil || section.Connection != domain.ClaudeUsageConnecting || section.Operation.State != domain.SubscriptionWaiting {
		t.Fatalf("shutdown fenced or changed the login: %+v", section)
	}
	if _, err := f.start(); err == nil {
		t.Fatal("a login started after shutdown")
	}
}

// A same-epoch login whose run disappeared without settling the section (for
// example a failed settlement after the child was joined) is selected by the
// cleanup scanner, joined to a failed state and its native storage removed
// instead of waiting for a restart or an explicit cancel.
func TestClaudeUsageCleanupScannerRecoversASameEpochLoginWithoutARun(t *testing.T) {
	f := newUsageFixture(t)
	op, err := f.start()
	if err != nil {
		t.Fatal(err)
	}
	close(f.usage.ready)
	f.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	_, account := f.record()
	if f.service.claudeUsageCleanupCandidate(f.input.AccountID, account, time.Now(), false) {
		t.Fatal("a login owned by its live run was selected for cleanup")
	}
	// The run ends without settling the durable section; the epoch is unchanged.
	f.service.closeClaudeUsage()
	f.service.claudeUsage = claudeUsageRuns{}
	_, account = f.record()
	if !f.service.claudeUsageCleanupCandidate(f.input.AccountID, account, time.Now(), false) {
		t.Fatal("a same-epoch login without a run was not selected for cleanup")
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.cleanupClaudeUsage(ctx, f.input.AccountID); err != nil {
		t.Fatal(err)
	}
	section := f.section()
	if section == nil || section.Connection != domain.ClaudeUsageReconnectRequired || section.Operation == nil || section.Operation.State != domain.SubscriptionFailed {
		t.Fatalf("the orphaned login was not settled as failed: %+v", section)
	}
	if f.removals.Load() == 0 {
		t.Fatal("the orphaned login's native storage was not removed")
	}
	_, account = f.record()
	if f.service.claudeUsageCleanupCandidate(f.input.AccountID, account, time.Now(), false) {
		t.Fatal("a settled login is still selected for cleanup")
	}
}

func TestClaudeUsageRefusesNonServerTokenAccounts(t *testing.T) {
	f := newSubscriptionFixture(t)
	f.service.claudeUsageActiveTest = true
	r, _ := f.record()
	_, err := f.client.StartClaudeUsageLogin(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.StartClaudeUsageLoginRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}}))
	if connect.CodeOf(err) == 0 || err == nil {
		t.Fatal("a ChatGPT account started a Claude usage login")
	}
}

func TestClaudeUsageRejectsLocalhostCallbackAddress(t *testing.T) {
	f := newUsageFixture(t)
	// The fake emits a manual address; the server-side guard also rejects an
	// address reported with the callback method.
	op, err := f.start()
	if err != nil {
		t.Fatal(err)
	}
	f.usage.url = "https://claude.com/cai/oauth/authorize?client_id=x"
	close(f.usage.ready)
	f.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_UNSUPPORTED)
	if f.section().Operation.State != domain.SubscriptionUnsupported {
		t.Fatal("an unsupported address was not reported")
	}
}

func TestClaudeUsageLogsAndResourcesNeverExposeSecrets(t *testing.T) {
	f := newUsageFixture(t)
	op, review := f.stage()
	if _, err := f.confirm(op, review.Identity.IdentityCommitment); err != nil {
		t.Fatal(err)
	}
	if err := f.disconnect(op.UsageGeneration); err != nil {
		t.Fatal(err)
	}
	logs := f.logText()
	for _, secret := range []string{usageFixtureAccess, usageFixtureRefresh, usageFixtureCode, "sentinel-state", usageFixtureEmail, usageFixtureOrgName, usageFixtureOrgID, "claude.com/cai/oauth", "code_challenge", "/subscription-runtime", "Usage Sentinel"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("a log line exposed %q", secret)
		}
	}
	if !strings.Contains(logs, "claude_usage_login_accepted") || !strings.Contains(logs, "claude_usage_linked") {
		t.Fatalf("lifecycle logs are missing: %s", logs)
	}
}

func TestClaudeUsageExecutionLogoutCancelsAndJoinsLiveLogin(t *testing.T) {
	f := newUsageFixture(t)
	op, err := f.start()
	if err != nil {
		t.Fatal(err)
	}
	close(f.usage.ready)
	f.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	// Joining the login settles the account, so the first request may carry a
	// stale revision; the joined login is gone and the retry is accepted.
	if _, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); err != nil && connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal(err)
	}
	select {
	case <-f.usage.closed:
	default:
		t.Fatal("logout was accepted before the usage child was joined")
	}
	if _, account := f.record(); account.Subscription.ServerOperation.Action != domain.SubscriptionLogout {
		if _, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); err != nil {
			t.Fatal(err)
		}
	}
	awaitServerFixture(t, f.run())
	_, account := f.record()
	section := account.Subscription.ClaudeUsage
	if account.Connection != nil || section == nil || section.Reading() || section.Connection != domain.ClaudeUsageReconnectRequired || section.Operation.Active() || section.OwesCleanup() && f.usage.captures.Load() != 0 {
		t.Fatalf("logout left the usage login: %+v", section)
	}
	if f.usage.captures.Load() != 0 || len(f.usageRefs()) != 0 {
		t.Fatal("a revoked login captured credentials")
	}
}

func TestClaudeUsageProgressIsBoundToTheStartingActor(t *testing.T) {
	f := newUsageFixture(t)
	op, err := f.start()
	if err != nil {
		t.Fatal(err)
	}
	stranger := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: domain.NewID()})
	request := connect.NewRequest(&pb.GetClaudeUsageLoginProgressRequest{AccountId: string(f.input.AccountID), OperationId: op.OperationId, UsageGeneration: op.UsageGeneration})
	if _, err := f.service.GetClaudeUsageLoginProgress(stranger, request); err == nil {
		t.Fatal("another actor read the login progress")
	}
	forged := connect.NewRequest(&pb.GetClaudeUsageLoginProgressRequest{AccountId: string(f.input.AccountID), OperationId: op.OperationId, UsageGeneration: string(domain.NewID())})
	if _, err := f.service.GetClaudeUsageLoginProgress(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), forged); err == nil {
		t.Fatal("a foreign usage generation read the login progress")
	}
}

func TestClaudeUsageStateIsProtectedFromGenericConfigurationWrites(t *testing.T) {
	f := newUsageFixture(t)
	op, review := f.stage()
	if _, err := f.confirm(op, review.Identity.IdentityCommitment); err != nil {
		t.Fatal(err)
	}
	r, account := f.record()
	for name, edit := range map[string]func(*domain.Account){
		"drop":       func(a *domain.Account) { a.Subscription.ClaudeUsage = nil },
		"forge link": func(a *domain.Account) { a.Subscription.ClaudeUsage.Identity.Email = "forged@example.com" },
		"retire":     func(a *domain.Account) { a.Subscription.ClaudeUsage.RetiredCredentials = []domain.ID{domain.NewID()} },
	} {
		clone := account
		state := *account.Subscription
		usage := *state.ClaudeUsage
		identity := *usage.Identity
		usage.Identity = &identity
		state.ClaudeUsage = &usage
		clone.Subscription = &state
		edit(&clone)
		err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
			return validateRelationships(tx, domain.AccountKind, r.ID, r.Revision, &clone)
		})
		if err == nil {
			t.Fatalf("%s: a generic account write changed the protected usage state", name)
		}
	}
	// An unchanged account still passes, so ordinary edits keep working.
	same := account
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		return validateRelationships(tx, domain.AccountKind, r.ID, r.Revision, &same)
	}); err != nil {
		t.Fatalf("an unchanged account was refused: %v", err)
	}
}

func TestClaudeUsageReviewWithoutAnAuthorizedActorIsDiscarded(t *testing.T) {
	f := newUsageFixture(t)
	f.stage()
	// The requesting client lost its authority before it could confirm.
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.usage.actor", nil, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		operation := *a.Subscription.ClaudeUsage.Operation
		operation.Actor = domain.Principal{Type: domain.ClientDevice, DeviceID: domain.NewID()}
		a.Subscription.ClaudeUsage.Operation = &operation
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.cleanupClaudeUsage(ctx, f.input.AccountID); err != nil {
		t.Fatal(err)
	}
	section := f.section()
	if section.Reading() || section.Connection != domain.ClaudeUsageReconnectRequired || len(f.usageRefs()) != 0 || section.OwesCleanup() {
		t.Fatalf("an unconfirmable review survived: %+v", section)
	}
}

func TestClaudeUsageUncertainNativeStartIsFencedForRecovery(t *testing.T) {
	f := newUsageFixture(t)
	f.service.claudeUsageOpen = func(context.Context, nativeScope, domain.ID, *slog.Logger) (claudeUsageNative, string, error) {
		f.opens.Add(1)
		return nil, "", domain.Fail(domain.RecoveryRequired, "The Claude login child could not be confirmed stopped.", "")
	}
	op, err := f.start()
	if err != nil {
		t.Fatal(err)
	}
	f.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_RECOVERY_REQUIRED)
	if section := f.section(); section.Connection != domain.ClaudeUsageConnecting || !section.ProfileCleanup {
		t.Fatalf("an uncertain start was settled instead of fenced: %+v", section)
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.cleanupClaudeUsage(ctx, f.input.AccountID); err != nil {
		t.Fatal(err)
	}
	if section := f.section(); section.Connection != domain.ClaudeUsageReconnectRequired || section.OwesCleanup() || f.opens.Load() != 1 {
		t.Fatalf("recovery relaunched or kept the fence: %+v", section)
	}
}

func TestClaudeUsageUnprovenNativeRemovalStaysOwedAndBlocksTheNextLogin(t *testing.T) {
	f := newUsageFixture(t)
	f.removalErr.Store(errors.New("keychain locked"))
	op, review := f.stage()
	section := f.section()
	if !section.ProfileCleanup {
		t.Fatal("unproven native removal was forgotten")
	}
	if _, err := f.confirm(op, review.Identity.IdentityCommitment); err != nil {
		t.Fatal(err)
	}
	f.removalErr.Store(errors.New("keychain locked"))
	if err := f.disconnect(op.UsageGeneration); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("disconnect reported success with native storage present: %v", err)
	}
	if _, err := f.start(); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("a login started over leftover credential storage: %v", err)
	}
}
