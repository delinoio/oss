// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
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
