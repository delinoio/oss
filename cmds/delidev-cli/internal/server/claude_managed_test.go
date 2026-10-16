// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const managedFixtureToken = "sk-ant-oat01-ServerFixtureTokenABCDEFGHIJKLMNOP0123456789_-"

func TestManagedClaudeLoginReportsProtocolDiscoveryPhase(t *testing.T) {
	f := newManagedClaudeFixture(t)
	f.service.claudeSetupOpen = func(context.Context, nativeScope, domain.ID, *slog.Logger) (managedClaudeLogin, string, error) {
		return nil, "2.1.287", managedClaudeDiscoveryError{domain.Fail(domain.Unsupported, "The installed Claude Code protocol is unavailable.", "Refresh native discovery on the server.")}
	}
	_, started, cleanup, version, phase, err := f.service.runManagedClaudeLogin(context.Background(), domain.NewID(), domain.ServerSubscriptionOperation{ID: domain.NewID()})
	if started || !cleanup || version != "2.1.287" || phase != domain.NativeSubscriptionDiscovery || domain.SafeError(err).Code != domain.Unsupported {
		t.Fatalf("discovery failure = started %t cleanup %t version %q phase %v code %s", started, cleanup, version, phase, domain.SafeError(err).Code)
	}
}

func managedFixtureURL() string {
	return strings.Replace(claudeFixtureURL(), "user%3Aprofile+user%3Ainference+user%3Asessions%3Aclaude_code", "user%3Ainference", 1)
}

// fakeManagedLogin is the injected setup-token child. It never reads a native
// profile; tests release progress, completion and failures explicitly.
type fakeManagedLogin struct {
	url                    string
	token                  []byte
	progressErr, tokenErr  error
	waitErr, closeErr      error
	ready                  chan struct{}
	finish                 chan struct{}
	codes                  chan []byte
	closed                 atomic.Bool
	submitErr              error
	submitCalls, closeCall atomic.Int32
}

func newFakeManagedLogin() *fakeManagedLogin {
	return &fakeManagedLogin{url: managedFixtureURL(), token: []byte(managedFixtureToken), ready: make(chan struct{}), finish: make(chan struct{}), codes: make(chan []byte, 4)}
}
func (n *fakeManagedLogin) Progress() (claude.SetupTokenProgress, error) {
	if n.progressErr != nil {
		return claude.SetupTokenProgress{}, n.progressErr
	}
	select {
	case <-n.ready:
		return claude.SetupTokenProgress{URL: n.url}, nil
	default:
		return claude.SetupTokenProgress{}, nil
	}
}
func (n *fakeManagedLogin) Submit(code []byte) error {
	n.submitCalls.Add(1)
	n.codes <- bytes.Clone(code)
	clear(code)
	return n.submitErr
}
func (n *fakeManagedLogin) Done() <-chan struct{} { return n.finish }
func (n *fakeManagedLogin) Wait() error           { return n.waitErr }
func (n *fakeManagedLogin) Token() ([]byte, error) {
	if n.tokenErr != nil {
		return nil, n.tokenErr
	}
	return bytes.Clone(n.token), nil
}
func (n *fakeManagedLogin) Close() error {
	n.closeCall.Add(1)
	n.closed.Store(true)
	return n.closeErr
}

type managedFixture struct {
	*subscriptionFixture
	native *fakeManagedLogin
}

// newManagedClaudeFixture converts the account into a new Claude account with
// no Runner Device capability, registered Runner or installation at all.
func newManagedClaudeFixture(t *testing.T) *managedFixture {
	t.Helper()
	f := newSubscriptionFixture(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.managed-claude", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.SubscriptionService = domain.SubscriptionClaude
		a.Alias = "Claude"
		if _, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		mr, m, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		m.WorkerCapabilities = []domain.WorkerCapability{domain.ManagedCodexSubscriptionsV1}
		m.Installations = nil
		_, err = tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", m)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	m := &managedFixture{subscriptionFixture: f, native: newFakeManagedLogin()}
	f.service.claudeSetupOpen = func(context.Context, nativeScope, domain.ID, *slog.Logger) (managedClaudeLogin, string, error) {
		return m.native, domain.ClaudeProtocolVersion, nil
	}
	return m
}

func (f *managedFixture) request(action pb.SubscriptionAction) (*pb.RequestSubscriptionResponse, error) {
	r, _ := f.record()
	response, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: action}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *managedFixture) run() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.service.runManagedClaude(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
	}()
	return done
}

func (f *managedFixture) awaitState(op string, want pb.SubscriptionLoginState) *pb.GetSubscriptionProgressResponse {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		progress := f.progressFor(op)
		if progress.State == want {
			return progress
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out waiting for %v, saw %v", want, progress.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *managedFixture) submitCode(op string, code string) error {
	r, _ := f.record()
	_, err := f.client.SubmitSubscriptionLoginCode(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.SubmitSubscriptionLoginCodeRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: op, Code: []byte(code)}))
	return err
}

// connect completes one successful server-owned login and returns its generation.
func (f *managedFixture) connect() domain.ID {
	f.t.Helper()
	op, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		f.t.Fatal(err)
	}
	done := f.run()
	close(f.native.ready)
	f.awaitState(op.OperationId, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING)
	if err := f.submitCode(op.OperationId, "fixture-approval#original-state"); err != nil {
		f.t.Fatal(err)
	}
	select {
	case <-f.native.codes:
	case <-time.After(10 * time.Second):
		f.t.Fatal("approval code never reached the original child")
	}
	close(f.native.finish)
	awaitServerFixture(f.t, done)
	_, a := f.record()
	if a.Connection == nil || a.Subscription.Generation == "" {
		f.t.Fatal("login did not connect")
	}
	return a.Subscription.Generation
}

func TestManagedClaudeLoginWithoutRunnerPublishesSealedTokenOnce(t *testing.T) {
	f := newManagedClaudeFixture(t)
	// No registered Runner advertises any Claude capability and none is selected.
	op, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	r, a := f.record()
	if !a.Subscription.ManagedClaude() || a.Subscription.Pending == nil || a.Subscription.Pending.MachineID != "" || a.Subscription.NativeProfileID != "" || a.Subscription.OwnerMachineID != "" || a.Subscription.NativeOperation != nil {
		t.Fatal("server login borrowed Runner or native-profile ownership")
	}
	replay, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: op.OperationId, Id: string(r.ID), ExpectedRevision: op.Account.Revision - 1}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}))
	if err != nil || !replay.Msg.Replayed {
		t.Fatalf("duplicate click did not retain the original request: %v", err)
	}
	if _, err = f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err == nil {
		t.Fatal("a second click started another login")
	}
	done := f.run()
	if p := f.progressFor(op.OperationId); p.Url != "" {
		t.Fatal("a URL was presented before the original child produced it")
	}
	close(f.native.ready)
	progress := f.awaitState(op.OperationId, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING)
	if progress.Url != managedFixtureURL() || progress.LoginMethod != pb.SubscriptionLoginMethod_SUBSCRIPTION_LOGIN_METHOD_BROWSER_CODE || progress.SuggestedName != "" {
		t.Fatal("waiting presentation changed the original address or exposed a name")
	}
	if err := f.submitCode(op.OperationId, "fixture-approval#original-state"); err != nil {
		t.Fatal(err)
	}
	if err := f.submitCode(op.OperationId, "second#attempt"); err == nil {
		t.Fatal("a second approval input was admitted")
	}
	select {
	case code := <-f.native.codes:
		if string(code) != "fixture-approval#original-state" {
			t.Fatal("the original approval code changed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("approval code never reached the original child")
	}
	if len(f.service.claudeLoginCodes[domain.ID(op.OperationId)]) != 0 {
		t.Fatal("approval code stayed in server memory after consumption")
	}
	for _, name := range []string{"state.sqlite", "state.sqlite-wal", "state.sqlite-shm"} {
		raw, err := os.ReadFile(filepath.Join(f.service.Store.Root(), name))
		if err == nil && (bytes.Contains(raw, []byte("fixture-approval")) || bytes.Contains(raw, []byte(managedFixtureURL()))) {
			t.Fatal("sensitive login presentation persisted")
		}
	}
	close(f.native.finish)
	awaitServerFixture(t, done)
	if f.native.closeCall.Load() == 0 {
		t.Fatal("the original child was not joined")
	}
	_, a = f.record()
	st := a.Subscription
	if a.Connection == nil || a.Health != domain.AccountReady || st.Pending != nil || st.Lease != nil || st.OwnerMachineID != "" || st.NativeProfileID != "" || st.ServerOperation.NativeStarted || st.Generation != st.ServerOperation.FinishID || len(st.IdentityCommitment) != 64 {
		t.Fatal("success did not release exclusive ownership with a sealed generation")
	}
	final := f.progressFor(op.OperationId)
	if final.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || final.Generation != string(st.Generation) || final.SuggestedName != "Claude" || final.Url != "" {
		t.Fatal("success presentation changed")
	}
	// The envelope is bound to this server, account and generation.
	raw := f.secrets.values[credentials.Ref{Owner: f.input.AccountID, ID: st.Generation, Purpose: credentials.AccountLogin}]
	opened, err := subscription.OpenClaudeToken(raw, f.service.Identity.ServerID, f.input.AccountID, st.Generation)
	if err != nil || string(opened.Token) != managedFixtureToken {
		t.Fatalf("vault envelope is not the sealed token: %v", err)
	}
	opened.Clear()
	if _, err := subscription.OpenClaudeToken(raw, f.service.Identity.ServerID, f.input.AccountID, domain.NewID()); err == nil {
		t.Fatal("the envelope is not generation-bound")
	}
	if st.IdentityCommitment == managedFixtureToken || bytes.Contains(mustJSON(t, a), []byte(managedFixtureToken)) || bytes.Contains(mustJSON(t, final), []byte("sk-ant")) {
		t.Fatal("the token entered a resource or progress document")
	}
	for _, name := range []string{"state.sqlite", "state.sqlite-wal", "state.sqlite-shm"} {
		if data, err := os.ReadFile(filepath.Join(f.service.Store.Root(), name)); err == nil && bytes.Contains(data, []byte(managedFixtureToken)) {
			t.Fatal("the token persisted in the database")
		}
	}
	// Device-owned lifecycle never adopts this account.
	if _, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: 0}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH})); err == nil {
		t.Fatal("a Runner-bound refresh was admitted for a server-token account")
	}
	if _, err = f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err == nil {
		t.Fatal("a connected account accepted another login")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestManagedClaudeFailuresNeverConnectOrWriteCredentials(t *testing.T) {
	unsupported := domain.Fail(domain.Unsupported, "unsupported", "")
	for _, scenario := range []struct {
		name     string
		mutate   func(*fakeManagedLogin)
		state    pb.SubscriptionLoginState
		recovery bool
	}{
		{"malformed output", func(n *fakeManagedLogin) { n.tokenErr = unsupported }, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_UNSUPPORTED, false},
		{"foreign url profile", func(n *fakeManagedLogin) { n.url = "https://claude.com/cai/oauth/authorize?client_id=foreign" }, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_UNSUPPORTED, false},
		{"progress failure", func(n *fakeManagedLogin) { n.progressErr = domain.Fail(domain.Unavailable, "failed", "") }, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED, false},
		{"child failure", func(n *fakeManagedLogin) { n.waitErr = domain.Fail(domain.Unavailable, "exit", "") }, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED, false},
		{"uncertain cleanup", func(n *fakeManagedLogin) { n.closeErr = errors.New("still running") }, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newManagedClaudeFixture(t)
			scenario.mutate(f.native)
			op, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			if err != nil {
				t.Fatal(err)
			}
			done := f.run()
			close(f.native.ready)
			close(f.native.finish)
			awaitServerFixture(t, done)
			_, a := f.record()
			if a.Connection != nil || a.Subscription.Generation != "" || a.Health == domain.AccountReady {
				t.Fatal("an unsafe emission connected the account")
			}
			if len(f.secrets.values) != 0 || f.secrets.puts != 0 {
				t.Fatal("an unsafe emission wrote a credential")
			}
			if a.Subscription.RecoveryRequired != scenario.recovery {
				t.Fatalf("recovery fence = %v, want %v", a.Subscription.RecoveryRequired, scenario.recovery)
			}
			if got := f.progressFor(op.OperationId); got.State != scenario.state {
				t.Fatalf("state %v, want %v", got.State, scenario.state)
			}
			if f.native.closeCall.Load() == 0 {
				t.Fatal("the original child was not joined")
			}
		})
	}
}

func TestManagedClaudeCancelWaitsForCleanupAndCannotPublish(t *testing.T) {
	f := newManagedClaudeFixture(t)
	op, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	done := f.run()
	close(f.native.ready)
	f.awaitState(op.OperationId, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING)
	r, _ := f.record()
	if _, err := f.client.CancelSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}})); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, done)
	_, a := f.record()
	if a.Connection != nil || a.Subscription.Pending != nil || f.secrets.puts != 0 || !f.native.closed.Load() {
		t.Fatalf("cancellation published authentication or skipped cleanup: conn=%v pending=%+v puts=%d closed=%v recovery=%v op=%+v", a.Connection != nil, a.Subscription.Pending, f.secrets.puts, f.native.closed.Load(), a.Subscription.RecoveryRequired, a.Subscription.ServerOperation)
	}
	if got := f.progressFor(op.OperationId); got.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_CANCELED {
		t.Fatalf("cancel state %v", got.State)
	}
}

func TestManagedClaudeRequestAuthorizationAndNewAccountOnly(t *testing.T) {
	f := newManagedClaudeFixture(t)
	r, _ := f.record()
	request := func(token string, revision uint64, machine string) error {
		_, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: revision}, MachineId: machine, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}))
		return err
	}
	if request(f.workerToken, r.Revision, "") == nil {
		t.Fatal("a Worker started a server login")
	}
	if request(f.service.Identity.Token, r.Revision+9, "") == nil {
		t.Fatal("a stale revision started a server login")
	}
	if err := request(f.service.Identity.Token, r.Revision, ""); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if !a.Subscription.ManagedClaude() {
		t.Fatal("the new account did not select the server-token mode")
	}
	// A machine-bound request is retired: it is rejected and never converts the
	// account to server-owned or creates a Runner-owned operation.
	device := newClaudeSubscriptionFixture(t)
	dr, _ := device.record()
	_, err := device.client.RequestSubscription(context.Background(), subscriptionRequest(device.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(dr.ID), ExpectedRevision: dr.Revision}, MachineId: string(device.input.MachineID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}))
	if domain.SafeError(rpc.ClientError(err)).Code != domain.Unsupported {
		t.Fatalf("the Runner login was not rejected as unsupported: %v", err)
	}
	if _, da := device.record(); da.Subscription != nil && (da.Subscription.ManagedClaude() || da.Subscription.NativeOperation != nil) {
		t.Fatal("the rejected Runner login changed the account")
	}
}

// TestManagedClaudeRunnerBoundSignInIsRetiredBeforeManagedRouting covers an
// existing server-token account: routing sends it to the managed handler first,
// which must report the same typed retirement result as the Runner lane.
func TestManagedClaudeRunnerBoundSignInIsRetiredBeforeManagedRouting(t *testing.T) {
	for _, action := range []pb.SubscriptionAction{pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH} {
		t.Run(action.String(), func(t *testing.T) {
			f := newManagedClaudeFixture(t)
			f.connect()
			beforeRecord, before := f.record()
			_, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(beforeRecord.ID), ExpectedRevision: beforeRecord.Revision}, MachineId: string(f.input.MachineID), Action: action}))
			problem := domain.SafeError(rpc.ClientError(err))
			if problem.Code != domain.Unsupported || problem.Message == "" || problem.Guidance == "" {
				t.Fatalf("machine-bound %s on a server-token account was not rejected as unsupported with guidance: %+v", action, problem)
			}
			afterRecord, after := f.record()
			if afterRecord.Revision != beforeRecord.Revision || !reflect.DeepEqual(before.Subscription, after.Subscription) {
				t.Fatal("a rejected machine-bound request changed the server-token account")
			}
		})
	}
}

func TestManagedClaudeStatusAdvertisesIndependentCapabilities(t *testing.T) {
	f := newManagedClaudeFixture(t)
	if f.service.Identity.ServerID == "" {
		t.Fatal("fixture identity missing")
	}
	// The capability set is part of the server status response.
	status, err := f.service.GetStatus(context.Background(), connect.NewRequest(&pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[pb.SystemCapability]bool{}
	for _, capability := range status.Msg.Capabilities {
		seen[capability] = true
	}
	for _, capability := range []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_CLAUDE_SERVER_LOGIN_V1, pb.SystemCapability_SYSTEM_CAPABILITY_CLAUDE_MANAGED_EXECUTION_V1, pb.SystemCapability_SYSTEM_CAPABILITY_CLAUDE_SUBSCRIPTIONS_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_SUBSCRIPTION_LOGIN_V1} {
		if !seen[capability] {
			t.Fatalf("capability %v is not advertised", capability)
		}
	}
}

type managedWorker struct {
	token                          string
	machine, instance, device, job domain.ID
	revision                       uint64
}

// prepareExecution freezes a Claude subscription assignment for the connected
// account on the fixture's own Worker.
func (f *managedFixture) prepareExecution(capabilities ...domain.WorkerCapability) managedWorker {
	f.t.Helper()
	_, account := f.record()
	c := &f.input.Configuration
	c.Harness, c.SubscriptionService, c.Subscription, c.ProviderID = domain.ClaudeCode, domain.SubscriptionClaude, true, ""
	c.ModelID = (domain.ModelIdentity{SubscriptionService: domain.SubscriptionClaude, NativeID: c.NativeModel}).Key()
	c.Options = domain.AgentOptions{Permission: domain.PermissionDefault}
	f.input.ConnectionID = account.Connection.ID
	f.input.Installation = domain.Installation{Harness: domain.ClaudeCode, State: domain.InstallationDetected, Version: domain.ClaudeProtocolVersion, ProtocolVerified: true, Protocol: &domain.ProtocolObservation{Protocol: domain.ProtocolFor(domain.ClaudeCode), State: domain.ProtocolVerified}}
	digest, err := f.input.Configuration.Digest()
	if err != nil {
		f.t.Fatal(err)
	}
	f.input.ConfigurationDigest = digest
	worker := managedWorker{token: f.workerToken, machine: f.input.MachineID, instance: f.instance, job: f.job}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.managed-claude-execution", nil, func(tx *store.Tx) (any, error) {
		mr, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = capabilities
		if _, err := tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine); err != nil {
			return nil, err
		}
		sr, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		session.InitialExecution.ConnectionID = f.input.ConnectionID
		session.InitialExecution.Configuration = f.input.Configuration
		session.InitialExecution.ConfigurationDigest = f.input.ConfigurationDigest
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.SessionID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		jr, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](jr)
		if err != nil {
			return nil, err
		}
		job.Input, _ = json.Marshal(f.input)
		updated, err := tx.PutJob(jr.ID, jr.Revision, jr.SessionID, jr.ProjectID, job)
		worker.revision = updated.Revision
		return nil, err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return worker
}

// addWorker registers an independent Worker device, instance and claimed job.
func (f *managedFixture) addWorker(capabilities ...domain.WorkerCapability) managedWorker {
	f.t.Helper()
	token, err := security.RandomToken()
	if err != nil {
		f.t.Fatal(err)
	}
	w := managedWorker{token: token, machine: domain.NewID(), instance: domain.NewID(), device: domain.NewID(), job: domain.NewID()}
	input := f.input
	input.MachineID = w.machine
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.managed-claude-worker", nil, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.MachineKind, w.machine, 0, "", "", domain.Machine{Name: "Second", OS: "linux", Architecture: "arm64", WorkerCapabilities: capabilities, LastSeen: time.Now().UTC()}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.DeviceKind, w.device, 0, "", "", domain.Device{Name: "Second Worker", Type: domain.WorkerDevice, MachineID: w.machine}); err != nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(w.token))
		if err := tx.PutCredential(w.device, digest[:]); err != nil {
			return nil, err
		}
		if err := tx.SetWorkerInstance(w.machine, w.instance, time.Now().UTC()); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(input)
		updated, err := tx.PutJob(w.job, 0, f.input.SessionID, "", domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobClaimed, MachineID: w.machine, InstanceID: w.instance, AssignedDeviceID: w.device, Input: raw, AcceptedAt: time.Now().UTC()})
		w.revision = updated.Revision
		return nil, err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return w
}

func (f *managedFixture) take(w managedWorker, request domain.ID) (*pb.TakeSubscriptionResponse, error) {
	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(w.token, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(request), Id: string(f.input.AccountID), ExpectedRevision: w.revision}, MachineId: string(w.machine), InstanceId: string(w.instance), OperationId: string(w.job), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *managedFixture) finishLease(w managedWorker, lease *pb.TakeSubscriptionResponse, cleanup, success bool) error {
	_, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(w.token, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(w.machine), InstanceId: string(w.instance), CleanupConfirmed: cleanup, Succeeded: success}))
	return err
}

func (f *managedFixture) requireToken(lease *pb.TakeSubscriptionResponse) {
	f.t.Helper()
	opened, err := subscription.OpenClaudeToken(lease.Bundle, f.service.Identity.ServerID, f.input.AccountID, domain.ID(lease.GenerationId))
	if err != nil || string(opened.Token) != managedFixtureToken {
		f.t.Fatalf("lease did not deliver the sealed generation: %v", err)
	}
	opened.Clear()
}

func TestManagedClaudeSameGenerationExecutesOnTwoWorkersSequentially(t *testing.T) {
	f := newManagedClaudeFixture(t)
	generation := f.connect()
	a := f.prepareExecution(domain.ManagedClaudeExecutionV1)
	b := f.addWorker(domain.ManagedClaudeExecutionV1)
	legacy := f.addWorker(domain.NativeClaudeSubscriptionsV1, domain.ManagedCodexSubscriptionsV1)
	if lease, err := f.take(legacy, domain.NewID()); err == nil || lease != nil {
		t.Fatal("a legacy-only Worker received a token")
	}
	request := domain.NewID()
	first, err := f.take(a, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.GenerationId != string(generation) || first.NativeProfileId != "" {
		t.Fatal("the lease changed generation or fabricated a native profile")
	}
	f.requireToken(first)
	if lease, err := f.take(b, domain.NewID()); err == nil || lease != nil {
		t.Fatal("a simultaneous request received a second original lease")
	}
	if err := f.finishLease(b, first, true, true); err == nil {
		t.Fatal("another Worker finished the original lease")
	}
	if err := f.finishLease(a, first, true, true); err != nil {
		t.Fatal(err)
	}
	_, account := f.record()
	if account.Subscription.Lease != nil || account.Health != domain.AccountReady || account.Subscription.Generation != generation {
		t.Fatal("joined cleanup did not release the original lease")
	}
	second, err := f.take(b, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if second.GenerationId != string(generation) {
		t.Fatal("the second Worker did not execute the same settled generation")
	}
	f.requireToken(second)
	if err := f.finishLease(b, second, true, true); err != nil {
		t.Fatal(err)
	}
}

func TestManagedClaudeReplayedTakeFencesTheUnfinishedLeaseForOriginalWorkerCleanup(t *testing.T) {
	f := newManagedClaudeFixture(t)
	f.connect()
	a := f.prepareExecution(domain.ManagedClaudeExecutionV1)
	b := f.addWorker(domain.ManagedClaudeExecutionV1)
	request := domain.NewID()
	first, err := f.take(a, request)
	if err != nil {
		t.Fatal(err)
	}
	f.requireToken(first)
	// Another Worker replaying the request ID cannot fence the lease it does not own.
	if _, err := f.take(b, request); err == nil {
		t.Fatal("a foreign replay received a token")
	}
	_, account := f.record()
	if account.Subscription.RecoveryRequired || account.Subscription.Lease == nil {
		t.Fatal("a foreign replay changed the original lease")
	}
	// The first response was lost: the original Worker retries the same request.
	if replay, err := f.take(a, request); err == nil || replay != nil {
		t.Fatal("a replayed receipt redistributed the token")
	}
	_, account = f.record()
	if !account.Subscription.RecoveryRequired || account.Subscription.Lease == nil || account.Health != domain.AccountFailed {
		t.Fatal("the replay did not fence the unfinished lease for recovery")
	}
	if _, err := f.take(b, domain.NewID()); err == nil {
		t.Fatal("another Worker bypassed the replay fence")
	}
	if err := f.finishLease(a, first, true, true); err == nil {
		t.Fatal("recovery accepted execution success")
	}
	if err := f.finishLease(a, first, true, false); err != nil {
		t.Fatal(err)
	}
	_, account = f.record()
	if account.Subscription.RecoveryRequired || account.Subscription.Lease != nil || account.Health != domain.AccountReady {
		t.Fatal("original-Worker cleanup did not release the replay fence")
	}
}

func TestManagedClaudeCleanupAfterCredentialLoadFailureKeepsTheAccountOutOfService(t *testing.T) {
	cases := map[string]func(f *managedFixture, ref credentials.Ref){
		"missing": func(f *managedFixture, ref credentials.Ref) { delete(f.secrets.values, ref) },
		"malformed": func(f *managedFixture, ref credentials.Ref) {
			f.secrets.values[ref] = []byte(`{"kind":"unrelated"}`)
		},
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			f := newManagedClaudeFixture(t)
			generation := f.connect()
			ref := credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}
			a := f.prepareExecution(domain.ManagedClaudeExecutionV1)
			f.secrets.mu.Lock()
			damage(f, ref)
			f.secrets.mu.Unlock()
			request := domain.NewID()
			if _, err := f.take(a, request); err == nil {
				t.Fatal("an unusable credential was granted")
			}
			_, account := f.record()
			if !account.Subscription.RecoveryRequired || account.Subscription.Lease == nil || account.Health != domain.AccountFailed {
				t.Fatal("the failed credential load did not fence the committed lease")
			}
			// The Worker never received a response; its journal names the committed lease.
			lease := &pb.TakeSubscriptionResponse{LeaseId: string(request), GenerationId: string(generation), LeaseRevision: account.Subscription.Lease.Revision}
			// The original Worker's pre-delivery cleanup releases the lease only.
			if err := f.finishLease(a, lease, true, false); err != nil {
				t.Fatal(err)
			}
			_, account = f.record()
			if account.Subscription.Lease != nil || account.Connection == nil || account.Health != domain.AccountFailed {
				t.Fatal("cleanup restored an account whose credential cannot be loaded")
			}
			if account.Validate() != nil {
				t.Fatalf("the settled account is invalid: %v", account.Validate())
			}
			// No later session is admitted only to repeat the failed Take.
			if _, err := f.take(a, domain.NewID()); err == nil {
				t.Fatal("a failed account was admitted again")
			}
			_, account = f.record()
			if account.Subscription.Lease != nil || account.Subscription.RecoveryRequired {
				t.Fatal("a denied admission left a lease or fence behind")
			}
			// The failed account can still be disconnected and then reconnected.
			if _, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); err != nil {
				t.Fatalf("a failed account could not be disconnected: %v", err)
			}
		})
	}
}

func TestManagedClaudeCleanupRestoresReadinessOnlyAfterTheCredentialLoadsAgain(t *testing.T) {
	f := newManagedClaudeFixture(t)
	generation := f.connect()
	ref := credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}
	a := f.prepareExecution(domain.ManagedClaudeExecutionV1)
	f.secrets.mu.Lock()
	saved := bytes.Clone(f.secrets.values[ref])
	delete(f.secrets.values, ref)
	f.secrets.mu.Unlock()
	request := domain.NewID()
	if _, err := f.take(a, request); err == nil {
		t.Fatal("a missing credential was granted")
	}
	// A transient outage that has ended is proven by loading the same envelope.
	f.secrets.mu.Lock()
	f.secrets.values[ref] = saved
	f.secrets.mu.Unlock()
	_, account := f.record()
	lost := &pb.TakeSubscriptionResponse{LeaseId: string(request), GenerationId: string(generation), LeaseRevision: account.Subscription.Lease.Revision}
	if err := f.finishLease(a, lost, true, false); err != nil {
		t.Fatal(err)
	}
	_, account = f.record()
	if account.Subscription.RecoveryRequired || account.Subscription.Lease != nil || account.Health != domain.AccountReady {
		t.Fatal("a verified credential did not restore readiness")
	}
	lease, err := f.take(a, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	f.requireToken(lease)
}

func TestManagedClaudeLateReplayAfterJoinedCleanupDoesNotFence(t *testing.T) {
	f := newManagedClaudeFixture(t)
	f.connect()
	a := f.prepareExecution(domain.ManagedClaudeExecutionV1)
	request := domain.NewID()
	first, err := f.take(a, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.finishLease(a, first, true, true); err != nil {
		t.Fatal(err)
	}
	if replay, err := f.take(a, request); err == nil || replay != nil {
		t.Fatal("a late replay redistributed the token")
	}
	_, account := f.record()
	if account.Subscription.RecoveryRequired || account.Subscription.Lease != nil || account.Health != domain.AccountReady {
		t.Fatal("a late replay fenced an already released account")
	}
}

func TestManagedClaudeUncertainCleanupRetainsFenceUntilOriginalWorkerConfirms(t *testing.T) {
	f := newManagedClaudeFixture(t)
	f.connect()
	a := f.prepareExecution(domain.ManagedClaudeExecutionV1)
	b := f.addWorker(domain.ManagedClaudeExecutionV1)
	lease, err := f.take(a, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.finishLease(a, lease, false, false); err != nil {
		t.Fatal(err)
	}
	_, account := f.record()
	if !account.Subscription.RecoveryRequired || account.Subscription.Lease == nil || account.Health != domain.AccountFailed {
		t.Fatal("unconfirmed cleanup did not retain the fence")
	}
	if _, err := f.take(b, domain.NewID()); err == nil {
		t.Fatal("another Worker bypassed the cleanup fence")
	}
	if err := f.finishLease(b, lease, true, false); err == nil {
		t.Fatal("another Worker confirmed cleanup it did not own")
	}
	if err := f.finishLease(a, lease, true, true); err == nil {
		t.Fatal("recovery accepted execution success")
	}
	if err := f.finishLease(a, lease, true, false); err != nil {
		t.Fatal(err)
	}
	_, account = f.record()
	if account.Subscription.RecoveryRequired || account.Subscription.Lease != nil {
		t.Fatal("original positive cleanup did not release the fence")
	}
}

func TestManagedClaudeDisconnectWaitsForOriginalWorkerThenDeletesVault(t *testing.T) {
	f := newManagedClaudeFixture(t)
	generation := f.connect()
	a := f.prepareExecution(domain.ManagedClaudeExecutionV1)
	lease, err := f.take(a, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	logout, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if err != nil {
		t.Fatal(err)
	}
	_, account := f.record()
	if account.Health != domain.AccountRevoked || account.Subscription.Lease == nil || managedClaudeCandidate(account) {
		t.Fatal("disconnect did not revoke readiness while retaining the original lease")
	}
	// An offline Worker cannot finish: the claim is refused and the vault stays.
	f.native = newFakeManagedLogin()
	awaitServerFixture(t, f.run())
	_, account = f.record()
	ref := credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}
	if account.Connection == nil || len(f.secrets.values[ref]) == 0 || account.Subscription.ServerOperation.NativeStarted {
		t.Fatal("deletion proceeded before original cleanup was proved")
	}
	if err := f.finishLease(a, lease, true, true); err != nil {
		t.Fatal(err)
	}
	_, account = f.record()
	if !managedClaudeCandidate(account) {
		t.Fatal("confirmed cleanup did not admit the disconnect claim")
	}
	awaitServerFixture(t, f.run())
	_, account = f.record()
	if account.Connection != nil || account.Subscription.Generation != "" || account.Health != domain.AccountDisconnected || len(f.secrets.values[ref]) != 0 {
		t.Fatal("disconnect did not retire the connection and delete the vault generation")
	}
	if got := f.progressFor(logout.OperationId); got.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		t.Fatalf("logout state %v", got.State)
	}
	if _, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err == nil {
		t.Fatal("a disconnected account was reconnected instead of connecting a new account")
	}
}

func TestManagedClaudeLoginRetainsStaleGenerationsWithoutCleanupCheckpoint(t *testing.T) {
	f := newManagedClaudeFixture(t)
	stale := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	f.secrets.values[stale] = []byte("stale-generation")
	f.secrets.deleteError = errors.New("vault deletion unavailable")
	generation := f.connect()
	current := credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}
	_, account := f.record()
	o := account.Subscription.ServerOperation
	if account.Connection == nil || account.Health != domain.AccountReady || account.Subscription.RecoveryRequired || account.Subscription.Pending != nil || account.Subscription.Generation != generation {
		t.Fatal("routine login depended on vault deletion")
	}
	if o.State != domain.SubscriptionSucceeded || o.CleanupPhase != "" || managedClaudeNeedsRecovery(account) {
		t.Fatal("routine login left a credential-deletion checkpoint")
	}
	if len(f.secrets.values[stale]) == 0 || len(f.secrets.values[current]) == 0 {
		t.Fatal("routine login deleted a retained generation")
	}
}

func TestManagedClaudeLateRecoveryMarkNeverRewritesASettledOperation(t *testing.T) {
	f := newManagedClaudeFixture(t)
	generation := f.connect()
	_, account := f.record()
	o := account.Subscription.ServerOperation
	if err := f.service.markManagedClaudeRecovery(f.input.AccountID, o.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, account = f.record()
	if account.Connection == nil || account.Health != domain.AccountReady || account.Subscription.Generation != generation || account.Subscription.RecoveryRequired || account.Subscription.Pending != nil || account.Subscription.ServerOperation.State != domain.SubscriptionSucceeded {
		t.Fatal("an uncertain finish error unsettled a committed login")
	}
}

func TestManagedClaudeLogoutCheckpointSurvivesFailedVaultDeletion(t *testing.T) {
	f := newManagedClaudeFixture(t)
	generation := f.connect()
	ref := credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}
	logout, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if err != nil {
		t.Fatal(err)
	}
	f.secrets.deleteError = errors.New("server stopped before vault deletion")
	awaitServerFixture(t, f.run())
	_, account := f.record()
	o := account.Subscription.ServerOperation
	if account.Connection != nil || account.Subscription.Generation != "" || account.Subscription.RecoveryRequired || account.Subscription.Pending != nil || o.State != domain.SubscriptionSucceeded {
		t.Fatal("settled logout was not published")
	}
	if len(f.secrets.values[ref]) == 0 || o.CleanupPhase != domain.SubscriptionCredentialCleanupPending || !managedClaudeNeedsRecovery(account) {
		t.Fatal("the retained setup token has no durable cleanup checkpoint")
	}
	if got := f.progressFor(logout.OperationId); got.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		t.Fatalf("logout state %v", got.State)
	}
	f.secrets.deleteError = nil
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.recoverManagedClaude(ctx, f.input.AccountID, domain.ID(logout.OperationId)); err != nil {
		t.Fatalf("checkpoint recovery failed: %v", err)
	}
	_, account = f.record()
	if len(f.secrets.values[ref]) != 0 || account.Subscription.ServerOperation.CleanupPhase != "" || managedClaudeNeedsRecovery(account) || account.Connection != nil {
		t.Fatal("recovery did not delete the retained token and clear the checkpoint")
	}
}

func TestManagedClaudeCheckpointCleanupRetriesAfterFailedScanAttempts(t *testing.T) {
	f := newManagedClaudeFixture(t)
	f.service.serverCleanupRetryBase = 20 * time.Millisecond
	generation := f.connect()
	ref := credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}
	if _, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); err != nil {
		t.Fatal(err)
	}
	f.secrets.deleteError = errors.New("vault unavailable")
	awaitServerFixture(t, f.run())
	_, account := f.record()
	if account.Subscription.ServerOperation.CleanupPhase != domain.SubscriptionCredentialCleanupPending {
		t.Fatal("the settled logout has no cleanup checkpoint")
	}
	baseline := func() int {
		f.secrets.mu.Lock()
		defer f.secrets.mu.Unlock()
		return f.secrets.deletes
	}
	before := baseline()
	ctx, stop := context.WithCancel(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}))
	done := make(chan struct{})
	go func() { defer close(done); f.service.runServerSubscriptions(ctx) }()
	defer func() { stop(); awaitServerFixture(t, done) }()
	// The scanner's first attempt and its retries all fail while the vault is down.
	deadline := time.Now().Add(10 * time.Second)
	for baseline() < before+3 {
		if time.Now().After(deadline) {
			t.Fatal("the scanner stopped retrying a failed checkpoint cleanup")
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.secrets.mu.Lock()
	f.secrets.deleteError = nil
	f.secrets.mu.Unlock()
	for time.Now().Before(deadline) {
		_, account = f.record()
		f.secrets.mu.Lock()
		retained := len(f.secrets.values[ref]) != 0
		f.secrets.mu.Unlock()
		if !retained && account.Subscription.ServerOperation.CleanupPhase == "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("a recovered vault did not complete the retained checkpoint in the same server process")
}

func TestManagedClaudeRestartRecoveryOnlyReconcilesTheOriginalRuntime(t *testing.T) {
	f := newManagedClaudeFixture(t)
	f.native.closeErr = errors.New("still running")
	op, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	close(f.native.ready)
	close(f.native.finish)
	awaitServerFixture(t, f.run())
	_, account := f.record()
	if !account.Subscription.RecoveryRequired || !managedClaudeNeedsRecovery(account) || managedClaudeCandidate(account) {
		t.Fatal("uncertain cleanup is not selected only for recovery")
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.recoverManagedClaude(ctx, f.input.AccountID, domain.ID(op.OperationId)); err == nil {
		t.Fatal("recovery succeeded without proof of the original process owner")
	}
	runtimeRoot := filepath.Join(f.service.Store.Root(), "subscription-runtime")
	for _, path := range []string{runtimeRoot, filepath.Join(runtimeRoot, "processes"), filepath.Join(runtimeRoot, "processes", op.OperationId)} {
		if err := security.PrivateDir(path); err != nil {
			t.Fatal(err)
		}
	}
	f.native.closeErr = nil
	if err := f.service.recoverManagedClaude(ctx, f.input.AccountID, domain.ID(op.OperationId)); err != nil {
		t.Fatalf("recovery with joined owner proof failed: %v", err)
	}
	_, account = f.record()
	if account.Subscription.RecoveryRequired || account.Subscription.Pending != nil || account.Connection != nil || account.Subscription.ServerOperation.State != domain.SubscriptionFailed || managedClaudeNeedsRecovery(account) {
		t.Fatal("recovery did not retire the failed login without connecting it")
	}
	if f.native.submitCalls.Load() != 0 || f.secrets.puts != 0 {
		t.Fatal("recovery replayed input or wrote credentials")
	}
}

func TestManagedClaudeRestartedLeaseKeepsQueuedLogoutRecoverableAfterWorkerCleanup(t *testing.T) {
	f := newManagedClaudeFixture(t)
	generation := f.connect()
	ref := credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}
	a := f.prepareExecution(domain.ManagedClaudeExecutionV1)
	lease, err := f.take(a, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	logout, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if err != nil {
		t.Fatal(err)
	}
	// The server restarts while the original execution lease is still active.
	f.service.subscriptionEpoch = domain.NewID()
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.initializeServerSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	_, account := f.record()
	if !account.Subscription.RecoveryRequired || account.Subscription.Lease == nil || managedClaudeNeedsRecovery(account) {
		t.Fatal("restart did not fence the logout behind the original lease")
	}
	// Only the original Worker's joined cleanup releases the lease.
	if err := f.finishLease(a, lease, true, false); err != nil {
		t.Fatal(err)
	}
	_, account = f.record()
	if account.Subscription.Lease != nil || account.Health != domain.AccountRevoked || account.Connection == nil {
		t.Fatal("cleanup did not release the lease while keeping the account revoked")
	}
	if !account.Subscription.RecoveryRequired || !managedClaudeNeedsRecovery(account) || managedClaudeCandidate(account) {
		t.Fatal("releasing the restarted lease stranded the old-epoch logout outside the scanner")
	}
	if err := f.service.recoverManagedClaude(ctx, f.input.AccountID, domain.ID(logout.OperationId)); err != nil {
		t.Fatalf("logout recovery failed: %v", err)
	}
	_, account = f.record()
	if account.Connection != nil || account.Subscription.Generation != "" || account.Health != domain.AccountDisconnected || account.Subscription.RecoveryRequired || account.Subscription.Pending != nil || len(f.secrets.values[ref]) != 0 {
		t.Fatal("recovery did not complete the pending logout and delete the vault generation")
	}
	if got := f.progressFor(logout.OperationId); got.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		t.Fatalf("logout state %v", got.State)
	}
}

func TestManagedClaudeLogoutDeadlineStartsAfterTheOriginalLeaseClears(t *testing.T) {
	f := newManagedClaudeFixture(t)
	generation := f.connect()
	ref := credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}
	a := f.prepareExecution(domain.ManagedClaudeExecutionV1)
	lease, err := f.take(a, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	logout, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if err != nil {
		t.Fatal(err)
	}
	// The execution outlives the whole operation window from acceptance.
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.managed-claude-logout-aged", nil, func(tx *store.Tx) (any, error) {
		r, account, err := managedClaudeAccount(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		// The accepted window is the full 15 minutes and has already elapsed.
		account.Subscription.ServerOperation.StartedAt = time.Now().UTC().Add(-managedClaudeOperationWindow - time.Minute)
		account.Subscription.ServerOperation.ExpiresAt = time.Now().UTC().Add(-time.Minute)
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", account)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.progressFor(logout.OperationId); got.State == pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_EXPIRED {
		t.Fatal("a logout still queued behind the original lease was reported expired")
	}
	if err := f.finishLease(a, lease, true, true); err != nil {
		t.Fatal(err)
	}
	_, account := f.record()
	if !managedClaudeCandidate(account) || !account.Subscription.ServerOperation.ExpiresAt.After(time.Now().UTC().Add(10*time.Minute)) {
		t.Fatal("the logout deadline did not restart when the original lease cleared")
	}
	// The restarted window is a whole operation window, so the persisted account
	// still satisfies its own domain validator.
	if o := account.Subscription.ServerOperation; o.ExpiresAt.Sub(o.StartedAt) > managedClaudeOperationWindow || account.Validate() != nil {
		t.Fatalf("the restarted logout window exceeds the operation bound: %v", account.Validate())
	}
	awaitServerFixture(t, f.run())
	_, account = f.record()
	if account.Connection != nil || account.Health != domain.AccountDisconnected || account.Subscription.Pending != nil || len(f.secrets.values[ref]) != 0 {
		t.Fatal("the queued logout expired instead of deleting the credential")
	}
	if got := f.progressFor(logout.OperationId); got.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		t.Fatalf("logout state %v", got.State)
	}
}

// A commit that reached SQLite but whose acknowledgment was lost must keep the
// staged token: the account is already connected and ready under that generation.
func TestManagedClaudeAmbiguousFinishCommitKeepsThePublishedToken(t *testing.T) {
	f := newManagedClaudeFixture(t)
	stale := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	f.secrets.values[stale] = []byte("stale-generation")
	f.service.claudeFinishAck = func() error {
		return domain.Fail(domain.Unavailable, "The commit acknowledgment was lost.", "")
	}
	generation := f.connect()
	current := credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}
	_, account := f.record()
	o := account.Subscription.ServerOperation
	if account.Connection == nil || account.Health != domain.AccountReady || account.Subscription.Generation != generation || generation != o.FinishID || o.State != domain.SubscriptionSucceeded || account.Subscription.RecoveryRequired {
		t.Fatal("a lost commit acknowledgment unsettled the published login")
	}
	if len(f.secrets.values[current]) == 0 {
		t.Fatal("the published token was deleted after an ambiguous commit error")
	}
	// Reconciliation continues the normal retirement of obsolete references.
	if len(f.secrets.values[stale]) != 0 || o.CleanupPhase != "" || managedClaudeNeedsRecovery(account) {
		t.Fatal("a confirmed publication did not retire stale references")
	}
	raw := f.secrets.values[current]
	opened, err := subscription.OpenClaudeToken(raw, f.service.Identity.ServerID, f.input.AccountID, generation)
	if err != nil || string(opened.Token) != managedFixtureToken {
		t.Fatalf("the retained envelope is not the sealed token: %v", err)
	}
	opened.Clear()
}

// A finish that definitely did not commit never connected the account, so its
// staged token is removed and recovery settles the operation without relaunch.
func TestManagedClaudeUncommittedFinishRemovesTheStagedToken(t *testing.T) {
	f := newManagedClaudeFixture(t)
	f.secrets.afterPut = func() {
		// A concurrent fence makes the metadata transaction fail before it commits.
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.fence", nil, func(tx *store.Tx) (any, error) {
			r, a, err := accountFromTx(tx, f.input.AccountID, 0)
			if err != nil {
				return nil, err
			}
			a.Subscription.RecoveryRequired = true
			a.Health = domain.AccountFailed
			_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
			return nil, err
		})
		if err != nil {
			t.Error(err)
		}
	}
	op, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	done := f.run()
	close(f.native.ready)
	close(f.native.finish)
	awaitServerFixture(t, done)
	_, account := f.record()
	if account.Connection != nil || account.Subscription.Generation != "" || account.Health == domain.AccountReady {
		t.Fatal("an uncommitted finish connected the account")
	}
	if f.secrets.puts == 0 {
		t.Fatal("the fixture never staged a token")
	}
	for ref, value := range f.secrets.values {
		if ref.Owner == f.input.AccountID && len(value) != 0 && !f.secrets.removed[ref] {
			t.Fatal("an unpublished staged token stayed in the vault")
		}
	}
	if got := f.progressFor(op.OperationId); got.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED {
		t.Fatalf("state %v", got.State)
	}
}

// The approval code is single-use input and is never retained after the
// operation finishes, even when the child exited before the server consumed it.
func TestManagedClaudeFinishClearsAnUnconsumedApprovalCode(t *testing.T) {
	f := newManagedClaudeFixture(t)
	op, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	code := []byte("fixture-approval#unconsumed")
	f.service.claudeLoginCodes = map[domain.ID][]byte{domain.ID(op.OperationId): code}
	done := f.run()
	close(f.native.finish)
	awaitServerFixture(t, done)
	if _, retained := f.service.claudeLoginCodes[domain.ID(op.OperationId)]; retained || strings.Contains(string(code), "fixture-approval") {
		t.Fatal("a finished operation retained its approval code")
	}
}

// delayedPollWindow spans at least two 250 ms polls of the login loop, so a
// regression that polls for the single-use code again surfaces as a failure.
const delayedPollWindow = 800 * time.Millisecond

// startDeliveredLogin drives one login until the original child received the
// approval code, leaving the child running so tests can hold it for several polls.
func (f *managedFixture) startDeliveredLogin() (string, <-chan struct{}) {
	f.t.Helper()
	op, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		f.t.Fatal(err)
	}
	done := f.run()
	close(f.native.ready)
	f.awaitState(op.OperationId, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING)
	if err := f.submitCode(op.OperationId, "fixture-approval#original-state"); err != nil {
		f.t.Fatal(err)
	}
	select {
	case <-f.native.codes:
	case <-time.After(10 * time.Second):
		f.t.Fatal("approval code never reached the original child")
	}
	return op.OperationId, done
}

// requireStillPending holds the delivered child across several polls and proves
// the operation neither failed nor fenced and no second code was delivered.
func (f *managedFixture) requireStillPending(op string, done <-chan struct{}) {
	f.t.Helper()
	time.Sleep(delayedPollWindow)
	select {
	case <-done:
		f.t.Fatal("the login ended while the original child was still running")
	default:
	}
	_, a := f.record()
	if a.Subscription.RecoveryRequired || a.Connection != nil {
		f.t.Fatal("a pending delivered login fenced or connected the account")
	}
	if got := f.progressFor(op); got.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING {
		f.t.Fatalf("pending state %v, want waiting", got.State)
	}
	if f.native.closeCall.Load() != 0 {
		f.t.Fatal("the original child was closed before it finished")
	}
	if f.native.submitCalls.Load() != 1 {
		f.t.Fatalf("submit calls = %d, want 1", f.native.submitCalls.Load())
	}
	if err := f.submitCode(op, "second#attempt"); err == nil {
		f.t.Fatal("a second approval input was admitted")
	}
	if len(f.native.codes) != 0 {
		f.t.Fatal("a second code reached the original child")
	}
}

func TestManagedClaudeDelayedChildSuccessStaysPendingAndConnectsOnce(t *testing.T) {
	f := newManagedClaudeFixture(t)
	op, done := f.startDeliveredLogin()
	f.requireStillPending(op, done)
	close(f.native.finish)
	awaitServerFixture(t, done)
	_, a := f.record()
	if a.Connection == nil || a.Subscription.Generation == "" || a.Subscription.RecoveryRequired || a.Subscription.Generation != a.Subscription.ServerOperation.FinishID {
		t.Fatal("the delayed login did not connect one generation")
	}
	if f.native.closeCall.Load() == 0 || f.native.submitCalls.Load() != 1 {
		t.Fatal("the original child was not joined exactly once-delivered")
	}
	if got := f.progressFor(op); got.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		t.Fatalf("final state %v", got.State)
	}
}

func TestManagedClaudeDelayedChildFailureEndsFailedWithoutRecovery(t *testing.T) {
	f := newManagedClaudeFixture(t)
	f.native.waitErr = domain.Fail(domain.Unavailable, "exit", "")
	op, done := f.startDeliveredLogin()
	f.requireStillPending(op, done)
	close(f.native.finish)
	awaitServerFixture(t, done)
	_, a := f.record()
	if a.Connection != nil || a.Subscription.Generation != "" || a.Subscription.RecoveryRequired || len(f.secrets.values) != 0 || f.secrets.puts != 0 {
		t.Fatal("a failed delayed child connected, fenced or wrote a credential")
	}
	if got := f.progressFor(op); got.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED {
		t.Fatalf("final state %v, want failed", got.State)
	}
	if f.native.closeCall.Load() == 0 || f.native.submitCalls.Load() != 1 {
		t.Fatal("cleanup was not confirmed or a second code was sent")
	}
}

func TestManagedClaudeCancelWhileDeliveredChildRunsJoinsWithoutResending(t *testing.T) {
	f := newManagedClaudeFixture(t)
	op, done := f.startDeliveredLogin()
	f.requireStillPending(op, done)
	r, _ := f.record()
	if _, err := f.client.CancelSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}})); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, done)
	_, a := f.record()
	if a.Connection != nil || a.Subscription.Pending != nil || f.secrets.puts != 0 || !f.native.closed.Load() {
		t.Fatal("cancellation published authentication or skipped cleanup")
	}
	if got := f.progressFor(op); got.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_CANCELED {
		t.Fatalf("cancel state %v", got.State)
	}
	if f.native.submitCalls.Load() != 1 {
		t.Fatal("cancellation sent the approval code again")
	}
}

func TestManagedClaudeSubmitErrorAfterClaimStillEndsTheOperation(t *testing.T) {
	f := newManagedClaudeFixture(t)
	f.native.submitErr = domain.Fail(domain.Unavailable, "write failed", "")
	op, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	done := f.run()
	close(f.native.ready)
	f.awaitState(op.OperationId, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING)
	if err := f.submitCode(op.OperationId, "fixture-approval#original-state"); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, done)
	_, a := f.record()
	if a.Connection != nil || f.secrets.puts != 0 || f.native.closeCall.Load() == 0 {
		t.Fatal("a failed submit connected the account or skipped cleanup")
	}
	if f.native.submitCalls.Load() != 1 {
		t.Fatalf("submit retried: %d calls", f.native.submitCalls.Load())
	}
	if got := f.progressFor(op.OperationId); got.State == pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING || got.State == pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		t.Fatalf("a failed submit left state %v", got.State)
	}
}

// codeDroppingLogin loses the in-memory approval bytes between claim and
// delivery, as a restart would, by clearing them right before the next take.
type codeDroppingLogin struct {
	*fakeManagedLogin
	service *Service
}

func (n *codeDroppingLogin) Progress() (claude.SetupTokenProgress, error) {
	if unlock, err := n.service.lockAccounts(context.Background()); err == nil {
		for operation := range n.service.claudeLoginCodes {
			clear(n.service.claudeLoginCodes[operation])
			delete(n.service.claudeLoginCodes, operation)
		}
		unlock()
	}
	return n.fakeManagedLogin.Progress()
}

func TestManagedClaudeMissingApprovalBytesBeforeDeliveryStillFailClosed(t *testing.T) {
	f := newManagedClaudeFixture(t)
	f.service.claudeSetupOpen = func(context.Context, nativeScope, domain.ID, *slog.Logger) (managedClaudeLogin, string, error) {
		return &codeDroppingLogin{fakeManagedLogin: f.native, service: f.service}, "2.1.237-rc.1", nil
	}
	op, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	done := f.run()
	close(f.native.ready)
	f.awaitState(op.OperationId, pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING)
	if err := f.submitCode(op.OperationId, "fixture-approval#original-state"); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, done)
	if f.native.submitCalls.Load() != 0 {
		t.Fatal("a code was delivered although its bytes were missing")
	}
	_, a := f.record()
	if a.Connection != nil || f.secrets.puts != 0 {
		t.Fatal("missing approval bytes connected the account")
	}
	// The operation ends with the recovery_required denial code and no retry.
	if d := a.Subscription.ServerOperation.ClaudeDiagnostic; d == nil || d.Code != domain.RecoveryRequired {
		t.Fatalf("diagnostic %+v, want recovery_required", d)
	} else if d.DetectedVersion != "2.1.237-rc.1" || d.Phase != domain.NativeSubscriptionLogin || d.Validate() != nil {
		// The newer observed version, not the pinned baseline, is attributed.
		t.Fatalf("diagnostic %+v, want the observed version and login phase", d)
	}
	if got := f.progressFor(op.OperationId); got.State == pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING || got.State == pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		t.Fatalf("state %v, want a failed-closed end", got.State)
	}
	if f.native.closeCall.Load() == 0 {
		t.Fatal("the original child was not joined")
	}
}

// restoreManagedClaudeImage applies the account transformation of a database
// restore: the connection is cleared, the historical generation, operation and
// lease are retained, and the account is recovery-required.
func (f *managedFixture) restoreManagedClaudeImage() {
	f.t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.managed-claude-restore", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Health = domain.AccountDisconnected
		a.Connection, a.Removal, a.Validation, a.Catalog = nil, nil, nil, nil
		a.RetainedConnections = nil
		a.Subscription.RecoveryRequired = true
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return nil, err
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

// A restored connected account keeps its historical generation and recovery
// fence. Nothing else recovers it, so the scanner must retire the generation and
// its vault references and leave an ordinary logged-out account.
func TestManagedClaudeRestoredGenerationIsRetiredByRecovery(t *testing.T) {
	f := newManagedClaudeFixture(t)
	generation := f.connect()
	ref := credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}
	stale := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	f.secrets.values[stale] = []byte("stale-generation")
	f.restoreManagedClaudeImage()
	_, account := f.record()
	o := account.Subscription.ServerOperation
	if !managedClaudeRestoredGeneration(account) || !managedClaudeNeedsRecovery(account) {
		t.Fatal("a restored settled generation was not selected for recovery")
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.recoverManagedClaude(ctx, f.input.AccountID, o.ID); err != nil {
		t.Fatalf("restored generation recovery failed: %v", err)
	}
	_, account = f.record()
	st := account.Subscription
	if account.Connection != nil || account.Health != domain.AccountDisconnected || st.Generation != "" || st.IdentityCommitment != "" || st.RecoveryRequired || st.Pending != nil || st.Lease != nil {
		t.Fatal("recovery did not clear the restored generation and fence")
	}
	if len(f.secrets.values[ref]) != 0 || len(f.secrets.values[stale]) != 0 {
		t.Fatal("recovery kept a vault reference of the quarantined generation")
	}
	if st.ServerOperation.State != domain.SubscriptionSucceeded || st.ServerOperation.CleanupPhase != "" || managedClaudeNeedsRecovery(account) {
		t.Fatal("recovery rewrote the settled operation or left it selectable")
	}
	// A restored account never signs in again: reconnecting means a new account.
	if freshManagedClaudeLogin(account) {
		t.Fatal("a recovered restored account accepted another login")
	}
	if err := account.Validate(); err != nil {
		t.Fatalf("recovered account is invalid: %v", err)
	}
}

// The original Worker's cleanup of a restored lease releases only the lease. It
// must not mark the disconnected account ready or drop the recovery fence.
func TestManagedClaudeRestoredLeaseCleanupKeepsTheRecoveryFence(t *testing.T) {
	f := newManagedClaudeFixture(t)
	generation := f.connect()
	ref := credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}
	a := f.prepareExecution(domain.ManagedClaudeExecutionV1)
	lease, err := f.take(a, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	f.restoreManagedClaudeImage()
	if err := f.finishLease(a, lease, true, false); err != nil {
		t.Fatal(err)
	}
	_, account := f.record()
	if account.Subscription.Lease != nil || account.Health == domain.AccountReady || account.Connection != nil || !account.Subscription.RecoveryRequired {
		t.Fatal("restored lease cleanup marked the disconnected account ready or dropped its fence")
	}
	if !managedClaudeNeedsRecovery(account) {
		t.Fatal("the released restored generation was stranded outside the scanner")
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.recoverManagedClaude(ctx, f.input.AccountID, account.Subscription.ServerOperation.ID); err != nil {
		t.Fatal(err)
	}
	_, account = f.record()
	if account.Subscription.Generation != "" || account.Subscription.RecoveryRequired || account.Health != domain.AccountDisconnected || len(f.secrets.values[ref]) != 0 {
		t.Fatal("recovery did not retire the restored generation after lease cleanup")
	}
}
