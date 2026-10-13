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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const managedFixtureToken = "sk-ant-oat01-ServerFixtureTokenABCDEFGHIJKLMNOP0123456789_-"

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
	f.service.claudeSetupOpen = func(context.Context, string, domain.ID, *slog.Logger) (managedClaudeLogin, error) {
		return m.native, nil
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
	// A device-owned account keeps its own flow and is never converted.
	device := newClaudeSubscriptionFixture(t)
	dr, _ := device.record()
	if _, err := device.client.RequestSubscription(context.Background(), subscriptionRequest(device.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(dr.ID), ExpectedRevision: dr.Revision}, MachineId: string(device.input.MachineID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN})); err != nil {
		t.Fatal(err)
	}
	_, da := device.record()
	if da.Subscription.ManagedClaude() || da.Subscription.NativeOperation == nil {
		t.Fatal("the device-owned flow was converted")
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

func TestManagedClaudeLoginKeepsSettledStateAndRetriesStaleCleanupFromCheckpoint(t *testing.T) {
	f := newManagedClaudeFixture(t)
	stale := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	f.secrets.values[stale] = []byte("stale-generation")
	f.secrets.deleteError = errors.New("vault deletion unavailable")
	generation := f.connect()
	current := credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}
	_, account := f.record()
	o := account.Subscription.ServerOperation
	if account.Connection == nil || account.Health != domain.AccountReady || account.Subscription.RecoveryRequired || account.Subscription.Pending != nil || account.Subscription.Generation != generation {
		t.Fatal("failed stale-reference cleanup unsettled the connected account")
	}
	if o.State != domain.SubscriptionSucceeded || o.CleanupPhase != domain.SubscriptionCredentialCleanupPending || !managedClaudeNeedsRecovery(account) {
		t.Fatal("failed stale-reference cleanup left no durable retry checkpoint")
	}
	if len(f.secrets.values[stale]) == 0 || len(f.secrets.values[current]) == 0 {
		t.Fatal("cleanup fixture changed unexpectedly")
	}
	f.secrets.deleteError = nil
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.recoverManagedClaude(ctx, f.input.AccountID, o.ID); err != nil {
		t.Fatalf("checkpoint recovery failed: %v", err)
	}
	_, account = f.record()
	if len(f.secrets.values[stale]) != 0 || len(f.secrets.values[current]) == 0 {
		t.Fatal("recovery did not retire only the stale reference")
	}
	o = account.Subscription.ServerOperation
	if account.Connection == nil || account.Health != domain.AccountReady || account.Subscription.Generation != generation || account.Subscription.RecoveryRequired || o.State != domain.SubscriptionSucceeded || o.CleanupPhase != "" || managedClaudeNeedsRecovery(account) {
		t.Fatal("checkpoint recovery did not preserve the settled generation and clear the checkpoint")
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
	awaitServerFixture(t, f.run())
	_, account = f.record()
	if account.Connection != nil || account.Health != domain.AccountDisconnected || account.Subscription.Pending != nil || len(f.secrets.values[ref]) != 0 {
		t.Fatal("the queued logout expired instead of deleting the credential")
	}
	if got := f.progressFor(logout.OperationId); got.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		t.Fatalf("logout state %v", got.State)
	}
}
