// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type recoveryServer struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	t  *testing.T
	mu sync.Mutex

	capture        *pb.ReadSubscriptionRecoveryCaptureResponse
	syncs          []*pb.SyncSubscriptionRecoveryCaptureRequest
	syncAuth       [][]byte
	syncError      error
	reads          int
	recovers       []*pb.RecoverSubscriptionRequest
	recoverOutcome pb.SubscriptionRecoveryOutcome
	recoverReason  pb.SubscriptionRecoveryBlockedReason
	recoverError   error
	home           string
}

func (s *recoveryServer) SyncSubscriptionRecoveryCapture(_ context.Context, req *connect.Request[pb.SyncSubscriptionRecoveryCaptureRequest]) (*connect.Response[pb.SyncSubscriptionRecoveryCaptureResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The capture must precede any deletion of the transient authentication.
	present, _ := os.ReadFile(filepath.Join(s.home, "auth.json"))
	s.syncAuth = append(s.syncAuth, present)
	s.syncs = append(s.syncs, &pb.SyncSubscriptionRecoveryCaptureRequest{Mutation: req.Msg.Mutation, LeaseId: req.Msg.LeaseId, InstanceId: req.Msg.InstanceId, GenerationId: req.Msg.GenerationId, Original: bytes.Clone(req.Msg.Original), Latest: bytes.Clone(req.Msg.Latest)})
	if s.syncError != nil {
		return nil, s.syncError
	}
	return connect.NewResponse(&pb.SyncSubscriptionRecoveryCaptureResponse{CaptureId: req.Msg.Mutation.RequestId}), nil
}

func (s *recoveryServer) ReadSubscriptionRecoveryCapture(context.Context, *connect.Request[pb.ReadSubscriptionRecoveryCaptureRequest]) (*connect.Response[pb.ReadSubscriptionRecoveryCaptureResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.capture == nil {
		problem := domain.Fail(domain.NotFound, "The lease has no recovery capture.", "")
		problem.Cause = "subscription_recovery_capture_absent"
		return nil, rpc.Error(problem, "")
	}
	return connect.NewResponse(&pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: s.capture.CaptureId, Original: bytes.Clone(s.capture.Original), Latest: bytes.Clone(s.capture.Latest)}), nil
}

func (s *recoveryServer) RecoverSubscription(_ context.Context, req *connect.Request[pb.RecoverSubscriptionRequest]) (*connect.Response[pb.RecoverSubscriptionResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recovers = append(s.recovers, req.Msg)
	if s.recoverError != nil {
		return nil, s.recoverError
	}
	if req.Msg.Report == pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_BLOCKED {
		return connect.NewResponse(&pb.RecoverSubscriptionResponse{Outcome: pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED, BlockedReason: req.Msg.BlockedReason}), nil
	}
	outcome := s.recoverOutcome
	if outcome == pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_UNSPECIFIED {
		outcome = pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_RESTORED
	}
	return connect.NewResponse(&pb.RecoverSubscriptionResponse{Outcome: outcome, BlockedReason: s.recoverReason}), nil
}

// settling returns the recoveries that carried a settlement report.
func (s *recoveryServer) settling() []*pb.RecoverSubscriptionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*pb.RecoverSubscriptionRequest
	for _, r := range s.recovers {
		if r.Report != pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_BLOCKED {
			out = append(out, r)
		}
	}
	return out
}

type recoveryWorkerFixture struct {
	t          *testing.T
	config     Config
	credential Credential
	server     *recoveryServer
	client     delidevv1connect.SubscriptionServiceClient
	accountID  domain.ID
	lease      domain.SubscriptionLease
	home       string
	journal    managedSubscriptionJournal
	path       string
	latest     []byte
	original   []byte
	instance   domain.ID
	url        string
}

func newRecoveryWorkerFixture(t *testing.T) *recoveryWorkerFixture {
	t.Helper()
	root := t.TempDir()
	f := &recoveryWorkerFixture{t: t, accountID: domain.NewID(), instance: domain.NewID()}
	f.config = Config{Root: root}
	f.credential = Credential{MachineID: domain.NewID(), ServerID: domain.NewID(), DeviceID: domain.NewID(), Token: "synthetic-token"}
	if err := security.PrivateDir(filepath.Join(root, "processes")); err != nil {
		t.Fatal(err)
	}
	f.home = filepath.Join(root, "native-profiles", "p", string(domain.NewID()), "codex")
	if err := security.PrivateDir(filepath.Dir(f.home)); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(f.home); err != nil {
		t.Fatal(err)
	}
	if canonical, err := filepath.EvalSymlinks(f.home); err == nil {
		f.home = canonical
	}
	f.original, f.latest = workerSubscriptionBundle("first"), workerSubscriptionBundle("second")
	if err := security.WriteAtomic(filepath.Join(f.home, "auth.json"), f.latest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, "history.jsonl"), []byte("ordinary retained history"), 0600); err != nil {
		t.Fatal(err)
	}
	f.lease = domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 3, Action: domain.SubscriptionExecute, MachineID: f.credential.MachineID, InstanceID: domain.NewID(), DeviceID: f.credential.DeviceID, Epoch: domain.NewID(), Generation: domain.NewID(), StartedAt: time.Now().UTC()}
	f.journal = managedSubscriptionJournal{Version: 1, Lease: f.lease.ID, Account: f.accountID, Operation: f.lease.OperationID, Instance: f.lease.InstanceID, Finish: domain.NewID(), Generation: f.lease.Generation, LeaseRevision: f.lease.Revision, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE, State: managedClosed, Server: f.credential.ServerID, AuthHome: f.home, NativeOwnerEvidence: true}
	f.path = filepath.Join(root, "managed-auth", string(f.lease.ID)+".json")
	if err := security.PrivateDir(filepath.Dir(f.path)); err != nil {
		t.Fatal(err)
	}
	f.writeJournal()
	f.server = &recoveryServer{t: t, home: f.home}
	httpServer := httptest.NewServer(mustHandler(f.server))
	t.Cleanup(httpServer.Close)
	f.url = httpServer.URL
	f.client = delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, httpServer.URL)
	return f
}

func mustHandler(s *recoveryServer) http.Handler {
	_, handler := delidevv1connect.NewSubscriptionServiceHandler(s)
	return handler
}

func (f *recoveryWorkerFixture) writeJournal() {
	f.t.Helper()
	if err := writeJSON(f.path, f.journal); err != nil {
		f.t.Fatal(err)
	}
}

func (f *recoveryWorkerFixture) readJournal() managedSubscriptionJournal {
	f.t.Helper()
	raw, err := security.ReadPrivate(f.path, 16<<10)
	if err != nil {
		f.t.Fatal(err)
	}
	var journal managedSubscriptionJournal
	if domain.Decode(raw, &journal) != nil {
		f.t.Fatal("journal is unreadable")
	}
	return journal
}

func (f *recoveryWorkerFixture) account() domain.Account {
	lease := f.lease
	return domain.Account{Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionChatGPT, Subscription: &domain.SubscriptionState{RecoveryRequired: true, Lease: &lease, Generation: lease.Generation}}
}

func (f *recoveryWorkerFixture) run() recoveryResult {
	return runCodexRecovery(context.Background(), f.config, f.client, f.credential, f.instance, f.accountID, f.accountValue())
}

func (f *recoveryWorkerFixture) accountValue() domain.Account { return f.account() }

func (f *recoveryWorkerFixture) authExists() bool {
	_, err := os.Lstat(filepath.Join(f.home, "auth.json"))
	return err == nil
}

func TestCodexRecoveryRestoresFromCaptureThroughAReplacementInstance(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	f.server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: f.original, Latest: f.latest}
	// A large retained history is scanned completely and survives.
	for i := 0; i < 2100; i++ {
		if err := os.WriteFile(filepath.Join(f.home, "rollout-"+strings.Repeat("x", 3)+string(rune('a'+i%26))+"-"+time.Duration(i).String()), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	result := f.run()
	if result.disposition != recoverySettled {
		t.Fatal("recovery did not settle", result)
	}
	if f.authExists() {
		t.Fatal("recovery left the transient authentication")
	}
	if _, err := os.Stat(filepath.Join(f.home, "history.jsonl")); err != nil {
		t.Fatal("recovery deleted retained history", err)
	}
	settling := f.server.settling()
	if len(settling) != 1 {
		t.Fatal("recovery sent an unexpected number of settlement requests", len(settling))
	}
	request := settling[0]
	if request.Report != pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE || request.CaptureId != f.server.capture.CaptureId || request.LeaseId != string(f.lease.ID) {
		t.Fatal("recovery report does not name the capture and lease")
	}
	if request.InstanceId != string(f.instance) || request.OriginalInstanceId != string(f.lease.InstanceID) || request.GenerationId != string(f.lease.Generation) || request.Mutation.ExpectedRevision != f.lease.Revision {
		t.Fatal("replacement instance did not bind the original instance, generation and lease revision")
	}
	if request.Mutation.RequestId == string(f.journal.Finish) {
		t.Fatal("recovery reused the original Finish request identity")
	}
	journal := f.readJournal()
	if journal.State != managedReported || journal.RecoveryRequest != domain.ID(request.Mutation.RequestId) {
		t.Fatal("recovery did not retain its durable request identity")
	}
}

// An interrupted profile can still hold the exact original generation the Take
// delivered while the capture already carries a refreshed latest bundle. Both are
// authenticated recovery evidence, so cleanup removes either exact file.
func TestCodexRecoveryRemovesTheCapturedOriginalGeneration(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	f.server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: f.original, Latest: f.latest}
	if err := security.WriteAtomic(filepath.Join(f.home, "auth.json"), f.original); err != nil {
		t.Fatal(err)
	}
	if result := f.run(); result.disposition != recoverySettled {
		t.Fatal("the captured original generation was not accepted for cleanup", result)
	}
	if f.authExists() {
		t.Fatal("recovery left the delivered original authentication")
	}
	if settling := f.server.settling(); len(settling) != 1 || settling[0].Report != pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE {
		t.Fatal("cleanup of the original generation was not reported against the capture")
	}
	// A file that is neither captured bundle stays in place.
	g := newRecoveryWorkerFixture(t)
	g.server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: g.original, Latest: g.latest}
	if err := security.WriteAtomic(filepath.Join(g.home, "auth.json"), workerSubscriptionBundle("third")); err != nil {
		t.Fatal(err)
	}
	if result := g.run(); result.disposition != recoveryRetry || !g.authExists() || len(g.server.settling()) != 0 {
		t.Fatal("an unrelated authentication file was removed", result)
	}
}

func TestCodexRecoveryKeepsCredentialRemnantsProtected(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	f.server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: f.original, Latest: f.latest}
	parsed, _, err := subscription.Parse(f.original)
	if err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(f.home, "rollout.jsonl")
	content := append(bytes.Repeat([]byte("x"), (64<<10)-3), []byte(parsed.Tokens.Refresh)...)
	if err := os.WriteFile(history, content, 0600); err != nil {
		t.Fatal(err)
	}
	result := f.run()
	if result.disposition != recoveryRetry || result.reason != domain.RecoveryCleanupUnconfirmed {
		t.Fatal("a credential remnant did not keep the lease protected", result)
	}
	if len(f.server.settling()) != 0 {
		t.Fatal("a remnant produced a settlement report")
	}
	if _, err := os.Stat(history); err != nil {
		t.Fatal("recovery deleted history to pass the scan", err)
	}
}

func TestCodexRecoveryResendsTheSameRequestAfterACrash(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	f.server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: f.original, Latest: f.latest}
	f.server.recoverError = connect.NewError(connect.CodeUnavailable, nil)
	if result := f.run(); result.disposition != recoveryRetry {
		t.Fatal("a lost response was not retried", result)
	}
	first := f.server.settling()
	if len(first) != 1 || f.authExists() {
		t.Fatal("cleanup was not proven before the report")
	}
	journal := f.readJournal()
	if !journal.RecoveryCleaned || journal.RecoveryRequest != domain.ID(first[0].Mutation.RequestId) {
		t.Fatal("the proven cleanup and request identity were not journaled before the report")
	}
	// The capture is retired by the server once the decision commits; the resend
	// never reads it again and repeats the identical request.
	f.server.mu.Lock()
	f.server.recoverError = nil
	reads := f.server.reads
	f.server.mu.Unlock()
	if result := f.run(); result.disposition != recoverySettled {
		t.Fatal("the resend did not settle", result)
	}
	second := f.server.settling()
	if len(second) != 2 || second[1].Mutation.RequestId != first[0].Mutation.RequestId || second[1].CaptureId != first[0].CaptureId || second[1].Report != first[0].Report {
		t.Fatal("the resend changed the recovery request")
	}
	f.server.mu.Lock()
	again := f.server.reads == reads
	f.server.mu.Unlock()
	if !again {
		t.Fatal("the resend read the capture again")
	}
}

func TestCodexRecoveryLegacyAuthenticationFileIsCapturedBeforeRemoval(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	result := f.run()
	if result.disposition != recoverySettled {
		t.Fatal("legacy retained authentication was not recovered", result)
	}
	if len(f.server.syncs) != 1 || len(f.server.syncs[0].Original) != 0 || !bytes.Equal(f.server.syncs[0].Latest, f.latest) {
		t.Fatal("legacy recovery did not synchronize only the latest bundle")
	}
	if !bytes.Equal(f.server.syncAuth[0], f.latest) {
		t.Fatal("the capture did not precede the removal of the authentication file")
	}
	settling := f.server.settling()
	if len(settling) != 1 || settling[0].Report != pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE || settling[0].CaptureId != f.server.syncs[0].Mutation.RequestId {
		t.Fatal("legacy recovery did not settle against its new capture")
	}
}

func TestCodexRecoveryLegacyWithoutProofStaysProtected(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	if err := os.Remove(filepath.Join(f.home, "auth.json")); err != nil {
		t.Fatal(err)
	}
	result := f.run()
	if result.disposition != recoveryTerminal || result.reason != domain.RecoveryCredentialProofUnavailable {
		t.Fatal("missing proof did not stay protected with the closed diagnostic", result)
	}
	if len(f.server.settling()) != 0 || len(f.server.syncs) != 0 {
		t.Fatal("missing proof fabricated a receipt or capture")
	}
	f.server.mu.Lock()
	defer f.server.mu.Unlock()
	if len(f.server.recovers) != 1 || f.server.recovers[0].BlockedReason != pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CREDENTIAL_PROOF_UNAVAILABLE {
		t.Fatal("the blocked reason was not reported for observation")
	}
}

func TestCodexRecoveryUnusableRetainedAuthenticationSettlesWithoutCredentials(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	garbage := []byte(`{"auth_mode":"chatgpt","tokens":"not usable"}`)
	if err := security.WriteAtomic(filepath.Join(f.home, "auth.json"), garbage); err != nil {
		t.Fatal(err)
	}
	f.server.recoverOutcome = pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_DISCONNECTED
	if result := f.run(); result.disposition != recoverySettled {
		t.Fatal("unusable retained authentication did not settle", result)
	}
	settling := f.server.settling()
	if len(settling) != 1 || settling[0].Report != pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL || settling[0].CaptureId != "" || f.authExists() {
		t.Fatal("unusable retained authentication was not reported as the absence of credentials")
	}
}

// Unusable or rejected retained authentication has no server capture, so the
// file is the only copy of its evidence. It must survive a retained-history scan
// that fails (a removable remnant or a canceled scan) so the next attempt still
// holds the evidence, and is removed only once the whole scan has passed.
func TestCodexRecoveryKeepsRejectedLegacyBytesUntilTheScanCommits(t *testing.T) {
	rejected := domain.Fail(domain.RecoveryRequired, "The bundle is refused.", "")
	rejected.Cause = domain.SubscriptionRecoveryBundleRejectedCause
	for name, tc := range map[string]struct {
		syncError error
		garbage   []byte
	}{
		"unparsable":      {nil, []byte(`{"auth_mode":"chatgpt","tokens":"not usable"}`)},
		"server rejected": {rpc.Error(rejected, ""), nil},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRecoveryWorkerFixture(t)
			f.server.syncError = tc.syncError
			f.server.recoverOutcome = pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_DISCONNECTED
			legacy := f.latest
			if tc.garbage != nil {
				legacy = tc.garbage
				if err := security.WriteAtomic(filepath.Join(f.home, "auth.json"), legacy); err != nil {
					t.Fatal(err)
				}
			}
			remnant := filepath.Join(f.home, "rollout.jsonl")
			if err := os.WriteFile(remnant, append([]byte("prefix "), legacy...), 0600); err != nil {
				t.Fatal(err)
			}
			result := f.run()
			if result.disposition != recoveryRetry || result.reason != domain.RecoveryCleanupUnconfirmed {
				t.Fatal("a failed scan did not retry", result)
			}
			kept, err := os.ReadFile(filepath.Join(f.home, "auth.json"))
			if err != nil || !bytes.Equal(kept, legacy) {
				t.Fatal("a failed scan removed the only copy of the rejected bytes", err)
			}
			if len(f.server.settling()) != 0 || f.readJournal().RecoveryCleaned {
				t.Fatal("a failed scan produced a cleanup checkpoint")
			}
			// A canceled scan keeps the evidence too.
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if err := os.Remove(remnant); err != nil {
				t.Fatal(err)
			}
			if result := runCodexRecovery(canceled, f.config, f.client, f.credential, f.instance, f.accountID, f.accountValue()); result.disposition == recoverySettled || !f.authExists() {
				t.Fatal("a canceled attempt removed the only copy of the rejected bytes", result)
			}
			// Once the problem is fixed the next attempt settles and removes it.
			if result := f.run(); result.disposition != recoverySettled {
				t.Fatal("the retained evidence did not recover after the scan problem was fixed", result)
			}
			if f.authExists() {
				t.Fatal("the rejected authentication file survived a committed cleanup")
			}
			if settling := f.server.settling(); len(settling) != 1 || settling[0].Report != pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL {
				t.Fatal("the committed cleanup was not reported as the absence of credentials")
			}
		})
	}
}

// Only the server's distinct positive bundle rejection lets the Worker settle
// without credentials; every other capture refusal keeps the only surviving
// credential file and retries.
func TestCodexRecoveryLegacyCaptureRefusalKeepsTheCredentialUnlessTheBundleIsRejected(t *testing.T) {
	rejected := domain.Fail(domain.RecoveryRequired, "The bundle is refused.", "")
	rejected.Cause = domain.SubscriptionRecoveryBundleRejectedCause
	for name, tc := range map[string]struct {
		err     error
		settles bool
	}{
		"bundle rejected":       {rpc.Error(rejected, ""), true},
		"authorization refusal": {rpc.Error(domain.Fail(domain.RecoveryRequired, "The account cannot grant authority.", ""), ""), false},
		"invalid request":       {rpc.Error(domain.Fail(domain.InvalidArgument, "The request is invalid.", ""), ""), false},
		"unavailable transport": {connect.NewError(connect.CodeUnavailable, nil), false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRecoveryWorkerFixture(t)
			f.server.syncError = tc.err
			result := f.run()
			if tc.settles {
				if result.disposition != recoverySettled || f.authExists() {
					t.Fatal("a positively rejected bundle did not settle without credentials", result)
				}
				if settling := f.server.settling(); len(settling) != 1 || settling[0].Report != pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL {
					t.Fatal("a rejected bundle was not reported as the absence of credentials")
				}
				return
			}
			if result.disposition != recoveryRetry || result.reason != domain.RecoveryCleanupUnconfirmed {
				t.Fatal("an unproven capture refusal did not retry", result)
			}
			if !f.authExists() {
				t.Fatal("an unproven capture refusal deleted the only surviving credential")
			}
			if len(f.server.settling()) != 0 {
				t.Fatal("an unproven capture refusal settled the lease")
			}
		})
	}
}

// A journaled home that no longer exists proves cleanup only when the absence is
// bound to the Worker root and to a profile the registry no longer names.
func TestCodexRecoveryMissingHomeAbsenceIsBoundToTheWorkerRootAndRegistry(t *testing.T) {
	missing := func(f *recoveryWorkerFixture, home string) {
		f.server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: f.original, Latest: f.latest}
		f.journal.AuthHome = home
		f.writeJournal()
	}
	rejected := func(t *testing.T, f *recoveryWorkerFixture, reason domain.SubscriptionRecoveryBlockedReason) {
		t.Helper()
		result := f.run()
		if result.reason != reason || result.disposition == recoverySettled || len(f.server.settling()) != 0 || !f.authExists() {
			t.Fatal("an unbound missing home gained cleanup authority", result)
		}
	}
	t.Run("outside the root", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		missing(f, filepath.Join(t.TempDir(), "nowhere", "codex"))
		rejected(t, f, domain.RecoveryOwnershipEvidenceInvalid)
	})
	t.Run("not canonical", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		missing(f, f.home+"/../gone/codex")
		rejected(t, f, domain.RecoveryOwnershipEvidenceInvalid)
	})
	t.Run("relative", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		missing(f, "native-profiles/p/gone/codex")
		rejected(t, f, domain.RecoveryOwnershipEvidenceInvalid)
	})
	t.Run("through a symlinked ancestor", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		outside := t.TempDir()
		link := filepath.Join(filepath.Dir(filepath.Dir(f.home)), "link")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		missing(f, filepath.Join(link, "gone", "codex"))
		rejected(t, f, domain.RecoveryOwnershipEvidenceInvalid)
	})
	t.Run("still registered", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		registry, err := nativeProfiles(f.config, f.credential.MachineID)
		if err != nil {
			t.Fatal(err)
		}
		root, err := filepath.EvalSymlinks(f.config.Root)
		if err != nil {
			t.Fatal(err)
		}
		history := domain.NewID()
		relative := "runtimes/" + string(history)
		registered := filepath.Join(root, "runtimes", string(history))
		if err := security.PrivateDir(filepath.Dir(registered)); err != nil {
			t.Fatal(err)
		}
		if err := security.PrivateDir(registered); err != nil {
			t.Fatal(err)
		}
		key := nativeprofile.Key{Harness: domain.Codex, Purpose: nativeprofile.PurposeConversation, Account: f.accountID, Conversation: history}
		if _, err := registry.RegisterLegacy(context.Background(), key, relative, history); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(registered); err != nil {
			t.Fatal(err)
		}
		f.journal.AuthHome = registered
		f.writeJournal()
		f.server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: f.original, Latest: f.latest}
		result := f.run()
		if result.disposition != recoveryRetry || result.reason != domain.RecoveryCleanupUnconfirmed || len(f.server.settling()) != 0 {
			t.Fatal("a home the registry still names was accepted as removed", result)
		}
	})
	t.Run("explicitly deleted profile", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		f.server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: f.original, Latest: f.latest}
		if err := os.RemoveAll(f.home); err != nil {
			t.Fatal(err)
		}
		if result := f.run(); result.disposition != recoverySettled {
			t.Fatal("a removed, unregistered home did not settle", result)
		}
	})
}

func TestCodexRecoveryRejectsForeignOrRestoredEvidence(t *testing.T) {
	cases := map[string]func(*recoveryWorkerFixture){
		"other server":        func(f *recoveryWorkerFixture) { f.journal.Server = domain.NewID() },
		"other account":       func(f *recoveryWorkerFixture) { f.journal.Account = domain.NewID() },
		"replaced generation": func(f *recoveryWorkerFixture) { f.journal.Generation = domain.NewID() },
		"other operation":     func(f *recoveryWorkerFixture) { f.journal.Operation = domain.NewID() },
		"other instance":      func(f *recoveryWorkerFixture) { f.journal.Instance = domain.NewID() },
		"member journal":      func(f *recoveryWorkerFixture) { f.journal.OwnerLease = domain.NewID() },
		"relocated home": func(f *recoveryWorkerFixture) {
			outside := filepath.Join(t.TempDir(), "codex")
			if err := security.PrivateDir(outside); err != nil {
				t.Fatal(err)
			}
			f.journal.AuthHome = outside
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRecoveryWorkerFixture(t)
			f.server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: f.original, Latest: f.latest}
			mutate(f)
			f.writeJournal()
			result := f.run()
			if result.disposition != recoveryTerminal || result.reason != domain.RecoveryOwnershipEvidenceInvalid {
				t.Fatal("foreign evidence was not rejected", result)
			}
			if len(f.server.settling()) != 0 || !f.authExists() {
				t.Fatal("foreign evidence gained cleanup authority")
			}
		})
	}
	t.Run("other worker", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		f.lease.MachineID = domain.NewID()
		if result := f.run(); result.disposition != recoveryTerminal || len(f.server.settling()) != 0 || !f.authExists() {
			t.Fatal("another Worker gained cleanup authority", result)
		}
	})
	t.Run("missing journal", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		if err := os.Remove(f.path); err != nil {
			t.Fatal(err)
		}
		if result := f.run(); result.disposition != recoveryTerminal || len(f.server.settling()) != 0 || !f.authExists() {
			t.Fatal("a missing journal gained cleanup authority", result)
		}
	})
}

func TestCodexRecoveryDefersToALiveExecution(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	codexLeasesInFlight.Store(string(f.lease.ID), struct{}{})
	defer codexLeasesInFlight.Delete(string(f.lease.ID))
	result := f.run()
	if result.disposition != recoveryRetry || result.reason != domain.RecoveryActiveOwner {
		t.Fatal("recovery raced a live execution", result)
	}
	if len(f.server.recovers) != 0 || f.server.reads != 0 || !f.authExists() {
		t.Fatal("recovery touched a live execution")
	}
}

func TestCodexRecoverySettlementCheckpointResumesWithoutLocalWork(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	request := domain.NewID()
	f.journal.RecoveryRequest, f.journal.RecoveryCleaned = request, true
	f.journal.RecoveryReport = int32(pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL)
	f.writeJournal()
	account := f.account()
	account.Subscription.Lease = nil
	account.Subscription.RecoverySettlement = &domain.SubscriptionRecoverySettlement{Request: request, Lease: f.lease.ID, Machine: f.lease.MachineID, Device: f.lease.DeviceID, Outcome: domain.RecoveryDisconnected, StartedAt: time.Now().UTC()}
	f.server.recoverOutcome = pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_DISCONNECTED
	result := runCodexRecovery(context.Background(), f.config, f.client, f.credential, f.instance, f.accountID, account)
	if result.disposition != recoverySettled {
		t.Fatal("the committed checkpoint was not completed", result)
	}
	settling := f.server.settling()
	if len(settling) != 1 || settling[0].Mutation.RequestId != string(request) || f.server.reads != 0 {
		t.Fatal("the checkpoint resend was not the original request")
	}
	// A checkpoint of another request cannot be completed from this journal.
	account.Subscription.RecoverySettlement.Request = domain.NewID()
	if result := runCodexRecovery(context.Background(), f.config, f.client, f.credential, f.instance, f.accountID, account); result.disposition != recoveryTerminal {
		t.Fatal("a foreign checkpoint was completed", result)
	}
}

func TestCodexRecoveryServerBlockClearsTheRequestAndRetries(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	f.server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: f.original, Latest: f.latest}
	f.server.recoverOutcome = pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED
	f.server.recoverReason = pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_MEMBERS_UNSETTLED
	result := f.run()
	if result.disposition != recoveryRetry || result.reason != domain.RecoveryMembersUnsettled {
		t.Fatal("a server block was not retried with its reason", result)
	}
	if journal := f.readJournal(); journal.RecoveryRequest != "" || journal.RecoveryCleaned {
		t.Fatal("a server block kept the blocked request identity")
	}
}

func TestCodexRecoveryMembersMustBelongToTheOwner(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	f.server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: f.original, Latest: f.latest}
	member := domain.NewID()
	account := f.account()
	account.Subscription.Lease.Shared = true
	account.Subscription.Members = []domain.SubscriptionMember{{LeaseID: f.lease.ID, OperationID: f.lease.OperationID, Revision: 1, State: domain.SubscriptionMemberRetained}, {LeaseID: member, OperationID: domain.NewID(), Revision: 2, State: domain.SubscriptionMemberRetained}}
	if result := runCodexRecovery(context.Background(), f.config, f.client, f.credential, f.instance, f.accountID, account); result.disposition != recoveryTerminal || !f.authExists() {
		t.Fatal("a member without retained journal evidence was settled", result)
	}
	journal := managedSubscriptionJournal{Version: 1, Lease: member, Account: f.accountID, Operation: account.Subscription.Members[1].OperationID, Instance: f.lease.InstanceID, Finish: domain.NewID(), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE, OwnerLease: f.lease.ID, NativeOwnerEvidence: true}
	if err := writeJSON(filepath.Join(f.config.Root, "managed-auth", string(member)+".json"), journal); err != nil {
		t.Fatal(err)
	}
	if result := runCodexRecovery(context.Background(), f.config, f.client, f.credential, f.instance, f.accountID, account); result.disposition != recoverySettled {
		t.Fatal("owned members blocked the owner settlement", result)
	}
	settling := f.server.settling()
	if len(settling) != 1 || len(settling[0].SettledMemberLeaseIds) != 2 {
		t.Fatal("the owner did not report every settled member")
	}
}

// A missing process index proves that no process ran only when the governing
// retained journal proves the owner never launched; a lost index of a journaled
// owner, or a journal that proves nothing, never proves completion.
func TestCodexRecoveryMissingProcessIndexRequiresNoLaunchEvidence(t *testing.T) {
	blocked := func(t *testing.T, f *recoveryWorkerFixture, account domain.Account, reason domain.SubscriptionRecoveryBlockedReason) {
		t.Helper()
		f.server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: f.original, Latest: f.latest}
		result := runCodexRecovery(context.Background(), f.config, f.client, f.credential, f.instance, f.accountID, account)
		if result.disposition != recoveryTerminal || result.reason != reason || len(f.server.settling()) != 0 || !f.authExists() {
			t.Fatal("a missing process index was read as proof that no process ran", result)
		}
	}
	t.Run("journaled job owner lost its index", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		f.journal.NativeOwners = []domain.ID{f.lease.OperationID}
		f.writeJournal()
		blocked(t, f, f.account(), domain.RecoveryOwnershipEvidenceInvalid)
	})
	t.Run("journaled owner lease lost its index", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		f.journal.NativeOwners = []domain.ID{f.lease.ID}
		f.writeJournal()
		blocked(t, f, f.account(), domain.RecoveryOwnershipEvidenceInvalid)
	})
	t.Run("journal without launch evidence", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		f.journal.NativeOwnerEvidence = false
		f.writeJournal()
		blocked(t, f, f.account(), domain.RecoveryOwnershipEvidenceInvalid)
	})
	t.Run("legacy journal without a home", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		f.journal.NativeOwnerEvidence, f.journal.AuthHome = false, ""
		f.writeJournal()
		blocked(t, f, f.account(), domain.RecoveryCredentialProofUnavailable)
	})
	t.Run("member that launched lost its index", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		member, operation := domain.NewID(), domain.NewID()
		account := f.account()
		account.Subscription.Lease.Shared = true
		account.Subscription.Members = []domain.SubscriptionMember{{LeaseID: f.lease.ID, OperationID: f.lease.OperationID, Revision: 1, State: domain.SubscriptionMemberRetained}, {LeaseID: member, OperationID: operation, Revision: 2, State: domain.SubscriptionMemberRetained}}
		journal := managedSubscriptionJournal{Version: 1, Lease: member, Account: f.accountID, Operation: operation, Instance: f.lease.InstanceID, Finish: domain.NewID(), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE, OwnerLease: f.lease.ID, NativeOwnerEvidence: true, NativeOwners: []domain.ID{operation}}
		if err := writeJSON(filepath.Join(f.config.Root, "managed-auth", string(member)+".json"), journal); err != nil {
			t.Fatal(err)
		}
		blocked(t, f, account, domain.RecoveryOwnershipEvidenceInvalid)
		// The same member that never launched settles with its owner.
		journal.NativeOwners = nil
		if err := writeJSON(filepath.Join(f.config.Root, "managed-auth", string(member)+".json"), journal); err != nil {
			t.Fatal(err)
		}
		if result := runCodexRecovery(context.Background(), f.config, f.client, f.credential, f.instance, f.accountID, account); result.disposition != recoverySettled {
			t.Fatal("a member without a launch did not settle", result)
		}
	})
	t.Run("an existing index is still reconciled", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		f.journal.NativeOwners = []domain.ID{f.lease.OperationID}
		f.writeJournal()
		if err := security.PrivateDir(filepath.Join(f.config.Root, "processes", string(f.lease.OperationID))); err != nil {
			t.Fatal(err)
		}
		f.server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: f.original, Latest: f.latest}
		if result := f.run(); result.disposition != recoverySettled {
			t.Fatal("an intact process index blocked recovery", result)
		}
	})
}

// The index of every native owner exists, and is journaled, before its
// credentialed launch.
func TestRecordNativeOwnerCreatesAndJournalsTheIndex(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	lease := &managedSubscriptionLease{client: f.client, credential: f.credential, instance: f.lease.InstanceID, account: f.accountID, journalPath: f.path, journal: f.journal}
	owner := domain.NewID()
	for i := 0; i < 2; i++ {
		if err := lease.recordNativeOwner(owner); err != nil {
			t.Fatal(err)
		}
	}
	if err := security.CheckPrivateDir(filepath.Join(f.config.Root, "processes", string(owner))); err != nil {
		t.Fatal("the process index was not created", err)
	}
	if owners := f.readJournal().NativeOwners; len(owners) != 1 || owners[0] != owner {
		t.Fatal("the owner was not journaled exactly once", owners)
	}
	if err := lease.recordNativeOwner("not-an-id"); err == nil {
		t.Fatal("an invalid owner was journaled")
	}
}

func TestCodexCleanupCapturesTheLatestBundleBeforeDeletingAuthentication(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	lease := &managedSubscriptionLease{recoveryCapture: true, client: f.client, credential: f.credential, instance: f.lease.InstanceID, account: f.accountID, response: &pb.TakeSubscriptionResponse{LeaseId: string(f.lease.ID), GenerationId: string(f.lease.Generation), LeaseRevision: f.lease.Revision, Bundle: f.original}, journalPath: f.path, journal: f.journal}
	f.server.syncError = connect.NewError(connect.CodeUnavailable, nil)
	err := lease.cleanupAuthentication(context.Background(), f.home, f.latest, f.original)
	if err == nil || !f.authExists() {
		t.Fatal("an unconfirmed capture still deleted the authentication", err)
	}
	var failure *executionAuthenticationFailure
	if !errors.As(err, &failure) || failure.reason != executionAuthCapture {
		t.Fatal("an unconfirmed capture lost its closed reason", err)
	}
	first := f.readJournal().CaptureRequest
	f.server.mu.Lock()
	f.server.syncError = nil
	f.server.mu.Unlock()
	if err := lease.cleanupAuthentication(context.Background(), f.home, f.latest, f.original); err != nil {
		t.Fatal(err)
	}
	if f.authExists() {
		t.Fatal("confirmed capture left the authentication")
	}
	journal := f.readJournal()
	if !journal.CaptureSynced || journal.CaptureRequest != first || !bytes.Equal(f.server.syncAuth[len(f.server.syncAuth)-1], f.latest) {
		t.Fatal("crash replay did not reuse the same capture identity or ran after the removal")
	}
	if len(f.server.syncs) != 2 || f.server.syncs[0].Mutation.RequestId != f.server.syncs[1].Mutation.RequestId {
		t.Fatal("the capture request identity changed between attempts")
	}
}

// Worker shutdown reaches the normal cleanup: the retained-history scan stops with
// the fail-closed canceled result, and a capture synchronization stops before any
// deletion.
func TestCodexCleanupObservesTheExecutionContext(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	t.Run("scan", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		lease := &managedSubscriptionLease{client: f.client, credential: f.credential, instance: f.lease.InstanceID, account: f.accountID, response: &pb.TakeSubscriptionResponse{LeaseId: string(f.lease.ID), Bundle: f.original}, journalPath: f.path, journal: f.journal}
		err := lease.cleanupAuthentication(canceled, f.home, f.latest, f.original)
		var failure *executionAuthenticationFailure
		if !errors.As(err, &failure) || failure.reason != executionAuthCanceled {
			t.Fatal("a canceled scan did not fail closed", err)
		}
		if _, statErr := os.Stat(filepath.Join(f.home, "history.jsonl")); statErr != nil {
			t.Fatal("a canceled scan deleted retained history", statErr)
		}
	})
	t.Run("capture", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		lease := &managedSubscriptionLease{recoveryCapture: true, client: f.client, credential: f.credential, instance: f.lease.InstanceID, account: f.accountID, response: &pb.TakeSubscriptionResponse{LeaseId: string(f.lease.ID), GenerationId: string(f.lease.Generation), LeaseRevision: f.lease.Revision, Bundle: f.original}, journalPath: f.path, journal: f.journal}
		err := lease.cleanupAuthentication(canceled, f.home, f.latest, f.original)
		var failure *executionAuthenticationFailure
		if !errors.As(err, &failure) || failure.reason != executionAuthCapture || !f.authExists() {
			t.Fatal("a canceled capture deleted the authentication", err)
		}
	})
	t.Run("config", func(t *testing.T) {
		if (Config{}).cleanupContext() == nil {
			t.Fatal("a Config without an execution context has no cleanup context")
		}
		if (Config{executionContext: canceled}).cleanupContext().Err() == nil {
			t.Fatal("the cleanup context is not the Worker execution context")
		}
	})
}

func TestCodexCleanupWithoutNegotiatedRecoveryKeepsTheLegacyFlow(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	lease := &managedSubscriptionLease{client: f.client, credential: f.credential, instance: f.lease.InstanceID, account: f.accountID, response: &pb.TakeSubscriptionResponse{LeaseId: string(f.lease.ID), Bundle: f.original}, journalPath: f.path, journal: f.journal}
	if err := lease.cleanupAuthentication(context.Background(), f.home, f.latest, f.original); err != nil {
		t.Fatal(err)
	}
	if len(f.server.syncs) != 0 {
		t.Fatal("an older peer received a capture")
	}
}

// The legacy decision that retained authentication holds no usable credential is
// journaled before the file, its only evidence, is deleted. A server-side block
// (members or credential users not yet settled) or a crash after the deletion
// therefore never turns the account into a terminal absence of proof.
func TestCodexRecoveryNoCredentialDecisionSurvivesBlocksAndCrashes(t *testing.T) {
	rejected := domain.Fail(domain.RecoveryRequired, "The bundle is refused.", "")
	rejected.Cause = domain.SubscriptionRecoveryBundleRejectedCause
	for name, tc := range map[string]struct {
		syncError error
		garbage   []byte
	}{
		"unparsable":      {nil, []byte(`{"auth_mode":"chatgpt","tokens":"not usable"}`)},
		"server rejected": {rpc.Error(rejected, ""), nil},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRecoveryWorkerFixture(t)
			f.server.syncError = tc.syncError
			if tc.garbage != nil {
				if err := security.WriteAtomic(filepath.Join(f.home, "auth.json"), tc.garbage); err != nil {
					t.Fatal(err)
				}
			}
			f.server.recoverOutcome = pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED
			f.server.recoverReason = pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_MEMBERS_UNSETTLED
			if result := f.run(); result.disposition != recoveryRetry || result.reason != domain.RecoveryMembersUnsettled {
				t.Fatal("a server block was not retried with its reason", result)
			}
			if f.authExists() || !f.readJournal().RecoveryNoCredential {
				t.Fatal("the cleaned attempt did not retain the durable no-credential decision")
			}
			// The blocker disappears; the next attempt must settle instead of
			// terminating as an unprovable absence of evidence.
			f.server.mu.Lock()
			f.server.recoverOutcome = pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_DISCONNECTED
			f.server.recoverReason = pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED
			f.server.mu.Unlock()
			if result := f.run(); result.disposition != recoverySettled {
				t.Fatal("the cleaned no-credential decision was lost after a server block", result)
			}
			settling := f.server.settling()
			if len(settling) != 2 || settling[1].Report != pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL || settling[1].CaptureId != "" || settling[0].Mutation.RequestId == settling[1].Mutation.RequestId {
				t.Fatal("the retried cleanup was not a fresh no-credential report")
			}
		})
	}
	t.Run("crash after deletion before the cleanup checkpoint", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		f.journal.RecoveryNoCredential = true
		f.writeJournal()
		if err := os.Remove(filepath.Join(f.home, "auth.json")); err != nil {
			t.Fatal(err)
		}
		f.server.recoverOutcome = pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_DISCONNECTED
		if result := f.run(); result.disposition != recoverySettled {
			t.Fatal("a journaled no-credential decision was lost with the deleted file", result)
		}
		if settling := f.server.settling(); len(settling) != 1 || settling[0].Report != pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL {
			t.Fatal("the journaled decision was not reported as the absence of credentials")
		}
	})
	t.Run("a server capture overrides the decision", func(t *testing.T) {
		f := newRecoveryWorkerFixture(t)
		f.journal.RecoveryNoCredential = true
		f.writeJournal()
		f.server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: f.original, Latest: f.latest}
		if result := f.run(); result.disposition != recoverySettled {
			t.Fatal("recovery did not settle", result)
		}
		if settling := f.server.settling(); len(settling) != 1 || settling[0].Report != pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE {
			t.Fatal("a stored capture was declared absent")
		}
	})
}

// The legacy capture request owns a copy of the retained credential. It is
// cleared as soon as the bounded RPC returns, after a success and a failure.
func TestCodexRecoveryClearsTheCredentialCopyOfTheLegacyCaptureRequest(t *testing.T) {
	for name, syncError := range map[string]error{
		"success": nil,
		"failure": connect.NewError(connect.CodeUnavailable, nil),
	} {
		t.Run(name, func(t *testing.T) {
			f := newRecoveryWorkerFixture(t)
			f.server.syncError = syncError
			var sent *pb.SyncSubscriptionRecoveryCaptureRequest
			f.client = delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.url, connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
				return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
					if message, ok := req.Any().(*pb.SyncSubscriptionRecoveryCaptureRequest); ok {
						sent = message
					}
					return next(ctx, req)
				}
			})))
			f.run()
			if sent == nil || len(sent.Latest) == 0 {
				t.Fatal("the legacy capture request was not observed")
			}
			if !bytes.Equal(sent.Latest, make([]byte, len(sent.Latest))) {
				t.Fatal("the request copy of the retained credential outlived the RPC")
			}
		})
	}
}

// Every deletion of a managed lease's transient authentication, including the
// pre-native cleanup of an unused original bundle, is preceded by the protected
// capture. An unconfirmed capture keeps the file so a fenced lease stays recoverable.
func TestCodexPreNativeCleanupCapturesTheOriginalBeforeDeletingAuthentication(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	if err := security.WriteAtomic(filepath.Join(f.home, "auth.json"), f.original); err != nil {
		t.Fatal(err)
	}
	lease := &managedSubscriptionLease{recoveryCapture: true, client: f.client, credential: f.credential, instance: f.lease.InstanceID, account: f.accountID, response: &pb.TakeSubscriptionResponse{LeaseId: string(f.lease.ID), GenerationId: string(f.lease.Generation), LeaseRevision: f.lease.Revision, Bundle: f.original}, journalPath: f.path, journal: f.journal}
	f.server.syncError = connect.NewError(connect.CodeUnavailable, nil)
	confirmed, err := lease.cleanupUnusedAuthentication(context.Background(), f.home, f.original, false)
	var failure *executionAuthenticationFailure
	if confirmed || !errors.As(err, &failure) || failure.reason != executionAuthCapture || !f.authExists() {
		t.Fatal("an unconfirmed capture still deleted the unused original", confirmed, err)
	}
	f.server.mu.Lock()
	f.server.syncError = nil
	f.server.mu.Unlock()
	confirmed, err = lease.cleanupUnusedAuthentication(context.Background(), f.home, f.original, false)
	if !confirmed || err != nil || f.authExists() {
		t.Fatal("a confirmed capture did not allow the cleanup", confirmed, err)
	}
	last := len(f.server.syncs) - 1
	if last != 1 || !bytes.Equal(f.server.syncAuth[last], f.original) || !bytes.Equal(f.server.syncs[last].Latest, f.original) || !bytes.Equal(f.server.syncs[last].Original, f.original) || f.server.syncs[0].Mutation.RequestId != f.server.syncs[1].Mutation.RequestId {
		t.Fatal("the pre-native capture did not precede the deletion under one durable identity")
	}
	// A retained helper keeps the lease unconfirmed even when the capture is in place.
	if err := security.WriteAtomic(filepath.Join(f.home, "auth.json"), f.original); err != nil {
		t.Fatal(err)
	}
	if confirmed, err := lease.cleanupUnusedAuthentication(context.Background(), f.home, f.original, true); confirmed || err != nil {
		t.Fatal("retained helpers did not keep the pre-native cleanup unconfirmed", confirmed, err)
	}
}

func TestCodexPreNativeCleanupWithoutNegotiatedRecoveryKeepsTheLegacyFlow(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	if err := security.WriteAtomic(filepath.Join(f.home, "auth.json"), f.original); err != nil {
		t.Fatal(err)
	}
	lease := &managedSubscriptionLease{client: f.client, credential: f.credential, instance: f.lease.InstanceID, account: f.accountID, response: &pb.TakeSubscriptionResponse{LeaseId: string(f.lease.ID), Bundle: f.original}, journalPath: f.path, journal: f.journal}
	if confirmed, err := lease.cleanupUnusedAuthentication(context.Background(), f.home, f.original, false); !confirmed || err != nil || f.authExists() {
		t.Fatal("the legacy pre-native cleanup changed", confirmed, err)
	}
	if len(f.server.syncs) != 0 {
		t.Fatal("an older peer received a capture")
	}
}

func TestCodexRecoveryRetryDelaysAndRotation(t *testing.T) {
	expected := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, want := range expected {
		if got := codexRecoveryDelay(i + 1); got != want {
			t.Fatalf("delay after failure %d is %s, want %s", i+1, got, want)
		}
	}
	now := time.Now()
	lane := newCodexRecoveryLane()
	lane.now = func() time.Time { return now }
	key := domain.NewID()
	if !lane.due(key, "a") {
		t.Fatal("a new account is not due")
	}
	if delay := lane.failure(key, "a", false); delay != time.Second || lane.due(key, "a") {
		t.Fatal("a failed account retried without its delay")
	}
	now = now.Add(time.Second)
	if !lane.due(key, "a") {
		t.Fatal("the delay did not expire")
	}
	if delay := lane.failure(key, "a", false); delay != 2*time.Second {
		t.Fatal("the delay did not double", delay)
	}
	// A changed original obligation is reconsidered at once; terminal evidence waits for it.
	if !lane.due(key, "b") {
		t.Fatal("a changed obligation was not reconsidered")
	}
	lane.failure(key, "b", true)
	now = now.Add(time.Hour)
	if lane.due(key, "b") {
		t.Fatal("terminal evidence retried in a hot loop")
	}
	if !lane.due(key, "c") {
		t.Fatal("a changed obligation did not clear the terminal state")
	}
	lane.success(key)
	if !lane.due(key, "c") {
		t.Fatal("a settled account kept its retry state")
	}
	if !newCodexRecoveryLane().due(key, "b") {
		t.Fatal("a reconnected lane did not reconsider terminal evidence")
	}
}

func TestCodexRecoveryRowAndObligation(t *testing.T) {
	lease := domain.SubscriptionLease{ID: domain.NewID(), Revision: 2, Action: domain.SubscriptionExecute, Generation: domain.NewID(), InstanceID: domain.NewID()}
	fenced := domain.Account{Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionChatGPT, Subscription: &domain.SubscriptionState{RecoveryRequired: true, Lease: &lease}}
	if !codexRecoveryRow(fenced) || codexRecoveryKey(fenced) != lease.ID {
		t.Fatal("a fenced Execute lease is not a recovery row")
	}
	login := fenced
	login.Subscription = &domain.SubscriptionState{RecoveryRequired: true, Lease: &domain.SubscriptionLease{ID: domain.NewID(), Action: domain.SubscriptionLogin}}
	claude := fenced
	claude.SubscriptionService = domain.SubscriptionClaude
	if codexRecoveryRow(login) || codexRecoveryRow(claude) {
		t.Fatal("another lease kind or service entered the ChatGPT recovery lane")
	}
	before := codexRecoveryObligation(fenced)
	changed := lease
	changed.Revision++
	fenced.Subscription = &domain.SubscriptionState{RecoveryRequired: true, Lease: &changed}
	if codexRecoveryObligation(fenced) == before {
		t.Fatal("a changed lease revision did not change the obligation")
	}
}

type watchRecoveryServer struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	mu       sync.Mutex
	accounts []*pb.Resource
	ticks    int
	reads    map[string]int
	reported map[string]int
}

func (s *watchRecoveryServer) WatchSubscription(ctx context.Context, _ *connect.Request[pb.WatchSubscriptionRequest], stream *connect.ServerStream[pb.WatchSubscriptionResponse]) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		rows := s.accounts
		s.ticks++
		s.mu.Unlock()
		for _, r := range rows {
			if err := stream.Send(&pb.WatchSubscriptionResponse{Account: r}); err != nil {
				return err
			}
		}
		if err := stream.Send(&pb.WatchSubscriptionResponse{}); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *watchRecoveryServer) ReadSubscriptionRecoveryCapture(_ context.Context, req *connect.Request[pb.ReadSubscriptionRecoveryCaptureRequest]) (*connect.Response[pb.ReadSubscriptionRecoveryCaptureResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads[req.Msg.AccountId]++
	return nil, connect.NewError(connect.CodeUnavailable, nil)
}

func (s *watchRecoveryServer) RecoverSubscription(_ context.Context, req *connect.Request[pb.RecoverSubscriptionRequest]) (*connect.Response[pb.RecoverSubscriptionResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reported[req.Msg.Mutation.Id]++
	return connect.NewResponse(&pb.RecoverSubscriptionResponse{Outcome: pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED, BlockedReason: req.Msg.BlockedReason}), nil
}

// The watch lane starts one attempt per account within the lane bound, never
// repeats terminal evidence in a hot loop, and shuts down by cancellation.
func TestCodexRecoveryWatchLaneBoundsAttemptsAndJoinsOnShutdown(t *testing.T) {
	root := t.TempDir()
	credential := Credential{MachineID: domain.NewID(), ServerID: domain.NewID(), DeviceID: domain.NewID(), Token: "synthetic-token"}
	server := &watchRecoveryServer{reads: map[string]int{}, reported: map[string]int{}}
	for i := 0; i < 6; i++ {
		lease := domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 3, Action: domain.SubscriptionExecute, MachineID: credential.MachineID, InstanceID: domain.NewID(), DeviceID: credential.DeviceID, Epoch: domain.NewID(), Generation: domain.NewID(), StartedAt: time.Now().UTC()}
		account := domain.Account{Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionChatGPT, Subscription: &domain.SubscriptionState{RecoveryRequired: true, Lease: &lease, Generation: lease.Generation}}
		raw, err := json.Marshal(account)
		if err != nil {
			t.Fatal(err)
		}
		// No retained journal exists for these accounts: their evidence is terminal.
		server.accounts = append(server.accounts, &pb.Resource{Id: string(domain.NewID()), Revision: 4, Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, DocumentJson: raw})
	}
	_, handler := delidevv1connect.NewSubscriptionServiceHandler(server)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	credential.Endpoint = httpServer.URL
	config := Config{Root: root, codexRecovery: true}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- watchSubscriptions(ctx, config, credential, domain.NewID()) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		reported, ticks := len(server.reported), server.ticks
		server.mu.Unlock()
		if reported == 6 && ticks >= 8 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	server.mu.Lock()
	for id, count := range server.reported {
		if count != 1 {
			t.Fatal("terminal evidence of an account was reported repeatedly", id, count)
		}
	}
	if len(server.reported) != 6 {
		t.Fatal("not every fenced account was served", len(server.reported))
	}
	server.mu.Unlock()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watch lane did not join on shutdown")
	}
}

func TestCodexRecoveryWatchLaneIgnoresRowsWithoutNegotiation(t *testing.T) {
	credential := Credential{MachineID: domain.NewID(), ServerID: domain.NewID(), DeviceID: domain.NewID(), Token: "synthetic-token"}
	server := &watchRecoveryServer{reads: map[string]int{}, reported: map[string]int{}}
	lease := domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 3, Action: domain.SubscriptionExecute, MachineID: credential.MachineID, InstanceID: domain.NewID(), DeviceID: credential.DeviceID, Epoch: domain.NewID(), Generation: domain.NewID(), StartedAt: time.Now().UTC()}
	raw, _ := json.Marshal(domain.Account{Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionChatGPT, Subscription: &domain.SubscriptionState{RecoveryRequired: true, Lease: &lease, Generation: lease.Generation}})
	server.accounts = []*pb.Resource{{Id: string(domain.NewID()), Revision: 4, DocumentJson: raw}}
	_, handler := delidevv1connect.NewSubscriptionServiceHandler(server)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	credential.Endpoint = httpServer.URL
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	err := watchSubscriptions(ctx, Config{Root: t.TempDir()}, credential, domain.NewID())
	// The Connect client sends the context deadline as a timeout header, so
	// a loaded runner can receive the server-side deadline as a wire error that
	// no longer wraps context.DeadlineExceeded. It is still a clean lane stop.
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && connect.CodeOf(err) != connect.CodeCanceled && connect.CodeOf(err) != connect.CodeDeadlineExceeded {
		t.Fatal("an unnegotiated recovery row aborted the lane", err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.reported) != 0 || len(server.reads) != 0 {
		t.Fatal("a Worker without the negotiated capability attempted recovery")
	}
}

// The recovery capture carries both maximum-size bundles in each direction; the
// subscription transport must neither refuse to send nor to read them.
func TestSubscriptionTransportCarriesTwoMaximumBundles(t *testing.T) {
	server := &recoveryServer{t: t, home: t.TempDir()}
	original, latest := bytes.Repeat([]byte("o"), subscription.MaxBundle), bytes.Repeat([]byte("l"), subscription.MaxBundle)
	server.capture = &pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(domain.NewID()), Original: original, Latest: latest}
	httpServer := httptest.NewServer(mustHandler(server))
	t.Cleanup(httpServer.Close)
	client := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, httpServer.URL, subscriptionRPCOptions()...)
	request := &pb.SyncSubscriptionRecoveryCaptureRequest{
		Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(domain.NewID()), ExpectedRevision: 1<<62 - 1},
		LeaseId:  string(domain.NewID()), MachineId: string(domain.NewID()), InstanceId: string(domain.NewID()), GenerationId: string(domain.NewID()),
		Original: original, Latest: latest,
	}
	if _, err := client.SyncSubscriptionRecoveryCapture(context.Background(), connect.NewRequest(request)); err != nil {
		t.Fatal("two maximum bundles could not be sent", err)
	}
	read, err := client.ReadSubscriptionRecoveryCapture(context.Background(), connect.NewRequest(&pb.ReadSubscriptionRecoveryCaptureRequest{AccountId: string(domain.NewID()), LeaseId: string(domain.NewID()), MachineId: string(domain.NewID()), InstanceId: string(domain.NewID()), OriginalInstanceId: string(domain.NewID()), GenerationId: string(domain.NewID())}))
	if err != nil || len(read.Msg.Original) != subscription.MaxBundle || len(read.Msg.Latest) != subscription.MaxBundle {
		t.Fatal("two maximum bundles could not be read", err)
	}
}
