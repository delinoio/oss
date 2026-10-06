// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const serverFixtureState = "fixture-original-state-123456"
const serverFixtureURL = "https://auth.openai.com/oauth/authorize?state=" + serverFixtureState + "&redirect_uri=http%3A%2F%2Flocalhost%3A1457%2Fauth%2Fcallback&response_type=code&code_challenge_method=S256"

type serverLoginFixture struct {
	started    chan struct{}
	finish     chan struct{}
	closeError error
	startError error
	waitError  error
	bundle     []byte
	calls      atomic.Int32
}

func (n *serverLoginFixture) Version() string { return "0.159.2" }
func (n *serverLoginFixture) StartManagedLogin(context.Context, bool) (codex.ManagedLoginProgress, error) {
	n.calls.Add(1)
	if n.startError != nil {
		return codex.ManagedLoginProgress{}, n.startError
	}
	return codex.ManagedLoginProgress{LoginID: "fixture-login", URL: serverFixtureURL}, nil
}
func (n *serverLoginFixture) WaitManagedLogin(ctx context.Context, _ string) error {
	close(n.started)
	select {
	case <-n.finish:
		return n.waitError
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (n *serverLoginFixture) CancelManagedLogin(context.Context, string) error { return nil }
func (n *serverLoginFixture) ManagedBundle(context.Context, bool) ([]byte, error) {
	return bytes.Clone(n.bundle), nil
}
func (n *serverLoginFixture) LogoutManaged(context.Context) error { return nil }
func (n *serverLoginFixture) Close([]byte) error                  { return n.closeError }
func (f *subscriptionFixture) serverStart(action pb.SubscriptionAction) *pb.RequestSubscriptionResponse {
	f.t.Helper()
	r, _ := f.record()
	result, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: action}))
	if err != nil {
		f.t.Fatal(err)
	}
	return result.Msg
}
func (f *subscriptionFixture) serverRun(n *serverLoginFixture) <-chan struct{} {
	f.t.Helper()
	f.service.subscriptionOpen = func(_ context.Context, _ string, _ domain.ID, _ []byte, _ *slog.Logger) (serverSubscriptionNative, error) {
		return n, nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.service.runServerSubscription(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
	}()
	return done
}
func (f *subscriptionFixture) progressFor(op string) *pb.GetSubscriptionProgressResponse {
	f.t.Helper()
	result, err := f.client.GetSubscriptionProgress(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.GetSubscriptionProgressRequest{AccountId: string(f.input.AccountID), OperationId: op}))
	if err != nil {
		f.t.Fatal(err)
	}
	return result.Msg
}
func awaitServerFixture(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("fixture timed out")
	}
}
func TestServerSubscriptionLoginWithoutWorkerAndTransientName(t *testing.T) {
	f := newSubscriptionFixture(t)
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	r, a := f.record()
	if a.Subscription.Pending.MachineID != "" || a.Subscription.ServerOperation == nil {
		t.Fatal("server login borrowed Worker ownership")
	}
	replay, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: op.OperationId, Id: string(r.ID), ExpectedRevision: op.Account.Revision - 1}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}))
	if err != nil || !replay.Msg.Replayed {
		t.Fatalf("original request replay: %v", err)
	}
	n := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("server-native-account", "first", time.Now().UTC())}
	done := f.serverRun(n)
	awaitServerFixture(t, n.started)
	if p := f.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING || p.Url != serverFixtureURL || p.UserCode != "" || p.SuggestedName != "" {
		t.Fatal("unconfirmed login exposed a name or code")
	}
	if _, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err == nil {
		t.Fatal("Worker consumed a server operation")
	}
	close(n.finish)
	awaitServerFixture(t, done)
	r, a = f.record()
	if a.Connection == nil || a.Subscription.Pending != nil || a.Subscription.Lease != nil || a.Subscription.OwnerMachineID != "" || a.Subscription.ServerOperation.NativeStarted {
		t.Fatal("server success did not release exclusive authentication ownership")
	}
	p := f.progressFor(op.OperationId)
	if p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || p.Generation != string(a.Subscription.Generation) || p.SuggestedName != "fixture@example.invalid" {
		t.Fatal("original success did not publish its transient suggestion")
	}
	if bytes.Contains(r.Data, []byte("fixture@example.invalid")) || bytes.Contains(r.Data, []byte("synthetic-refresh")) || n.calls.Load() != 1 {
		t.Fatal("persisted sensitive presentation or repeated login")
	}
	// A later status read cannot inherit the prior original success after rotation.
	f.service.subscriptionProgress[domain.ID(op.OperationId)] = subscriptionProgress{Name: "stale", Generation: domain.NewID(), Until: time.Now().Add(time.Minute)}
	if f.progressFor(op.OperationId).SuggestedName != "" {
		t.Fatal("suggestion crossed generations")
	}
}
func TestServerSubscriptionCancelCompletionAndCleanupRace(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "joined", true: "uncertain"}[uncertain], func(t *testing.T) {
			f := newSubscriptionFixture(t)
			op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			n := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("canceled-account", "first", time.Now().UTC())}
			if uncertain {
				n.closeError = errors.New("fixture cleanup failure")
			}
			done := f.serverRun(n)
			awaitServerFixture(t, n.started)
			r, _ := f.record()
			_, err := f.client.CancelSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}}))
			if err != nil {
				t.Fatal(err)
			}
			close(n.finish)
			awaitServerFixture(t, done)
			_, a := f.record()
			if a.Connection != nil || a.Subscription.Generation != "" {
				t.Fatal("canceled completion committed credentials")
			}
			want := pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_CANCELED
			if uncertain {
				want = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED
				if !a.Subscription.RecoveryRequired || a.Subscription.Pending == nil {
					t.Fatal("uncertain cleanup lost original fence")
				}
			}
			if f.progressFor(op.OperationId).State != want {
				t.Fatal("cancellation/cleanup state not preserved")
			}
		})
	}
}

type callbackFixtureTransport struct {
	calls atomic.Int32
	fail  bool
}

func (r *callbackFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls.Add(1)
	if req.URL.Host != "127.0.0.1:1457" || req.Host != "localhost:1457" || req.URL.Path != "/auth/callback" {
		return nil, errors.New("foreign callback authority")
	}
	if r.fail {
		return nil, errors.New("fixture lost response")
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("constant fixture page")), Header: make(http.Header)}, nil
}
func TestServerSubscriptionCallbackRejectsDuplicateForeignAndLate(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "unknown"}[unknown], func(t *testing.T) {
			f := newSubscriptionFixture(t)
			op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			n := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("callback-account", "first", time.Now().UTC())}
			done := f.serverRun(n)
			awaitServerFixture(t, n.started)
			transport := &callbackFixtureTransport{fail: unknown}
			f.service.subscriptionCallbackTransport = transport
			send := func(operation, query string) error {
				_, err := f.client.ForwardSubscriptionCallback(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.ForwardSubscriptionCallbackRequest{AccountId: string(f.input.AccountID), OperationId: operation, CallbackQuery: []byte(query)}))
				return err
			}
			for _, query := range []string{"code=fixture&state=foreign", "code=fixture&state=" + serverFixtureState + "&code=other", "code=fixture&state=" + serverFixtureState + "&redirect_uri=https://external.invalid"} {
				if send(op.OperationId, query) == nil {
					t.Fatal("accepted foreign or duplicate callback")
				}
			}
			query := "code=fixture&state=" + serverFixtureState
			if send(string(domain.NewID()), query) == nil {
				t.Fatal("accepted foreign operation")
			}
			err := send(op.OperationId, query)
			if (err != nil) != unknown {
				t.Fatalf("delivery result: %v", err)
			}
			if send(op.OperationId, query) == nil || transport.calls.Load() != 1 {
				t.Fatal("callback dispatched more than once")
			}
			close(n.finish)
			awaitServerFixture(t, done)
			if send(op.OperationId, query) == nil || transport.calls.Load() != 1 {
				t.Fatal("late callback dispatched")
			}
		})
	}
}
func TestServerSubscriptionRestartNeverRelaunchesAcceptedLogin(t *testing.T) {
	f := newSubscriptionFixture(t)
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	f.service.subscriptionEpoch = domain.NewID()
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.initializeServerSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	called := false
	f.service.subscriptionOpen = func(context.Context, string, domain.ID, []byte, *slog.Logger) (serverSubscriptionNative, error) {
		called = true
		return nil, nil
	}
	f.service.runServerSubscription(ctx, f.input.AccountID)
	_, a := f.record()
	if called || !a.Subscription.RecoveryRequired || a.Subscription.Pending == nil || f.progressFor(op.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED {
		t.Fatal("restart repeated login or discarded ownership")
	}
}
func TestServerSubscriptionWaitsForOriginalExecutionLease(t *testing.T) {
	f := newSubscriptionFixture(t)
	f.login()
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.execution-lease", nil, func(tx *store.Tx) (any, error) {
		r, a, e := subscriptionAccount(tx, f.input.AccountID, 0)
		if e != nil {
			return nil, e
		}
		a.Subscription.Lease = &domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: r.Revision + 1, Action: domain.SubscriptionExecute, MachineID: f.input.MachineID, InstanceID: f.instance, DeviceID: f.device, Epoch: f.service.subscriptionServerEpoch(), Generation: a.Subscription.Generation, StartedAt: time.Now().UTC()}
		_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	called := false
	f.service.subscriptionOpen = func(context.Context, string, domain.ID, []byte, *slog.Logger) (serverSubscriptionNative, error) {
		called = true
		return nil, nil
	}
	f.service.runServerSubscription(ctx, f.input.AccountID)
	_, a := f.record()
	if called || a.Subscription.ServerOperation.NativeStarted || a.Subscription.Lease == nil || a.Subscription.Pending.Phase != domain.SubscriptionQueued {
		t.Fatal("server refresh stole the original execution lease")
	}
}
func TestSubscriptionNameFallback(t *testing.T) {
	for _, v := range []struct {
		i    subscription.Identity
		want string
	}{{subscription.Identity{Email: "email@fixture.invalid", DisplayName: "Display"}, "email@fixture.invalid"}, {subscription.Identity{DisplayName: "Display"}, "Display"}, {subscription.Identity{}, "ChatGPT"}, {subscription.Identity{Email: strings.Repeat("a", 257), DisplayName: "Display"}, "Display"}} {
		if suggestedSubscriptionName(v.i) != v.want {
			t.Fatal("invalid suggestion precedence")
		}
	}
}

func TestServerSubscriptionNameSavePreservesCurrentOwnership(t *testing.T) {
	f := newSubscriptionFixture(t)
	f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	n := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("name-save-account", "first", time.Now().UTC())}
	done := f.serverRun(n)
	awaitServerFixture(t, n.started)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	r, a := f.record()
	original, _ := json.Marshal(a.Subscription)
	a.Alias = "User edited name"
	raw, _ := json.Marshal(a)
	request := connect.NewRequest(&pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, SchemaVersion: 2, DocumentJson: raw})
	if _, err := f.service.SaveConfiguration(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SaveConfiguration(ctx, connect.NewRequest(&pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, SchemaVersion: 2, DocumentJson: raw})); err == nil {
		t.Fatal("stale revision changed the name")
	}
	_, a = f.record()
	preserved, _ := json.Marshal(a.Subscription)
	if !bytes.Equal(original, preserved) {
		t.Fatal("name save changed server ownership")
	}
	close(n.finish)
	awaitServerFixture(t, done)
	_, a = f.record()
	if a.Alias != "User edited name" || a.Connection == nil {
		t.Fatal("completion overwrote user name or lost original authentication")
	}
}

func TestServerSubscriptionSafeDurableDiagnostics(t *testing.T) {
	for _, scenario := range []struct {
		name, version string
		phase         domain.CodexPhase
		code          domain.Code
		recovery      bool
	}{
		{"missing", "", domain.CodexDiscovery, domain.NotFound, false},
		{"old", "0.150.9", domain.CodexVersion, domain.Unsupported, false},
		{"initialize", "0.159.2", domain.CodexInitialize, domain.Unsupported, false},
		{"login", "0.159.2", domain.CodexLogin, domain.Unauthenticated, false},
		{"timeout", "0.159.2", domain.CodexLogin, domain.Unavailable, false},
		{"cleanup", "0.159.2", domain.CodexCleanup, domain.RecoveryRequired, true},
		{"native-recovery", "0.159.2", domain.CodexLogin, domain.RecoveryRequired, true},
		{"login-cleanup", "0.159.2", domain.CodexLogin, domain.Unauthenticated, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newSubscriptionFixture(t)
			var logs bytes.Buffer
			f.service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			n := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("private-native-identity", "first", time.Now().UTC())}
			close(n.finish)
			switch scenario.name {
			case "missing", "old", "initialize":
				f.service.subscriptionOpen = func(context.Context, string, domain.ID, []byte, *slog.Logger) (serverSubscriptionNative, error) {
					return nil, domain.WithCodexDiagnostic(scenario.version, scenario.phase, domain.Fail(scenario.code, "raw-token/path/native-sentinel", "secret-url"))
				}
			case "login", "login-cleanup":
				n.startError = domain.Fail(domain.Unauthenticated, "raw-token/native-sentinel", "secret-url")
			case "native-recovery":
				n.startError = subscriptionDenied()
			case "timeout":
				n.startError = context.DeadlineExceeded
			}
			if scenario.recovery && scenario.name != "native-recovery" {
				n.closeError = subscriptionDenied()
			}
			if scenario.name == "missing" || scenario.name == "old" || scenario.name == "initialize" {
				f.service.runServerSubscription(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
			} else {
				awaitServerFixture(t, f.serverRun(n))
			}
			p := f.progressFor(op.OperationId)
			if p.Diagnostic == nil || p.Diagnostic.DetectedVersion != scenario.version || p.Diagnostic.MinimumVersion != domain.CodexMinimumVersion || p.Diagnostic.Code != string(scenario.code) || p.Diagnostic.CorrelationId != op.OperationId || p.Url != "" || p.UserCode != "" {
				t.Fatalf("incorrect safe progress: %#v", p.Diagnostic)
			}
			_, a := f.record()
			d := a.Subscription.ServerOperation.Diagnostic
			if d == nil || d.Validate() != nil || d.Phase != scenario.phase || strings.Contains(d.Message, "sentinel") || strings.Contains(d.Guidance, "secret") || strings.Contains(d.Message, "identity") {
				t.Fatal("original diagnostic was lost or raw content escaped")
			}
			if scenario.recovery && p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED {
				t.Fatal("cleanup uncertainty lost recovery authority")
			}
			// The retained failure can be observed after transient progress is gone;
			// the original server operation is never launched again.
			delete(f.service.subscriptionProgress, domain.ID(d.CorrelationID))
			if f.progressFor(op.OperationId).Diagnostic == nil {
				t.Fatal("diagnostic depended on transient presentation")
			}
			for _, protected := range []string{"account_id", string(f.input.AccountID), "native-sentinel", "secret-url", "private-native-identity", serverFixtureURL} {
				if strings.Contains(logs.String(), protected) {
					t.Fatal("subscription logs retained protected native or account metadata")
				}
			}
			observed := false
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				var record map[string]any
				if json.Unmarshal([]byte(line), &record) != nil {
					t.Fatal("subscription log was not structured JSON")
				}
				if record["msg"] == "server_subscription_native_failed" {
					observed = record["version"] == scenario.version && record["minimum_version"] == domain.CodexMinimumVersion && record["phase"] == string(scenario.phase) && record["code"] == string(scenario.code) && record["correlation_id"] == op.OperationId
				}
			}
			if !observed {
				t.Fatal("subscription log lost safe native failure attribution")
			}
		})
	}
}
