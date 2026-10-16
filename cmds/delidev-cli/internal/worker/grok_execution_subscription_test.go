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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

// grokExecutionFixture is the protected execute-lease side of a managed Grok
// account. At the moment each Finish arrives it records what the private home
// held, which proves the credential was retired before the lease was released.
type grokExecutionFixture struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	mu                         sync.Mutex
	t                          *testing.T
	config                     Config
	credential                 Credential
	account, generation, lease domain.ID
	home                       string
	bundle                     subscription.GrokBundle
	omitBundle, profileLeak    bool
	finishes                   []*pb.FinishSubscriptionRequest
	homeAtFinish               []bool
	afterFinish                func(int)
	// finishError, when set, fails the numbered Finish request after it was
	// recorded, as a lost or refused response would.
	finishError func(int) error
	logs        *bytes.Buffer
}

func (f *grokExecutionFixture) TakeSubscription(_ context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (*connect.Response[pb.TakeSubscriptionResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lease = domain.ID(req.Msg.Mutation.RequestId)
	if req.Msg.Action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE {
		f.t.Error("a Worker requested a lifecycle operation")
	}
	raw, err := security.ReadPrivate(filepath.Join(f.config.Root, "managed-auth", string(f.lease)+".json"), 16<<10)
	var claim managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &claim) != nil || claim.State != managedClaimed || claim.Action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE {
		f.t.Error("protected delivery preceded the original durable claim")
		return nil, connect.NewError(connect.CodeFailedPrecondition, nil)
	}
	envelope, err := subscription.SealGrokBundle(f.credential.ServerID, f.account, f.generation, f.bundle)
	if err != nil {
		f.t.Fatal(err)
	}
	response := &pb.TakeSubscriptionResponse{LeaseId: string(f.lease), LeaseRevision: 3, GenerationId: string(f.generation), Bundle: envelope}
	if f.omitBundle {
		response.Bundle = nil
	}
	if f.profileLeak {
		response.NativeProfileId = string(domain.NewID())
	}
	return connect.NewResponse(response), nil
}

func (f *grokExecutionFixture) FinishSubscription(_ context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishes = append(f.finishes, proto.Clone(req.Msg).(*pb.FinishSubscriptionRequest))
	present := false
	for _, name := range []string{subscription.GrokAuthFileName, subscription.GrokAuthFileName + ".lock"} {
		if _, err := os.Lstat(filepath.Join(f.home, name)); err == nil {
			present = true
		}
	}
	f.homeAtFinish = append(f.homeAtFinish, present)
	if f.afterFinish != nil {
		f.afterFinish(len(f.finishes))
	}
	if f.finishError != nil {
		if err := f.finishError(len(f.finishes)); err != nil {
			return nil, err
		}
	}
	document, _ := json.Marshal(domain.Account{Subscription: &domain.SubscriptionState{}})
	return connect.NewResponse(&pb.FinishSubscriptionResponse{Account: &pb.Resource{Id: string(f.account), Revision: 4, DocumentJson: document}}), nil
}

func newGrokExecutionFixture(t *testing.T) *grokExecutionFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "worker")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	native, err := subscription.ParseGrokAuthFile(grokWorkerFile(t, "synthetic-principal-1", "one", "2026-10-14T06:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	f := &grokExecutionFixture{t: t, logs: logs, bundle: native, config: Config{Root: root, Logger: slog.New(slog.NewJSONHandler(logs, nil))}, credential: Credential{ServerID: domain.NewID(), MachineID: domain.NewID(), DeviceID: domain.NewID()}, account: domain.NewID(), generation: domain.NewID()}
	f.home = grokHome(t)
	_, handler := delidevv1connect.NewSubscriptionServiceHandler(f)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	f.credential.Endpoint = server.URL
	grokCleanupRetryWait = 0
	t.Cleanup(func() { grokCleanupRetryWait = time.Second })
	return f
}

func (f *grokExecutionFixture) begin(t *testing.T) (*grokSubscriptionExecution, error) {
	t.Helper()
	f.config.execution = &PublicationConfig{Credential: f.credential, Instance: domain.NewID(), Assignment: &pb.Resource{Revision: 2}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return beginGrokSubscriptionExecution(ctx, f.config, domain.NewID(), domain.ExecutionJobInput{AccountID: f.account})
}

func (f *grokExecutionFixture) authPath() string {
	return filepath.Join(f.home, subscription.GrokAuthFileName)
}

func (f *grokExecutionFixture) onlyFinish(t *testing.T) *pb.FinishSubscriptionRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.finishes) != 1 {
		t.Fatalf("finish reports = %d", len(f.finishes))
	}
	return f.finishes[0]
}

// rotated rewrites auth.json the way a native process refreshes its login.
func (f *grokExecutionFixture) rotate(t *testing.T, principal string) {
	t.Helper()
	if err := os.WriteFile(f.authPath(), grokWorkerFile(t, principal, "two", "2026-10-14T12:00:00Z"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGrokExecutionMaterializesOnlyInThePrivateHomeAndReleasesAnUnchangedCredential(t *testing.T) {
	f := newGrokExecutionFixture(t)
	managed, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(f.authPath()); !os.IsNotExist(err) {
		t.Fatal("the credential was written before the launch hook ran")
	}
	if err := managed.credentialHook(f.home)(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(f.authPath())
	if err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("auth.json is not an owner-only regular file")
	}
	if len(managed.lease.response.Bundle) != 0 {
		t.Fatal("the delivered envelope stayed in memory")
	}
	// The sealed envelope and the plaintext are never journaled; only the home is.
	journal, err := os.ReadFile(managed.lease.journalPath)
	if err != nil || bytes.Contains(journal, []byte("synthetic")) || !bytes.Contains(journal, []byte(`"grok_home"`)) {
		t.Fatal("the journal leaks credential data or lost the recovery home")
	}
	if err := managed.credentialHook(f.home)(); err == nil {
		t.Fatal("the credential was materialized twice")
	}
	if err := managed.finish(true); err != nil {
		t.Fatal(err)
	}
	finish := f.onlyFinish(t)
	if !finish.CleanupConfirmed || !finish.Succeeded || finish.RefreshConfirmed || len(finish.Bundle) != 0 || finish.LeaseId != string(f.lease) || finish.GenerationId != string(f.generation) {
		t.Fatal("an unchanged credential was not a clean release")
	}
	if f.homeAtFinish[0] {
		t.Fatal("the lease was released while the credential file or lock was still present")
	}
	if _, err := os.Stat(f.authPath()); !os.IsNotExist(err) {
		t.Fatal("the credential survived")
	}
	if err := managed.finish(true); err != nil || len(f.finishes) != 1 {
		t.Fatal("a second settlement was reported")
	}
}

func TestGrokExecutionReturnsAVerifiedRefreshAndRetiresTheFileAndLock(t *testing.T) {
	f := newGrokExecutionFixture(t)
	managed, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.credentialHook(f.home)(); err != nil {
		t.Fatal(err)
	}
	f.rotate(t, "synthetic-principal-1")
	if err := os.WriteFile(filepath.Join(f.home, subscription.GrokAuthFileName+".lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := managed.finish(true); err != nil {
		t.Fatal(err)
	}
	finish := f.onlyFinish(t)
	if !finish.CleanupConfirmed || !finish.Succeeded || !finish.RefreshConfirmed || len(finish.Bundle) == 0 || f.homeAtFinish[0] {
		t.Fatal("a refreshed credential was not returned with confirmed cleanup")
	}
	next, err := subscription.OpenGrokBundle(finish.Bundle, f.credential.ServerID, f.account, domain.ID(finish.Mutation.RequestId))
	if err != nil || next.IdentityKey() != f.bundle.IdentityKey() || subscription.GrokRefreshed(f.bundle, next) != nil {
		t.Fatal("the returned bundle is not the verified successor sealed for the Finish generation", err)
	}
	next.Clear()
	for _, name := range []string{subscription.GrokAuthFileName, subscription.GrokAuthFileName + ".lock"} {
		if _, err := os.Lstat(filepath.Join(f.home, name)); !os.IsNotExist(err) {
			t.Fatalf("%s survived", name)
		}
	}
}

func TestGrokExecutionNeverReturnsAChangedIdentityAndStillRetiresTheFile(t *testing.T) {
	f := newGrokExecutionFixture(t)
	managed, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.credentialHook(f.home)(); err != nil {
		t.Fatal(err)
	}
	f.rotate(t, "synthetic-principal-2")
	if err := managed.finish(true); err != nil {
		t.Fatal(err)
	}
	finish := f.onlyFinish(t)
	if finish.Succeeded || finish.RefreshConfirmed || len(finish.Bundle) != 0 || !finish.CleanupConfirmed || f.homeAtFinish[0] {
		t.Fatal("a foreign identity was returned or the file survived")
	}
}

// Stop, crash and timeout all end with the native process joined and the file in
// whatever state the process left it; every one of them runs the same exit.
func TestGrokExecutionCleansUpAfterEveryJoinedNativeOutcome(t *testing.T) {
	for name, leave := range map[string]func(*testing.T, *grokExecutionFixture){
		"unchanged": func(*testing.T, *grokExecutionFixture) {},
		"crash left a partial file": func(t *testing.T, f *grokExecutionFixture) {
			_ = os.WriteFile(f.authPath(), []byte(`{"truncated`), 0o600)
		},
		"crash deleted the file": func(t *testing.T, f *grokExecutionFixture) { _ = os.Remove(f.authPath()) },
		"stop left lock and temporary": func(t *testing.T, f *grokExecutionFixture) {
			_ = os.WriteFile(filepath.Join(f.home, subscription.GrokAuthFileName+".lock"), nil, 0o600)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newGrokExecutionFixture(t)
			managed, err := f.begin(t)
			if err != nil {
				t.Fatal(err)
			}
			if err := managed.credentialHook(f.home)(); err != nil {
				t.Fatal(err)
			}
			leave(t, f)
			if err := managed.finish(true); err != nil {
				t.Fatal(err)
			}
			finish := f.onlyFinish(t)
			if !finish.CleanupConfirmed || f.homeAtFinish[0] || len(finish.Bundle) != 0 && !finish.RefreshConfirmed {
				t.Fatal("the credential outlived the lease or a bundle was returned without confirmation")
			}
			if entries, _ := os.ReadDir(f.home); len(entries) != 0 {
				t.Fatalf("the private home still holds %d entries", len(entries))
			}
		})
	}
}

func TestGrokExecutionNeverTouchesTheFileOfAProcessThatWasNotJoined(t *testing.T) {
	f := newGrokExecutionFixture(t)
	managed, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.credentialHook(f.home)(); err != nil {
		t.Fatal(err)
	}
	f.rotate(t, "synthetic-principal-1")
	if err := managed.finish(false); err != nil {
		t.Fatal(err)
	}
	finish := f.onlyFinish(t)
	if finish.CleanupConfirmed || finish.Succeeded || len(finish.Bundle) != 0 || finish.RefreshConfirmed {
		t.Fatal("an unjoined process was reported as cleaned up")
	}
	if _, err := os.Stat(f.authPath()); err != nil {
		t.Fatal("the credential of a possibly running process was removed")
	}
}

func TestGrokExecutionReleasesAnUntouchedLeaseWhenTheLaunchNeverGotThatFar(t *testing.T) {
	f := newGrokExecutionFixture(t)
	managed, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.finish(true); err != nil {
		t.Fatal(err)
	}
	finish := f.onlyFinish(t)
	if !finish.CleanupConfirmed || !finish.Succeeded || len(finish.Bundle) != 0 {
		t.Fatal("a lease that never wrote a credential was not released cleanly")
	}
}

func TestGrokExecutionFailedMaterializationFencesWithoutClaimingSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission semantics differ")
	}
	f := newGrokExecutionFixture(t)
	if err := os.Chmod(f.home, 0o755); err != nil {
		t.Fatal(err)
	}
	managed, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.credentialHook(f.home)(); err == nil {
		t.Fatal("a loose home was accepted")
	}
	if err := managed.finish(true); err != nil {
		t.Fatal(err)
	}
	finish := f.onlyFinish(t)
	if finish.Succeeded || finish.RefreshConfirmed || len(finish.Bundle) != 0 || finish.CleanupConfirmed {
		t.Fatal("a failed materialization claimed success or an unproven cleanup")
	}
	if entries, _ := os.ReadDir(f.home); len(entries) != 0 {
		t.Fatal("a rejected home received files")
	}
}

func TestGrokExecutionRetriesAnUnconfirmedCleanupBeforeReleasing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory replacement semantics differ")
	}
	f := newGrokExecutionFixture(t)
	managed, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.credentialHook(f.home)(); err != nil {
		t.Fatal(err)
	}
	stuck := filepath.Join(f.home, security.OwnedTemporaryPrefix(f.authPath())+"stuck")
	if err := os.Mkdir(stuck, 0o700); err != nil {
		t.Fatal(err)
	}
	f.afterFinish = func(count int) {
		if count == 2 {
			_ = os.Remove(stuck)
		}
	}
	if err := managed.finish(true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.finishes) != 3 || f.finishes[0].CleanupConfirmed || f.finishes[1].CleanupConfirmed || !f.finishes[2].CleanupConfirmed || !f.finishes[2].Succeeded {
		t.Fatalf("the cleanup was not retried to confirmation: %d reports", len(f.finishes))
	}
	if f.finishes[0].Mutation.RequestId == f.finishes[1].Mutation.RequestId || f.finishes[1].Mutation.RequestId == f.finishes[2].Mutation.RequestId {
		t.Fatal("a retry reused its Finish identity")
	}
}

func TestGrokExecutionKeepsTheLeaseWhenCleanupCannotBeConfirmed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory replacement semantics differ")
	}
	f := newGrokExecutionFixture(t)
	managed, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.credentialHook(f.home)(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.home, security.OwnedTemporaryPrefix(f.authPath())+"stuck"), 0o700); err != nil {
		t.Fatal(err)
	}
	err = managed.finish(true)
	var uncertain *managedExecutionUncertain
	if !asManagedExecutionUncertain(err, &uncertain) {
		t.Fatalf("an unconfirmed cleanup did not close the lane: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, finish := range f.finishes {
		if finish.CleanupConfirmed || len(finish.Bundle) != 0 {
			t.Fatal("an unconfirmed cleanup released the lease or returned a bundle")
		}
	}
	if len(f.finishes) != 1+grokCleanupRetries {
		t.Fatalf("finish reports = %d", len(f.finishes))
	}
}

// A failed protected Finish (lost response, refused RPC, a failed retry) leaves
// the lease possibly live or its commit unacknowledged. None of those may be
// reported as an ordinary job error: each closes the primary lane through
// managedExecutionUncertain.
func TestGrokExecutionEveryFailedProtectedFinishIsExecutionUncertainty(t *testing.T) {
	failAt := func(n int) func(int) error {
		return func(count int) error {
			if count == n {
				return connect.NewError(connect.CodeUnavailable, nil)
			}
			return nil
		}
	}
	stuck := func(t *testing.T, f *grokExecutionFixture) {
		if err := os.Mkdir(filepath.Join(f.home, security.OwnedTemporaryPrefix(f.authPath())+"stuck"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, test := range map[string]struct {
		prepare func(*testing.T, *grokExecutionFixture, *grokSubscriptionExecution)
		joined  bool
		fail    func(int) error
	}{
		"final report lost":  {joined: true, fail: failAt(1)},
		"not joined":         {joined: false, fail: failAt(1)},
		"cleanup retry lost": {joined: true, fail: failAt(2), prepare: func(t *testing.T, f *grokExecutionFixture, _ *grokSubscriptionExecution) { stuck(t, f) }},
		"never materialized": {joined: true, fail: failAt(1), prepare: func(t *testing.T, f *grokExecutionFixture, m *grokSubscriptionExecution) { m.attempted = false }},
	} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("directory replacement semantics differ")
			}
			f := newGrokExecutionFixture(t)
			f.finishError = test.fail
			managed, err := f.begin(t)
			if err != nil {
				t.Fatal(err)
			}
			if name != "never materialized" {
				if err := managed.credentialHook(f.home)(); err != nil {
					t.Fatal(err)
				}
			}
			if test.prepare != nil {
				test.prepare(t, f, managed)
			}
			err = managed.finish(test.joined)
			var uncertain *managedExecutionUncertain
			if err == nil || !asManagedExecutionUncertain(err, &uncertain) {
				t.Fatalf("a failed Finish was not execution uncertainty: %v", err)
			}
		})
	}
}

// A Finish whose journal cannot be written is as unconfirmed as a lost response.
func TestGrokExecutionFinishJournalFailureIsExecutionUncertainty(t *testing.T) {
	f := newGrokExecutionFixture(t)
	managed, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.credentialHook(f.home)(); err != nil {
		t.Fatal(err)
	}
	// The journal path becomes a directory, so the next write of it fails.
	if err := os.Remove(managed.lease.journalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(managed.lease.journalPath, 0o700); err != nil {
		t.Fatal(err)
	}
	err = managed.finish(true)
	var uncertain *managedExecutionUncertain
	if err == nil || !asManagedExecutionUncertain(err, &uncertain) {
		t.Fatalf("a journal write failure was not execution uncertainty: %v", err)
	}
	if len(f.finishes) != 0 {
		t.Fatal("a Finish was sent although its journal could not be written")
	}
}

func TestGrokExecutionRefusesMalformedDeliveries(t *testing.T) {
	for name, edit := range map[string]func(*grokExecutionFixture){
		"no bundle":              func(f *grokExecutionFixture) { f.omitBundle = true },
		"a device-owned profile": func(f *grokExecutionFixture) { f.profileLeak = true },
	} {
		t.Run(name, func(t *testing.T) {
			f := newGrokExecutionFixture(t)
			edit(f)
			managed, err := f.begin(t)
			if err == nil || managed != nil {
				t.Fatal("a malformed delivery started an execution")
			}
			finish := f.onlyFinish(t)
			if finish.Succeeded || finish.CleanupConfirmed {
				t.Fatal("a malformed delivery was released as clean")
			}
		})
	}
}

func TestGrokExecutionLogsCarryTheExecutionPhaseAndNoCredentialPathOrIdentity(t *testing.T) {
	f := newGrokExecutionFixture(t)
	managed, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.credentialHook(f.home)(); err != nil {
		t.Fatal(err)
	}
	f.rotate(t, "synthetic-principal-1")
	if err := managed.finish(true); err != nil {
		t.Fatal(err)
	}
	logs := f.logs.String()
	if !strings.Contains(logs, `"phase":"EXECUTION"`) || !strings.Contains(logs, "grok_subscription_execution_started") || !strings.Contains(logs, "grok_subscription_finish") {
		t.Fatal("the execution lifecycle was not logged with the EXECUTION phase")
	}
	for _, private := range []string{"synthetic", f.home, f.config.Root, "refresh_token", "user_id", "principal"} {
		if strings.Contains(logs, private) {
			t.Fatalf("the logs contain %q", private)
		}
	}
}

func asManagedExecutionUncertain(err error, target **managedExecutionUncertain) bool {
	for err != nil {
		if value, ok := err.(*managedExecutionUncertain); ok {
			*target = value
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// grokRecoveryAccount builds the fenced account a restarted server or a lost
// Worker leaves behind: an Execute lease owned by this Runner Device.
func (f *grokExecutionFixture) grokRecoveryAccount(t *testing.T, journal managedSubscriptionJournal) (domain.Account, domain.ID) {
	t.Helper()
	lease, job, instance := domain.NewID(), domain.NewID(), domain.NewID()
	journal.Version, journal.Lease, journal.Account, journal.Operation, journal.Instance = 1, lease, f.account, job, instance
	journal.Finish, journal.Generation, journal.Action, journal.State = domain.NewID(), f.generation, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE, managedClaimed
	if err := security.PrivateDir(filepath.Join(f.config.Root, "managed-auth")); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(f.config.Root, "managed-auth", string(lease)+".json"), journal); err != nil {
		t.Fatal(err)
	}
	f.lease = lease
	return domain.Account{Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionGrok, Subscription: &domain.SubscriptionState{RecoveryRequired: true, Generation: f.generation, Lease: &domain.SubscriptionLease{ID: lease, OperationID: job, Revision: 3, Action: domain.SubscriptionExecute, MachineID: f.credential.MachineID, InstanceID: instance, DeviceID: f.credential.DeviceID, Generation: f.generation}}}, job
}

func TestGrokRecoveryRetiresTheOriginalHomeAndReportsCleanupOnly(t *testing.T) {
	f := newGrokExecutionFixture(t)
	if err := os.WriteFile(f.authPath(), grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.authPath()+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(f.config.Root, "processes")); err != nil {
		t.Fatal(err)
	}
	account, job := f.grokRecoveryAccount(t, managedSubscriptionJournal{GrokHome: f.home, NativeStarted: true})
	// The supervised process directory of the job exists once its inspections ran.
	if err := security.PrivateDir(filepath.Join(f.config.Root, "processes", string(job))); err != nil {
		t.Fatal(err)
	}
	if !grokExecutionRecovery(account) {
		t.Fatal("the fenced lease was not recognized as a Grok execution recovery")
	}
	client := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.credential.Endpoint)
	if err := runGrokRecovery(context.Background(), f.config, client, f.credential, f.account, account); err != nil {
		t.Fatal(err)
	}
	finish := f.onlyFinish(t)
	if !finish.CleanupConfirmed || finish.Succeeded || finish.RefreshConfirmed || len(finish.Bundle) != 0 || f.homeAtFinish[0] {
		t.Fatal("recovery was not a cleanup-only report after the credential was retired")
	}
	if finish.LeaseId != string(f.lease) || finish.GenerationId != string(f.generation) || finish.MachineId != string(f.credential.MachineID) {
		t.Fatal("recovery did not name the original lease")
	}
	if entries, _ := os.ReadDir(f.home); len(entries) != 0 {
		t.Fatal("recovery left files in the private home")
	}
}

// The watch exposes a fenced lease as soon as its first Finish reported an
// unconfirmed cleanup, while the owning execution still has its in-process
// retries to run. Recovery must not run beside them: it would retire the
// credential and release the lease under the retry.
func TestGrokRecoveryWaitsForTheLiveExecutionToFinishItsCleanupRetries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory replacement semantics differ")
	}
	f := newGrokExecutionFixture(t)
	managed, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.credentialHook(f.home)(); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(f.config.Root, "processes")); err != nil {
		t.Fatal(err)
	}
	journal := managed.lease.journal
	fenced := domain.Account{Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionGrok, Subscription: &domain.SubscriptionState{RecoveryRequired: true, Generation: f.generation, Lease: &domain.SubscriptionLease{ID: domain.ID(managed.lease.response.LeaseId), OperationID: journal.Operation, Revision: 3, Action: domain.SubscriptionExecute, MachineID: f.credential.MachineID, InstanceID: journal.Instance, DeviceID: f.credential.DeviceID, Generation: f.generation, CleanupUnconfirmed: true}}}
	client := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.credential.Endpoint)
	// The watch delivers the fenced lease while the execution still owns it.
	if err := runGrokRecovery(context.Background(), f.config, client, f.credential, f.account, fenced); err != nil {
		t.Fatalf("a deferred recovery must not fail: %v", err)
	}
	f.mu.Lock()
	reported, present := len(f.finishes), false
	if _, err := os.Lstat(f.authPath()); err == nil {
		present = true
	}
	f.mu.Unlock()
	if reported != 0 || !present {
		t.Fatalf("recovery ran beside the live execution: %d reports, credential present = %v", reported, present)
	}
	// The execution ends with an unconfirmed cleanup, so its lane closes and the
	// next watch may recover the lease.
	if err := os.Mkdir(filepath.Join(f.home, security.OwnedTemporaryPrefix(f.authPath())+"stuck"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := managed.finish(true); err == nil {
		t.Fatal("an unconfirmed cleanup ended without closing the lane")
	}
	if grokExecutionInFlight(domain.ID(managed.lease.response.LeaseId)) {
		t.Fatal("the finished execution still holds its lease")
	}
	if err := runGrokRecovery(context.Background(), f.config, client, f.credential, f.account, fenced); err == nil {
		t.Fatal("recovery was still deferred after the execution ended")
	}
}

func TestGrokRecoveryRefusesForeignJournalsAndUnprovenCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory replacement semantics differ")
	}
	f := newGrokExecutionFixture(t)
	if err := os.WriteFile(f.authPath(), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(f.config.Root, "processes")); err != nil {
		t.Fatal(err)
	}
	client := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.credential.Endpoint)
	// A journal that names another account is never trusted.
	account, job := f.grokRecoveryAccount(t, managedSubscriptionJournal{GrokHome: f.home})
	if err := security.PrivateDir(filepath.Join(f.config.Root, "processes", string(job))); err != nil {
		t.Fatal(err)
	}
	if err := runGrokRecovery(context.Background(), f.config, client, f.credential, domain.NewID(), account); err == nil {
		t.Fatal("recovery trusted a journal of another account")
	}
	// A lease of another Runner Device is not this Worker's to clean up.
	foreign := account
	state := *account.Subscription
	lease := *state.Lease
	lease.MachineID = domain.NewID()
	state.Lease, foreign.Subscription = &lease, &state
	if err := runGrokRecovery(context.Background(), f.config, client, f.credential, f.account, foreign); err == nil {
		t.Fatal("recovery cleaned a lease of another Runner Device")
	}
	// An obstruction keeps the cleanup unproven: nothing is reported.
	if err := os.Mkdir(filepath.Join(f.home, security.OwnedTemporaryPrefix(f.authPath())+"stuck"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runGrokRecovery(context.Background(), f.config, client, f.credential, f.account, account); err == nil || domain.SafeError(err).Message != "The private Grok credential cleanup could not be confirmed." {
		t.Fatal("recovery reported an unproven cleanup", err)
	}
	if len(f.finishes) != 0 {
		t.Fatal("recovery reported without proof")
	}
}

// A missing pathname cannot prove that the journaled credential directory was
// retired. A profile home that was moved away with its auth.json is still
// registered, so recovery reports nothing and keeps the lease fenced; a profile
// that an explicit deletion removed (directory and registry entry) took the
// credential with it and is clean.
func TestGrokRecoveryDoesNotTreatAMissingHomePathAsClean(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory replacement semantics differ")
	}
	for _, test := range []struct {
		name    string
		remove  func(t *testing.T, f *grokExecutionFixture, key nativeprofile.Key, home string)
		cleaned bool
	}{
		{"moved away", func(t *testing.T, f *grokExecutionFixture, _ nativeprofile.Key, home string) {
			if err := os.Rename(filepath.Dir(home), filepath.Join(t.TempDir(), "moved")); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"explicitly deleted", func(t *testing.T, f *grokExecutionFixture, key nativeprofile.Key, _ string) {
			registry, err := nativeProfiles(f.config, f.credential.MachineID)
			if err != nil {
				t.Fatal(err)
			}
			if deleted, err := registry.Delete(context.Background(), nativeprofile.DeleteScope{Conversation: key.Conversation}); err != nil || deleted != 1 {
				t.Fatalf("deleted %d profiles: %v", deleted, err)
			}
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGrokExecutionFixture(t)
			key := nativeprofile.Key{Harness: domain.GrokBuild, Purpose: nativeprofile.PurposeConversation, Account: f.account, Conversation: domain.NewID()}
			lease, err := conversationProfile(context.Background(), f.config, f.credential.MachineID, key)
			if err != nil {
				t.Fatal(err)
			}
			home := lease.Root
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, subscription.GrokAuthFileName), grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := security.PrivateDir(filepath.Join(f.config.Root, "processes")); err != nil {
				t.Fatal(err)
			}
			account, job := f.grokRecoveryAccount(t, managedSubscriptionJournal{GrokHome: home, NativeStarted: true})
			if err := security.PrivateDir(filepath.Join(f.config.Root, "processes", string(job))); err != nil {
				t.Fatal(err)
			}
			test.remove(t, f, key, home)
			client := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.credential.Endpoint)
			err = runGrokRecovery(context.Background(), f.config, client, f.credential, f.account, account)
			if test.cleaned {
				if err != nil {
					t.Fatal(err)
				}
				if finish := f.onlyFinish(t); !finish.CleanupConfirmed || finish.Succeeded {
					t.Fatal("recovery of a deleted profile was not a cleanup-only report")
				}
				return
			}
			if err == nil || domain.SafeError(err).Message != "The private Grok credential cleanup could not be confirmed." {
				t.Fatalf("recovery reported a cleanup it cannot prove: %v", err)
			}
			if len(f.finishes) != 0 {
				t.Fatal("recovery released the lease while the credential may remain")
			}
		})
	}
}

func TestGrokRecoveryOfALeaseThatNeverWroteACredentialStillReports(t *testing.T) {
	f := newGrokExecutionFixture(t)
	account, _ := f.grokRecoveryAccount(t, managedSubscriptionJournal{})
	client := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.credential.Endpoint)
	if err := runGrokRecovery(context.Background(), f.config, client, f.credential, f.account, account); err != nil {
		t.Fatal(err)
	}
	if finish := f.onlyFinish(t); !finish.CleanupConfirmed || finish.Succeeded {
		t.Fatal("recovery of an untouched lease was not a cleanup-only report")
	}
}

// The durable claim precedes the protected delivery. A Take response lost after
// the server committed the lease leaves a journal without generation or lease
// revision; the original Worker still reports the cleanup-only Finish, naming the
// generation and revision from the authenticated server lease, instead of
// leaving the account fenced forever.
func TestGrokRecoveryOfALeaseWhoseTakeResponseWasLostStillReports(t *testing.T) {
	f := newGrokExecutionFixture(t)
	account, _ := f.grokRecoveryAccount(t, managedSubscriptionJournal{})
	path := filepath.Join(f.config.Root, "managed-auth", string(f.lease)+".json")
	raw, err := security.ReadPrivate(path, 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	var journal managedSubscriptionJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		t.Fatal(err)
	}
	journal.Generation, journal.LeaseRevision = "", 0
	if err := writeJSON(path, journal); err != nil {
		t.Fatal(err)
	}
	client := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.credential.Endpoint)
	if err := runGrokRecovery(context.Background(), f.config, client, f.credential, f.account, account); err != nil {
		t.Fatal(err)
	}
	finish := f.onlyFinish(t)
	if !finish.CleanupConfirmed || finish.Succeeded || finish.LeaseId != string(f.lease) || finish.GenerationId != string(f.generation) || finish.Mutation.ExpectedRevision != account.Subscription.Lease.Revision {
		t.Fatal("recovery did not report a cleanup-only Finish from the authenticated server lease")
	}
	// Once a field was persisted it must agree with the server lease, and a
	// journal that already named a home is no pre-delivery claim.
	for name, edit := range map[string]func(*managedSubscriptionJournal){
		"foreign generation": func(j *managedSubscriptionJournal) { j.Generation = domain.NewID() },
		"foreign revision":   func(j *managedSubscriptionJournal) { j.Generation, j.LeaseRevision = "", 99 },
		"named home":         func(j *managedSubscriptionJournal) { j.Generation, j.GrokHome = "", f.home },
	} {
		t.Run(name, func(t *testing.T) {
			g := newGrokExecutionFixture(t)
			other, _ := g.grokRecoveryAccount(t, managedSubscriptionJournal{})
			p := filepath.Join(g.config.Root, "managed-auth", string(g.lease)+".json")
			r, err := security.ReadPrivate(p, 16<<10)
			if err != nil {
				t.Fatal(err)
			}
			var j managedSubscriptionJournal
			if err := json.Unmarshal(r, &j); err != nil {
				t.Fatal(err)
			}
			edit(&j)
			if err := writeJSON(p, j); err != nil {
				t.Fatal(err)
			}
			c := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, g.credential.Endpoint)
			if err := runGrokRecovery(context.Background(), g.config, c, g.credential, g.account, other); err == nil {
				t.Fatal("recovery trusted a journal that disagrees with the server lease")
			}
			if len(g.finishes) != 0 {
				t.Fatal("recovery reported from an untrusted journal")
			}
		})
	}
}

// Follow-up input resumes no native history of a managed account: a continuation
// or fork never takes a lease and never receives the bundle.
func TestGrokExecutionNeverTakesALeaseForAContinuationOrFork(t *testing.T) {
	for name, edit := range map[string]func(*domain.ExecutionJobInput){
		"continuation": func(input *domain.ExecutionJobInput) { input.Continuation = &domain.ExecutionContinuation{} },
		"fork":         func(input *domain.ExecutionJobInput) { input.Fork = &domain.ForkExecution{} },
	} {
		t.Run(name, func(t *testing.T) {
			f := newGrokExecutionFixture(t)
			f.config.execution = &PublicationConfig{Credential: f.credential, Instance: domain.NewID(), Assignment: &pb.Resource{Revision: 2}}
			input := domain.ExecutionJobInput{AccountID: f.account}
			edit(&input)
			managed, err := beginGrokSubscriptionExecution(context.Background(), f.config, domain.NewID(), input)
			if managed != nil || err == nil || domain.SafeError(err).Code != domain.Unsupported {
				t.Fatal("a continuation received a managed lease", err)
			}
			if f.lease != "" || len(f.finishes) != 0 {
				t.Fatal("a lease was taken for a continuation")
			}
		})
	}
}
