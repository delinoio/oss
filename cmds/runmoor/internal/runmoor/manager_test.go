package runmoor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/actions/scaleset"
)

type fakeRemote struct {
	mu      sync.Mutex
	busy    bool
	failure error
	session *fakeSession
	created int
	removed int
}

func (f *fakeRemote) Check(context.Context, Pool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failure
}
func (f *fakeRemote) Ensure(_ context.Context, p PoolState, mark func() error) (int, error) {
	if p.ScaleSetID == 0 {
		if e := mark(); e != nil {
			return 0, e
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	return 1, f.failure
}
func (f *fakeRemote) Session(context.Context, PoolState, string) (RemoteSession, error) {
	return f.session, nil
}
func (f *fakeRemote) JIT(context.Context, PoolState, Runner) (int, string, error) {
	return 1, "fixture-jit", nil
}
func (f *fakeRemote) Remove(context.Context, PoolState, Runner) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy {
		return problem(ErrBusy, "Busy.", "Wait.")
	}
	f.removed++
	return f.failure
}
func (f *fakeRemote) Find(context.Context, PoolState, Runner) (bool, error) { return true, nil }
func (f *fakeRemote) DeletePool(context.Context, PoolState) error           { return nil }

type fakeSession struct {
	messages chan *scaleset.RunnerScaleSetMessage
	ack      func(int) error
	initial  int
}

func (f *fakeSession) ID() string         { return "fixture-session" }
func (f *fakeSession) InitialDemand() int { return f.initial }
func (f *fakeSession) Poll(ctx context.Context, _, _ int) (*scaleset.RunnerScaleSetMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case m := <-f.messages:
		return m, nil
	}
}
func (f *fakeSession) Ack(_ context.Context, id int) error {
	if f.ack != nil {
		return f.ack(id)
	}
	return nil
}
func (f *fakeSession) Acquire(context.Context, []int64) error { return nil }
func (f *fakeSession) Close(context.Context) error            { return nil }

type fakeDriver struct {
	mu         sync.Mutex
	live       map[string]bool
	stops      int
	cleanupErr error
}

func (f *fakeDriver) Validate(context.Context, Config, Pool, Snapshot) error { return nil }
func (f *fakeDriver) Prepare(_ context.Context, _ Config, _ Pool, r Runner, _ Snapshot, _ string, publish func(Handle) error) error {
	if e := publish(Handle{Container: r.ID}); e != nil {
		return e
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live[r.ID] = true
	return nil
}
func (f *fakeDriver) Inspect(_ context.Context, _ Config, r Runner, _ Snapshot) (Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	running, exists := f.live[r.ID]
	return Observation{Exists: exists, Running: running, Handle: r.Handle}, nil
}
func (f *fakeDriver) Stop(_ context.Context, _ Config, r Runner, _ Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	f.live[r.ID] = false
	return nil
}
func (f *fakeDriver) Cleanup(_ context.Context, _ Config, r Runner, _ Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cleanupErr != nil {
		return f.cleanupErr
	}
	delete(f.live, r.ID)
	return nil
}

type fakePower struct{}

func (fakePower) Set(bool) *Problem { return nil }
func (fakePower) Release()          {}
func testManager(t *testing.T) (*Manager, Config, *fakeRemote, *fakeDriver, string) {
	t.Helper()
	c, s := fixtureStore(t)
	if e := privateDir(diagnosticDir(c)); e != nil {
		t.Fatal(e)
	}
	m := NewManager(s, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := &fakeRemote{session: &fakeSession{messages: make(chan *scaleset.RunnerScaleSetMessage, 10)}}
	d := &fakeDriver{live: map[string]bool{}}
	m.RemoteFactory = func(Connection) (Remote, error) { return r, nil }
	m.Drivers = func(Backend) (Driver, error) { return d, nil }
	m.Power = fakePower{}
	if e := m.activate(c); e != nil {
		t.Fatal(e)
	}
	var id string
	for key := range s.View().Pools {
		id = key
	}
	if e := s.Update(func(s *Snapshot) error { s.Pools[id].ScaleSetID = 1; s.Pools[id].Session = r.session.ID(); return nil }); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.cancel(); m.wg.Wait() })
	return m, c, r, d, id
}
func seedRunner(t *testing.T, m *Manager, pool string, phase RunnerPhase) string {
	t.Helper()
	id := newID()
	e := m.Store.Update(func(s *Snapshot) error {
		p := s.Pools[pool]
		s.Runners[id] = &Runner{ID: id, Name: "runmoor-" + id, PoolID: pool, Generation: p.Generation, Phase: phase, Backend: Docker, Resources: Resources{1, 128}, GitHubID: 1, CreatedAt: nowUTC(), Deadline: time.Now().Add(time.Hour)}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func TestMessageDurabilityBeforeAcknowledgementAndRedelivery(t *testing.T) {
	m, _, remote, _, pool := testManager(t)
	id := seedRunner(t, m, pool, Idle)
	r := m.Store.View().Runners[id]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	remote.session.ack = func(n int) error {
		v := m.Store.View()
		if v.Pools[pool].LastMessage != n || v.Runners[id].Phase != Busy {
			t.Error("ack preceded durable state")
		}
		count++
		if count == 2 {
			cancel()
		}
		return nil
	}
	msg := &scaleset.RunnerScaleSetMessage{MessageID: 7, Statistics: &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 4}, JobStartedMessages: []*scaleset.JobStarted{{RunnerID: 1, RunnerName: r.Name}}}
	remote.session.messages <- msg
	remote.session.messages <- msg
	_ = m.listen(ctx, pool, remote.session)
	v := m.Store.View()
	if count != 2 || v.Pools[pool].Demand != 4 {
		t.Fatal("redelivery changed statistics demand")
	}
	started := v.Runners[id].StartedAt
	deadline := v.Runners[id].Deadline
	if started.IsZero() || deadline.Sub(started) != 6*time.Hour {
		t.Fatal("job timeout not persisted")
	}
	stale := applyMessage(&v, pool, "old-session", msg)
	requireCode(t, stale, ErrRetry)
}
func TestIdleRetirementAssignmentRace(t *testing.T) {
	m, _, remote, d, pool := testManager(t)
	id := seedRunner(t, m, pool, Idle)
	remote.busy = true
	m.retireRunner(context.Background(), id)
	if m.Store.View().Runners[id].Phase != Busy || d.stops != 0 {
		t.Fatal("newly busy runner was terminated")
	}
}
func TestPreparationFailureDoesNotKillAssignmentRace(t *testing.T) {
	m, _, remote, d, pool := testManager(t)
	id := seedRunner(t, m, pool, Preparing)
	remote.busy = true
	m.failPreparation(id, errors.New("ambiguous provisioning response"))
	m.cleanup(context.Background(), id)
	if m.Store.View().Runners[id].Phase != Busy || d.stops != 0 {
		t.Fatal("ambiguous preparation killed assigned job")
	}
}
func TestCleanupCrashProgressAndBusyPreservation(t *testing.T) {
	m, _, _, d, pool := testManager(t)
	id := seedRunner(t, m, pool, Cleaning)
	d.live[id] = true
	d.cleanupErr = errors.New("daemon down")
	m.cleanup(context.Background(), id)
	r := m.Store.View().Runners[id]
	if !r.Terminated || r.LocalCleaned || r.Phase != Cleaning {
		t.Fatal("cleanup progress not retained")
	}
	d.cleanupErr = nil
	m.cleanup(context.Background(), id)
	r = m.Store.View().Runners[id]
	if r.Phase != Completed || !r.LocalCleaned || d.stops != 1 {
		t.Fatal("cleanup did not resume idempotently")
	}
}
func TestReloadGenerationsPreserveExistingJob(t *testing.T) {
	m, c, _, _, pool := testManager(t)
	id := seedRunner(t, m, pool, Busy)
	oldGen := m.Store.View().Runners[id].Generation
	c.Pools[0].Image = "example/runner@sha256:" + strings.Repeat("b", 64)
	if e := m.activate(c); e != nil {
		t.Fatal(e)
	}
	s := m.Store.View()
	if s.Pools[pool].Phase != Draining || s.Runners[id].Generation != oldGen || s.Generations[oldGen].Pools[0].Image == c.Pools[0].Image {
		t.Fatal("reload mutated active job configuration")
	}
	for key, p := range s.Pools {
		if key != pool {
			p.ScaleSetID = 2
			p.Session = "session"
			p.Demand = 1
			if eligible(s, p) {
				t.Fatal("same remote identity started before old generation drained")
			}
		}
	}
}
func TestRepeatedPreparationFailureSuspendsOnlyPool(t *testing.T) {
	m, _, _, _, pool := testManager(t)
	for i := 0; i < 3; i++ {
		id := seedRunner(t, m, pool, Preparing)
		m.failPreparation(id, errors.New("failure"))
	}
	p := m.Store.View().Pools[pool]
	if p.Phase != Suspended || p.PreparationFailures != 3 {
		t.Fatal("preparation circuit breaker not applied")
	}
}
func TestManagerRunsAndDrainsWithoutGitHub(t *testing.T) {
	m, c, remote, d, _ := testManager(t)
	remote.session.initial = 1
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- m.Run(ctx, c) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		n := len(d.live)
		d.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	d.mu.Lock()
	n := len(d.live)
	d.mu.Unlock()
	if n != 1 {
		t.Fatal("manager did not provision demand")
	}
	if e := m.Stop(true); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("manager did not drain")
	}
	if !allTerminated(m.Store.View()) {
		t.Fatal("manager exited with live resources")
	}
}
