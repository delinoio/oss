// SPDX-License-Identifier: Apache-2.0
package cursor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

// This fixture executor is the only component allowed to call an HTTP client.
// It models the central send budget and revocation check for every exchange.
type authorizedExecutor struct {
	mu         sync.Mutex
	client     *http.Client
	max, count int
	revoked    bool
	requests   []inference.AdapterRequest
}

func (x *authorizedExecutor) Do(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	x.mu.Lock()
	if x.revoked {
		x.mu.Unlock()
		return nil, inference.ErrUnauthorized
	}
	if x.count >= x.max {
		x.mu.Unlock()
		return nil, &inference.Error{Status: 429, Code: "request_send_budget_exhausted"}
	}
	x.count++
	x.requests = append(x.requests, r)
	x.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, bytes.NewReader(r.Body))
	if err != nil {
		return nil, err
	}
	req.Header = r.Headers.Clone()
	return x.client.Do(req)
}
func (x *authorizedExecutor) sends() int { x.mu.Lock(); defer x.mu.Unlock(); return x.count }

type memoryAdapterStore struct {
	mu    sync.Mutex
	items map[string][]byte
	saves int
	fail  error
}

func (m *memoryAdapterStore) Load(ctx context.Context, id string) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.items[id]
	if !ok {
		return nil, inference.ErrContinuation
	}
	return append([]byte(nil), b...), nil
}
func (m *memoryAdapterStore) Save(ctx context.Context, id string, b []byte, ttl time.Duration) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	if ttl != 15*time.Minute {
		return errors.New("wrong state TTL")
	}
	if m.items == nil {
		m.items = map[string][]byte{}
	}
	m.items[id] = append([]byte(nil), b...)
	m.saves++
	return nil
}

type duplexFixture struct {
	server        *httptest.Server
	mu            sync.Mutex
	runs          map[string]*fixtureRun
	ackInitial    chan struct{}
	extraContexts uint64
	refusals      []string
	errors        chan error
}
type fixtureRun struct {
	index          int
	conversationID string
	frames         chan []byte
	checkpoint     []byte
	sequence       uint64
}

func newDuplexFixture(t *testing.T) *duplexFixture {
	t.Helper()
	f := &duplexFixture{runs: map[string]*fixtureRun{}, ackInitial: make(chan struct{}), errors: make(chan error, 16)}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}
func (f *duplexFixture) fail(w http.ResponseWriter, format string, args ...any) {
	err := fmt.Errorf(format, args...)
	select {
	case f.errors <- err:
	default:
	}
	http.Error(w, "fixture failure", 500)
}
func (f *duplexFixture) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer fixture-only-token" {
		f.fail(w, "invalid method/auth")
		return
	}
	if r.URL.Path == RunSSEPath {
		frame, err := ReadFrame(r.Body)
		if err != nil {
			f.fail(w, "run body: %v", err)
			return
		}
		m, _ := parse(frame.Payload)
		id := m.str(1)
		if id == "" || id != r.Header.Get("X-Request-Id") {
			f.fail(w, "registration id mismatch")
			return
		}
		run := &fixtureRun{frames: make(chan []byte, 8)}
		f.mu.Lock()
		run.index = len(f.runs)
		f.runs[id] = run
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case b := <-run.frames:
				if _, err = w.Write(b); err != nil {
					return
				}
				w.(http.Flusher).Flush()
				if len(b) > 0 && b[0]&2 != 0 {
					return
				}
			}
		}
	}
	if r.URL.Path != BidiAppendPath || r.Header.Get("Content-Type") != "application/proto" {
		f.fail(w, "unexpected endpoint/content type")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxFrameBytes*2+1024))
	if err != nil {
		return
	}
	envelope, err := parse(body)
	if err != nil {
		f.fail(w, "append envelope")
		return
	}
	idMsg, _ := envelope.sub(2)
	id := idMsg.str(1)
	payload, err := hex.DecodeString(envelope.str(1))
	if err != nil {
		f.fail(w, "append hex")
		return
	}
	f.mu.Lock()
	run := f.runs[id]
	if run == nil || run.sequence != envelope.get(3).value {
		f.mu.Unlock()
		f.fail(w, "append before registration or sequence mismatch")
		return
	}
	sequence := run.sequence
	run.sequence++
	f.mu.Unlock()
	m, err := parse(payload)
	if err != nil {
		f.fail(w, "append protobuf")
		return
	}
	emit := func(frame Frame) bool {
		b, _ := EncodeFrame(frame)
		select {
		case run.frames <- b:
			return true
		case <-r.Context().Done():
			return false
		}
	}
	switch sequence {
	case 0:
		request, _ := m.sub(1)
		state, _ := request.sub(1)
		if len(state) == 0 {
			f.fail(w, "no root state")
			return
		}
		f.mu.Lock()
		run.conversationID = request.str(5)
		f.mu.Unlock()
		if run.index < len(f.refusals) {
			if !emit(Frame{Flags: 2, Payload: []byte(fmt.Sprintf("{\"error\":{\"code\":%q}}", f.refusals[run.index]))}) {
				return
			}
			break
		}
		run.checkpoint = append(append([]byte(nil), request.get(1).data...), pb(5, pv(1, 123))...)
		if !emit(Frame{Payload: pb(4, join(pv(1, 1), pb(2, pb(1, state.get(1).data))))}) {
			return
		}
		if !emit(Frame{Payload: pb(2, join(pv(1, 2), pb(10, nil)))}) {
			return
		}
		if !emit(interaction(1, ps(1, "hello before acknowledgement"))) {
			return
		}
		// The decoder must remain active while this acknowledgement is pending.
		select {
		case <-f.ackInitial:
		case <-r.Context().Done():
			return
		}
	case 1:
		kv, _ := m.sub(3)
		result, _ := kv.sub(2)
		if kv.get(1).value != 1 || !json.Valid(result.get(1).data) {
			f.fail(w, "KV reply lost correlation/content")
			return
		}
	default:
		if sequence > 2+f.extraContexts {
			f.fail(w, "unexpected append %d", sequence)
			return
		}
		exec, _ := m.sub(2)
		if exec.get(1).value != sequence || exec.get(10).number == 0 {
			f.fail(w, "context reply lost correlation")
			return
		}
		if sequence < 2+f.extraContexts {
			if !emit(Frame{Payload: pb(2, join(pv(1, sequence+1), pb(10, nil)))}) {
				return
			}
			break
		}
		if !emit(Frame{Payload: pb(3, run.checkpoint)}) {
			return
		}
		if !emit(interaction(8, pv(1, 7))) {
			return
		}
		if !emit(interaction(14, nil)) {
			return
		}
		if !emit(Frame{Flags: 2, Payload: []byte(`{}`)}) {
			return
		}
	}
	w.Header().Set("Content-Type", "application/proto")
	w.WriteHeader(200)
}

func TestExecuteHTTPDuplexPersistsAndRestores(t *testing.T) {
	f := newDuplexFixture(t)
	x := &authorizedExecutor{client: f.server.Client(), max: 8}
	store := &memoryAdapterStore{}
	in := input(simpleRequest)
	in.Target.BaseURL = f.server.URL
	a := New()
	ctx, err := a.RestoreState(context.Background(), in, store)
	if err != nil {
		t.Fatal(err)
	}
	r, wire, err := a.Execute(ctx, in, x, store)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if wire != inference.Chat {
		t.Fatal(wire)
	}
	scanner := bufio.NewReader(r.Body)
	first, err := scanner.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "hello before acknowledgement") {
		t.Fatalf("did not decode during append wait: %s", first)
	}
	close(f.ackInitial)
	rest, err := io.ReadAll(scanner)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if !bytes.Contains(rest, []byte("[DONE]")) || !bytes.Contains(rest, []byte(`"completion_tokens":7`)) {
		t.Fatalf("stream %s", rest)
	}
	if x.sends() != 4 {
		t.Fatalf("shared send count=%d", x.sends())
	}
	if err = a.CaptureState(ctx, in, "resp-public", store); err != nil {
		t.Fatal(err)
	}
	data, err := store.Load(context.Background(), "resp-public")
	if err != nil {
		t.Fatal(err)
	}
	var saved savedExecution
	json.Unmarshal(data, &saved)
	if saved.Continuation.Owner.OrganizationID != "org-a" || saved.Continuation.Owner.BaseURL != f.server.URL {
		t.Fatal("state owner changed")
	}
	// A new adapter and context simulate another process using the supplied store.
	in.PreviousResponseID = "resp-public"
	in.Body = json.RawMessage(`{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"answer"},{"role":"user","content":"next"}],"tools":[{"type":"function","function":{"name":"weather"}}]}`)
	ctx, err = New().RestoreState(context.Background(), in, store)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err = New().Execute(ctx, in, x, store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if x.sends() != 8 {
		t.Fatal("second run send budget")
	}
	select {
	case e := <-f.errors:
		t.Fatal(e)
	default:
	}
}

func TestExecuteBudgetAndRevocationPreventProtocolReplies(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprint(revoke), func(t *testing.T) {
			f := newDuplexFixture(t)
			x := &authorizedExecutor{client: f.server.Client(), max: 2}
			in := input(simpleRequest)
			in.Target.BaseURL = f.server.URL
			r, _, err := New().Execute(context.Background(), in, x, &memoryAdapterStore{})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			reader := bufio.NewReader(r.Body)
			if _, err = reader.ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			if revoke {
				x.mu.Lock()
				x.revoked = true
				x.mu.Unlock()
			}
			close(f.ackInitial)
			body, err := io.ReadAll(reader)
			r.Body.Close()
			if err == nil || bytes.Contains(body, []byte("[DONE]")) {
				t.Fatal("refused reply became success")
			}
			want := "request_send_budget_exhausted"
			if revoke {
				want = "unauthorized"
			}
			requireCode(t, err, want)
			if x.sends() != 2 {
				t.Fatal("unauthorized/unbudgeted physical send")
			}
		})
	}
}

type executorFunc func(context.Context, inference.AdapterRequest) (*http.Response, error)

func (f executorFunc) Do(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
	return f(ctx, r)
}
func protoResponse(body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/connect+proto"}}, Body: body}
}

func TestExecuteCloseJoinsAppendAndFrameWorkers(t *testing.T) {
	read, write := io.Pipe()
	defer write.Close()
	started := make(chan struct{})
	exited := make(chan struct{})
	x := executorFunc(func(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
		if strings.HasSuffix(r.URL, RunSSEPath) {
			return protoResponse(read), nil
		}
		close(started)
		<-ctx.Done()
		close(exited)
		return nil, ctx.Err()
	})
	r, _, err := New().Execute(context.Background(), input(simpleRequest), x, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("append did not start")
	}
	done := make(chan struct{})
	go func() { r.Body.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not join")
	}
	select {
	case <-exited:
	default:
		t.Fatal("append worker still active after Close")
	}
	if _, err = write.Write([]byte{0}); err == nil {
		t.Fatal("upstream reader remained open")
	}
}

func TestExecuteAmbiguousAppendAfterVisibilityNeverReplays(t *testing.T) {
	read, write := io.Pipe()
	defer write.Close()
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	x := executorFunc(func(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		if strings.HasSuffix(r.URL, RunSSEPath) {
			return protoResponse(read), nil
		}
		select {
		case <-release:
			return nil, inference.ErrAmbiguous
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	r, _, err := New().Execute(context.Background(), input(simpleRequest), x, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	go func() { write.Write(serialized(t, interaction(1, ps(1, "visible")))) }()
	reader := bufio.NewReader(r.Body)
	if _, err = reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	close(release)
	b, err := io.ReadAll(reader)
	r.Body.Close()
	requireCode(t, err, "upstream_reset_replay_refused")
	if bytes.Contains(b, []byte("[DONE]")) {
		t.Fatal("ambiguous append succeeded")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("replayed %d sends", calls)
	}
}

func TestExecuteRefusesMissingAndWrongOwnerStateBeforeSending(t *testing.T) {
	store := &memoryAdapterStore{}
	in := input(simpleRequest)
	in.PreviousResponseID = "absent"
	x := executorFunc(func(context.Context, inference.AdapterRequest) (*http.Response, error) {
		t.Error("network on invalid continuation")
		return nil, errors.New("unexpected")
	})
	_, _, err := New().Execute(context.Background(), in, x, store)
	requireCode(t, err, "continuation_unavailable")
	_, session := prepared(t)
	session.checkpoint = pb(5, pv(1, 1))
	c, err := session.Snapshot(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(savedExecution{Version: 1, Continuation: c})
	store.Save(context.Background(), "owned", data, 15*time.Minute)
	in.PreviousResponseID = "owned"
	in.Principal.OrganizationID = "other"
	_, _, err = New().Execute(context.Background(), in, x, store)
	requireCode(t, err, "continuation_unavailable")
}

func TestExecuteStateWriteFailureIsNotSuccessfulTerminal(t *testing.T) {
	f := newDuplexFixture(t)
	close(f.ackInitial)
	x := &authorizedExecutor{client: f.server.Client(), max: 4}
	store := &memoryAdapterStore{fail: inference.ErrUnavailable}
	in := input(simpleRequest)
	in.Target.BaseURL = f.server.URL
	r, _, err := New().Execute(context.Background(), in, x, store)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r.Body)
	r.Body.Close()
	requireCode(t, err, "authorization_unavailable")
	if bytes.Contains(b, []byte("[DONE]")) {
		t.Fatal("failed state save became success")
	}
}

func TestExecuteSuspendsClientToolsWithoutSyntheticResult(t *testing.T) {
	read, write := io.Pipe()
	defer write.Close()
	var mu sync.Mutex
	appends := 0
	x := executorFunc(func(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
		if strings.HasSuffix(r.URL, RunSSEPath) {
			return protoResponse(read), nil
		}
		mu.Lock()
		appends++
		mu.Unlock()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	store := &memoryAdapterStore{}
	in := input(simpleRequest)
	ctx, err := New().RestoreState(context.Background(), in, store)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := New().Execute(ctx, in, x, store)
	if err != nil {
		t.Fatal(err)
	}
	frames := serialized(t, toolUpdate(3, "client-call", "ocx_client_weather", "", map[string]any{"city": "Seoul"}), Frame{Payload: pb(3, join(ps(4, "pending client call"), pb(5, pv(1, 100))))})
	written := make(chan struct{})
	go func() { defer close(written); write.Write(frames) }()
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	<-written
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"finish_reason":"tool_calls"`)) || !bytes.Contains(body, []byte("[DONE]")) {
		t.Fatalf("tool suspension: %s", body)
	}
	mu.Lock()
	count := appends
	mu.Unlock()
	if count != 1 {
		t.Fatalf("fabricated tool result append: %d", count)
	}
	if err = New().CaptureState(ctx, in, "resp-tools", store); err != nil {
		t.Fatal(err)
	}
	data, _ := store.Load(context.Background(), "resp-tools")
	var state savedExecution
	json.Unmarshal(data, &state)
	if !state.Suspended {
		t.Fatal("suspended checkpoint not marked")
	}
	in.PreviousResponseID = "resp-tools"
	in.Body = json.RawMessage(`{"messages":[{"role":"user","content":"hello"},{"role":"user","content":"new goal"}]}`)
	_, _, err = New().Execute(context.Background(), in, x, store)
	requireCode(t, err, "continuation_unavailable")
}

func TestExecuteCanceledDuringRegistration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	exited := make(chan struct{})
	x := executorFunc(func(ctx context.Context, _ inference.AdapterRequest) (*http.Response, error) {
		close(started)
		<-ctx.Done()
		close(exited)
		return nil, ctx.Err()
	})
	done := make(chan error, 1)
	go func() { _, _, err := New().Execute(ctx, input(simpleRequest), x, nil); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("registration cancellation hung")
	}
	select {
	case <-exited:
	default:
		t.Fatal("registration still active")
	}
}

func TestExecuteHonorsAttemptDeadline(t *testing.T) {
	in := input(simpleRequest)
	in.Target.AttemptTimeout = 10 * time.Millisecond
	x := executorFunc(func(ctx context.Context, _ inference.AdapterRequest) (*http.Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	_, _, err := New().Execute(context.Background(), in, x, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("attempt deadline: %v", err)
	}
}

func TestExecuteStoreFalseDoesNotPersist(t *testing.T) {
	f := newDuplexFixture(t)
	close(f.ackInitial)
	x := &authorizedExecutor{client: f.server.Client(), max: 4}
	store := &memoryAdapterStore{}
	in := input(`{"store":false,"messages":[{"role":"user","content":"hi"}]}`)
	in.Target.BaseURL = f.server.URL
	r, _, err := New().Execute(context.Background(), in, x, store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.saves != 0 {
		t.Fatal("store:false persisted content")
	}
}

func TestExecuteAppendResponseBoundAndStatus(t *testing.T) {
	for _, status := range []int{200, 401, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			read, write := io.Pipe()
			defer write.Close()
			x := executorFunc(func(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
				if strings.HasSuffix(r.URL, RunSSEPath) {
					return protoResponse(read), nil
				}
				body := "{}"
				if status == 200 {
					body = strings.Repeat("x", 65537)
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			r, _, err := New().Execute(context.Background(), input(simpleRequest), x, nil)
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(r.Body)
			r.Body.Close()
			code := "cursor_append_response_limit"
			if status == 401 {
				code = "cursor_authentication_failed"
			}
			if status == 429 {
				code = "cursor_rate_limit_exceeded"
			}
			requireCode(t, err, code)
			if bytes.Contains(b, []byte("[DONE]")) {
				t.Fatal("invalid append succeeded")
			}
		})
	}
}

type integrationAuth struct {
	mu          sync.Mutex
	dispatches  int
	revokeAfter int
	denied      int
}

func (*integrationAuth) Admit(context.Context, string) (inference.Principal, func(), error) {
	p := input(simpleRequest).Principal
	p.KeyID = "fixture-key"
	return p, func() {}, nil
}
func (*integrationAuth) BeforeSend(ctx context.Context, _ inference.Principal) error {
	return ctx.Err()
}
func (a *integrationAuth) Dispatch(ctx context.Context, p inference.Principal, start func(context.Context) error) error {
	if err := a.BeforeSend(ctx, p); err != nil {
		return err
	}
	a.mu.Lock()
	if a.revokeAfter > 0 && a.dispatches >= a.revokeAfter {
		a.denied++
		a.mu.Unlock()
		return inference.ErrUnauthorized
	}
	a.dispatches++
	a.mu.Unlock()
	return start(ctx)
}

type integrationRouter struct{ route inference.Route }

func (r integrationRouter) Resolve(context.Context, inference.Principal, string, inference.Operation) (inference.Route, error) {
	return r.route, nil
}
func (r integrationRouter) Models(context.Context, inference.Principal) ([]inference.Model, error) {
	return nil, nil
}

type integrationCredentials struct{}

func (integrationCredentials) Select(context.Context, inference.Principal, inference.Target, string) (inference.Credential, error) {
	return input(simpleRequest).Credential, nil
}
func (integrationCredentials) Refresh(context.Context, inference.Principal, inference.Target, inference.Credential) (inference.Credential, error) {
	return inference.Credential{}, errors.New("unexpected refresh")
}

type integrationUsage struct{ record inference.UsageRecord }

func (u *integrationUsage) Record(_ context.Context, _ inference.Principal, r inference.UsageRecord) error {
	u.record = r
	return nil
}

type integrationState struct{ memoryAdapterStore }

func (s *integrationState) LoadProviderState(ctx context.Context, _ inference.Principal, namespace, id string) (inference.ProviderState, error) {
	b, e := s.Load(ctx, namespace+id)
	return inference.ProviderState{Data: b, ExpiresAt: time.Now().Add(time.Minute)}, e
}
func (s *integrationState) SaveProviderState(ctx context.Context, _ inference.Principal, namespace, id string, state inference.ProviderState) error {
	return s.Save(ctx, namespace+id, state.Data, 15*time.Minute)
}

func TestCoreExecutingAdapterRegistrationAndSharedBudget(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		budget, revokeAfter, sends int
		success                    bool
	}{
		{"short_budget", 2, 0, 2, false},
		{"legacy_four_send_budget", 4, 0, 4, false},
		{"cursor_physical_budget", 258, 0, 8, true},
		{"revoked_later_append", 258, 5, 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDuplexFixture(t)
			f.extraContexts = 4
			close(f.ackInitial)
			auth := &integrationAuth{revokeAfter: tc.revokeAfter}
			usage := &integrationUsage{}
			store := &integrationState{}
			target := input(simpleRequest).Target
			target.BaseURL = f.server.URL
			h, err := inference.New(inference.Config{Adapters: map[inference.Wire]inference.Adapter{Wire: New()}, Authorizer: auth, Router: integrationRouter{inference.Route{Targets: []inference.Target{target}, MaxSends: tc.budget}}, Credentials: integrationCredentials{}, Usage: usage, ProviderStates: store, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{f.server.URL}}})
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			json.Unmarshal([]byte(simpleRequest), &body)
			body["model"] = "fixture"
			body["stream"] = true
			b, _ := json.Marshal(body)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
			request.Header.Set("Authorization", "Bearer client-fixture")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, request)
			if usage.record.Sends != tc.sends {
				t.Fatalf("actual shared budget=%d, want %d; response=%d %s", usage.record.Sends, tc.sends, rec.Code, rec.Body.String())
			}
			auth.mu.Lock()
			dispatches := auth.dispatches
			denied := auth.denied
			auth.mu.Unlock()
			if tc.revokeAfter > 0 && denied != 1 {
				t.Fatalf("revoked dispatch attempts=%d, want 1", denied)
			}
			f.mu.Lock()
			runs, appends := len(f.runs), uint64(0)
			for _, run := range f.runs {
				appends += run.sequence
			}
			f.mu.Unlock()
			if runs != 1 || int(appends)+runs != tc.sends {
				t.Fatalf("physical requests: registrations=%d appends=%d, want %d total", runs, appends, tc.sends)
			}
			select {
			case err := <-f.errors:
				t.Fatal(err)
			default:
			}
			if dispatches != tc.sends {
				t.Fatalf("dispatch authorization count=%d", dispatches)
			}
			if tc.success {
				if !strings.Contains(rec.Body.String(), "[DONE]") {
					t.Fatalf("registered adapter failed: %s", rec.Body.String())
				}
				if usage.record.Usage.OutputTokens == nil || *usage.record.Usage.OutputTokens != 7 {
					t.Fatal("confirmed usage missing")
				}
				store.mu.Lock()
				saves := store.saves
				store.mu.Unlock()
				if saves != 1 {
					t.Fatalf("state saves=%d", saves)
				}
			} else {
				if strings.Contains(rec.Body.String(), "[DONE]") || !strings.Contains(rec.Body.String(), "\"error\"") {
					t.Fatalf("refusal did not produce a failed terminal: %s", rec.Body.String())
				}
				if tc.revokeAfter > 0 && !strings.Contains(rec.Body.String(), "hello before acknowledgement") {
					t.Fatalf("later revocation did not follow visible output: %s", rec.Body.String())
				}
				store.mu.Lock()
				saves := store.saves
				store.mu.Unlock()
				if saves != 0 {
					t.Fatalf("failed stream persisted %d states", saves)
				}
			}
		})
	}
}
