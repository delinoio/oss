// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
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
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: string(lease), LeaseRevision: 3, GenerationId: string(f.generation), Bundle: bytes.Clone(f.envelope)}), nil
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
	if err := run.prepare(context.Background()); err != nil {
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
	if err := run.prepare(context.Background()); err != nil {
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
			if err := run.prepare(context.Background()); err != nil {
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
	// The lease is still released: nothing was ever written.
	if request := f.onlyFinish(t); request.Succeeded || !request.CleanupConfirmed {
		t.Fatalf("an unusable home was not released cleanly: %+v", request)
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
	if err := run.prepare(context.Background()); err != nil {
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
	if err := run.prepare(context.Background()); err != nil {
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
	if err := run.prepare(context.Background()); err != nil {
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
