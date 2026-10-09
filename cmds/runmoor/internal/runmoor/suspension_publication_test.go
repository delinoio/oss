package runmoor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/actions/scaleset"
)

// Gates hold real worker calls until another worker commits its durable outcome.
type suspensionGate struct{ entered, release chan struct{} }

func newSuspensionGate(t *testing.T) *suspensionGate {
	g := &suspensionGate{make(chan struct{}), make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-g.release:
		default:
			close(g.release)
		}
	})
	return g
}
func (g *suspensionGate) block(ctx context.Context) error {
	close(g.entered)
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func waitSuspensionWorker(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach the controlled boundary")
	}
}

type suspensionPrepareDriver struct {
	Driver
	gate *suspensionGate
}

func (d *suspensionPrepareDriver) Prepare(ctx context.Context, c Config, p Pool, r Runner, s Snapshot, jit string, publish func(Handle) error) error {
	if err := d.gate.block(ctx); err != nil {
		return err
	}
	return d.Driver.Prepare(ctx, c, p, r, s, jit, publish)
}

type suspensionEnsureRemote struct {
	Remote
	gate *suspensionGate
}

func (r *suspensionEnsureRemote) Ensure(ctx context.Context, p PoolState, mark func() error) (int, error) {
	if err := r.gate.block(ctx); err != nil {
		return 0, err
	}
	return 42, nil
}

type suspensionPollSession struct {
	RemoteSession
	gate *suspensionGate
	err  error
}

func (s *suspensionPollSession) Poll(ctx context.Context, _, _ int) (*scaleset.RunnerScaleSetMessage, error) {
	if err := s.gate.block(ctx); err != nil {
		return nil, err
	}
	return nil, s.err
}
func assertSuspensionPreserved(t *testing.T, before, after *PoolState) {
	t.Helper()
	if after.Phase != Suspended || after.Problem == nil || before.Problem == nil || *after.Problem != *before.Problem || after.SuspensionSource != before.SuspensionSource || after.PreparationFailures != before.PreparationFailures {
		t.Fatalf("durable suspension changed: before=%+v after=%+v", before, after)
	}
}
func commitPreparationSuspension(t *testing.T, m *Manager, pool string, code ErrorCode) *PoolState {
	t.Helper()
	if err := m.Store.Update(func(s *Snapshot) error { s.Pools[pool].PreparationFailures = 2; return nil }); err != nil {
		t.Fatal(err)
	}
	m.failPreparation(seedRunner(t, m, pool, Preparing), problem(code, "Fixture committed failure.", "Correct the original dependency."))
	return m.Store.View().Pools[pool]
}

// Exercise the production validated-reload decision with the retained diagnosis.
func assertSuspensionRecovery(t *testing.T, m *Manager, c Config, p *PoolState) {
	t.Helper()
	next := c
	next.Pools = append([]Pool(nil), c.Pools...)
	next.Connections = append([]Connection(nil), c.Connections...)
	switch p.Problem.Code {
	case ErrAuth:
		next.Connections[0].Credential.Env = "RUNMOOR_REPLACEMENT_CREDENTIAL"
	case ErrOwnership:
		next.Pools[0].ScaleSet += "-corrected"
	default:
		next.Pools[0].Image = "example/runner@sha256:" + strings.Repeat("b", 64)
	}
	writeSuspensionReload(t, m, next)
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := false
	for _, q := range m.Store.View().Pools {
		if q.Phase == Ready && q.Problem == nil {
			recovered = true
		}
	}
	want := p.Problem.Code != ErrOwnership || p.SuspensionSource == SuspensionScaleSet
	if recovered != want {
		t.Fatalf("failure-specific recovery=%t want=%t", recovered, want)
	}
}

func TestLatePreparationSuccessPreservesSuspension(t *testing.T) {
	m, c, _, base, pool := testManager(t)
	gate := newSuspensionGate(t)
	m.Drivers = func(Backend) (Driver, error) { return &suspensionPrepareDriver{base, gate}, nil }
	a := seedRunner(t, m, pool, Preparing)
	done := make(chan struct{})
	go func() { defer close(done); m.prepare(context.Background(), a) }()
	waitSuspensionWorker(t, gate.entered)
	before := commitPreparationSuspension(t, m, pool, ErrPreparation)
	close(gate.release)
	waitSuspensionWorker(t, done)
	after := m.Store.View().Pools[pool]
	assertSuspensionPreserved(t, before, after)
	if m.Store.View().Runners[a].Phase != Idle {
		t.Fatal("successful original runner was not published")
	}
	if !managedImageFailure(after) {
		t.Fatal("late success disabled verified managed-image recovery")
	}
	assertSuspensionRecovery(t, m, c, after)
}
func TestLatePollFailurePreservesSuspension(t *testing.T) {
	for _, code := range []ErrorCode{ErrAuth, ErrOwnership, ErrPreparation} {
		for _, late := range []ErrorCode{ErrRetry, ErrAuth} {
			t.Run(fmt.Sprintf("%s/%s", code, late), func(t *testing.T) {
				m, c, remote, _, pool := testManager(t)
				gate := newSuspensionGate(t)
				session := &suspensionPollSession{remote.session, gate, problem(late, "Fixture late poll failure.", "Retry.")}
				done := make(chan struct{})
				go func() {
					defer close(done)
					err := m.listen(context.Background(), pool, session)
					m.poolProblem(pool, err, late == ErrAuth)
				}()
				waitSuspensionWorker(t, gate.entered)
				before := commitPreparationSuspension(t, m, pool, code)
				close(gate.release)
				waitSuspensionWorker(t, done)
				assertSuspensionPreserved(t, before, m.Store.View().Pools[pool])
				assertSuspensionRecovery(t, m, c, before)
			})
		}
	}
}
func TestLateEnsureSuccessPreservesSuspension(t *testing.T) {
	for _, code := range []ErrorCode{ErrAuth, ErrPreparation, ErrRunnerVersion} {
		t.Run(string(code), func(t *testing.T) {
			m, c, remote, _, pool := testManager(t)
			gate := newSuspensionGate(t)
			if err := m.Store.Update(func(s *Snapshot) error { s.Pools[pool].CreatePending = true; return nil }); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			errs := make(chan error, 1)
			go func() {
				defer close(done)
				_, err := m.ensureScaleSet(context.Background(), pool, &suspensionEnsureRemote{remote, gate})
				errs <- err
			}()
			waitSuspensionWorker(t, gate.entered)
			before := commitPreparationSuspension(t, m, pool, code)
			close(gate.release)
			waitSuspensionWorker(t, done)
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
			after := m.Store.View().Pools[pool]
			assertSuspensionPreserved(t, before, after)
			if after.ScaleSetID != 42 || after.CreatePending {
				t.Fatal("owned reconnect result was not durably settled")
			}
			assertSuspensionRecovery(t, m, c, after)
		})
	}
}
func TestActivePoolPublicationsRemainAvailable(t *testing.T) {
	m, _, remote, _, pool := testManager(t)
	prior := problem(ErrRetry, "Fixture prior initialization failure.", "Retry.")
	if err := m.Store.Update(func(s *Snapshot) error {
		p := s.Pools[pool]
		p.Problem = prior
		p.CreatePending = true
		p.PreparationFailures = 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ensureScaleSet(context.Background(), pool, remote); err != nil {
		t.Fatal(err)
	}
	p := m.Store.View().Pools[pool]
	if p.Problem != nil || p.CreatePending || p.ScaleSetID != 1 {
		t.Fatal("ordinary reconnect did not settle initialization")
	}
	m.prepare(context.Background(), seedRunner(t, m, pool, Preparing))
	if m.Store.View().Pools[pool].PreparationFailures != 0 {
		t.Fatal("ordinary success did not reset consecutive failures")
	}
	m.poolProblem(pool, prior, false)
	if p := m.Store.View().Pools[pool]; p.Problem == nil || p.Problem.Code != ErrRetry || p.Phase != Ready {
		t.Fatal("ordinary session diagnostic disappeared")
	}
}

func TestManagedRepairAfterLatePreparationSuccess(t *testing.T) {
	m, _, builder, _ := managedFixture(t)
	m.updateManaged(context.Background(), "linux")
	pool := managedCurrentPool(t, m.Store.View())
	base := &fakeDriver{live: map[string]bool{}}
	gate := newSuspensionGate(t)
	m.Drivers = func(Backend) (Driver, error) { return &suspensionPrepareDriver{base, gate}, nil }
	a := seedRunner(t, m, pool, Preparing)
	done := make(chan struct{})
	go func() { defer close(done); m.prepare(context.Background(), a) }()
	waitSuspensionWorker(t, gate.entered)
	before := commitPreparationSuspension(t, m, pool, ErrPreparation)
	close(gate.release)
	waitSuspensionWorker(t, done)
	assertSuspensionPreserved(t, before, m.Store.View().Pools[pool])
	m.Drivers = func(Backend) (Driver, error) {
		return validatingDriver{validate: func(context.Context, Config, Pool, Snapshot) error {
			if builder.calls == 1 {
				return problem(ErrImage, "Fixture current image missing.", "Repair.")
			}
			return nil
		}}, nil
	}
	m.updateManaged(context.Background(), "linux")
	if builder.calls != 2 {
		t.Fatal("verified managed replacement was not built")
	}
	if p := m.Store.View().Pools[managedCurrentPool(t, m.Store.View())]; p.Phase != Ready || p.Problem != nil || p.PreparationFailures != 0 {
		t.Fatalf("matching verified replacement did not recover circuit breaker: %+v", p)
	}
}

func TestLateSessionFailurePreservesScaleSetOwnershipSource(t *testing.T) {
	m, c, remote, _, pool := testManager(t)
	gate := newSuspensionGate(t)
	session := &suspensionPollSession{remote.session, gate, problem(ErrAuth, "Fixture late failure.", "Retry.")}
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.poolProblemWithSource(pool, m.listen(context.Background(), pool, session), true, SuspensionUnknown)
	}()
	waitSuspensionWorker(t, gate.entered)
	m.poolProblemWithSource(pool, problem(ErrOwnership, "Fixture remote ownership conflict.", "Correct identity."), true, SuspensionScaleSet)
	before := m.Store.View().Pools[pool]
	close(gate.release)
	waitSuspensionWorker(t, done)
	assertSuspensionPreserved(t, before, m.Store.View().Pools[pool])
	assertSuspensionRecovery(t, m, c, before)
}
