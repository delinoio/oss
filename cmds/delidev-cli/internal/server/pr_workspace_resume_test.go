// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// countingPATs counts token retrievals so tests can prove that a terminated
// request never touches the token again.
type countingPATs struct {
	integrationSecrets
	gets atomic.Int32
}

func (c *countingPATs) Get(ctx context.Context, ref credentials.PATRef) ([]byte, error) {
	c.gets.Add(1)
	return c.integrationSecrets.Get(ctx, ref)
}

type workspaceProviderFunc func(ctx context.Context, call int, token []byte, budget *gh.WorkspaceBudget) (gh.WorkspaceObservation, error)

type scriptedWorkspaces struct {
	calls atomic.Int32
	fn    workspaceProviderFunc
}

func (p *scriptedWorkspaces) WorkspaceWithBudget(ctx context.Context, token []byte, _, _ string, _ []string, budget *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
	return p.fn(ctx, int(p.calls.Add(1)), token, budget)
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) records(t *testing.T, name string) []map[string]any {
	t.Helper()
	var matches []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal("invalid JSON log", err)
		}
		if record["msg"] == name {
			matches = append(matches, record)
		}
	}
	return matches
}

func requireWorkspaceEvents(t *testing.T, logs *syncBuffer, name string, counts ...int) []map[string]any {
	t.Helper()
	records := logs.records(t, name)
	if len(records) != len(counts) {
		t.Fatalf("%s: got %d records, want %d: %s", name, len(records), len(counts), logs.String())
	}
	for i, record := range records {
		if record["attempt_count"] != float64(counts[i]) || record["correlation_id"] != "correlation-3361" {
			t.Fatalf("%s record %d lost admission count or correlation: %+v", name, i, record)
		}
	}
	return records
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (e *eventLog) add(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, name)
}
func (e *eventLog) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.events...)
}

type workspaceResult struct {
	response *connect.Response[pb.GetPullRequestWorkspaceResponse]
	err      error
}

type workspaceHarness struct {
	t        *testing.T
	f        *integrationFixture
	profile  *pb.Resource
	repo     *pb.Resource
	ctx      context.Context
	provider *scriptedWorkspaces
	tokens   *countingPATs
	logs     *syncBuffer
	events   *eventLog
	// baseGets is the token retrieval count after fixture setup.
	baseGets int32
}

func newWorkspaceHarness(t *testing.T, fn workspaceProviderFunc) *workspaceHarness {
	t.Helper()
	f := newIntegrationFixture(t)
	h := &workspaceHarness{t: t, f: f, events: &eventLog{}, logs: &syncBuffer{}}
	h.tokens = &countingPATs{integrationSecrets: f.native}
	f.service.integrationSecrets = h.tokens
	f.service.logger = slog.New(slog.NewJSONHandler(h.logs, nil))
	h.profile = f.replace(profileMutation(f.save("Selected")), "workspace-resume-token").Profile
	h.repo = f.repository(domain.ID(h.profile.Id))
	h.ctx = domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	h.provider = &scriptedWorkspaces{fn: fn}
	f.service.githubWorkspaces = h.provider
	h.baseGets = h.tokens.gets.Load()
	return h
}

// observation returns a structurally valid incomplete workspace whose reasons
// identify which attempt produced it.
func observation(reasons ...string) gh.WorkspaceObservation {
	return gh.WorkspaceObservation{
		Repository: domain.RemoteRepository{Provider: domain.GitHubCom, ID: "9", NodeID: "R_9", Owner: "fixture-owner", Name: "repo"},
		Nodes:      []domain.PRWorkspaceNode{}, Edges: []domain.PRWorkspaceEdge{}, Reasons: reasons,
		State: domain.PRWorkspaceIncomplete, Avatars: map[string]string{},
	}
}

func (h *workspaceHarness) request() *connect.Request[pb.GetPullRequestWorkspaceRequest] {
	req := connect.NewRequest(&pb.GetPullRequestWorkspaceRequest{RepositoryId: h.repo.Id, ExpectedRevision: h.repo.Revision, SeedNumbers: []string{"101", "102"}})
	req.Header().Set(rpc.CorrelationHeader, "correlation-3361")
	return req
}

func (h *workspaceHarness) start(ctx context.Context) <-chan workspaceResult {
	out := make(chan workspaceResult, 1)
	go func() {
		response, err := h.f.service.GetPullRequestWorkspace(ctx, h.request())
		out <- workspaceResult{response, err}
	}()
	return out
}

// foreground runs one foreground read whose callback blocks until release closes.
func (h *workspaceHarness) foreground(name string, entered chan<- struct{}, release <-chan struct{}) <-chan error {
	out := make(chan error, 1)
	go func() {
		_, err := h.f.service.withRepositoryIntegration(h.ctx, domain.ID(h.repo.Id), "detail", "foreground-"+name, func(ctx context.Context, _ []byte, _ repositoryIntegrationSelection) error {
			h.events.add(name + ":start")
			if entered != nil {
				close(entered)
			}
			if release != nil {
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			h.events.add(name + ":end")
			return nil
		})
		out <- err
	}()
	return out
}

func (h *workspaceHarness) await(result <-chan workspaceResult) workspaceResult {
	h.t.Helper()
	select {
	case r := <-result:
		return r
	case <-time.After(10 * time.Second):
		h.t.Fatal("workspace request did not finish")
		return workspaceResult{}
	}
}

func (h *workspaceHarness) decode(r workspaceResult) domain.PRWorkspace {
	h.t.Helper()
	if r.err != nil {
		h.t.Fatal("workspace failed", r.err)
	}
	var value domain.PRWorkspace
	if err := json.Unmarshal(r.response.Msg.DocumentJson, &value); err != nil {
		h.t.Fatal(err)
	}
	return value
}

func waitFor(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for " + what)
	}
}

func (h *workspaceHarness) counters() (int, int) {
	h.t.Helper()
	unlock, err := h.f.service.lockIntegrations(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	defer unlock()
	profile := domain.ID(h.profile.Id)
	return h.f.service.integrationQueued[profile], h.f.service.integrationForeground[profile]
}

func isZeroed(token []byte) bool {
	for _, b := range token {
		if b != 0 {
			return false
		}
	}
	return len(token) > 0
}

// Scenario 1: the displaced attempt joins and clears its token before the
// foreground read starts, the foreground read finishes before the fresh attempt
// starts, and the original RPC publishes only the final observation.
func TestWorkspaceResumesAfterForegroundDisplacement(t *testing.T) {
	var firstToken []byte
	firstJoined := make(chan struct{})
	enteredFirst := make(chan struct{})
	var h *workspaceHarness
	h = newWorkspaceHarness(t, func(ctx context.Context, call int, token []byte, _ *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
		if call == 1 {
			firstToken = token
			close(enteredFirst)
			<-ctx.Done()
			h.events.add("attempt1:end")
			close(firstJoined)
			return observation("partial_first"), ctx.Err()
		}
		h.events.add("attempt2:start")
		return observation("final"), nil
	})
	pending := h.start(h.ctx)
	waitFor(t, "first attempt", enteredFirst)
	entered, release := make(chan struct{}), make(chan struct{})
	detail := h.foreground("detail", entered, release)
	waitFor(t, "foreground read", entered)
	select {
	case <-firstJoined:
	default:
		t.Fatal("foreground started before the displaced attempt joined")
	}
	if !isZeroed(firstToken) {
		t.Fatal("displaced attempt token was not cleared before the foreground read")
	}
	select {
	case r := <-pending:
		t.Fatal("original RPC finished while the foreground read was active", r.err)
	case <-time.After(150 * time.Millisecond):
	}
	if h.provider.calls.Load() != 1 {
		t.Fatal("resumed attempt started while the foreground read held the profile")
	}
	close(release)
	if err := <-detail; err != nil {
		t.Fatal(err)
	}
	value := h.decode(h.await(pending))
	if got := h.events.snapshot(); strings.Join(got, ",") != "attempt1:end,detail:start,detail:end,attempt2:start" {
		t.Fatal("unexpected attempt order", got)
	}
	if h.provider.calls.Load() != 2 || len(value.Reasons) != 1 || value.Reasons[0] != "final" {
		t.Fatal("original RPC did not publish only the fresh observation", value.Reasons, h.provider.calls.Load())
	}
	log := h.logs.String()
	for _, want := range []string{"repository_integration_read_preempted", "pull_request_workspace_requeued", "pull_request_workspace_resumed", "pull_request_workspace_finished", `"attempt_count":2`, `"correlation_id":"correlation-3361"`, `"outcome":"succeeded"`} {
		if !strings.Contains(log, want) {
			t.Fatal("missing structured log", want, log)
		}
	}
	requireWorkspaceEvents(t, h.logs, "repository_integration_read_preempted", 1)
	requireWorkspaceEvents(t, h.logs, "pull_request_workspace_requeued", 1)
	requireWorkspaceEvents(t, h.logs, "pull_request_workspace_resumed", 2)
	finished := requireWorkspaceEvents(t, h.logs, "pull_request_workspace_finished", 2)
	if finished[0]["outcome"] != string(workspaceOutcomeSucceeded) || finished[0]["error_code"] != "" {
		t.Fatal("successful final log did not reflect the complete response", finished[0])
	}
	for _, leak := range []string{"workspace-resume-token", "partial_first"} {
		if strings.Contains(log, leak) {
			t.Fatal("log carries content", leak)
		}
	}
	if queued, foreground := h.counters(); queued != 0 || foreground != 0 {
		t.Fatal("queue counters leaked", queued, foreground)
	}
}

// Scenario 2: several foreground reads precede every resumed attempt, only one
// read owns the profile at a time and the active deadline is frozen.
func TestWorkspaceResumptionKeepsForegroundPriorityAndFrozenDeadline(t *testing.T) {
	var active, maxActive atomic.Int32
	var mu sync.Mutex
	var deadlines []time.Time
	enter := func() {
		n := active.Add(1)
		for {
			m := maxActive.Load()
			if n <= m || maxActive.CompareAndSwap(m, n) {
				break
			}
		}
	}
	entered := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var h *workspaceHarness
	h = newWorkspaceHarness(t, func(ctx context.Context, call int, _ []byte, _ *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
		enter()
		defer active.Add(-1)
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("attempt has no deadline")
		}
		mu.Lock()
		deadlines = append(deadlines, deadline)
		mu.Unlock()
		h.events.add("attempt" + string(rune('0'+call)))
		if call <= 2 {
			close(entered[call-1])
			<-ctx.Done()
			return observation("interrupted"), ctx.Err()
		}
		return observation("final"), nil
	})
	pending := h.start(h.ctx)
	waitFor(t, "attempt 1", entered[0])
	release := make(chan struct{})
	var foreground []<-chan error
	for _, name := range []string{"fg1", "fg2", "fg3"} {
		foreground = append(foreground, h.foreground(name, nil, release))
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	for _, c := range foreground {
		if err := <-c; err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "attempt 2", entered[1])
	late := h.foreground("fg4", nil, nil)
	if err := <-late; err != nil {
		t.Fatal(err)
	}
	h.decode(h.await(pending))
	events := h.events.snapshot()
	index := func(name string) int {
		for i, e := range events {
			if e == name {
				return i
			}
		}
		t.Fatal("missing event", name, events)
		return -1
	}
	for _, name := range []string{"fg1:end", "fg2:end", "fg3:end"} {
		if index(name) > index("attempt2") {
			t.Fatal("queued foreground read followed a resumed attempt", events)
		}
	}
	if index("fg4:end") > index("attempt3") || index("attempt1") > index("fg1:start") {
		t.Fatal("foreground priority lost", events)
	}
	if maxActive.Load() != 1 {
		t.Fatal("more than one workspace attempt was active", maxActive.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deadlines) != 3 || !deadlines[0].Equal(deadlines[1]) || !deadlines[1].Equal(deadlines[2]) {
		t.Fatal("active deadline was not frozen at first activation", deadlines)
	}
	if remaining := time.Until(deadlines[0]); remaining > integrationActiveBound {
		t.Fatal("active deadline exceeds its bound", remaining)
	}
	requireWorkspaceEvents(t, h.logs, "repository_integration_read_preempted", 1, 2)
	requireWorkspaceEvents(t, h.logs, "pull_request_workspace_requeued", 1, 2)
	requireWorkspaceEvents(t, h.logs, "pull_request_workspace_resumed", 2, 3)
	requireWorkspaceEvents(t, h.logs, "pull_request_workspace_finished", 3)
}

func TestWorkspaceFailedForegroundJoinCannotReplay(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel bool
	}{
		{name: "foreground caller canceled", cancel: true},
		{name: "join timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			h := newWorkspaceHarness(t, func(ctx context.Context, _ int, _ []byte, _ *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
				close(entered)
				<-ctx.Done()
				close(canceled)
				<-release
				return observation("interrupted"), ctx.Err()
			})
			pending := h.start(h.ctx)
			waitFor(t, "workspace provider", entered)
			fgCtx, cancel := context.WithCancel(h.ctx)
			defer cancel()
			var foregroundCalls atomic.Int32
			foreground := make(chan error, 1)
			go func() {
				_, err := h.f.service.withRepositoryIntegration(fgCtx, domain.ID(h.repo.Id), "detail", "foreground", func(context.Context, []byte, repositoryIntegrationSelection) error {
					foregroundCalls.Add(1)
					return nil
				})
				foreground <- err
			}()
			waitFor(t, "workspace cancellation", canceled)
			if tc.cancel {
				cancel()
			}
			select {
			case err := <-foreground:
				if err == nil {
					t.Fatal("failed foreground displacement succeeded")
				}
			case <-time.After(7 * time.Second):
				t.Fatal("foreground join did not terminate")
			}
			close(release)
			r := h.await(pending)
			if r.response != nil || connect.CodeOf(r.err) != connect.CodeUnavailable || foregroundCalls.Load() != 0 || h.provider.calls.Load() != 1 || h.tokens.gets.Load()-h.baseGets != 1 {
				t.Fatal("failed foreground join replayed or published workspace", r.err, foregroundCalls.Load(), h.provider.calls.Load(), h.tokens.gets.Load()-h.baseGets)
			}
			if len(h.logs.records(t, "repository_integration_displacement_failed")) != 1 {
				t.Fatal("failed displacement has no structured evidence")
			}
			finished := requireWorkspaceEvents(t, h.logs, "pull_request_workspace_finished", 1)
			if finished[0]["outcome"] != string(workspaceOutcomeFailed) {
				t.Fatal("failed displacement logged success", finished[0])
			}
			if queued, foreground := h.counters(); queued != 0 || foreground != 0 {
				t.Fatal("queue counters leaked", queued, foreground)
			}
		})
	}
}

// Scenario 3: parent cancellation terminates active and queued reads promptly
// with no later token access or provider attempt.
func TestWorkspaceParentCancellationDoesNotResume(t *testing.T) {
	t.Run("active", func(t *testing.T) {
		entered := make(chan struct{})
		h := newWorkspaceHarness(t, func(ctx context.Context, _ int, _ []byte, _ *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
			close(entered)
			<-ctx.Done()
			return observation("interrupted"), ctx.Err()
		})
		ctx, cancel := context.WithCancel(h.ctx)
		pending := h.start(ctx)
		waitFor(t, "attempt", entered)
		cancel()
		if r := h.await(pending); connect.CodeOf(r.err) != connect.CodeCanceled {
			t.Fatal("parent cancellation was not terminal", r.err)
		}
		if h.provider.calls.Load() != 1 || h.tokens.gets.Load()-h.baseGets != 1 {
			t.Fatal("work continued after cancellation")
		}
	})
	t.Run("queued", func(t *testing.T) {
		entered := make(chan struct{})
		h := newWorkspaceHarness(t, func(ctx context.Context, _ int, _ []byte, _ *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
			close(entered)
			<-ctx.Done()
			return observation("interrupted"), ctx.Err()
		})
		ctx, cancel := context.WithCancel(h.ctx)
		pending := h.start(ctx)
		waitFor(t, "attempt", entered)
		fgEntered, release := make(chan struct{}), make(chan struct{})
		detail := h.foreground("detail", fgEntered, release)
		waitFor(t, "foreground", fgEntered)
		time.Sleep(60 * time.Millisecond)
		cancel()
		if r := h.await(pending); connect.CodeOf(r.err) != connect.CodeCanceled {
			t.Fatal("queued cancellation was not terminal", r.err)
		}
		close(release)
		if err := <-detail; err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Millisecond)
		if h.provider.calls.Load() != 1 || h.tokens.gets.Load()-h.baseGets != 2 {
			t.Fatal("canceled request accessed a token or provider again", h.provider.calls.Load(), h.tokens.gets.Load())
		}
		if queued, foreground := h.counters(); queued != 0 || foreground != 0 {
			t.Fatal("queue counters leaked", queued, foreground)
		}
	})
	t.Run("simultaneous", func(t *testing.T) {
		for range 8 {
			entered := make(chan struct{})
			h := newWorkspaceHarness(t, func(ctx context.Context, _ int, _ []byte, _ *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
				close(entered)
				<-ctx.Done()
				return observation("interrupted"), ctx.Err()
			})
			ctx, cancel := context.WithCancel(h.ctx)
			pending := h.start(ctx)
			waitFor(t, "attempt", entered)
			detail := h.foreground("detail", nil, nil)
			cancel()
			if err := <-detail; err != nil {
				t.Fatal(err)
			}
			if r := h.await(pending); connect.CodeOf(r.err) != connect.CodeCanceled {
				t.Fatal("simultaneous cancellation was not terminal", r.err)
			}
			if h.provider.calls.Load() != 1 {
				t.Fatal("provider restarted after cancellation")
			}
		}
	})
}

// Scenario 4: changed or revoked authority and shutdown terminate a suspended
// request without replay or publication.
func TestWorkspaceSuspendedRequestTerminatesWhenScopeChanges(t *testing.T) {
	cases := []struct {
		name   string
		code   connect.Code
		change func(*workspaceHarness)
	}{
		{"repository revision", connect.CodeAborted, func(h *workspaceHarness) {
			if _, err := h.f.service.Store.Mutate(h.ctx, domain.NewID(), "test.repository.revision", h.repo.Id, func(tx *store.Tx) (any, error) {
				record, err := tx.Get(domain.RepositoryKind, domain.ID(h.repo.Id))
				if err != nil {
					return nil, err
				}
				repository, err := store.Decode[domain.Repository](record)
				if err != nil {
					return nil, err
				}
				repository.Name = "Renamed while suspended"
				return tx.Put(domain.RepositoryKind, domain.ID(h.repo.Id), record.Revision, "", "", repository)
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"token generation", connect.CodeAborted, func(h *workspaceHarness) {
			h.f.replace(profileMutation(h.profile), "replacement-token")
		}},
		{"shutdown", connect.CodeCanceled, func(h *workspaceHarness) { h.f.service.stopping.Store(true) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entered := make(chan struct{})
			h := newWorkspaceHarness(t, func(ctx context.Context, _ int, _ []byte, _ *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
				close(entered)
				<-ctx.Done()
				return observation("interrupted"), ctx.Err()
			})
			pending := h.start(h.ctx)
			waitFor(t, "attempt", entered)
			fgEntered, release := make(chan struct{}), make(chan struct{})
			detail := h.foreground("detail", fgEntered, release)
			waitFor(t, "foreground", fgEntered)
			tc.change(h)
			close(release)
			// The foreground read may legitimately fail its own post-read scope check.
			<-detail
			r := h.await(pending)
			if r.response != nil || connect.CodeOf(r.err) != tc.code {
				t.Fatal("suspended request did not terminate", r.err)
			}
			if h.provider.calls.Load() != 1 {
				t.Fatal("replay used replacement authority")
			}
		})
	}
	t.Run("profile deleted", func(t *testing.T) {
		entered := make(chan struct{})
		h := newWorkspaceHarness(t, func(ctx context.Context, _ int, _ []byte, _ *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
			close(entered)
			<-ctx.Done()
			return observation("interrupted"), ctx.Err()
		})
		pending := h.start(h.ctx)
		waitFor(t, "attempt", entered)
		fgEntered, release := make(chan struct{}), make(chan struct{})
		detail := h.foreground("detail", fgEntered, release)
		waitFor(t, "foreground", fgEntered)
		unlock, err := h.f.service.lockIntegrations(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = h.f.service.Store.Mutate(h.ctx, domain.NewID(), "test.profile.delete", h.profile.Id, func(tx *store.Tx) (any, error) {
			return nil, tx.Delete(domain.IntegrationKind, domain.ID(h.profile.Id), h.profile.Revision)
		})
		unlock()
		if err != nil {
			t.Fatal(err)
		}
		close(release)
		<-detail
		if r := h.await(pending); r.response != nil || r.err == nil {
			t.Fatal("deleted profile request was published", r.err)
		}
		if h.provider.calls.Load() != 1 {
			t.Fatal("deleted profile was replayed")
		}
	})
	t.Run("actor revoked", func(t *testing.T) {
		entered := make(chan struct{})
		h := newWorkspaceHarness(t, func(ctx context.Context, _ int, _ []byte, _ *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
			close(entered)
			<-ctx.Done()
			return observation("interrupted"), ctx.Err()
		})
		device, machine := domain.NewID(), domain.NewID()
		put := func(revoked bool, revision uint64) {
			if _, err := h.f.service.Store.Mutate(h.ctx, domain.NewID(), "test.device", device, func(tx *store.Tx) (any, error) {
				return tx.Put(domain.DeviceKind, device, revision, "", "", domain.Device{Name: "Client", Type: domain.ClientDevice, MachineID: machine, Revoked: revoked, PairedAt: time.Now().UTC()})
			}); err != nil {
				t.Fatal(err)
			}
		}
		put(false, 0)
		actor := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: device, MachineID: machine})
		pending := h.start(actor)
		waitFor(t, "attempt", entered)
		fgEntered, release := make(chan struct{}), make(chan struct{})
		detail := h.foreground("detail", fgEntered, release)
		waitFor(t, "foreground", fgEntered)
		put(true, 1)
		close(release)
		<-detail
		if r := h.await(pending); r.response != nil || connect.CodeOf(r.err) != connect.CodeUnauthenticated {
			t.Fatal("revoked actor request was not terminal", r.err)
		}
		if h.provider.calls.Load() != 1 {
			t.Fatal("revoked actor was replayed")
		}
	})
}

// Scenario 5: only the typed scheduler cause is replayed.
func TestWorkspaceOnlySchedulerDisplacementIsReplayed(t *testing.T) {
	cases := []struct {
		name       string
		fn         workspaceProviderFunc
		code       connect.Code
		domainCode domain.Code
	}{
		{"provider failure", func(context.Context, int, []byte, *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
			return gh.WorkspaceObservation{}, domain.Fail(domain.Unavailable, "Provider failed.", "Retry.")
		}, connect.CodeUnavailable, domain.Unavailable},
		{"malformed observation", func(context.Context, int, []byte, *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
			malformed := observation("bad")
			malformed.Nodes = nil
			return malformed, nil
		}, connect.CodeFailedPrecondition, domain.RecoveryRequired},
		{"untyped cancellation", func(context.Context, int, []byte, *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
			return gh.WorkspaceObservation{}, context.Canceled
		}, connect.CodeCanceled, domain.Canceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newWorkspaceHarness(t, tc.fn)
			r := h.await(h.start(h.ctx))
			if r.response != nil || connect.CodeOf(r.err) != tc.code {
				t.Fatal("unexpected terminal outcome", r.err)
			}
			if h.provider.calls.Load() != 1 {
				t.Fatal("non-scheduler outcome was replayed", h.provider.calls.Load())
			}
			if !strings.Contains(h.logs.String(), `"attempt_count":1`) {
				t.Fatal("final outcome log lacks attempt count")
			}
			finished := requireWorkspaceEvents(t, h.logs, "pull_request_workspace_finished", 1)
			if finished[0]["outcome"] != string(workspaceOutcomeFailed) || finished[0]["error_code"] != string(tc.domainCode) {
				t.Fatal("final log disagrees with RPC failure", finished[0])
			}
		})
	}
	t.Run("response limit", func(t *testing.T) {
		h := newWorkspaceHarness(t, func(context.Context, int, []byte, *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
			value := observation("large")
			now, no := time.Now().UTC(), false
			for i := 101; i < 151; i++ {
				number := strconv.Itoa(i)
				item := domain.RepositoryItem{IdentitySource: domain.RepositoryPullRequestIdentity, Provider: domain.GitHubCom, Kind: domain.RepositoryPullRequest, ID: number, NodeID: "PR_" + number, Number: number, Title: strings.Repeat("\x01", 4096), State: domain.RepositoryItemOpen, CreatedAt: now, UpdatedAt: now, URL: domain.RepositoryItemURL("fixture-owner", "repo", domain.RepositoryPullRequest, number), Draft: &no, Merged: &no, BaseRef: "main", BaseSHA: strings.Repeat("a", 40), HeadRef: "feature", HeadSHA: strings.Repeat("b", 40)}
				if err := item.ValidateWorkspaceNode(value.Repository, domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: number}); err != nil {
					t.Error("response limit fixture has an invalid node", number, err)
				}
				value.Nodes = append(value.Nodes, domain.PRWorkspaceNode{Item: item, Seed: i == 101 || i == 102})
			}
			return value, nil
		})
		r := h.await(h.start(h.ctx))
		if r.response != nil || connect.CodeOf(r.err) != connect.CodeResourceExhausted || h.provider.calls.Load() != 1 {
			t.Fatal("oversized valid workspace was published or replayed", r.err, h.provider.calls.Load())
		}
		finished := requireWorkspaceEvents(t, h.logs, "pull_request_workspace_finished", 1)
		if finished[0]["outcome"] != string(workspaceOutcomeFailed) || finished[0]["error_code"] != string(domain.ResourceExhausted) {
			t.Fatal("response limit logged success", finished[0])
		}
	})
	t.Run("profile cleanup cancellation", func(t *testing.T) {
		entered := make(chan struct{})
		h := newWorkspaceHarness(t, func(ctx context.Context, _ int, _ []byte, _ *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
			close(entered)
			<-ctx.Done()
			return observation("interrupted"), ctx.Err()
		})
		pending := h.start(h.ctx)
		waitFor(t, "attempt", entered)
		unlock, err := h.f.service.lockIntegrations(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		err = h.f.service.stopIntegrationCheck(context.Background(), domain.ID(h.profile.Id))
		unlock()
		if err != nil {
			t.Fatal(err)
		}
		if r := h.await(pending); connect.CodeOf(r.err) != connect.CodeCanceled {
			t.Fatal("untyped stop was not terminal", r.err)
		}
		if h.provider.calls.Load() != 1 {
			t.Fatal("untyped stop was replayed")
		}
	})
	t.Run("deadline", func(t *testing.T) {
		h := newWorkspaceHarness(t, func(ctx context.Context, _ int, _ []byte, _ *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
			<-ctx.Done()
			return observation("interrupted"), ctx.Err()
		})
		ctx, cancel := context.WithTimeout(h.ctx, 100*time.Millisecond)
		defer cancel()
		r := h.await(h.start(ctx))
		if r.response != nil || r.err == nil || h.provider.calls.Load() != 1 {
			t.Fatal("caller deadline was replayed", r.err)
		}
	})
	t.Run("avatar is not resumed", func(t *testing.T) {
		h := newWorkspaceHarness(t, nil)
		entered := make(chan struct{})
		avatar := make(chan error, 1)
		go func() {
			_, err := h.f.service.withRepositoryIntegration(h.ctx, domain.ID(h.repo.Id), "avatar", "avatar", func(ctx context.Context, _ []byte, _ repositoryIntegrationSelection) error {
				close(entered)
				<-ctx.Done()
				return ctx.Err()
			})
			avatar <- err
		}()
		waitFor(t, "avatar read", entered)
		if err := <-h.foreground("detail", nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := <-avatar; err == nil {
			t.Fatal("displaced avatar read was resumed")
		}
	})
}

// Scenario 6: a partial observation of an interrupted attempt is discarded.
func TestWorkspaceInterruptedObservationIsNeverPublished(t *testing.T) {
	entered := make(chan struct{})
	h := newWorkspaceHarness(t, func(ctx context.Context, call int, _ []byte, _ *gh.WorkspaceBudget) (gh.WorkspaceObservation, error) {
		if call == 1 {
			close(entered)
			<-ctx.Done()
			partial := observation("partial_graph")
			partial.Avatars["19"] = "https://avatars.githubusercontent.com/u/19?v=4"
			return partial, ctx.Err()
		}
		return observation("second_graph"), nil
	})
	pending := h.start(h.ctx)
	waitFor(t, "attempt", entered)
	if err := <-h.foreground("detail", nil, nil); err != nil {
		t.Fatal(err)
	}
	value := h.decode(h.await(pending))
	if len(value.Reasons) != 1 || value.Reasons[0] != "second_graph" || len(value.Nodes) != 0 {
		t.Fatal("interrupted observation leaked", value)
	}
	if len(h.f.service.prAvatars) != 0 {
		t.Fatal("interrupted avatar observation leaked")
	}
}

// Admission limits and frozen deadlines hold for a resumed attempt.
func TestResumedAdmissionKeepsLimits(t *testing.T) {
	setup := func(t *testing.T) (*workspaceHarness, *integrationResume) {
		h := newWorkspaceHarness(t, nil)
		var selected repositoryIntegrationSelection
		err := h.f.service.Store.Read(h.ctx, func(tx *store.Tx) error {
			var e error
			selected, e = repositoryIntegrationFromTx(tx, domain.ID(h.repo.Id))
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		return h, &integrationResume{initialized: true, scope: selected, activeDeadline: time.Now().Add(time.Minute)}
	}
	admit := func(h *workspaceHarness, resume *integrationResume) error {
		_, err := h.f.service.admitRepositoryIntegration(h.ctx, domain.ID(h.repo.Id), "workspace", "limits", 1, resume, func(context.Context, []byte, repositoryIntegrationSelection) error {
			t.Error("read admitted despite a limit")
			return nil
		})
		return err
	}
	occupy := func(h *workspaceHarness, mutate func(*Service, domain.ID)) {
		unlock, err := h.f.service.lockIntegrations(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		mutate(h.f.service, domain.ID(h.profile.Id))
	}
	t.Run("queue bound", func(t *testing.T) {
		h, resume := setup(t)
		occupy(h, func(s *Service, profile domain.ID) {
			s.integrationChecks = map[domain.ID]*integrationCheck{profile: {done: make(chan struct{}), cancel: func() {}}}
			s.integrationQueued = map[domain.ID]int{profile: 64}
			s.integrationForeground = map[domain.ID]int{}
		})
		if err := admit(h, resume); domain.SafeError(err).Code != domain.ResourceExhausted {
			t.Fatal("resumed attempt exceeded the profile queue bound", err)
		}
	})
	t.Run("global active bound", func(t *testing.T) {
		h, resume := setup(t)
		occupy(h, func(s *Service, _ domain.ID) {
			s.integrationPreviews = map[domain.ID]*integrationCheck{}
			for range 8 {
				s.integrationPreviews[domain.NewID()] = &integrationCheck{done: make(chan struct{}), cancel: func() {}}
			}
		})
		if err := admit(h, resume); domain.SafeError(err).Code != domain.ResourceExhausted {
			t.Fatal("resumed attempt exceeded the global active bound", err)
		}
	})
	t.Run("queue wait shares the frozen deadline", func(t *testing.T) {
		h, resume := setup(t)
		resume.activeDeadline = time.Now().Add(200 * time.Millisecond)
		occupy(h, func(s *Service, profile domain.ID) {
			s.integrationChecks = map[domain.ID]*integrationCheck{profile: {done: make(chan struct{}), cancel: func() {}}}
		})
		started := time.Now()
		err := admit(h, resume)
		if domain.SafeError(err).Code != domain.Unavailable || time.Since(started) > 5*time.Second {
			t.Fatal("resumed queue wait did not end at the frozen deadline", err, time.Since(started))
		}
		resume.activeDeadline = time.Now().Add(-time.Second)
		if err := admit(h, resume); err == nil {
			t.Fatal("expired deadline admitted a read")
		}
	})
}
