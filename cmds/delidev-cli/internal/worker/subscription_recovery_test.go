// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

type recoveryRPC struct {
	delidevv1connect.SubscriptionServiceClient
	mu         sync.Mutex
	original   []byte
	latest     []byte
	captureID  string
	recoverErr error
	synced     []*pb.SyncSubscriptionRecoveryCaptureRequest
	recovered  []*pb.RecoverSubscriptionRequest
	reads      int
}

func (r *recoveryRPC) ReadSubscriptionRecoveryCapture(context.Context, *connect.Request[pb.ReadSubscriptionRecoveryCaptureRequest]) (*connect.Response[pb.ReadSubscriptionRecoveryCaptureResponse], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	return connect.NewResponse(&pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: r.captureID, LatestBundle: append([]byte(nil), r.latest...), OriginalBundle: append([]byte(nil), r.original...)}), nil
}

func (r *recoveryRPC) SyncSubscriptionRecoveryCapture(_ context.Context, req *connect.Request[pb.SyncSubscriptionRecoveryCaptureRequest]) (*connect.Response[pb.SyncSubscriptionRecoveryCaptureResponse], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.synced = append(r.synced, proto.Clone(req.Msg).(*pb.SyncSubscriptionRecoveryCaptureRequest))
	return connect.NewResponse(&pb.SyncSubscriptionRecoveryCaptureResponse{CaptureId: req.Msg.Mutation.RequestId}), nil
}

func (r *recoveryRPC) RecoverSubscription(_ context.Context, req *connect.Request[pb.RecoverSubscriptionRequest]) (*connect.Response[pb.RecoverSubscriptionResponse], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recovered = append(r.recovered, proto.Clone(req.Msg).(*pb.RecoverSubscriptionRequest))
	if r.recoverErr != nil {
		return nil, r.recoverErr
	}
	return connect.NewResponse(&pb.RecoverSubscriptionResponse{}), nil
}

type recoveryWorkerFixture struct {
	t       *testing.T
	config  Config
	root    string
	home    string
	account domain.ID
	lease   *domain.SubscriptionLease
	journal managedSubscriptionJournal
	path    string
	rpc     *recoveryRPC
}

func newRecoveryWorkerFixture(t *testing.T) *recoveryWorkerFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &recoveryWorkerFixture{t: t, root: root, account: domain.NewID(), rpc: &recoveryRPC{}}
	f.config = Config{Root: root, Logger: slog.New(slog.DiscardHandler), recovery: true, codexOwners: newCodexOwnerRegistry()}
	f.home = filepath.Join(root, "runtimes", "accounts", string(f.account), "codex")
	if err := security.PrivateDir(f.home); err != nil {
		t.Fatal(err)
	}
	lease, operation, instance := domain.NewID(), domain.NewID(), domain.NewID()
	f.lease = &domain.SubscriptionLease{ID: lease, OperationID: operation, Revision: 7, Action: domain.SubscriptionExecute, MachineID: domain.NewID(), InstanceID: instance, DeviceID: domain.NewID(), Epoch: domain.NewID(), Generation: domain.NewID(), StartedAt: time.Now().UTC()}
	f.journal = managedSubscriptionJournal{Version: 1, Lease: lease, Account: f.account, Operation: operation, Instance: instance, Finish: domain.NewID(), Generation: f.lease.Generation, LeaseRevision: 7, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE, State: managedClosed, NativeHome: f.home, ProcessOwner: operation, InstallationRetention: true}
	f.path = filepath.Join(root, "managed-auth", string(lease)+".json")
	if err := security.PrivateDir(filepath.Dir(f.path)); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(root, "processes", string(operation))); err != nil {
		t.Fatal(err)
	}
	f.save()
	f.rpc.original = workerSubscriptionBundle("first")
	return f
}

func (f *recoveryWorkerFixture) save() {
	f.t.Helper()
	if err := writeJSON(f.path, f.journal); err != nil {
		f.t.Fatal(err)
	}
}

func (f *recoveryWorkerFixture) credential() Credential {
	return Credential{MachineID: f.lease.MachineID}
}

func (f *recoveryWorkerFixture) account_() domain.Account {
	return domain.Account{SubscriptionService: domain.SubscriptionChatGPT, Subscription: &domain.SubscriptionState{RecoveryRequired: true, Generation: f.lease.Generation, Lease: f.lease}}
}

func (f *recoveryWorkerFixture) run(tracker *recoveryTracker) error {
	return runManagedCodexRecovery(context.Background(), f.config, f.rpc, f.credential(), domain.NewID(), f.account, f.account_(), tracker)
}

func (f *recoveryWorkerFixture) reload() managedSubscriptionJournal {
	f.t.Helper()
	raw, err := security.ReadPrivate(f.path, 64<<10)
	if err != nil {
		f.t.Fatal(err)
	}
	var journal managedSubscriptionJournal
	if domain.Decode(raw, &journal) != nil {
		f.t.Fatal("journal is unreadable")
	}
	return journal
}

func TestManagedCodexRecoveryPublishesProtectedLatestAfterIndependentCleanup(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	f.rpc.latest, f.rpc.captureID = workerSubscriptionBundle("second"), string(domain.NewID())
	history := filepath.Join(f.home, "rollout.jsonl")
	if err := os.WriteFile(history, []byte("retained native history"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.run(newRecoveryTracker()); err != nil {
		t.Fatal(err)
	}
	if len(f.rpc.recovered) != 1 {
		t.Fatal("recovery did not settle exactly once", len(f.rpc.recovered))
	}
	request := f.rpc.recovered[0]
	journal := f.reload()
	if request.Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_CREDENTIALS_VERIFIED || request.CaptureId != f.rpc.captureID || request.Mutation.RequestId != string(journal.RecoveryID) || request.Mutation.RequestId == string(f.journal.Finish) {
		t.Fatal("recovery did not use a distinct durable identity and the protected capture")
	}
	if journal.State != managedReported || request.Mutation.ExpectedRevision != 7 {
		t.Fatal("settled recovery was not journaled against the original revision")
	}
	if _, err := os.Stat(history); err != nil {
		t.Fatal("recovery removed retained history", err)
	}
}

func TestManagedCodexRecoveryReplaysSameRequestAfterLostAcknowledgment(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	f.rpc.latest, f.rpc.captureID = workerSubscriptionBundle("second"), string(domain.NewID())
	f.rpc.recoverErr = connect.NewError(connect.CodeUnavailable, errors.New("lost"))
	if err := f.run(newRecoveryTracker()); err == nil {
		t.Fatal("a lost acknowledgment was reported as settled")
	}
	first := f.reload()
	if first.RecoveryOutcome != domain.RecoveryCredentialsVerified || first.RecoveryID == "" || first.State == managedReported {
		t.Fatal("the proven outcome was not durable before the report")
	}
	// A restart reuses the proven outcome without redoing cleanup or minting
	// another identity, even when the server capture can no longer be read.
	f.rpc.recoverErr, f.rpc.latest, f.rpc.captureID = nil, nil, ""
	f.rpc.reads = 0
	if err := f.run(newRecoveryTracker()); err != nil {
		t.Fatal(err)
	}
	if f.rpc.reads != 0 || len(f.rpc.recovered) != 2 || f.rpc.recovered[0].Mutation.RequestId != f.rpc.recovered[1].Mutation.RequestId {
		t.Fatal("restart did not replay the same recovery request", f.rpc.reads, len(f.rpc.recovered))
	}
}

func TestManagedCodexRecoveryLegacyJournalWithoutProofStaysProtected(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	f.journal.NativeHome, f.journal.ProcessOwner = "", ""
	f.save()
	tracker := newRecoveryTracker()
	err := f.run(tracker)
	var blocked *recoveryBlocked
	if !errors.As(err, &blocked) || blocked.reason != domain.RecoveryCredentialProofUnavailable {
		t.Fatal("legacy journal without latest proof did not stay protected", err)
	}
	if len(f.rpc.synced) != 0 {
		t.Fatal("a capture receipt was fabricated")
	}
	for _, request := range f.rpc.recovered {
		if request.Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED || request.Reason != pb.SubscriptionRecoveryReason_SUBSCRIPTION_RECOVERY_REASON_CREDENTIAL_PROOF_UNAVAILABLE {
			t.Fatal("legacy journal settled instead of reporting the closed block")
		}
	}
	if len(f.rpc.recovered) != 1 {
		t.Fatal("the closed reason was not reported once", len(f.rpc.recovered))
	}
	tracker.record(f.account, f.lease, err, time.Now())
	if tracker.ready(f.account, f.lease, time.Now().Add(time.Hour)) {
		t.Fatal("blocked proof was retried without a changed obligation")
	}
}

func TestManagedCodexRecoveryRefusesForeignEvidence(t *testing.T) {
	cases := map[string]func(*recoveryWorkerFixture){
		"other-account":  func(f *recoveryWorkerFixture) { f.journal.Account = domain.NewID() },
		"other-instance": func(f *recoveryWorkerFixture) { f.journal.Instance = domain.NewID() },
		"other-revision": func(f *recoveryWorkerFixture) { f.journal.LeaseRevision = 9 },
		"other-root": func(f *recoveryWorkerFixture) {
			f.journal.NativeHome = filepath.Join(t.TempDir(), "codex")
			if err := security.PrivateDir(f.journal.NativeHome); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRecoveryWorkerFixture(t)
			f.rpc.latest, f.rpc.captureID = workerSubscriptionBundle("second"), string(domain.NewID())
			mutate(f)
			f.save()
			var blocked *recoveryBlocked
			if err := f.run(newRecoveryTracker()); !errors.As(err, &blocked) {
				t.Fatal("foreign evidence gained cleanup authority", err)
			}
			for _, request := range f.rpc.recovered {
				if request.Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED {
					t.Fatal("foreign evidence settled recovery")
				}
			}
		})
	}
}

func TestManagedCodexRecoveryKeepsProtectionForCredentialRemnantsAndPendingFiles(t *testing.T) {
	for _, mode := range []string{"remnant", "pending", "special"} {
		t.Run(mode, func(t *testing.T) {
			f := newRecoveryWorkerFixture(t)
			latest := workerSubscriptionBundle("second")
			f.rpc.latest, f.rpc.captureID = latest, string(domain.NewID())
			parsed, _, _ := subscription.Parse(latest)
			history := filepath.Join(f.home, "rollout.jsonl")
			switch mode {
			case "remnant":
				if err := os.WriteFile(history, []byte("x"+parsed.Tokens.Refresh), 0600); err != nil {
					t.Fatal(err)
				}
			case "pending":
				if err := os.WriteFile(filepath.Join(f.home, ".pending-native-auth"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(history, []byte("history"), 0600); err != nil {
					t.Fatal(err)
				}
			case "special":
				if err := os.WriteFile(history, []byte("history"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(f.home, "sub"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(history, filepath.Join(f.home, "sub", "link")); err != nil {
					t.Skip("symlinks unavailable")
				}
				// A helper link is ordinary retained data; add a real remnant to make
				// the scan fail through a regular file instead.
				if err := os.WriteFile(filepath.Join(f.home, "sub", "copy"), []byte(parsed.Tokens.Refresh), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var blocked *recoveryBlocked
			if err := f.run(newRecoveryTracker()); !errors.As(err, &blocked) || blocked.reason != domain.RecoveryAuthenticationCleanupUnconfirmed {
				t.Fatal("uncertain filesystem evidence did not stay protected", err)
			}
			if _, err := os.Stat(history); err != nil && mode != "pending" {
				t.Fatal("history was deleted to pass the scan", err)
			}
			for _, request := range f.rpc.recovered {
				if request.Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED {
					t.Fatal("protection was cleared with unconfirmed cleanup")
				}
			}
		})
	}
}

func TestManagedCodexRecoveryProtectsCredentialFileBeforeDeletingIt(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	latest := workerSubscriptionBundle("second")
	if err := security.WriteAtomic(filepath.Join(f.home, "auth.json"), latest); err != nil {
		t.Fatal(err)
	}
	if err := f.run(newRecoveryTracker()); err != nil {
		t.Fatal(err)
	}
	if len(f.rpc.synced) != 1 || subscription.Digest(f.rpc.synced[0].Bundle) != subscription.Digest(latest) {
		t.Fatal("the surviving credential was deleted without a protected capture")
	}
	if _, err := os.Lstat(filepath.Join(f.home, "auth.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("transient authentication remained after recovery", err)
	}
	if len(f.rpc.recovered) != 1 || f.rpc.recovered[0].CaptureId != f.rpc.synced[0].Mutation.RequestId {
		t.Fatal("recovery did not bind the freshly protected capture")
	}
}

func TestManagedCodexRecoveryWithoutUsableCredentialsAllowsExistingSignIn(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	// Cleanup evidence exists, the credential file is invalid and no capture was
	// ever synchronized: independent cleanup alone is proven.
	if err := os.WriteFile(filepath.Join(f.home, "auth.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.run(newRecoveryTracker()); err != nil {
		t.Fatal(err)
	}
	if len(f.rpc.recovered) != 1 || f.rpc.recovered[0].Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_NO_USABLE_CREDENTIALS || f.rpc.recovered[0].CaptureId != "" {
		t.Fatal("cleanup without credentials did not report the no-credential outcome")
	}
	if _, err := os.Lstat(filepath.Join(f.home, "auth.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid transient authentication remained", err)
	}
}

func TestManagedCodexRecoveryUnreadableCapturedProofStaysProtected(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	f.journal.CaptureID, f.journal.CaptureSynced = domain.NewID(), true
	f.save()
	var blocked *recoveryBlocked
	if err := f.run(newRecoveryTracker()); !errors.As(err, &blocked) || blocked.reason != domain.RecoveryCredentialProofUnavailable {
		t.Fatal("a lost capture was fabricated into recovery", err)
	}
}

func TestManagedCodexRecoveryBacksOffAndSerializesPerAccount(t *testing.T) {
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30}
	for attempt, seconds := range want {
		if got := recoveryBackoff(attempt); got != seconds*time.Second {
			t.Fatalf("attempt %d delay = %s", attempt, got)
		}
	}
	tracker := newRecoveryTracker()
	lease := &domain.SubscriptionLease{ID: domain.NewID(), Generation: domain.NewID(), Epoch: domain.NewID()}
	account := domain.NewID()
	now := time.Now()
	if delay, blocked := tracker.record(account, lease, errors.New("transient"), now); delay != time.Second || blocked != nil {
		t.Fatal("first transient failure did not back off one second")
	}
	if tracker.ready(account, lease, now.Add(500*time.Millisecond)) || !tracker.ready(account, lease, now.Add(time.Second)) {
		t.Fatal("retry timing is not bounded by the backoff")
	}
	changed := *lease
	changed.Generation = domain.NewID()
	if !tracker.ready(account, &changed, now) {
		t.Fatal("a changed original obligation was not reconsidered")
	}
	tracker.record(account, lease, nil, now)
	if !tracker.ready(account, lease, now) {
		t.Fatal("a settled account kept its retry delay")
	}
}

func TestManagedCodexRecoveryRequiresNegotiationAndEligibleLease(t *testing.T) {
	f := newRecoveryWorkerFixture(t)
	f.config.recovery = false
	var blocked *recoveryBlocked
	if err := f.run(newRecoveryTracker()); !errors.As(err, &blocked) || len(f.rpc.recovered) != 0 && f.rpc.recovered[0].Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED {
		t.Fatal("an unsupported peer attempted recovery", err)
	}
	account := f.account_()
	account.Subscription.Lease.Action = domain.SubscriptionLogin
	if recoveryEligible(account, f.lease.MachineID) {
		t.Fatal("a non-Execute lease is eligible")
	}
	account = f.account_()
	if recoveryEligible(account, domain.NewID()) {
		t.Fatal("another machine's lease is eligible")
	}
}

func TestExecutionAuthenticationScanStreamsLargeHistoryWithoutLimits(t *testing.T) {
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	home := filepath.Join(t.TempDir(), "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(home, "auth.json"), bundle); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2100; i++ {
		if err := os.WriteFile(filepath.Join(home, "rollout-"+strconv.Itoa(i)), []byte("history"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	large, err := os.Create(filepath.Join(home, "large"))
	if err != nil {
		t.Fatal(err)
	}
	if err := large.Truncate(70 << 20); err != nil {
		large.Close()
		t.Fatal(err)
	}
	large.Close()
	if err := cleanupExecutionAuthentication(home, bundle, bundle); err != nil {
		t.Fatal("ordinary history size prevented cleanup", err)
	}
	if _, err := os.Stat(filepath.Join(home, "rollout-2099")); err != nil {
		t.Fatal("history was not preserved", err)
	}
}
