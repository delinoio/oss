// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http"
)

func unreferencedInitialSubscription(t *testing.T) *subscriptionFixture {
	t.Helper()
	f := newSubscriptionFixture(t)
	_, account := f.record()
	id := domain.NewID()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.initial.account.deletion", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.AccountKind, id, 0, "", "", account)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.input.AccountID = id
	return f
}

func failedLoginContext() context.Context {
	return domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
}

func (f *subscriptionFixture) changeFailedLogin(change func(*domain.Account)) {
	f.t.Helper()
	_, err := f.service.Store.Mutate(failedLoginContext(), domain.NewID(), "fixture.failed.login.state", nil, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		change(&a)
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *subscriptionFixture) deleteFailedLogin(request *pb.DeleteConfigurationRequest) error {
	f.t.Helper()
	client := delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, f.http.URL)
	_, err := client.DeleteConfiguration(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	return err
}

func (f *subscriptionFixture) failedLoginDeletion() *pb.DeleteConfigurationRequest {
	r, _ := f.record()
	return &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: &pb.Mutation{Id: string(r.ID), ExpectedRevision: r.Revision, RequestId: string(domain.NewID())}}
}

func TestFailedServerLoginCleanupAllowsDeletion(t *testing.T) {
	for _, scenario := range []string{"failed", "canceled", "shutdown"} {
		t.Run(scenario, func(t *testing.T) {
			f := unreferencedInitialSubscription(t)
			op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			n := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("failed-login-fixture", "first", time.Now().UTC())}
			var done <-chan struct{}
			want := domain.SubscriptionFailed
			if scenario == "failed" {
				n.startError = domain.Fail(domain.Unauthenticated, "private-login-failure", "private-guidance")
				done = f.serverRun(n)
			} else {
				want = domain.SubscriptionCanceled
				f.service.subscriptionOpen = func(context.Context, string, domain.ID, []byte, *slog.Logger) (serverSubscriptionNative, error) {
					return n, nil
				}
				ctx, stop := context.WithCancel(failedLoginContext())
				defer stop()
				finished := make(chan struct{})
				go func() { defer close(finished); f.service.runServerSubscription(ctx, f.input.AccountID) }()
				done = finished
				awaitServerFixture(t, n.started)
				if scenario == "shutdown" {
					stop()
				} else {
					r, _ := f.record()
					_, err := f.client.CancelSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{Id: string(r.ID), ExpectedRevision: r.Revision, RequestId: string(domain.NewID())}}))
					if err != nil {
						t.Fatal(err)
					}
					close(n.finish)
				}
			}
			awaitServerFixture(t, done)
			_, a := f.record()
			st := a.Subscription
			if a.Health != domain.AccountDisconnected || a.Connection != nil || st.Pending != nil || st.RecoveryRequired || st.ServerOperation.NativeStarted || st.ServerOperation.ID != domain.ID(op.OperationId) || st.ServerOperation.State != want || st.ServerOperation.CleanupPhase != domain.SubscriptionCredentialCleanupConfirmed {
				t.Fatal("confirmed failed login cleanup did not release account deletion", a)
			}
			if scenario == "failed" && (st.ServerOperation.Diagnostic == nil || st.ServerOperation.Diagnostic.Code != domain.Unauthenticated) {
				t.Fatal("cleanup erased the original failure")
			}
			request := f.failedLoginDeletion()
			if err := f.deleteFailedLogin(request); err != nil {
				t.Fatal(err)
			}
			if err := f.deleteFailedLogin(request); err != nil {
				t.Fatal("deletion replay", err)
			}
			if n.calls.Load() != 1 {
				t.Fatal("login was replayed")
			}
		})
	}
}

func legacyFailedServerLogin(t *testing.T, claimed bool) (*subscriptionFixture, domain.ID) {
	t.Helper()
	f := unreferencedInitialSubscription(t)
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	f.changeFailedLogin(func(a *domain.Account) {
		st := a.Subscription
		st.ServerOperation.State = domain.SubscriptionRecovery
		st.ServerOperation.NativeStarted = claimed
		st.ServerOperation.Epoch = domain.NewID()
		if claimed {
			st.Pending.Phase = domain.SubscriptionClaimed
		}
		st.RecoveryRequired = true
		a.Health = domain.AccountFailed
	})
	return f, domain.ID(op.OperationId)
}

func TestServerSubscriptionCleanupProcessHelper(t *testing.T) {
	if os.Getenv("DELIDEV_SERVER_CLEANUP_PROCESS_FIXTURE") != "1" {
		return
	}
	if os.Getenv("DELIDEV_SERVER_CLEANUP_PROCESS_BLOCK") == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
}

func failedLoginProcessConfig(t *testing.T, root string, owner domain.ID) process.Config {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return process.Config{Directory: filepath.Join(root, "subscription-runtime", "processes"), OwnerID: owner, Executable: executable, Cwd: root, Args: []string{"-test.run=^TestServerSubscriptionCleanupProcessHelper$"}, Env: []string{"DELIDEV_SERVER_CLEANUP_PROCESS_FIXTURE=1"}, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
}

func prepareFailedLoginRuntime(t *testing.T, f *subscriptionFixture, owner domain.ID) string {
	t.Helper()
	root := f.service.Store.Root()
	if err := process.Run(context.Background(), failedLoginProcessConfig(t, root, owner)); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "subscription-runtime", "auth", string(owner))
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(home, "auth.json"), []byte("synthetic-private-login-token")); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(root, "subscription-runtime", "probes", string(owner)+"-12345")
	if err := security.PrivateDir(probe); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(probe, "fixture"), []byte("synthetic-probe")); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestLegacyFailedServerLoginCleanupPreservesOtherOwnership(t *testing.T) {
	for _, removed := range []bool{false, true} {
		t.Run(map[bool]string{false: "retained-runtime", true: "previously-removed-runtime"}[removed], func(t *testing.T) {
			f, operation := legacyFailedServerLogin(t, true)
			home := prepareFailedLoginRuntime(t, f, operation)
			if removed {
				info, err := os.Stat(home)
				if err != nil {
					t.Fatal(err)
				}
				if err := subscription.CleanupRuntime(home, info); err != nil {
					t.Fatal(err)
				}
			}
			foreign := filepath.Join(f.service.Store.Root(), "subscription-runtime", "probes", string(domain.NewID())+"-12345")
			if err := security.PrivateDir(foreign); err != nil {
				t.Fatal(err)
			}
			ref := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
			other := credentials.Ref{Owner: domain.NewID(), ID: domain.NewID(), Purpose: credentials.AccountLogin}
			_, _ = f.secrets.Put(context.Background(), ref, []byte("synthetic-staged-token"))
			_, _ = f.secrets.Put(context.Background(), other, []byte("foreign-staged-token"))
			stale := f.failedLoginDeletion()
			var logs bytes.Buffer
			f.service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			if err := f.service.recoverFailedServerLogin(failedLoginContext(), f.input.AccountID, operation); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(home); !os.IsNotExist(err) {
				t.Fatal("private runtime retained")
			}
			if _, err := os.Stat(foreign); err != nil {
				t.Fatal("unrelated probe was removed", err)
			}
			if _, err := f.secrets.Get(context.Background(), other); err != nil {
				t.Fatal("unrelated protected reference removed")
			}
			if _, err := f.secrets.Get(context.Background(), ref); err == nil {
				t.Fatal("staged protected reference retained")
			}
			for _, protected := range []string{home, string(f.input.AccountID), "synthetic-staged-token", "synthetic-private-login-token"} {
				if strings.Contains(logs.String(), protected) {
					t.Fatal("cleanup logs exposed protected content")
				}
			}
			if err := f.deleteFailedLogin(stale); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("stale revision did not retain its conflict", err)
			}
			if err := f.deleteFailedLogin(f.failedLoginDeletion()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFailedServerLoginCleanupCheckpointSurvivesRestart(t *testing.T) {
	f, operation := legacyFailedServerLogin(t, true)
	home := prepareFailedLoginRuntime(t, f, operation)
	ref := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	_, _ = f.secrets.Put(context.Background(), ref, []byte("synthetic-protected-token"))
	f.secrets.deleteError = domain.Fail(domain.Unavailable, "Fixture vault unavailable.", "Retry the cleanup.")
	if err := f.service.recoverFailedServerLogin(failedLoginContext(), f.input.AccountID, operation); err == nil {
		t.Fatal("failed vault cleanup released ownership")
	}
	_, a := f.record()
	if a.Subscription.ServerOperation.CleanupPhase != domain.SubscriptionNativeCleanupConfirmed || !a.Subscription.RecoveryRequired || a.Subscription.Pending == nil || a.Health != domain.AccountFailed {
		t.Fatal("native cleanup checkpoint or credential fence was lost")
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatal("native runtime was not removed")
	}
	if err := f.deleteFailedLogin(f.failedLoginDeletion()); err == nil {
		t.Fatal("native cleanup alone permitted deletion")
	}
	// Round-trip the durable checkpoint through startup, without requiring the
	// already retired process journals or changing a user's later metadata edit.
	f.service.subscriptionEpoch = domain.NewID()
	if err := f.service.initializeServerSubscriptions(failedLoginContext()); err != nil {
		t.Fatal(err)
	}
	f.changeFailedLogin(func(a *domain.Account) { a.Alias = "Retained edited alias" })
	f.secrets.deleteError = nil
	if err := f.service.recoverFailedServerLogin(failedLoginContext(), f.input.AccountID, operation); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if a.Alias != "Retained edited alias" || a.Subscription.ServerOperation.CleanupPhase != domain.SubscriptionCredentialCleanupConfirmed || a.Subscription.RecoveryRequired || a.Subscription.Pending != nil {
		t.Fatal("restarted cleanup did not preserve metadata and release ownership")
	}
	if err := f.deleteFailedLogin(f.failedLoginDeletion()); err != nil {
		t.Fatal(err)
	}
}

func TestFailedServerLoginCleanupPreservesQueuedCancellation(t *testing.T) {
	f, operation := legacyFailedServerLogin(t, false)
	f.secrets.referenceError = domain.Fail(domain.Unavailable, "Fixture vault unavailable.", "Retry after restart.")
	if err := f.service.recoverFailedServerLogin(failedLoginContext(), f.input.AccountID, operation); err == nil {
		t.Fatal("vault failure released cleanup")
	}
	r, _ := f.record()
	_, err := f.client.CancelSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{Id: string(r.ID), ExpectedRevision: r.Revision, RequestId: string(domain.NewID())}}))
	if err != nil {
		t.Fatal(err)
	}
	f.secrets.referenceError = nil
	if err := f.service.recoverFailedServerLogin(failedLoginContext(), f.input.AccountID, operation); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if a.Subscription.ServerOperation.State != domain.SubscriptionCanceled || a.Subscription.Pending != nil || a.Subscription.RecoveryRequired {
		t.Fatal("cleanup erased accepted queued cancellation")
	}
}

func TestFailedServerLoginCleanupRejectsUnprovenRuntime(t *testing.T) {
	for _, scenario := range []string{"missing-index", "missing-journal", "foreign-journal", "symlink", "live-controller"} {
		t.Run(scenario, func(t *testing.T) {
			f, operation := legacyFailedServerLogin(t, true)
			root := f.service.Store.Root()
			var home string
			switch scenario {
			case "missing-index":
				home = filepath.Join(root, "subscription-runtime", "auth", string(operation))
				if err := security.PrivateDir(home); err != nil {
					t.Fatal(err)
				}
			case "live-controller":
				config := failedLoginProcessConfig(t, root, operation)
				config.Env = append(config.Env, "DELIDEV_SERVER_CLEANUP_PROCESS_BLOCK=1")
				handle, err := process.Start(context.Background(), config)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := handle.Close(); err != nil {
						t.Error(err)
					}
				}()
				if err := handle.Resume(); err != nil {
					t.Fatal(err)
				}
			default:
				home = prepareFailedLoginRuntime(t, f, operation)
				journals, err := filepath.Glob(filepath.Join(root, "subscription-runtime", "processes", string(operation), "*", "ownership.json"))
				if err != nil || len(journals) != 1 {
					t.Fatal("original process journal missing", err)
				}
				switch scenario {
				case "missing-journal":
					if err := os.Remove(journals[0]); err != nil {
						t.Fatal(err)
					}
				case "foreign-journal":
					raw, err := security.ReadPrivate(journals[0], 1<<20)
					if err != nil {
						t.Fatal(err)
					}
					var journal map[string]any
					if json.Unmarshal(raw, &journal) != nil {
						t.Fatal("invalid fixture journal")
					}
					journal["owner_id"] = domain.NewID()
					raw, _ = json.Marshal(journal)
					if err := security.WriteAtomic(journals[0], raw); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					info, _ := os.Stat(home)
					if err := subscription.CleanupRuntime(home, info); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(t.TempDir(), home); err != nil {
						t.Skip("native symlink privilege unavailable")
					}
				}
			}
			if err := f.service.recoverFailedServerLogin(failedLoginContext(), f.input.AccountID, operation); err == nil {
				t.Fatal("unproven native cleanup released recovery")
			}
			_, a := f.record()
			if !a.Subscription.RecoveryRequired || a.Subscription.ServerOperation.CleanupPhase != "" || a.Subscription.Pending == nil {
				t.Fatal("unproven runtime lost its original fence")
			}
			if home != "" {
				if _, err := os.Lstat(home); err != nil {
					t.Fatal("unproven runtime was deleted", err)
				}
			}
			if f.secrets.enumerations != 0 {
				t.Fatal("unproven process ownership reached protected credentials")
			}
		})
	}
}

func TestFailedServerLoginCleanupRejectsOtherAccountAuthority(t *testing.T) {
	for _, scenario := range []string{"connection", "generation", "worker", "native-owner", "refresh", "foreign-operation", "observation"} {
		t.Run(scenario, func(t *testing.T) {
			f, operation := legacyFailedServerLogin(t, false)
			f.changeFailedLogin(func(a *domain.Account) {
				st := a.Subscription
				switch scenario {
				case "connection":
					a.Connection = &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
				case "generation":
					st.Generation = domain.NewID()
					st.IdentityCommitment = strings.Repeat("a", 64)
				case "native-owner":
					st.OwnerMachineID = f.input.MachineID
				case "worker":
					st.Lease = &domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 1, Action: domain.SubscriptionExecute, MachineID: f.input.MachineID, DeviceID: f.device, InstanceID: f.instance, Epoch: f.service.subscriptionServerEpoch(), StartedAt: time.Now().UTC()}
				case "refresh":
					st.ServerOperation.Action = domain.SubscriptionRefresh
					st.Pending.Action = domain.SubscriptionRefresh
				case "foreign-operation":
					st.Pending.ID = domain.NewID()
				case "observation":
					st.Observation = &domain.SubscriptionObservationOperation{}
				}
			})
			before, _ := f.record()
			if err := f.service.recoverFailedServerLogin(failedLoginContext(), f.input.AccountID, operation); err == nil {
				t.Fatal("another authority was retired")
			}
			after, _ := f.record()
			if before.Revision != after.Revision || f.secrets.enumerations != 0 {
				t.Fatal("rejected cleanup changed ownership or opened the vault")
			}
		})
	}
}

func TestFailedServerLoginMaintenanceSettlesQueuedRestartWithoutRelaunch(t *testing.T) {
	f, operation := legacyFailedServerLogin(t, false)
	ctx, stop := context.WithCancel(failedLoginContext())
	done := make(chan struct{})
	go func() { defer close(done); f.service.runServerSubscriptions(ctx) }()
	defer func() { stop(); awaitServerFixture(t, done) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, a := f.record()
		if a.Subscription.ServerOperation.CleanupPhase == domain.SubscriptionCredentialCleanupConfirmed {
			if a.Subscription.ServerOperation.ID != operation || a.Subscription.Pending != nil || a.Health != domain.AccountDisconnected {
				t.Fatal("maintenance replaced the original login")
			}
			if err := f.deleteFailedLogin(f.failedLoginDeletion()); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("maintenance did not reconcile the initial login")
}

func TestFailedServerLoginCleanupCannotTouchReplacement(t *testing.T) {
	f, operation := legacyFailedServerLogin(t, false)
	if err := f.service.recoverFailedServerLogin(failedLoginContext(), f.input.AccountID, operation); err != nil {
		t.Fatal(err)
	}
	f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	before, _ := f.record()
	enumerations := f.secrets.enumerations
	if err := f.service.recoverFailedServerLogin(failedLoginContext(), f.input.AccountID, operation); err == nil {
		t.Fatal("old cleanup adopted a replacement operation")
	}
	after, _ := f.record()
	if after.Revision != before.Revision || f.secrets.enumerations != enumerations {
		t.Fatal("old cleanup modified a replacement")
	}
}

func TestFailedServerLoginCleanupAllowsFreshWorkerLogin(t *testing.T) {
	f, operation := legacyFailedServerLogin(t, false)
	if err := f.service.recoverFailedServerLogin(failedLoginContext(), f.input.AccountID, operation); err != nil {
		t.Fatal(err)
	}
	op := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	_, a := f.record()
	if a.Validate() != nil || a.Subscription.ServerOperation != nil {
		t.Fatal("settled cleanup checkpoint fenced a fresh Worker login")
	}
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	bundle := subscriptionTestBundle("replacement-worker-fixture", "first", time.Now().UTC())
	defer clear(bundle)
	if _, err := f.finish(lease, bundle, true, false, true); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if a.Validate() != nil || a.Connection == nil || a.Subscription.Generation == "" || a.Subscription.Lease != nil {
		t.Fatal("fresh Worker authentication inherited failed cleanup authority")
	}
}

func TestFailedServerLoginMaintenanceDoesNotRepeatFailedCleanup(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, false)
	f.secrets.referenceError = domain.Fail(domain.Unavailable, "Fixture vault unavailable.", "Retry after restart.")
	ctx, stop := context.WithCancel(failedLoginContext())
	done := make(chan struct{})
	go func() { defer close(done); f.service.runServerSubscriptions(ctx) }()
	defer func() { stop(); awaitServerFixture(t, done) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		f.secrets.mu.Lock()
		attempts := f.secrets.enumerations
		f.secrets.mu.Unlock()
		if attempts != 0 {
			time.Sleep(600 * time.Millisecond)
			f.secrets.mu.Lock()
			attempts = f.secrets.enumerations
			f.secrets.mu.Unlock()
			_, a := f.record()
			if attempts != 1 || !a.Subscription.RecoveryRequired || a.Subscription.Pending == nil {
				t.Fatal("failed cleanup was repeated or released its fence")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("maintenance did not attempt original cleanup")
}

func TestFailedServerLoginCleanupKeepsRetainedReferences(t *testing.T) {
	f := newSubscriptionFixture(t)
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	n := &serverLoginFixture{startError: domain.Fail(domain.Unauthenticated, "Fixture login failure.", "")}
	awaitServerFixture(t, f.serverRun(n))
	_, a := f.record()
	if a.Subscription.ServerOperation.ID != domain.ID(op.OperationId) || a.Health != domain.AccountDisconnected {
		t.Fatal("original failed login did not settle")
	}
	if err := f.deleteFailedLogin(f.failedLoginDeletion()); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("cleanup bypassed retained Agent/session references", err)
	}
}

func TestFailedServerLoginPreNativeVaultFailureRequiresCleanRuntime(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "retained"}[retained], func(t *testing.T) {
			root := t.TempDir()
			owner := domain.NewID()
			if retained {
				home := filepath.Join(root, "subscription-runtime", "auth", string(owner))
				if err := security.PrivateDir(home); err != nil {
					t.Fatal(err)
				}
			}
			err := reconcileFailedServerLoginPreNative(context.Background(), root, owner)
			if retained {
				if err == nil {
					t.Fatal("retained pre-native evidence released recovery")
				}
			} else if err != nil {
				t.Fatal("clean pre-native state was not accepted", err)
			}
		})
	}
}
