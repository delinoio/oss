// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type recoveryServer struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	mu         sync.Mutex
	order      []string
	capture    []byte
	syncError  error
	readError  error
	recoverErr error
	synced     []*pb.SyncSubscriptionRecoveryCaptureRequest
	recovered  []*pb.RecoverSubscriptionRequest
	reads      int
	// authAtSync records whether auth.json still existed when a capture arrived.
	authPath   string
	authAtSync []bool
	account    *pb.Resource
	watched    chan struct{}
}

func (s *recoveryServer) SyncSubscriptionRecoveryCapture(_ context.Context, req *connect.Request[pb.SyncSubscriptionRecoveryCaptureRequest]) (*connect.Response[pb.SyncSubscriptionRecoveryCaptureResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.order = append(s.order, "sync")
	if s.authPath != "" {
		_, err := os.Lstat(s.authPath)
		s.authAtSync = append(s.authAtSync, err == nil)
	}
	if s.syncError != nil {
		return nil, s.syncError
	}
	s.synced = append(s.synced, &pb.SyncSubscriptionRecoveryCaptureRequest{Mutation: req.Msg.Mutation, LeaseId: req.Msg.LeaseId, GenerationId: req.Msg.GenerationId, InstanceId: req.Msg.InstanceId, Bundle: bytes.Clone(req.Msg.Bundle)})
	s.capture = bytes.Clone(req.Msg.Bundle)
	return connect.NewResponse(&pb.SyncSubscriptionRecoveryCaptureResponse{CaptureId: req.Msg.Mutation.RequestId}), nil
}

func (s *recoveryServer) ReadSubscriptionRecoveryCapture(_ context.Context, _ *connect.Request[pb.ReadSubscriptionRecoveryCaptureRequest]) (*connect.Response[pb.ReadSubscriptionRecoveryCaptureResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	s.order = append(s.order, "read")
	if s.readError != nil {
		return nil, s.readError
	}
	return connect.NewResponse(&pb.ReadSubscriptionRecoveryCaptureResponse{Bundle: bytes.Clone(s.capture)}), nil
}

func (s *recoveryServer) RecoverSubscription(_ context.Context, req *connect.Request[pb.RecoverSubscriptionRequest]) (*connect.Response[pb.RecoverSubscriptionResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.order = append(s.order, "recover")
	s.recovered = append(s.recovered, &pb.RecoverSubscriptionRequest{Mutation: req.Msg.Mutation, LeaseId: req.Msg.LeaseId, InstanceId: req.Msg.InstanceId, GenerationId: req.Msg.GenerationId, CaptureId: req.Msg.CaptureId, Outcome: req.Msg.Outcome, BlockedReason: req.Msg.BlockedReason})
	if s.recoverErr != nil {
		return nil, s.recoverErr
	}
	return connect.NewResponse(&pb.RecoverSubscriptionResponse{}), nil
}

func (s *recoveryServer) WatchSubscription(ctx context.Context, _ *connect.Request[pb.WatchSubscriptionRequest], stream *connect.ServerStream[pb.WatchSubscriptionResponse]) error {
	if s.watched != nil {
		select {
		case s.watched <- struct{}{}:
		default:
		}
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.account != nil {
			if err := stream.Send(&pb.WatchSubscriptionResponse{Account: s.account}); err != nil {
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

type recoveryFixture struct {
	t          *testing.T
	root       string
	home       string
	server     *recoveryServer
	client     delidevv1connect.SubscriptionServiceClient
	credential Credential
	instance   domain.ID
	account    domain.ID
	lease      domain.SubscriptionLease
	state      *domain.SubscriptionState
	journal    managedSubscriptionJournal
	latest     []byte
	logs       *bytes.Buffer
	config     Config
}

func newRecoveryFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &recoveryFixture{t: t, root: root, account: domain.NewID(), instance: domain.NewID(), logs: &bytes.Buffer{}}
	f.credential = Credential{MachineID: domain.NewID(), Token: "recovery-fixture-token"}
	f.home = filepath.Join(root, "runtimes", "account-history", "codex")
	if err := security.PrivateDir(filepath.Join(root, "runtimes")); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(root, "runtimes", "account-history")); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(f.home); err != nil {
		t.Fatal(err)
	}
	f.latest = workerSubscriptionBundle("second")
	f.server = &recoveryServer{capture: bytes.Clone(f.latest)}
	_, handler := delidevv1connect.NewSubscriptionServiceHandler(f.server)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	f.client = delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, server.URL)
	f.credential.Endpoint = server.URL
	f.lease = domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 7, Action: domain.SubscriptionExecute, MachineID: f.credential.MachineID, InstanceID: domain.NewID(), DeviceID: domain.NewID(), Epoch: domain.NewID(), Generation: domain.NewID(), StartedAt: time.Now().UTC()}
	f.state = &domain.SubscriptionState{Generation: f.lease.Generation, RecoveryRequired: true, Lease: &f.lease, IdentityCommitment: string(bytes.Repeat([]byte("a"), 64))}
	f.journal = managedSubscriptionJournal{Version: 1, Lease: f.lease.ID, Account: f.account, Operation: f.lease.OperationID, Instance: f.lease.InstanceID, Finish: domain.NewID(), Generation: f.lease.Generation, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE, State: managedReported, LeaseRevision: f.lease.Revision, NativeHome: f.home}
	if err := security.PrivateDir(filepath.Join(root, "managed-auth")); err != nil {
		t.Fatal(err)
	}
	f.config = Config{Root: root, Logger: slog.New(slog.NewJSONHandler(f.logs, nil))}
	return f
}

func (f *recoveryFixture) journalPath() string {
	return filepath.Join(f.root, "managed-auth", string(f.lease.ID)+".json")
}

func (f *recoveryFixture) writeJournal() {
	f.t.Helper()
	if err := writeJSON(f.journalPath(), f.journal); err != nil {
		f.t.Fatal(err)
	}
}

// processOwner creates the retained, empty process owner index of the native
// owner, as left behind by a joined native tree.
func (f *recoveryFixture) processOwner(owner domain.ID) {
	f.t.Helper()
	if err := security.PrivateDir(filepath.Join(f.root, "processes")); err != nil {
		f.t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(f.root, "processes", string(owner))); err != nil {
		f.t.Fatal(err)
	}
}

func (f *recoveryFixture) withCapture() {
	f.state.RecoveryCapture = &domain.SubscriptionRecoveryCapture{ID: domain.NewID(), LeaseID: f.lease.ID, Generation: f.lease.Generation, IdentityCommitment: string(bytes.Repeat([]byte("a"), 64)), Usable: true, SyncedAt: time.Now().UTC()}
}

func (f *recoveryFixture) reconcile(instance domain.ID) codexRecoveryResult {
	f.t.Helper()
	return reconcileCodexExecution(context.Background(), f.config, f.client, f.credential, instance, f.account, domain.Account{SubscriptionService: domain.SubscriptionChatGPT, Subscription: f.state})
}

func TestCodexRecoveryRestoresFromProtectedCaptureWithDistinctRequestIdentity(t *testing.T) {
	f := newRecoveryFixture(t)
	f.processOwner(f.lease.OperationID)
	f.writeJournal()
	f.withCapture()
	// Large ordinary history must not block the scan and must survive it.
	for i := 0; i < 2049; i++ {
		if err := os.WriteFile(filepath.Join(f.home, "rollout-"+strconv.Itoa(i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	result := f.reconcile(f.instance)
	if !result.settled || result.blocked != "" || result.err != nil {
		t.Fatal("recovery did not settle", result)
	}
	if len(f.server.recovered) != 1 {
		t.Fatal("recovery did not report exactly one settlement")
	}
	request := f.server.recovered[0]
	if request.Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_SETTLED || request.CaptureId != string(f.state.RecoveryCapture.ID) || request.InstanceId != string(f.instance) || request.GenerationId != string(f.lease.Generation) || request.Mutation.ExpectedRevision != f.lease.Revision {
		t.Fatal("recovery request lost its original binding", request)
	}
	if domain.ID(request.Mutation.RequestId) == f.journal.Finish || request.Mutation.RequestId == string(f.lease.ID) || request.Mutation.RequestId == string(f.lease.OperationID) {
		t.Fatal("recovery reused the failed Finish, Take or operation identity")
	}
	for _, call := range f.server.order {
		if call == "sync" {
			t.Fatal("recovery invented a new capture when one was retained")
		}
	}
	entries, err := os.ReadDir(f.home)
	if err != nil || len(entries) != 2049 {
		t.Fatal("recovery deleted retained history", err, len(entries))
	}
	// The original Take/Finish identities stay in the journal verbatim.
	raw, err := security.ReadPrivate(f.journalPath(), 16<<10)
	var journal managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &journal) != nil || journal.Finish != f.journal.Finish || journal.Lease != f.lease.ID || !journal.Recovered || journal.RecoveryRequest != domain.ID(request.Mutation.RequestId) {
		t.Fatal("recovery rewrote the original evidence", err)
	}
	// A restart reuses the durable recovery identity.
	if again := f.reconcile(f.instance); !again.settled {
		t.Fatal("repeated recovery delivery did not settle", again)
	}
	if f.server.recovered[1].Mutation.RequestId != request.Mutation.RequestId {
		t.Fatal("a restarted Worker minted a second recovery identity")
	}
}

func TestCodexRecoveryReplacementInstanceUsesCurrentInstanceAndOriginalEvidence(t *testing.T) {
	f := newRecoveryFixture(t)
	f.processOwner(f.lease.OperationID)
	f.writeJournal()
	f.withCapture()
	replacement := domain.NewID()
	if result := f.reconcile(replacement); !result.settled {
		t.Fatal("the replacement instance of the original Worker could not recover", result)
	}
	if got := f.server.recovered[0].InstanceId; got != string(replacement) || got == string(f.lease.InstanceID) {
		t.Fatal("recovery did not present the current instance", got)
	}
}

func TestCodexRecoveryNeverFabricatesProofForLegacyOrForeignEvidence(t *testing.T) {
	cases := map[string]struct {
		prepare func(f *recoveryFixture)
		reason  domain.SubscriptionRecoveryReason
	}{
		"legacy-journal-without-home": {func(f *recoveryFixture) {
			f.journal.NativeHome = ""
			f.writeJournal()
			f.processOwner(f.lease.OperationID)
		}, domain.RecoveryCredentialProofUnavailable},
		"auth-deleted-without-capture": {func(f *recoveryFixture) {
			f.writeJournal()
			f.processOwner(f.lease.OperationID)
			f.state.RecoveryCapture = nil
		}, domain.RecoveryCredentialProofUnavailable},
		"missing-journal": {func(f *recoveryFixture) { f.processOwner(f.lease.OperationID) }, domain.RecoveryOwnershipUnproven},
		"foreign-account": {func(f *recoveryFixture) {
			f.journal.Account = domain.NewID()
			f.writeJournal()
			f.processOwner(f.lease.OperationID)
		}, domain.RecoveryOwnershipUnproven},
		"changed-instance-evidence": {func(f *recoveryFixture) {
			f.journal.Instance = domain.NewID()
			f.writeJournal()
			f.processOwner(f.lease.OperationID)
		}, domain.RecoveryOwnershipUnproven},
		"missing-process-owner-index": {func(f *recoveryFixture) { f.writeJournal() }, domain.RecoveryOwnershipUnproven},
		"another-device": {func(f *recoveryFixture) {
			f.lease.MachineID = domain.NewID()
			f.writeJournal()
			f.processOwner(f.lease.OperationID)
		}, domain.RecoveryOwnershipUnproven},
		"home-outside-root": {func(f *recoveryFixture) {
			f.journal.NativeHome = t.TempDir()
			f.writeJournal()
			f.processOwner(f.lease.OperationID)
		}, domain.RecoveryOwnershipUnproven},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRecoveryFixture(t)
			if name == "auth-deleted-without-capture" {
				f.state.RecoveryCapture = nil
			} else if name != "legacy-journal-without-home" {
				f.withCapture()
			}
			tc.prepare(f)
			result := f.reconcile(f.instance)
			if result.settled || result.err != nil || result.blocked != tc.reason {
				t.Fatalf("unexpected outcome %+v", result)
			}
			if len(f.server.recovered) != 0 {
				t.Fatal("insufficient proof reached the settlement RPC")
			}
		})
	}
}

func TestCodexRecoveryCapturesSurvivingTransientFileOnceBeforeDeletingIt(t *testing.T) {
	f := newRecoveryFixture(t)
	f.processOwner(f.lease.OperationID)
	f.writeJournal()
	if err := security.WriteAtomic(filepath.Join(f.home, "auth.json"), f.latest); err != nil {
		t.Fatal(err)
	}
	f.server.authPath = filepath.Join(f.home, "auth.json")
	if result := f.reconcile(f.instance); !result.settled {
		t.Fatal("a surviving transient file did not recover", result)
	}
	if len(f.server.authAtSync) != 1 || !f.server.authAtSync[0] {
		t.Fatal("the surviving file was deleted before its capture was confirmed")
	}
	if _, err := os.Lstat(filepath.Join(f.home, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("recovery left the transient credential file", err)
	}
	if len(f.server.synced) != 1 || f.server.recovered[0].CaptureId != f.server.synced[0].Mutation.RequestId || !bytes.Equal(f.server.synced[0].Bundle, f.latest) {
		t.Fatal("settlement was not bound to the captured file")
	}
	// A mismatching file next to a retained capture is uncertain evidence.
	g := newRecoveryFixture(t)
	g.processOwner(g.lease.OperationID)
	g.writeJournal()
	g.withCapture()
	if err := security.WriteAtomic(filepath.Join(g.home, "auth.json"), workerSubscriptionBundle("third")); err != nil {
		t.Fatal(err)
	}
	if result := g.reconcile(g.instance); result.blocked != domain.RecoveryCleanupUnconfirmed || len(g.server.recovered) != 0 {
		t.Fatal("a file that is not the captured bundle was trusted", result)
	}
	if _, err := os.Lstat(filepath.Join(g.home, "auth.json")); err != nil {
		t.Fatal("uncertain evidence was deleted", err)
	}
}

func TestCodexRecoveryKeepsProtectionWhenRetainedHistoryStillHoldsCredentials(t *testing.T) {
	parsed, _, err := subscription.Parse(workerSubscriptionBundle("second"))
	if err != nil {
		t.Fatal(err)
	}
	for name, setup := range map[string]func(t *testing.T, home string){
		"raw-token-across-chunks": func(t *testing.T, home string) {
			content := append(bytes.Repeat([]byte("x"), (64<<10)-3), []byte(parsed.Tokens.Refresh)...)
			if err := os.WriteFile(filepath.Join(home, "rollout.jsonl"), content, 0600); err != nil {
				t.Fatal(err)
			}
		},
		"pending-entry": func(t *testing.T, home string) {
			if err := os.WriteFile(filepath.Join(home, ".pending-native"), nil, 0600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRecoveryFixture(t)
			f.processOwner(f.lease.OperationID)
			f.writeJournal()
			f.withCapture()
			setup(t, f.home)
			result := f.reconcile(f.instance)
			if result.settled || result.blocked != domain.RecoveryCleanupUnconfirmed || len(f.server.recovered) != 0 {
				t.Fatal("remnant evidence did not keep the fence", result)
			}
			entries, _ := os.ReadDir(f.home)
			if len(entries) != 1 {
				t.Fatal("recovery deleted history to pass the scan")
			}
		})
	}
}

func TestCodexRecoveryClassifiesServerRefusalAndTransportFailures(t *testing.T) {
	f := newRecoveryFixture(t)
	f.processOwner(f.lease.OperationID)
	f.writeJournal()
	f.withCapture()
	f.server.recoverErr = rpc.Error(domain.Fail(domain.RecoveryRequired, "The managed account cannot grant authentication authority.", "Reconcile."), "")
	if result := f.reconcile(f.instance); result.blocked != domain.RecoveryOwnershipUnproven || result.err != nil {
		t.Fatal("a definite refusal was retried", result)
	}
	f.server.recoverErr = connect.NewError(connect.CodeUnavailable, nil)
	if result := f.reconcile(f.instance); result.err == nil || result.blocked != "" || result.settled {
		t.Fatal("a transport failure did not retry", result)
	}
	f.server.recoverErr = nil
	f.server.readError = connect.NewError(connect.CodeUnavailable, nil)
	if result := f.reconcile(f.instance); result.err == nil || result.settled {
		t.Fatal("an unavailable vault did not retry", result)
	}
}

func TestCodexRecoveryRefusesWhileThisProcessStillOwnsTheAccount(t *testing.T) {
	f := newRecoveryFixture(t)
	f.processOwner(f.lease.OperationID)
	f.writeJournal()
	f.withCapture()
	f.config.codexOwners = newCodexOwnerRegistry()
	gate := f.config.codexOwners.gate(f.account)
	gate.mu.Lock()
	gate.owner = &codexSharedOwner{}
	gate.mu.Unlock()
	if result := f.reconcile(f.instance); result.settled || result.err == nil || len(f.server.recovered) != 0 {
		t.Fatal("recovery ran against concurrent active original ownership", result)
	}
	gate.mu.Lock()
	gate.owner = nil
	gate.uncertain = true
	gate.mu.Unlock()
	if result := f.reconcile(f.instance); !result.settled {
		t.Fatal("an idle fenced account could not recover", result)
	}
	gate.mu.Lock()
	uncertain := gate.uncertain
	gate.mu.Unlock()
	if uncertain {
		t.Fatal("a settled recovery left the local fence closed")
	}
}

func TestCodexRecoveryScheduleBacksOffAndNeverSpinsOnBlockedProof(t *testing.T) {
	now := time.Unix(1000, 0)
	schedule := newCodexRecoverySchedule(func() time.Time { return now })
	account := domain.NewID()
	if !schedule.ready(account, "a") {
		t.Fatal("an unseen account was not ready")
	}
	var delays []time.Duration
	for i := 0; i < 8; i++ {
		delay := schedule.fail(account, "a")
		delays = append(delays, delay)
		if schedule.ready(account, "a") {
			t.Fatal("a failed account was retried before its delay")
		}
		now = now.Add(delay)
		if !schedule.ready(account, "a") {
			t.Fatal("a failed account was not retried after its delay")
		}
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second}
	for i := range want {
		if delays[i] != want[i] {
			t.Fatal("retry delays are not 1, 2, 4, 8, 16 then 30 seconds", delays)
		}
	}
	schedule.block(account, "a")
	now = now.Add(time.Hour)
	if schedule.ready(account, "a") {
		t.Fatal("blocked proof was retried without a changed obligation")
	}
	if !schedule.ready(account, "b") {
		t.Fatal("a changed original obligation was not reconsidered")
	}
	schedule.settled(account, "a")
	if schedule.ready(account, "a") {
		t.Fatal("a settled obligation was repeated while the server state was unobserved")
	}
	now = now.Add(31 * time.Second)
	if !schedule.ready(account, "a") || !schedule.ready(account, "c") {
		t.Fatal("a settled account kept its pacing")
	}
}

func recoveryLaneAccount(t *testing.T, f *recoveryFixture) {
	t.Helper()
	document, err := json.Marshal(domain.Account{SubscriptionService: domain.SubscriptionChatGPT, Subscription: f.state})
	if err != nil {
		t.Fatal(err)
	}
	f.server.account = &pb.Resource{Id: string(f.account), Revision: 3, DocumentJson: document}
}

func TestCodexRecoveryLaneReconcilesAutomaticallyWithoutKillingTheLane(t *testing.T) {
	f := newRecoveryFixture(t)
	f.processOwner(f.lease.OperationID)
	f.writeJournal()
	f.withCapture()
	recoveryLaneAccount(t, f)
	f.config.codexRecovery = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- watchSubscriptions(ctx, f.config, f.credential, f.instance) }()
	deadline := time.After(5 * time.Second)
	for {
		f.server.mu.Lock()
		settled := len(f.server.recovered)
		f.server.mu.Unlock()
		if settled > 0 {
			break
		}
		select {
		case err := <-result:
			t.Fatal("recovery closed the subscription lane", err)
		case <-deadline:
			t.Fatal("the original Worker did not reconcile its fenced lease")
		case <-time.After(10 * time.Millisecond):
		}
	}
	// The fenced account is offered every tick; the settled attempt is not repeated
	// per tick as a hot loop would be.
	time.Sleep(200 * time.Millisecond)
	f.server.mu.Lock()
	settledCalls := len(f.server.recovered)
	f.server.mu.Unlock()
	cancel()
	select {
	case <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("the lane did not join on shutdown")
	}
	if settledCalls != 1 {
		t.Fatal("automatic recovery spun on an offered account", settledCalls)
	}
}

func TestCodexRecoveryLaneStaysProtectedWithoutNegotiationAndNeverTakes(t *testing.T) {
	f := newRecoveryFixture(t)
	f.processOwner(f.lease.OperationID)
	f.writeJournal()
	f.withCapture()
	recoveryLaneAccount(t, f)
	f.config.codexRecovery = false
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := watchSubscriptions(ctx, f.config, f.credential, f.instance)
	if domain.SafeError(err).Code == "" || len(f.server.recovered) != 0 || len(f.server.order) != 0 {
		t.Fatal("an unnegotiated Worker reconciled a ChatGPT lease", err)
	}
}

func TestCodexCaptureSyncPrecedesAuthDeletionAndFailureKeepsTheFile(t *testing.T) {
	f := newRecoveryFixture(t)
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	auth := filepath.Join(f.home, "auth.json")
	if err := security.WriteAtomic(auth, f.latest); err != nil {
		t.Fatal(err)
	}
	f.server.authPath = auth
	lease := &managedSubscriptionLease{logger: f.config.Logger, client: f.client, credential: f.credential, instance: f.instance, account: f.account, response: &pb.TakeSubscriptionResponse{LeaseId: string(f.lease.ID), GenerationId: string(f.lease.Generation), LeaseRevision: f.lease.Revision}, journalPath: f.journalPath(), journal: f.journal, recoveryCapture: true}
	if err := cleanupCapturedExecutionAuthentication(f.config, lease, f.home, f.latest, bundle); err != nil {
		t.Fatal(err)
	}
	if len(f.server.authAtSync) != 1 || !f.server.authAtSync[0] {
		t.Fatal("the transient file was deleted before the capture was synchronized")
	}
	if _, err := os.Lstat(auth); !os.IsNotExist(err) {
		t.Fatal("the transient file survived a confirmed capture", err)
	}
	if !lease.journal.CaptureConfirmed || lease.journal.CaptureRequest == "" {
		t.Fatal("the confirmed capture was not journaled")
	}
	if bytes.Contains(f.logs.Bytes(), f.latest) {
		t.Fatal("the capture log contains secret bytes")
	}
	parsed, _, _ := subscription.Parse(f.latest)
	for _, secret := range []string{parsed.Tokens.Access, parsed.Tokens.Refresh} {
		if bytes.Contains(f.logs.Bytes(), []byte(secret)) {
			t.Fatal("the capture log contains a credential")
		}
	}

	// A capture that cannot be confirmed keeps auth.json and fails cleanup.
	g := newRecoveryFixture(t)
	if err := security.WriteAtomic(filepath.Join(g.home, "auth.json"), g.latest); err != nil {
		t.Fatal(err)
	}
	g.server.syncError = connect.NewError(connect.CodeUnavailable, nil)
	failing := &managedSubscriptionLease{logger: g.config.Logger, client: g.client, credential: g.credential, instance: g.instance, account: g.account, response: &pb.TakeSubscriptionResponse{LeaseId: string(g.lease.ID), GenerationId: string(g.lease.Generation), LeaseRevision: g.lease.Revision}, journalPath: g.journalPath(), journal: g.journal, recoveryCapture: true}
	err := cleanupCapturedExecutionAuthentication(g.config, failing, g.home, g.latest, g.latest)
	if err == nil {
		t.Fatal("an unconfirmed capture reported confirmed cleanup")
	}
	if _, statErr := os.Lstat(filepath.Join(g.home, "auth.json")); statErr != nil {
		t.Fatal("an unconfirmed capture deleted the only latest credential", statErr)
	}

	// An older server keeps the historical cleanup; a lease without the lane never calls it.
	h := newRecoveryFixture(t)
	if err := security.WriteAtomic(filepath.Join(h.home, "auth.json"), h.latest); err != nil {
		t.Fatal(err)
	}
	h.server.syncError = connect.NewError(connect.CodeUnimplemented, nil)
	legacy := &managedSubscriptionLease{logger: h.config.Logger, client: h.client, credential: h.credential, instance: h.instance, account: h.account, response: &pb.TakeSubscriptionResponse{LeaseId: string(h.lease.ID), GenerationId: string(h.lease.Generation), LeaseRevision: h.lease.Revision}, journalPath: h.journalPath(), journal: h.journal, recoveryCapture: true}
	if err := cleanupCapturedExecutionAuthentication(h.config, legacy, h.home, h.latest, h.latest); err != nil {
		t.Fatal("an unsupported server blocked the historical cleanup", err)
	}
	k := newRecoveryFixture(t)
	if err := security.WriteAtomic(filepath.Join(k.home, "auth.json"), k.latest); err != nil {
		t.Fatal(err)
	}
	plain := &managedSubscriptionLease{logger: k.config.Logger, client: k.client, credential: k.credential, instance: k.instance, account: k.account, response: &pb.TakeSubscriptionResponse{LeaseId: string(k.lease.ID)}, journalPath: k.journalPath(), journal: k.journal}
	if err := cleanupCapturedExecutionAuthentication(k.config, plain, k.home, k.latest, k.latest); err != nil || len(k.server.order) != 0 {
		t.Fatal("a lease without the negotiated lane synchronized a capture", err)
	}
}

func TestCodexExecutionAuthenticationRecordsHomeBeforeWritingPlaintext(t *testing.T) {
	f := newRecoveryFixture(t)
	bundle := workerSubscriptionBundle("first")
	lease := &managedSubscriptionLease{logger: f.config.Logger, response: &pb.TakeSubscriptionResponse{LeaseId: string(f.lease.ID), Bundle: bundle}, journalPath: f.journalPath(), journal: f.journal, recoveryCapture: true}
	lease.journal.NativeHome = ""
	if err := lease.writeExecutionAuthentication(f.home); err != nil {
		t.Fatal(err)
	}
	raw, err := security.ReadPrivate(f.journalPath(), 16<<10)
	var journal managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &journal) != nil || journal.NativeHome != f.home {
		t.Fatal("the retained home was not journaled", err)
	}
	for _, secret := range [][]byte{bundle} {
		if bytes.Contains(raw, secret) {
			t.Fatal("the journal contains secret bytes")
		}
	}
	if written, err := security.ReadPrivate(filepath.Join(f.home, "auth.json"), subscription.MaxBundle); err != nil || !bytes.Equal(written, bundle) {
		t.Fatal("the transient authentication was not written", err)
	}
}

func TestCodexRecoveryLaneReportsMissingProofOnceWithoutHotLoop(t *testing.T) {
	f := newRecoveryFixture(t)
	f.journal.NativeHome = "" // a legacy journal that never recorded its home
	f.processOwner(f.lease.OperationID)
	f.writeJournal()
	f.withCapture()
	recoveryLaneAccount(t, f)
	f.config.codexRecovery = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- watchSubscriptions(ctx, f.config, f.credential, f.instance) }()
	time.Sleep(400 * time.Millisecond)
	cancel()
	select {
	case <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("the lane did not join on shutdown")
	}
	f.server.mu.Lock()
	defer f.server.mu.Unlock()
	if len(f.server.recovered) != 1 || f.server.recovered[0].Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED || f.server.recovered[0].BlockedReason != pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CREDENTIAL_PROOF_UNAVAILABLE {
		t.Fatal("missing proof was not reported exactly once with its closed reason", len(f.server.recovered))
	}
	if f.server.reads != 0 || len(f.server.synced) != 0 {
		t.Fatal("missing proof still touched the capture")
	}
}

func TestCodexPreNativeCleanupCapturesUnusedOriginalBeforeDeletingIt(t *testing.T) {
	f := newRecoveryFixture(t)
	auth := filepath.Join(f.home, "auth.json")
	if err := security.WriteAtomic(auth, f.latest); err != nil {
		t.Fatal(err)
	}
	f.server.authPath = auth
	lease := &managedSubscriptionLease{logger: f.config.Logger, client: f.client, credential: f.credential, instance: f.instance, account: f.account, response: &pb.TakeSubscriptionResponse{LeaseId: string(f.lease.ID), GenerationId: string(f.lease.Generation), LeaseRevision: f.lease.Revision}, journalPath: f.journalPath(), journal: f.journal, recoveryCapture: true}
	confirmed, err := cleanupCapturedPreNativeAuthentication(f.config, lease, f.home, f.latest, false)
	if err != nil || !confirmed {
		t.Fatal("unused original cleanup was not confirmed", err)
	}
	if len(f.server.authAtSync) != 1 || !f.server.authAtSync[0] {
		t.Fatal("the unused original was deleted before its capture")
	}
	f.server.syncError = connect.NewError(connect.CodeUnavailable, nil)
	if err := security.WriteAtomic(auth, f.latest); err != nil {
		t.Fatal(err)
	}
	if confirmed, err := cleanupCapturedPreNativeAuthentication(f.config, lease, f.home, f.latest, false); confirmed || err == nil {
		t.Fatal("an unconfirmed capture reported confirmed pre-native cleanup")
	}
	if _, err := os.Lstat(auth); err != nil {
		t.Fatal("an unconfirmed capture deleted the original file", err)
	}
}
