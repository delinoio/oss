// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

// grokExecutionServer is the protected lease side of a Grok account: it
// delivers the sealed generation on Take and records every Finish together with
// what the private home held at the instant the Finish was reported.
type grokExecutionServer struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	mu                    sync.Mutex
	t                     *testing.T
	config                Config
	credential            Credential
	account, generation   domain.ID
	envelope              []byte
	home                  string
	busy                  atomic.Int32
	takes                 []*pb.TakeSubscriptionRequest
	finishes              []*pb.FinishSubscriptionRequest
	homeEntriesAtFinish   [][]string
	logs                  *bytes.Buffer
	failFinishWithConnect bool
	// installationJSON is the verified observation an execute Take returns; nil
	// returns none.
	installationJSON []byte
	// onTake inspects the durable claim a Take finds; takenClaims counts the calls.
	onTake      func(managedSubscriptionJournal)
	takenClaims atomic.Int32
}

func newGrokExecutionServer(t *testing.T) *grokExecutionServer {
	t.Helper()
	root := filepath.Join(t.TempDir(), "worker")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	f := &grokExecutionServer{t: t, logs: logs, account: domain.NewID(), generation: domain.NewID(), credential: Credential{ServerID: domain.NewID(), MachineID: domain.NewID()}}
	f.config = Config{Root: root, Logger: slog.New(slog.NewJSONHandler(logs, nil)), grokExecution: true}
	f.installationJSON = f.installationDocument(domain.Installation{Harness: domain.GrokBuild, Version: domain.GrokSubscriptionBaselineVersion, ResolvedPath: filepath.Join(root, "controlled", "grok")})
	f.envelope = mustSeal(t, f.credential.ServerID, f.account, f.generation, mustParse(t, grokWorkerFile(t, "synthetic-principal-1", "one", "2026-10-14T06:00:00Z")))
	f.home = filepath.Join(root, "profile", "grok")
	if err := security.PrivateDir(filepath.Dir(f.home)); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(f.home); err != nil {
		t.Fatal(err)
	}
	_, handler := delidevv1connect.NewSubscriptionServiceHandler(f)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	f.credential.Endpoint = server.URL
	f.config.execution = &PublicationConfig{Credential: f.credential, Instance: domain.NewID(), Assignment: &pb.Resource{Revision: 2}}
	return f
}

func (f *grokExecutionServer) installationDocument(installation domain.Installation) []byte {
	f.t.Helper()
	raw, err := json.Marshal(installation)
	if err != nil {
		f.t.Fatal(err)
	}
	return raw
}

func (f *grokExecutionServer) TakeSubscription(_ context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (*connect.Response[pb.TakeSubscriptionResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.takes = append(f.takes, proto.Clone(req.Msg).(*pb.TakeSubscriptionRequest))
	if req.Msg.Action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE {
		f.t.Error("a Grok execution requested a lifecycle operation")
	}
	if f.busy.Add(-1) >= 0 {
		return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "The selected account is exclusively leased.", "Wait."), "")
	}
	lease := domain.ID(req.Msg.Mutation.RequestId)
	raw, err := security.ReadPrivate(filepath.Join(f.config.Root, "managed-auth", string(lease)+".json"), 16<<10)
	var claim managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &claim) != nil || claim.State != managedClaimed {
		f.t.Error("protected delivery preceded the original durable claim")
		return nil, connect.NewError(connect.CodeFailedPrecondition, nil)
	}
	if f.onTake != nil {
		f.takenClaims.Add(1)
		f.onTake(claim)
	}
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: string(lease), LeaseRevision: 3, GenerationId: string(f.generation), Bundle: bytes.Clone(f.envelope), InstallationJson: bytes.Clone(f.installationJSON)}), nil
}

func (f *grokExecutionServer) FinishSubscription(_ context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishes = append(f.finishes, proto.Clone(req.Msg).(*pb.FinishSubscriptionRequest))
	var names []string
	entries, _ := os.ReadDir(f.home)
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	f.homeEntriesAtFinish = append(f.homeEntriesAtFinish, names)
	if f.failFinishWithConnect {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	document, _ := json.Marshal(domain.Account{Subscription: &domain.SubscriptionState{}})
	return connect.NewResponse(&pb.FinishSubscriptionResponse{Account: &pb.Resource{Id: string(f.account), Revision: 4, DocumentJson: document}}), nil
}

func (f *grokExecutionServer) begin(t *testing.T) (*grokSubscriptionRun, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return beginGrokSubscriptionExecution(ctx, f.config, domain.NewID(), domain.ExecutionJobInput{AccountID: f.account}, f.home)
}

func (f *grokExecutionServer) authPath() string {
	return filepath.Join(f.home, subscription.GrokAuthFileName)
}

func (f *grokExecutionServer) onlyFinish(t *testing.T) *pb.FinishSubscriptionRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.finishes) != 1 {
		t.Fatalf("the lease reported %d Finish requests", len(f.finishes))
	}
	if len(f.homeEntriesAtFinish[0]) != 0 && f.finishes[0].CleanupConfirmed {
		t.Fatalf("Finish confirmed cleanup while the private home still held %v", f.homeEntriesAtFinish[0])
	}
	return f.finishes[0]
}

// nativeRotate is what the native process does when it refreshes itself.
func (f *grokExecutionServer) nativeRotate(t *testing.T) []byte {
	t.Helper()
	raw := grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z")
	if err := os.WriteFile(f.authPath(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, subscription.GrokLockFileName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGrokExecutionMaterializesOnlyAfterTakeAndRetiresBeforeFinish(t *testing.T) {
	f := newGrokExecutionServer(t)
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(f.authPath()); !os.IsNotExist(err) {
		t.Fatal("the credential was written before the harness asked for it")
	}
	if _, err := run.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(f.authPath())
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("auth.json was not materialized")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("auth.json is not mode 0600")
	}
	if len(run.lease.response.Bundle) != 0 {
		t.Fatal("the delivered envelope stayed in memory")
	}
	rotated := f.nativeRotate(t)
	if err := run.finish(true); err != nil {
		t.Fatal(err)
	}
	request := f.onlyFinish(t)
	if !request.Succeeded || !request.CleanupConfirmed || request.RefreshConfirmed {
		t.Fatalf("a joined execution did not report a clean success: %+v", request)
	}
	next, err := subscription.OpenGrokBundle(request.Bundle, f.credential.ServerID, f.account, domain.ID(request.Mutation.RequestId))
	if err != nil || !next.Equal(mustParse(t, rotated)) {
		t.Fatal("the native rotation was not returned bound to the Finish generation")
	}
	for _, name := range []string{subscription.GrokAuthFileName, subscription.GrokLockFileName} {
		if _, err := os.Lstat(filepath.Join(f.home, name)); !os.IsNotExist(err) {
			t.Fatalf("%s survived the lease", name)
		}
	}
	var journal managedSubscriptionJournal
	raw, err := security.ReadPrivate(run.lease.journalPath, 16<<10)
	if err != nil || domain.Decode(raw, &journal) != nil || journal.State != managedReported || !journal.ExecutionStarted || journal.GrokHome != f.home || journal.Action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE {
		t.Fatal("the journal did not record the execute lease, home and report")
	}
}

func TestGrokExecutionReturnsAnUnchangedCredentialWithoutRefreshConfirmation(t *testing.T) {
	f := newGrokExecutionServer(t)
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := run.finish(true); err != nil {
		t.Fatal(err)
	}
	request := f.onlyFinish(t)
	next, err := subscription.OpenGrokBundle(request.Bundle, f.credential.ServerID, f.account, domain.ID(request.Mutation.RequestId))
	if err != nil || !request.Succeeded || !request.CleanupConfirmed || !next.Equal(mustParse(t, grokWorkerFile(t, "synthetic-principal-1", "one", "2026-10-14T06:00:00Z"))) {
		t.Fatal("an unrotated credential was not returned unchanged")
	}
}

// Crash, timeout and Stop all end in the same Finish: the credential file is
// retired whatever state the native process left it in.
func TestGrokExecutionCleansUpOnEveryExitPath(t *testing.T) {
	for name, test := range map[string]struct {
		setup    func(*testing.T, *grokExecutionServer)
		joined   bool
		success  bool
		cleanup  bool
		hasBound bool
	}{
		"crash leaving a truncated credential": {setup: func(t *testing.T, f *grokExecutionServer) {
			if err := os.WriteFile(f.authPath(), []byte(`{"truncated`), 0o600); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(f.home, subscription.GrokLockFileName), nil, 0o600)
		}, joined: true, success: false, cleanup: true},
		"crash leaving a foreign identity": {setup: func(t *testing.T, f *grokExecutionServer) {
			if err := os.WriteFile(f.authPath(), grokWorkerFile(t, "synthetic-principal-2", "two", "2026-10-14T12:00:00Z"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, joined: true, success: false, cleanup: true},
		"stop with an untouched credential": {setup: func(*testing.T, *grokExecutionServer) {}, joined: true, success: true, cleanup: true, hasBound: true},
		"timeout with a native process that could not be joined": {setup: func(t *testing.T, f *grokExecutionServer) {
			f.nativeRotate(t)
		}, joined: false, success: false, cleanup: false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newGrokExecutionServer(t)
			run, err := f.begin(t)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := run.prepare(context.Background()); err != nil {
				t.Fatal(err)
			}
			test.setup(t, f)
			if err := run.finish(test.joined); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			request := f.finishes[0]
			entries := f.homeEntriesAtFinish[0]
			f.mu.Unlock()
			if request.Succeeded != test.success || request.CleanupConfirmed != test.cleanup || (len(request.Bundle) != 0) != test.hasBound && test.success {
				t.Fatalf("unexpected report: succeeded=%v cleanup=%v bundle=%d", request.Succeeded, request.CleanupConfirmed, len(request.Bundle))
			}
			if !test.success && len(request.Bundle) != 0 {
				t.Fatal("an unusable run returned a credential")
			}
			if len(entries) != 0 {
				t.Fatalf("the home still held %v when the lease was released", entries)
			}
		})
	}
}

func TestGrokExecutionThatNeverMaterializedConfirmsNothingWasWritten(t *testing.T) {
	f := newGrokExecutionServer(t)
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := run.finish(true); err != nil {
		t.Fatal(err)
	}
	request := f.onlyFinish(t)
	if request.Succeeded || !request.CleanupConfirmed || len(request.Bundle) != 0 {
		t.Fatalf("an execution that failed before the native start reported %+v", request)
	}
	if entries, _ := os.ReadDir(f.home); len(entries) != 0 {
		t.Fatal("a credential was written without the harness asking for it")
	}
}

func TestGrokExecutionRefusesAHomeThatIsNotPrivateWithoutWritingAnything(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits differ")
	}
	f := newGrokExecutionServer(t)
	if err := os.Chmod(f.home, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.begin(t); err == nil {
		t.Fatal("a group-readable home was bound")
	}
	if entries, _ := os.ReadDir(f.home); len(entries) != 0 {
		t.Fatal("a credential reached an unsafe home")
	}
	// The home is bound before the Take, so no lease was requested and no
	// credential crossed the Worker boundary for a precondition already known to
	// be invalid.
	if len(f.takes) != 0 || len(f.finishes) != 0 {
		t.Fatalf("an unusable home still reached the server (%d takes, %d finishes)", len(f.takes), len(f.finishes))
	}
}

// The home identity is journaled with the durable claim before the Take, so a
// crash between the Take and any later write still leaves the recovery evidence.
func TestGrokExecutionJournalsTheBoundHomeBeforeTheTake(t *testing.T) {
	f := newGrokExecutionServer(t)
	f.onTake = func(journal managedSubscriptionJournal) {
		if journal.GrokHome != f.home || journal.GrokHomeIdentity == "" {
			t.Errorf("the claim preceding the Take lacks the bound home: %q %q", journal.GrokHome, journal.GrokHomeIdentity)
		}
	}
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if f.takenClaims.Load() != 1 {
		t.Fatal("the Take did not inspect its claim")
	}
	if run.lease.journal.GrokHome != f.home || run.lease.journal.GrokHomeIdentity == "" {
		t.Fatal("the lease journal does not carry the bound home")
	}
	if err := run.finish(true); err != nil {
		t.Fatal(err)
	}
}

// A home whose directory identity cannot be established is refused before the
// Take and before any credential is written: recovery would reject the empty
// identity and leave the credential and the lease fenced for good.
func TestGrokExecutionRefusesAHomeWithoutAStableIdentityBeforeTheTake(t *testing.T) {
	f := newGrokExecutionServer(t)
	original := grokHomeIdentity
	grokHomeIdentity = func(subscription.GrokHome) string { return "" }
	defer func() { grokHomeIdentity = original }()
	if _, err := f.begin(t); err == nil {
		t.Fatal("a home without a stable identity was accepted")
	}
	if len(f.takes) != 0 || len(f.finishes) != 0 {
		t.Fatalf("an unidentifiable home still reached the server (%d takes)", len(f.takes))
	}
	if entries, _ := os.ReadDir(f.home); len(entries) != 0 {
		t.Fatal("a credential reached an unidentifiable home")
	}
}

func TestGrokExecutionRetriesABusyAccountUntilTheLeaseIsFree(t *testing.T) {
	f := newGrokExecutionServer(t)
	f.busy.Store(1)
	started := time.Now()
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.takes) != 2 || time.Since(started) < time.Second {
		t.Fatalf("a busy account was not retried with backoff (%d takes)", len(f.takes))
	}
	if err := run.finish(true); err != nil {
		t.Fatal(err)
	}
}

func TestGrokExecutionLogsUseTheExecutionPhaseWithoutCredentialsPathsOrIdentity(t *testing.T) {
	f := newGrokExecutionServer(t)
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.nativeRotate(t)
	if err := run.finish(true); err != nil {
		t.Fatal(err)
	}
	logs := f.logs.String()
	if !strings.Contains(logs, `"phase":"EXECUTION"`) {
		t.Fatal("execution logs lost the EXECUTION phase")
	}
	for _, private := range []string{"synthetic-key", "synthetic-refresh", "synthetic-principal", "synthetic-user", f.home, f.config.Root, string(f.envelope[:32])} {
		if strings.Contains(logs, private) {
			t.Fatalf("execution logs disclosed %q", private)
		}
	}
}

// The original Worker retires a credential a crash left behind and reports a
// credential-free, unsuccessful, cleanup-confirmed Finish for the original
// instance, without retaking the lease.
func TestGrokRecoveryRetiresTheLeftoverCredentialAndReportsCleanupOnly(t *testing.T) {
	f := newGrokExecutionServer(t)
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.nativeRotate(t)
	// The Worker dies here: no Finish is reported, the home keeps its files.
	run.closeHTTP()
	_ = run.lease.grokHome.Close()
	account, lease := f.fencedAccount(t, run)
	client := delidevv1connect.NewSubscriptionServiceClient(httptest.NewServer(nil).Client(), f.credential.Endpoint)
	if err := runGrokRecovery(context.Background(), f.config, client, f.credential, f.account, account); err != nil {
		t.Fatal(err)
	}
	request := f.onlyFinish(t)
	if request.Succeeded || !request.CleanupConfirmed || len(request.Bundle) != 0 || request.InstanceId != string(lease.InstanceID) || request.LeaseId != string(lease.ID) {
		t.Fatalf("recovery reported %+v", request)
	}
	for _, name := range []string{subscription.GrokAuthFileName, subscription.GrokLockFileName} {
		if _, err := os.Lstat(filepath.Join(f.home, name)); !os.IsNotExist(err) {
			t.Fatalf("recovery left %s", name)
		}
	}
	// Recovery is idempotent: the same claim is not replayed once reported.
	if err := runGrokRecovery(context.Background(), f.config, client, f.credential, f.account, account); err == nil {
		t.Fatal("a reported lease was recovered twice")
	}
}

func TestGrokRecoveryNeverConfirmsCleanupItCannotProve(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory replacement semantics differ")
	}
	f := newGrokExecutionServer(t)
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.nativeRotate(t)
	run.closeHTTP()
	_ = run.lease.grokHome.Close()
	// A lock file that cannot be removed keeps the lease fenced.
	if err := os.Remove(filepath.Join(f.home, subscription.GrokLockFileName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.home, subscription.GrokLockFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	account, _ := f.fencedAccount(t, run)
	client := delidevv1connect.NewSubscriptionServiceClient(httptest.NewServer(nil).Client(), f.credential.Endpoint)
	if err := runGrokRecovery(context.Background(), f.config, client, f.credential, f.account, account); err == nil {
		t.Fatal("recovery confirmed a cleanup that left a lock behind")
	}
	if len(f.finishes) != 0 {
		t.Fatal("an unconfirmed cleanup was reported")
	}
}

func TestGrokRecoveryRefusesForeignOrNonExecuteLeases(t *testing.T) {
	f := newGrokExecutionServer(t)
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	run.closeHTTP()
	_ = run.lease.grokHome.Close()
	account, lease := f.fencedAccount(t, run)
	client := delidevv1connect.NewSubscriptionServiceClient(httptest.NewServer(nil).Client(), f.credential.Endpoint)
	other := f.credential
	other.MachineID = domain.NewID()
	for name, mutate := range map[string]func(*domain.Account) (Credential, domain.ID){
		"another Runner Device": func(*domain.Account) (Credential, domain.ID) { return other, f.account },
		"another account":       func(*domain.Account) (Credential, domain.ID) { return f.credential, domain.NewID() },
		"not execute": func(a *domain.Account) (Credential, domain.ID) {
			a.Subscription.Lease.Action = domain.SubscriptionRefresh
			return f.credential, f.account
		},
		"another operation": func(a *domain.Account) (Credential, domain.ID) {
			a.Subscription.Lease.OperationID = domain.NewID()
			return f.credential, f.account
		},
		"not fenced": func(a *domain.Account) (Credential, domain.ID) {
			a.Subscription.RecoveryRequired = false
			return f.credential, f.account
		},
	} {
		t.Run(name, func(t *testing.T) {
			copied := account
			state := *account.Subscription
			leaseCopy := *lease
			state.Lease = &leaseCopy
			copied.Subscription = &state
			credential, id := mutate(&copied)
			if err := runGrokRecovery(context.Background(), f.config, client, credential, id, copied); err == nil {
				t.Fatal("recovery accepted a lease that is not this Worker's execute lease")
			}
		})
	}
	if len(f.finishes) != 0 {
		t.Fatal("a refused recovery reported a Finish")
	}
}

// fencedAccount is the account document the server presents after fencing the
// execute lease of the run.
func (f *grokExecutionServer) fencedAccount(t *testing.T, run *grokSubscriptionRun) (domain.Account, *domain.SubscriptionLease) {
	t.Helper()
	var journal managedSubscriptionJournal
	raw, err := security.ReadPrivate(run.lease.journalPath, 16<<10)
	if err != nil || domain.Decode(raw, &journal) != nil {
		t.Fatal("missing lease journal")
	}
	lease := &domain.SubscriptionLease{ID: journal.Lease, OperationID: journal.Operation, Revision: journal.LeaseRevision, Action: domain.SubscriptionExecute, MachineID: f.credential.MachineID, InstanceID: journal.Instance, DeviceID: domain.NewID(), Generation: f.generation, StartedAt: time.Now().UTC()}
	return domain.Account{SubscriptionService: domain.SubscriptionGrok, Subscription: &domain.SubscriptionState{RecoveryRequired: true, Lease: lease, Generation: f.generation}}, lease
}

func TestGrokWatchRecognizesOnlyAFencedExecuteLease(t *testing.T) {
	fenced := func(edit func(*domain.Account)) domain.Account {
		a := domain.Account{SubscriptionService: domain.SubscriptionGrok, Subscription: &domain.SubscriptionState{RecoveryRequired: true, Lease: &domain.SubscriptionLease{Action: domain.SubscriptionExecute}}}
		if edit != nil {
			edit(&a)
		}
		return a
	}
	if !grokRecoveryAccount(fenced(nil)) {
		t.Fatal("a fenced Grok execute lease was not recognized")
	}
	for name, edit := range map[string]func(*domain.Account){
		"live lease":     func(a *domain.Account) { a.Subscription.RecoveryRequired = false },
		"no lease":       func(a *domain.Account) { a.Subscription.Lease = nil },
		"refresh lease":  func(a *domain.Account) { a.Subscription.Lease.Action = domain.SubscriptionRefresh },
		"shared owner":   func(a *domain.Account) { a.Subscription.Lease.Shared = true },
		"another vendor": func(a *domain.Account) { a.SubscriptionService = domain.SubscriptionClaude },
		"no state":       func(a *domain.Account) { a.Subscription = nil },
	} {
		if grokRecoveryAccount(fenced(edit)) {
			t.Fatalf("%s was treated as Grok execute recovery", name)
		}
	}
}

func TestGrokSubscriptionExecutionIsRefusedUnlessWorker21WasNegotiated(t *testing.T) {
	f := newGrokExecutionServer(t)
	input := domain.ExecutionJobInput{AccountID: f.account, Installation: domain.Installation{Version: domain.GrokSubscriptionBaselineVersion}}
	input.Configuration.Subscription = true
	input.Configuration.SubscriptionService = domain.SubscriptionGrok
	for name, edit := range map[string]func(*Config, *domain.ExecutionJobInput){
		"capability not negotiated": func(c *Config, _ *domain.ExecutionJobInput) { c.grokExecution = false },
		"below the baseline":        func(_ *Config, i *domain.ExecutionJobInput) { i.Installation.Version = domain.GrokProtocolVersion },
		"another service": func(_ *Config, i *domain.ExecutionJobInput) {
			i.Configuration.SubscriptionService = domain.SubscriptionClaude
		},
	} {
		t.Run(name, func(t *testing.T) {
			config, job := f.config, input
			edit(&config, &job)
			_, err := executeGrokSession(context.Background(), config, domain.NewID(), job, config.Logger)
			if err == nil || domain.SafeError(err).Code != domain.Unsupported {
				t.Fatalf("a Worker without the negotiated profile started a Grok subscription: %v", err)
			}
		})
	}
	if len(f.takes) != 0 {
		t.Fatal("a refused execution took a lease")
	}
}

// The execute Take returns the only installation the lease may launch. Discovery
// can replace the job's frozen observation between dispatch and Take, so the
// Worker uses and validates the returned one.
func TestGrokExecutionUsesTheInstallationTheTakeAuthorized(t *testing.T) {
	f := newGrokExecutionServer(t)
	authorized := filepath.Join(f.config.Root, "authorized-grok")
	if err := os.WriteFile(authorized, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(authorized)
	if err != nil {
		t.Fatal(err)
	}
	f.installationJSON = f.installationDocument(domain.Installation{Harness: domain.GrokBuild, Version: "1.0.99", ResolvedPath: resolved})
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	path, version, err := run.executable()
	if err != nil || path != resolved || version != "1.0.99" {
		t.Fatalf("the lease did not use the authorized installation: %q %q %v", path, version, err)
	}
	// A path that is not its own canonical identity (a symlink swapped in since the
	// Take) is refused.
	link := filepath.Join(f.config.Root, "swapped-grok")
	if err := os.Symlink(resolved, link); err != nil {
		t.Skip("symlinks are unavailable:", err)
	}
	run.installation.ResolvedPath = link
	if _, _, err := run.executable(); err == nil {
		t.Fatal("a swapped executable identity was accepted")
	}
	if err := run.finish(true); err != nil {
		t.Fatal(err)
	}
}

func TestGrokExecutionRefusesAnUnusableAuthorizedInstallationAndReleasesTheLease(t *testing.T) {
	for name, document := range map[string]func(*grokExecutionServer) []byte{
		"none":     func(*grokExecutionServer) []byte { return nil },
		"not json": func(*grokExecutionServer) []byte { return []byte("not json") },
		"another harness": func(f *grokExecutionServer) []byte {
			return f.installationDocument(domain.Installation{Harness: domain.Codex, Version: domain.GrokSubscriptionBaselineVersion, ResolvedPath: "/controlled/native/grok"})
		},
		"below the baseline": func(f *grokExecutionServer) []byte {
			return f.installationDocument(domain.Installation{Harness: domain.GrokBuild, Version: domain.GrokProtocolVersion, ResolvedPath: "/controlled/native/grok"})
		},
		"relative path": func(f *grokExecutionServer) []byte {
			return f.installationDocument(domain.Installation{Harness: domain.GrokBuild, Version: domain.GrokSubscriptionBaselineVersion, ResolvedPath: "grok"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newGrokExecutionServer(t)
			f.installationJSON = document(f)
			if _, err := f.begin(t); err == nil {
				t.Fatal("an unusable authorized installation was accepted")
			}
			request := f.onlyFinish(t)
			if request.Succeeded || !request.CleanupConfirmed || len(request.Bundle) != 0 {
				t.Fatalf("the refused lease was not released with confirmed cleanup: %+v", request)
			}
			if entries, _ := os.ReadDir(f.home); len(entries) != 0 {
				t.Fatal("a credential was written for an unusable installation")
			}
		})
	}
}

// A Finish whose response is lost may leave the execute lease held. Every
// post-Take failure then reports execution uncertainty, which closes the
// primary lane and reconciles ownership, instead of an ordinary failed job.
func TestGrokExecutionWithAnUnacknowledgedFinishIsUncertain(t *testing.T) {
	t.Run("failure after the Take", func(t *testing.T) {
		f := newGrokExecutionServer(t)
		f.installationJSON = nil
		f.failFinishWithConnect = true
		_, err := f.begin(t)
		var uncertain *managedExecutionUncertain
		if !errors.As(err, &uncertain) {
			t.Fatalf("an unacknowledged Finish after a failed begin was an ordinary error: %v", err)
		}
	})
	t.Run("release after the execution", func(t *testing.T) {
		f := newGrokExecutionServer(t)
		run, err := f.begin(t)
		if err != nil {
			t.Fatal(err)
		}
		f.failFinishWithConnect = true
		var uncertain *managedExecutionUncertain
		if err := run.release(true); !errors.As(err, &uncertain) {
			t.Fatalf("an unacknowledged Finish was an ordinary error: %v", err)
		}
	})
}

// A missing or replaced home does not prove the credential gone: the original
// directory may have been renamed away with the plaintext inside. Recovery
// confirms cleanup only when no credential was ever written, the Worker already
// retired it, or the directory at the path is the one the lease bound.
func TestGrokRecoveryDoesNotConfirmCleanupOfAHomeItCannotProve(t *testing.T) {
	recover := func(t *testing.T, f *grokExecutionServer, run *grokSubscriptionRun) error {
		t.Helper()
		run.closeHTTP()
		_ = run.lease.grokHome.Close()
		account, _ := f.fencedAccount(t, run)
		client := delidevv1connect.NewSubscriptionServiceClient(httptest.NewServer(nil).Client(), f.credential.Endpoint)
		return runGrokRecovery(context.Background(), f.config, client, f.credential, f.account, account)
	}
	materialized := func(t *testing.T) (*grokExecutionServer, *grokSubscriptionRun) {
		t.Helper()
		f := newGrokExecutionServer(t)
		run, err := f.begin(t)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.prepare(context.Background()); err != nil {
			t.Fatal(err)
		}
		f.nativeRotate(t)
		return f, run
	}
	t.Run("renamed away after the credential was written", func(t *testing.T) {
		f, run := materialized(t)
		moved := f.home + ".moved"
		if err := os.Rename(f.home, moved); err != nil {
			t.Fatal(err)
		}
		if err := recover(t, f, run); err == nil {
			t.Fatal("cleanup was confirmed for a home that was renamed away with the credential inside")
		}
		if len(f.finishes) != 0 {
			t.Fatal("a cleanup-confirmed Finish was reported")
		}
		if _, err := os.Lstat(filepath.Join(moved, subscription.GrokAuthFileName)); err != nil {
			t.Fatal("the test did not leave the renamed credential in place")
		}
	})
	t.Run("replaced by another directory", func(t *testing.T) {
		f, run := materialized(t)
		if run.lease.journal.GrokHomeIdentity == "" {
			t.Skip("the platform exposes no directory identity")
		}
		moved := f.home + ".moved"
		if err := os.Rename(f.home, moved); err != nil {
			t.Fatal(err)
		}
		if err := security.PrivateDir(f.home); err != nil {
			t.Fatal(err)
		}
		if err := recover(t, f, run); err == nil {
			t.Fatal("cleanup was confirmed for a successor of the original home")
		}
		if len(f.finishes) != 0 {
			t.Fatal("a cleanup-confirmed Finish was reported")
		}
	})
	t.Run("missing before any credential was written", func(t *testing.T) {
		f := newGrokExecutionServer(t)
		run, err := f.begin(t)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(f.home); err != nil {
			t.Fatal(err)
		}
		if err := recover(t, f, run); err != nil {
			t.Fatalf("a home that never held a credential blocked recovery: %v", err)
		}
	})
	t.Run("retired by the Worker before a lost Finish", func(t *testing.T) {
		f, run := materialized(t)
		f.failFinishWithConnect = true
		if err := run.finish(true); err == nil {
			t.Fatal("the lost Finish was acknowledged")
		}
		if !run.lease.journal.GrokRetired {
			t.Fatal("the confirmed retirement was not journaled")
		}
		f.failFinishWithConnect = false
		if err := os.RemoveAll(f.home); err != nil {
			t.Fatal(err)
		}
		account, _ := f.fencedAccount(t, run)
		client := delidevv1connect.NewSubscriptionServiceClient(httptest.NewServer(nil).Client(), f.credential.Endpoint)
		if err := runGrokRecovery(context.Background(), f.config, client, f.credential, f.account, account); err != nil {
			t.Fatalf("recovery of a retired credential was blocked: %v", err)
		}
	})
}

// The materialized credential's own values are handed to the native frame guard,
// so a native frame that reflects the cached access or refresh token is held back
// like an API token.
func TestGrokPreparedCredentialReturnsItsProtectedValues(t *testing.T) {
	f := newGrokExecutionServer(t)
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	protected, err := run.prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(protected) != 2 || protected[0] == "" || protected[1] == "" || protected[0] == protected[1] {
		t.Fatalf("the credential's access and refresh values were not protected: %d values", len(protected))
	}
	if err := run.finish(true); err != nil {
		t.Fatal(err)
	}
}

// The owner's process index is retained before the execution is journaled as
// started and before any credential exists, so a missing index can never be read
// as "nothing was running".
func TestGrokPrepareRetainsTheOwnerProcessIndexBeforeAnyCredential(t *testing.T) {
	f := newGrokExecutionServer(t)
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(run.processes, string(run.owner))
	if _, err := os.Lstat(index); !os.IsNotExist(err) {
		t.Fatal("the process index existed before the execution started")
	}
	if _, err := run.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := security.CheckPrivateDir(index); err != nil {
		t.Fatalf("the owner process index was not retained as a private directory: %v", err)
	}
	if !run.lease.journal.ExecutionStarted {
		t.Fatal("the journal does not record the started execution")
	}
}

// Cell table of the native-join evidence of an execution that started: only a
// present, empty-reconciling index proves the owned process gone.
func TestGrokNativeJoinEvidenceNeverTreatsAMissingIndexAsProof(t *testing.T) {
	processes := filepath.Join(t.TempDir(), "processes")
	if err := security.PrivateDir(processes); err != nil {
		t.Fatal(err)
	}
	owner := domain.NewID()
	for name, test := range map[string]struct {
		prepare func(t *testing.T)
		started bool
		want    bool
	}{
		"never started, no index":    {started: false, want: true},
		"started, no index":          {started: true, want: false},
		"started, retained empty":    {started: true, want: true, prepare: func(t *testing.T) { _ = security.PrivateDir(filepath.Join(processes, string(owner))) }},
		"never started, empty index": {started: false, want: true, prepare: func(t *testing.T) { _ = security.PrivateDir(filepath.Join(processes, string(owner))) }},
	} {
		t.Run(name, func(t *testing.T) {
			_ = os.RemoveAll(filepath.Join(processes, string(owner)))
			if test.prepare != nil {
				test.prepare(t)
			}
			if got := grokNativeJoined(context.Background(), processes, owner, test.started); got != test.want {
				t.Fatalf("joined = %v, want %v", got, test.want)
			}
		})
	}
}

// An index removed after the execution started (while the child may still be
// alive) leaves the cleanup unconfirmed, so the Finish keeps the lease fenced.
func TestGrokExecutionWhoseProcessIndexDisappearedNeverConfirmsCleanup(t *testing.T) {
	f := newGrokExecutionServer(t)
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(run.processes, string(run.owner))); err != nil {
		t.Fatal(err)
	}
	if err := run.finish(true); err != nil {
		t.Fatal(err)
	}
	if request := f.onlyFinish(t); request.CleanupConfirmed || request.Succeeded {
		t.Fatalf("a missing process index was reported as a joined process: %+v", request)
	}
}

// Recovery follows the same evidence: an execution journaled as started whose
// index is gone keeps the lease; one that never started has nothing to join.
func TestGrokRecoveryOfAStartedExecutionRequiresItsProcessIndex(t *testing.T) {
	for name, started := range map[string]bool{"started": true, "never started": false} {
		t.Run(name, func(t *testing.T) {
			f := newGrokExecutionServer(t)
			run, err := f.begin(t)
			if err != nil {
				t.Fatal(err)
			}
			if started {
				if _, err := run.prepare(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(filepath.Join(run.processes, string(run.owner))); err != nil {
					t.Fatal(err)
				}
			}
			run.closeHTTP()
			_ = run.lease.grokHome.Close()
			account, _ := f.fencedAccount(t, run)
			client := delidevv1connect.NewSubscriptionServiceClient(httptest.NewServer(nil).Client(), f.credential.Endpoint)
			err = runGrokRecovery(context.Background(), f.config, client, f.credential, f.account, account)
			if started && (err == nil || len(f.finishes) != 0) {
				t.Fatalf("recovery confirmed a started execution without its process index: %v", err)
			}
			if !started && (err != nil || len(f.finishes) != 1 || !f.finishes[0].CleanupConfirmed) {
				t.Fatalf("recovery of an execution that never started failed: %v", err)
			}
		})
	}
}
