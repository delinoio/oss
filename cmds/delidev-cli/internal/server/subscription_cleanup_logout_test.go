// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

var errFixtureConfirmation = domain.Fail(domain.ConfirmationRequired, "Fixture operating-system credential access needs confirmation.", "Authorize the fixture store and retry.")

// terminalServerLogout drives a real server LOGIN and LOGOUT. The injected
// ConfirmationRequired vault failure happens after the LOGOUT published its
// disconnected metadata, producing the retained terminal shape under test.
func terminalServerLogout(t *testing.T) (*subscriptionFixture, domain.ID, *serverLoginFixture) {
	t.Helper()
	f := unreferencedInitialSubscription(t)
	f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	login := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("terminal-logout-fixture", "first", time.Now().UTC())}
	done := f.serverRun(login)
	awaitServerFixture(t, login.started)
	close(login.finish)
	awaitServerFixture(t, done)
	if _, a := f.record(); a.Connection == nil || a.Subscription.Generation == "" {
		t.Fatal("fixture login did not connect")
	}
	f.secrets.deleteError = errFixtureConfirmation
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	awaitServerFixture(t, f.serverRun(&serverLoginFixture{}))
	_, a := f.record()
	if !serverLogoutRecoveryCandidate(a) || a.Subscription.ServerOperation.ID != domain.ID(op.OperationId) {
		t.Fatal("terminal logout did not reach the retained recovery shape", a.Health, a.Subscription)
	}
	return f, domain.ID(op.OperationId), login
}

func (f *subscriptionFixture) accountRefs() int {
	f.secrets.mu.Lock()
	defer f.secrets.mu.Unlock()
	count := 0
	for ref := range f.secrets.values {
		if ref.Owner == f.input.AccountID {
			count++
		}
	}
	return count
}

func (f *subscriptionFixture) cleanupChildren(request string) []failedCleanupCheckpoint {
	f.t.Helper()
	var out []failedCleanupCheckpoint
	err := f.service.Store.Read(failedLoginContext(), func(tx *store.Tx) error {
		children, err := tx.Jobs("", domain.ID(request), "", "", 10)
		if err != nil {
			return err
		}
		for _, row := range children {
			_, _, checkpoint, err := decodeFailedCleanupAccount(row, domain.ID(request))
			if err != nil {
				return err
			}
			out = append(out, checkpoint)
		}
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *subscriptionFixture) reopenStore() {
	f.t.Helper()
	root := f.service.Store.Root()
	if err := f.service.Store.Close(); err != nil {
		f.t.Fatal(err)
	}
	reopened, err := store.Open(failedLoginContext(), root)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = reopened.Close() })
	f.service.Store = reopened
}

func TestTerminalServerLogoutCleanupConfirmationThenDeletion(t *testing.T) {
	f, operation, login := terminalServerLogout(t)
	prepareFailedLoginRuntime(t, f, operation)
	if f.accountRefs() == 0 {
		t.Fatal("fixture has no protected reference to clean")
	}
	var logs bytes.Buffer
	f.service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	request := f.failedLoginDeletion()
	err := f.deleteFailedLogin(request)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "operating-system credential access was not confirmed") {
		t.Fatal("denied OS authorization did not retain the account with a safe cause", err)
	}
	children := f.cleanupChildren(request.Mutation.RequestId)
	if len(children) != 1 || !children[0].Started || !children[0].NativeConfirmed || !children[0].CredentialsStarted || children[0].CredentialsConfirmed || children[0].ProblemCode != domain.ConfirmationRequired {
		t.Fatal("native proof was not persisted before the vault attempt", children)
	}
	if _, a := f.record(); !serverLogoutRecoveryCandidate(a) || a.Subscription.ServerOperation.CleanupPhase != "" {
		t.Fatal("retained account lost its recovery fence or reused a LOGIN checkpoint")
	}
	deletes := func() int {
		f.secrets.mu.Lock()
		defer f.secrets.mu.Unlock()
		return f.secrets.deletes
	}()
	// Replay, status polling, remount and reconnect all read the terminal child.
	for range 3 {
		if err := f.deleteFailedLogin(request); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatal("terminal replay changed its answer", err)
		}
	}
	f.secrets.mu.Lock()
	replayDeletes := f.secrets.deletes
	f.secrets.mu.Unlock()
	if replayDeletes != deletes {
		t.Fatal("terminal replay reopened the credential attempt")
	}
	// The proof and terminal outcome survive a restart without a second attempt.
	f.reopenStore()
	if err := f.deleteFailedLogin(request); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("restart reopened a terminal request", err)
	}
	// The operating system is now authorized; only a fresh confirmation proceeds.
	f.secrets.deleteError = nil
	next := f.failedLoginDeletion()
	if err := f.deleteFailedLogin(next); err != nil {
		t.Fatal(err)
	}
	if f.accountRefs() != 0 {
		t.Fatal("original protected reference retained")
	}
	if _, err := f.service.Store.Get(failedLoginContext(), domain.AccountKind, f.input.AccountID); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("account remains", err)
	}
	if login.calls.Load() != 1 {
		t.Fatal("cleanup replayed native login")
	}
	if err := f.deleteFailedLogin(next); err != nil {
		t.Fatal("deletion receipt did not replay", err)
	}
	for _, line := range strings.Split(logs.String(), "\n") {
		if !strings.Contains(line, "server_logout_cleanup_step") {
			continue
		}
		for _, protected := range []string{string(f.input.AccountID), "synthetic-private-login-token", "synthetic-staged-token"} {
			if strings.Contains(line, protected) {
				t.Fatal("cleanup logs exposed protected content")
			}
		}
	}
	if !strings.Contains(logs.String(), `"phase":"native"`) || !strings.Contains(logs.String(), `"result":"confirmed"`) {
		t.Fatal("cleanup did not log closed phase/result fields")
	}
}

func TestTerminalServerLogoutCleanupRestartAfterNativeProof(t *testing.T) {
	f, operation, _ := terminalServerLogout(t)
	prepareFailedLoginRuntime(t, f, operation)
	request := f.failedLoginDeletion()
	if err := f.deleteFailedLogin(request); err == nil {
		t.Fatal("denied vault attempt deleted the account")
	}
	f.reopenStore()
	// A new deletion after restart reproves the original ownership journals
	// idempotently and never launches a second native logout or login.
	f.secrets.deleteError = nil
	if err := f.deleteFailedLogin(f.failedLoginDeletion()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Store.Get(failedLoginContext(), domain.AccountKind, f.input.AccountID); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("account remains")
	}
}

func TestTerminalServerLogoutCleanupResumesPersistedNativeProof(t *testing.T) {
	f, operation, _ := terminalServerLogout(t)
	prepareFailedLoginRuntime(t, f, operation)
	r, a := f.record()
	request := domain.NewID()
	parent := domain.NewID()
	child := domain.NewID()
	actor := domain.Principal{Type: domain.OwnerDevice}
	_, err := f.service.Store.Mutate(failedLoginContext(), domain.NewID(), "fixture.logout.child", nil, func(tx *store.Tx) (any, error) {
		target := serverLogoutCleanupChild(a, f.input.AccountID, r.Revision, r.Revision, request)
		raw, _ := json.Marshal(target)
		out, _ := json.Marshal(failedCleanupCheckpoint{Revision: r.Revision, Started: true, NativeConfirmed: true, Outcome: pb.FailedSubscriptionCleanupOutcome_FAILED_SUBSCRIPTION_CLEANUP_OUTCOME_PENDING})
		now := time.Now().UTC()
		if _, err := tx.PutJob(child, 0, "", "", domain.Job{Type: domain.CleanupFailedSubscriptionJob, State: domain.JobQueued, ParentID: parent, Input: raw, Output: out, AcceptedAt: now}); err != nil {
			return nil, err
		}
		in, _ := json.Marshal(failedCleanupIntent{Version: 1, RequestID: request, Input: failedCleanupInput{Actor: actor, ServerID: f.service.Identity.ServerID}, Total: 1})
		counts, _ := json.Marshal(failedCleanupCounts{})
		_, err := tx.PutJob(parent, 0, "", "", domain.Job{Type: domain.CleanupFailedSubscriptionsJob, State: domain.JobQueued, Input: in, Output: counts, AcceptedAt: now})
		return struct{}{}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	f.secrets.deleteError = nil
	// Remove the original journals: a persisted proof must not require them again.
	if err := os.RemoveAll(filepath.Join(f.service.Store.Root(), "subscription-runtime")); err != nil {
		t.Fatal(err)
	}
	row, err := f.service.Store.Get(failedLoginContext(), domain.JobKind, parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.runFailedSubscriptionCleanup(failedLoginContext(), row); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Store.Get(failedLoginContext(), domain.AccountKind, f.input.AccountID); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("persisted native proof did not resume cleanup")
	}
}

func TestTerminalServerLogoutCleanupRejectsOtherAuthority(t *testing.T) {
	scenarios := map[string]func(*subscriptionFixture, *domain.Account){
		"connection": func(f *subscriptionFixture, a *domain.Account) {
			a.Connection = &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
		},
		"replacement-generation": func(f *subscriptionFixture, a *domain.Account) {
			a.Subscription.Generation = domain.NewID()
			a.Subscription.IdentityCommitment = strings.Repeat("a", 64)
		},
		"worker-owner":   func(f *subscriptionFixture, a *domain.Account) { a.Subscription.OwnerMachineID = f.input.MachineID },
		"native-profile": func(f *subscriptionFixture, a *domain.Account) { a.Subscription.NativeProfileID = domain.NewID() },
		"worker-lease": func(f *subscriptionFixture, a *domain.Account) {
			a.Subscription.Lease = &domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 1, Action: domain.SubscriptionExecute, MachineID: f.input.MachineID, DeviceID: f.device, InstanceID: f.instance, Epoch: f.service.subscriptionServerEpoch(), StartedAt: time.Now().UTC()}
		},
		"quota-work": func(f *subscriptionFixture, a *domain.Account) {
			a.Subscription.Observation = &domain.SubscriptionObservationOperation{Phase: domain.SubscriptionObservationUncertain}
		},
		"reset-credits": func(f *subscriptionFixture, a *domain.Account) {
			a.Subscription.ResetCredits = &domain.SubscriptionResetCredits{}
		},
		"foreign-removal":  func(f *subscriptionFixture, a *domain.Account) { a.Removal = &domain.AccountRemoval{} },
		"native-restarted": func(f *subscriptionFixture, a *domain.Account) { a.Subscription.ServerOperation.NativeStarted = true },
		"login-operation": func(f *subscriptionFixture, a *domain.Account) {
			a.Subscription.ServerOperation.Action = domain.SubscriptionLogin
		},
		"foreign-pending":   func(f *subscriptionFixture, a *domain.Account) { a.Subscription.Pending.ID = domain.NewID() },
		"no-pending":        func(f *subscriptionFixture, a *domain.Account) { a.Subscription.Pending = nil },
		"no-original-proof": func(f *subscriptionFixture, a *domain.Account) { a.Subscription.ServerOperation.Generation = "" },
		"login-checkpoint": func(f *subscriptionFixture, a *domain.Account) {
			a.Subscription.ServerOperation.CleanupPhase = domain.SubscriptionNativeCleanupConfirmed
		},
		"recovery-only-flag": func(f *subscriptionFixture, a *domain.Account) { a.Subscription.RecoveryRequired = false },
		"healthy":            func(f *subscriptionFixture, a *domain.Account) { a.Health = domain.AccountDisconnected },
	}
	for name, mutate := range scenarios {
		t.Run(name, func(t *testing.T) {
			f, operation, _ := terminalServerLogout(t)
			prepareFailedLoginRuntime(t, f, operation)
			f.changeFailedLogin(func(a *domain.Account) { mutate(f, a) })
			f.secrets.deleteError = nil
			before, _ := f.record()
			enumerations := f.secrets.enumerations
			request := f.failedLoginDeletion()
			err := f.deleteFailedLogin(request)
			if name == "healthy" {
				// A disconnected record without recovery metadata is ordinary deletion.
				return
			}
			if err == nil {
				t.Fatal("unauthorized cleanup deleted the account")
			}
			after, _ := f.record()
			if before.Revision != after.Revision || f.secrets.enumerations != enumerations || f.accountRefs() == 0 {
				t.Fatal("rejected cleanup changed state or reached the vault")
			}
		})
	}
}

func TestTerminalServerLogoutCleanupRejectsUnprovenOwnership(t *testing.T) {
	for _, scenario := range []string{"missing-index", "missing-journal", "live-controller", "changed-revision"} {
		t.Run(scenario, func(t *testing.T) {
			f, operation, _ := terminalServerLogout(t)
			f.secrets.deleteError = nil
			root := f.service.Store.Root()
			baseline := f.secrets.enumerations
			request := f.failedLoginDeletion()
			switch scenario {
			case "missing-journal":
				prepareFailedLoginRuntime(t, f, operation)
				journals, err := filepath.Glob(filepath.Join(root, "subscription-runtime", "processes", string(operation), "*", "ownership.json"))
				if err != nil || len(journals) != 1 {
					t.Fatal("original journal missing", err)
				}
				if err := os.Remove(journals[0]); err != nil {
					t.Fatal(err)
				}
			case "live-controller":
				config := failedLoginProcessConfig(t, root, operation)
				config.Env = append(config.Env, "DELIDEV_SERVER_CLEANUP_PROCESS_BLOCK=1")
				handle, err := process.Start(context.Background(), config)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = handle.Close() }()
				if err := handle.Resume(); err != nil {
					t.Fatal(err)
				}
			case "changed-revision":
				prepareFailedLoginRuntime(t, f, operation)
				f.changeFailedLogin(func(a *domain.Account) { a.Alias = "Changed after confirmation" })
			}
			err := f.deleteFailedLogin(request)
			if err == nil {
				t.Fatal("unproven ownership allowed cleanup")
			}
			if scenario != "changed-revision" && f.secrets.enumerations != baseline {
				t.Fatal("unproven ownership reached protected credentials")
			}
			if _, a := f.record(); !serverLogoutRecoveryCandidate(a) {
				t.Fatal("rejected cleanup released the recovery fence")
			}
			if f.accountRefs() == 0 {
				t.Fatal("rejected cleanup removed credentials")
			}
		})
	}
}

func TestTerminalServerLogoutCleanupKeepsReferencedAccount(t *testing.T) {
	f := newSubscriptionFixture(t)
	f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	login := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("referenced-logout-fixture", "first", time.Now().UTC())}
	done := f.serverRun(login)
	awaitServerFixture(t, login.started)
	close(login.finish)
	awaitServerFixture(t, done)
	f.secrets.deleteError = errFixtureConfirmation
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	awaitServerFixture(t, f.serverRun(&serverLoginFixture{}))
	prepareFailedLoginRuntime(t, f, domain.ID(op.OperationId))
	f.secrets.deleteError = nil
	// The original fixture account is still used by its Agent/Project, so the
	// complete reference checks still run after credential cleanup.
	if err := f.deleteFailedLogin(f.failedLoginDeletion()); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("late retained reference did not block deletion", err)
	}
	if _, a := f.record(); a.Health != domain.AccountDisconnected || a.Subscription.RecoveryRequired || a.Subscription.Pending != nil {
		t.Fatal("confirmed cleanup did not clear only the matching recovery state", a.Health)
	}
	if f.accountRefs() != 0 {
		t.Fatal("credential cleanup did not complete before the reference check")
	}
}

func TestFailedInitialLoginConfirmationRequiredRetriesWithNewRequest(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, false)
	ref := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	if _, err := f.secrets.Put(context.Background(), ref, []byte("synthetic-staged-token")); err != nil {
		t.Fatal(err)
	}
	f.secrets.deleteError = errFixtureConfirmation
	request := f.failedLoginDeletion()
	err := f.deleteFailedLogin(request)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("ConfirmationRequired was not reported", err)
	}
	if _, a := f.record(); a.Subscription.ServerOperation.CleanupPhase != domain.SubscriptionNativeCleanupConfirmed || !a.Subscription.RecoveryRequired {
		t.Fatal("retained account lost its native checkpoint")
	}
	f.secrets.deleteError = nil
	if err := f.deleteFailedLogin(request); err == nil {
		t.Fatal("terminal request retried the vault")
	}
	if err := f.deleteFailedLogin(f.failedLoginDeletion()); err != nil {
		t.Fatal("new confirmed request failed after authorization", err)
	}
	if _, err := f.secrets.Get(context.Background(), ref); err == nil {
		t.Fatal("original reference retained")
	}
}

func TestTerminalServerLogoutIsNotSelectedByBatchCleanup(t *testing.T) {
	f, _, _ := terminalServerLogout(t)
	excludeUnrelatedBatchFixture(t, f)
	if a := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID()); a.Job.Total != 0 {
		t.Fatal("batch cleanup selected a recovery-only server logout", a.Job)
	}
}

func TestSubscriptionCleanupRecoveryCapabilityAdvertised(t *testing.T) {
	f := newSubscriptionFixture(t)
	response, err := f.service.GetStatus(failedLoginContext(), connect.NewRequest(&pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	found := map[pb.SystemCapability]bool{}
	for _, capability := range response.Msg.Capabilities {
		found[capability] = true
	}
	if !found[pb.SystemCapability_SYSTEM_CAPABILITY_SUBSCRIPTION_CLEANUP_RECOVERY_V1] || !found[pb.SystemCapability_SYSTEM_CAPABILITY_FAILED_SUBSCRIPTION_CLEANUP_V1] {
		t.Fatal("cleanup capabilities were not both advertised")
	}
}
