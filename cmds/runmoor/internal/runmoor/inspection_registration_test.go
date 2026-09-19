package runmoor

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/actions/scaleset"
)

type inspectionRemote struct {
	*fakeRemote
	find func(context.Context) (bool, error)
}

func (r *inspectionRemote) Find(ctx context.Context, _ PoolState, _ Runner) (bool, error) {
	return r.find(ctx)
}

type inspectionDriver struct {
	*fakeDriver
	inspect func(context.Context) (Observation, error)
	stopErr error
}

func (d *inspectionDriver) Inspect(ctx context.Context, c Config, r Runner, s Snapshot) (Observation, error) {
	if d.inspect != nil {
		return d.inspect(ctx)
	}
	return d.fakeDriver.Inspect(ctx, c, r, s)
}

func (d *inspectionDriver) Stop(ctx context.Context, c Config, r Runner, s Snapshot) error {
	if d.stopErr != nil {
		return d.stopErr
	}
	return d.fakeDriver.Stop(ctx, c, r, s)
}

func completeInspectedRunner(t *testing.T, m *Manager, id string) {
	t.Helper()
	if err := m.Store.Update(func(s *Snapshot) error {
		r := s.Runners[id]
		p := s.Pools[r.PoolID]
		return applyMessage(s, p.ID, p.Session, &scaleset.RunnerScaleSetMessage{
			MessageID:            p.LastMessage + 1,
			Statistics:           &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 0},
			JobCompletedMessages: []*scaleset.JobCompleted{{RunnerID: r.GitHubID, RunnerName: r.Name}},
		})
	}); err != nil {
		t.Fatal(err)
	}
}

// Block only at the lookup boundary; channels establish the ordering without
// sleeps. Cleanup releases the worker even if an assertion fails while blocked.
func blockInspectionLookup(t *testing.T, m *Manager, base *fakeRemote, id string) func() {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }); m.wg.Wait() }
	t.Cleanup(unblock)
	m.RemoteFactory = func(Connection) (Remote, error) {
		return &inspectionRemote{fakeRemote: base, find: func(ctx context.Context) (bool, error) {
			close(entered)
			select {
			case <-release:
				return false, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}}, nil
	}
	if !m.startWork(id, func(ctx context.Context) { m.inspect(ctx, id) }) {
		t.Fatal("inspection worker did not start")
	}
	<-entered
	return unblock
}

func pauseInspectionFixture(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.Store.Update(func(s *Snapshot) error {
		s.Paused = true
		for _, p := range s.Pools {
			p.Phase = Suspended
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func stepInspectionFixture(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.step(); err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
}

func TestInspectionCompletionOrdering(t *testing.T) {
	for _, order := range []string{"before worker", "during lookup", "after absence"} {
		t.Run(order, func(t *testing.T) {
			m, _, remote, driver, pool := testManager(t)
			id := seedRunner(t, m, pool, Busy)
			driver.live[id] = true
			var logs bytes.Buffer
			m.Log = slog.New(slog.NewJSONHandler(&logs, nil))
			finds := 0
			m.RemoteFactory = func(Connection) (Remote, error) {
				return &inspectionRemote{fakeRemote: remote, find: func(context.Context) (bool, error) {
					finds++
					if order == "during lookup" {
						completeInspectedRunner(t, m, id)
					}
					return false, nil
				}}, nil
			}
			if order == "before worker" {
				completeInspectedRunner(t, m, id)
			}
			m.inspect(context.Background(), id)
			if order == "after absence" {
				r := m.Store.View().Runners[id]
				if r.Phase != Quarantined || !strings.Contains(logs.String(), string(ErrOwnership)) {
					t.Fatal("absence without completion did not quarantine and warn", r, logs.String())
				}
				completeInspectedRunner(t, m, id)
			} else if logs.Len() != 0 {
				t.Fatal("discarded inspection warned", logs.String())
			}
			r := m.Store.View().Runners[id]
			if r.Phase != Cleaning || !r.CompletedJob || r.Forced || r.Terminated || r.RemoteRemoved || driver.stops != 0 {
				t.Fatal("inspection overwrote completion or claimed termination", r)
			}
			if order != "after absence" && r.Problem != nil {
				t.Fatal("stale absence recorded an ownership problem", r.Problem)
			}
			if order == "before worker" && finds != 0 {
				t.Fatal("obsolete worker performed a registration lookup")
			}
			if _, reserved, _ := usage(m.Store.View()); reserved != 1 {
				t.Fatal("completion released capacity before confirmed termination")
			}
			driver.live[id] = false
			pauseInspectionFixture(t, m)
			stepInspectionFixture(t, m)
			r = m.Store.View().Runners[id]
			if r.Phase != Completed || !r.Terminated || !r.LocalCleaned || !r.RemoteRemoved || r.Forced || remote.removed != 1 {
				t.Fatal("ordinary reconciliation did not finish cleanup", r)
			}
			if _, reserved, _ := usage(m.Store.View()); reserved != 0 || pendingCleanup(m.Store.View()) != 0 {
				t.Fatal("completed cleanup retained its reservation")
			}
		})
	}
}

func TestInspectionAbsentResultPreservesConcurrentLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Snapshot, string)
	}{
		{"completion", nil},
		{"cleaning", func(s *Snapshot, id string) { s.Runners[id].Phase = Cleaning }},
		{"completed", func(s *Snapshot, id string) {
			r := s.Runners[id]
			r.Phase, r.CompletedAt = Completed, nowUTC()
			r.CompletedJob, r.Terminated, r.LocalCleaned, r.RemoteRemoved, r.DiagnosticsSaved = true, true, true, true, true
		}},
		{"forced cleanup", func(s *Snapshot, id string) { s.Runners[id].Phase, s.Runners[id].Forced = Cleaning, true }},
		{"remote removed cleanup", func(s *Snapshot, id string) { s.Runners[id].Phase, s.Runners[id].RemoteRemoved = Cleaning, true }},
		{"completion flag", func(s *Snapshot, id string) { s.Runners[id].CompletedJob = true }},
		{"forced flag", func(s *Snapshot, id string) { s.Runners[id].Forced = true }},
		{"remote removed flag", func(s *Snapshot, id string) { s.Runners[id].RemoteRemoved = true }},
		{"termination flag", func(s *Snapshot, id string) { s.Runners[id].Terminated = true }},
		{"quarantined", func(s *Snapshot, id string) { s.Runners[id].Phase = Quarantined }},
		{"pruned", func(s *Snapshot, id string) { delete(s.Runners, id) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, remote, driver, pool := testManager(t)
			id := seedRunner(t, m, pool, Busy)
			driver.live[id] = true
			var logs bytes.Buffer
			m.Log = slog.New(slog.NewJSONHandler(&logs, nil))
			unblock := blockInspectionLookup(t, m, remote, id)
			if err := m.Store.Update(func(s *Snapshot) error {
				s.Runners[id].Problem = problem(ErrDependency, "Retained diagnostic.", "Retry cleanup.")
				if tc.change != nil {
					tc.change(s, id)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if tc.change == nil {
				completeInspectedRunner(t, m, id)
			}
			before := m.Store.View()
			unblock()
			if !reflect.DeepEqual(before, m.Store.View()) || logs.Len() != 0 {
				t.Fatal("obsolete lookup changed authoritative state or emitted a warning", logs.String())
			}
		})
	}
}

func TestInspectionSkipsAlreadyResolvedWork(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Runner)
	}{
		{"cleaning", func(r *Runner) { r.Phase = Cleaning }},
		{"completed", func(r *Runner) { r.Phase = Completed }},
		{"quarantined", func(r *Runner) { r.Phase = Quarantined }},
		{"completed job", func(r *Runner) { r.CompletedJob = true }},
		{"remote removed", func(r *Runner) { r.RemoteRemoved = true }},
		{"terminated", func(r *Runner) { r.Terminated = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _, _, pool := testManager(t)
			id := seedRunner(t, m, pool, Busy)
			if err := m.Store.Update(func(s *Snapshot) error { tc.change(s.Runners[id]); return nil }); err != nil {
				t.Fatal(err)
			}
			m.Drivers = func(Backend) (Driver, error) {
				t.Error("obsolete inspection selected a backend")
				return nil, errors.New("obsolete inspection")
			}
			before := m.Store.View()
			m.inspect(context.Background(), id)
			if !reflect.DeepEqual(before, m.Store.View()) {
				t.Fatal("obsolete inspection changed durable state")
			}
		})
	}
}

func TestInspectionOwnershipErrorsStillQuarantineCompletedJobs(t *testing.T) {
	for _, backend := range []Backend{Docker, Tart} {
		for _, boundary := range []string{"backend inspection", "registration lookup", "cleanup termination"} {
			t.Run(string(backend)+"/"+boundary, func(t *testing.T) {
				m, _, base, driver, pool := testManager(t)
				id := seedRunner(t, m, pool, Busy)
				driver.live[id] = true
				if err := m.Store.Update(func(s *Snapshot) error { s.Runners[id].Backend = backend; return nil }); err != nil {
					t.Fatal(err)
				}
				ownership := problem(ErrOwnership, "Foreign identity.", "Preserve resources.")
				d := &inspectionDriver{fakeDriver: driver}
				m.Drivers = func(Backend) (Driver, error) { return d, nil }
				m.RemoteFactory = func(Connection) (Remote, error) {
					return &inspectionRemote{fakeRemote: base, find: func(context.Context) (bool, error) {
						completeInspectedRunner(t, m, id)
						return false, ownership
					}}, nil
				}
				var logs bytes.Buffer
				m.Log = slog.New(slog.NewJSONHandler(&logs, nil))
				switch boundary {
				case "backend inspection":
					d.inspect = func(context.Context) (Observation, error) {
						completeInspectedRunner(t, m, id)
						return Observation{}, ownership
					}
					m.inspect(context.Background(), id)
				case "registration lookup":
					m.inspect(context.Background(), id)
				case "cleanup termination":
					completeInspectedRunner(t, m, id)
					d.stopErr = ownership
					m.cleanup(context.Background(), id)
				}
				r := m.Store.View().Runners[id]
				if r.Phase != Quarantined || !r.CompletedJob || r.Forced || r.Terminated || r.LocalCleaned || driver.stops != 0 || base.removed != 0 {
					t.Fatal("completion suppressed a real ownership failure", r)
				}
				requireCode(t, r.Problem, ErrOwnership)
				if _, reserved, _ := usage(m.Store.View()); reserved != 1 || !strings.Contains(logs.String(), string(ErrOwnership)) {
					t.Fatal("ownership failure lost capacity or its diagnostic", logs.String())
				}
			})
		}
	}
}

func TestInspectionUnregisteredLiveWorkRemainsQuarantined(t *testing.T) {
	for _, phase := range []RunnerPhase{Preparing, Idle, Busy} {
		t.Run(string(phase), func(t *testing.T) {
			m, _, remote, driver, pool := testManager(t)
			id := seedRunner(t, m, pool, phase)
			driver.live[id] = true
			m.RemoteFactory = func(Connection) (Remote, error) {
				return &inspectionRemote{fakeRemote: remote, find: func(context.Context) (bool, error) { return false, nil }}, nil
			}
			m.inspect(context.Background(), id)
			pauseInspectionFixture(t, m)
			power := &failingPower{}
			m.Power = power
			stepInspectionFixture(t, m)
			r := m.Store.View().Runners[id]
			if r.Phase != Quarantined || r.CompletedJob || r.Terminated || r.Forced || driver.stops != 0 || remote.removed != 0 || !power.active {
				t.Fatal("unregistered live execution lost conservative protection", r)
			}
			requireCode(t, r.Problem, ErrOwnership)
			if _, reserved, _ := usage(m.Store.View()); reserved != 1 {
				t.Fatal("registration absence released capacity")
			}
		})
	}
}

func TestInspectionTransientLookupFailurePreservesReservations(t *testing.T) {
	for _, completion := range []bool{false, true} {
		m, _, remote, driver, pool := testManager(t)
		id := seedRunner(t, m, pool, Busy)
		driver.live[id] = true
		var logs bytes.Buffer
		m.Log = slog.New(slog.NewJSONHandler(&logs, nil))
		m.RemoteFactory = func(Connection) (Remote, error) {
			return &inspectionRemote{fakeRemote: remote, find: func(context.Context) (bool, error) {
				if completion {
					completeInspectedRunner(t, m, id)
				}
				return false, errors.New("raw-upstream-secret")
			}}, nil
		}
		m.inspect(context.Background(), id)
		want := Busy
		if completion {
			want = Cleaning
		}
		r := m.Store.View().Runners[id]
		if r.Phase != want || r.Terminated || r.Forced || driver.stops != 0 || remote.removed != 0 {
			t.Fatal("transient lookup failure authorized deletion", r)
		}
		requireCode(t, r.Problem, ErrRetry)
		if _, reserved, _ := usage(m.Store.View()); reserved != 1 || strings.Contains(logs.String(), "raw-upstream-secret") {
			t.Fatal("transient failure released capacity or exposed upstream output")
		}
	}
}

func TestInspectionStoppedExecutionKeepsBusyAwareCleanup(t *testing.T) {
	for _, busy := range []bool{false, true} {
		m, _, remote, driver, pool := testManager(t)
		id := seedRunner(t, m, pool, Busy)
		driver.live[id], remote.busy = false, busy
		m.inspect(context.Background(), id)
		if r := m.Store.View().Runners[id]; r.Phase != Cleaning || r.Terminated {
			t.Fatal("stopped observation bypassed cleanup", r)
		}
		m.cleanup(context.Background(), id)
		r := m.Store.View().Runners[id]
		if busy {
			if r.Phase != Busy || r.Terminated || r.Forced || driver.stops != 0 {
				t.Fatal("cleanup ignored GitHub's busy rejection", r)
			}
		} else if r.Phase != Completed || !r.Terminated || !r.LocalCleaned || !r.RemoteRemoved {
			t.Fatal("stopped execution did not finish ordinary cleanup", r)
		}
	}
}

func TestInspectionQuarantineCommitFailureIsReportedSafely(t *testing.T) {
	m, _, remote, driver, pool := testManager(t)
	id := seedRunner(t, m, pool, Busy)
	driver.live[id] = true
	m.RemoteFactory = func(Connection) (Remote, error) {
		return &inspectionRemote{fakeRemote: remote, find: func(context.Context) (bool, error) { return false, nil }}, nil
	}
	// Reject writes in this fixture's database without replacing durable state.
	if _, err := m.Store.db.Exec("CREATE TRIGGER reject_inspection BEFORE UPDATE ON snapshot BEGIN SELECT RAISE(FAIL, 'private-storage-detail'); END"); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	m.Log = slog.New(slog.NewJSONHandler(&logs, nil))
	before := m.Store.View()
	m.inspect(context.Background(), id)
	if !reflect.DeepEqual(before, m.Store.View()) {
		t.Fatal("failed quarantine commit changed memory state")
	}
	if !strings.Contains(logs.String(), string(ErrState)) || !strings.Contains(logs.String(), id) ||
		strings.Contains(logs.String(), string(ErrOwnership)) || strings.Contains(logs.String(), "private-storage-detail") {
		t.Fatal("failed commit claimed quarantine or exposed storage details", logs.String())
	}
}

func TestInspectionCompletionHandsOffCapacityAfterConfirmedTermination(t *testing.T) {
	for _, failure := range []string{"local cleanup", "remote removal"} {
		t.Run(failure, func(t *testing.T) {
			m, _, remote, base, pool := testManager(t)
			id := seedRunner(t, m, pool, Busy)
			base.live[id] = true
			other := newID()
			if err := m.Store.Update(func(s *Snapshot) error {
				s.Config.Host.MaxRunners = 1
				p := *s.Pools[pool]
				p.ID, p.Spec.Name, p.Spec.ScaleSet, p.ScaleSetID = other, "queued", "queued", 2
				p.Demand = 1
				s.Pools[other] = &p
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			unblock := blockInspectionLookup(t, m, remote, id)
			completeInspectedRunner(t, m, id)
			unblock()
			d := &inspectionDriver{fakeDriver: base, stopErr: problem(ErrDependency, "Termination unconfirmed.", "Retry.")}
			m.Drivers = func(Backend) (Driver, error) { return d, nil }
			// Suppress session/provisioning workers while ordinary step cleanup
			// runs. Use the same snapshot with eligible pools to test allocation.
			pauseInspectionFixture(t, m)
			allocation := func() []string {
				s := m.Store.View()
				s.Paused = false
				for _, p := range s.Pools {
					p.Phase = Ready
				}
				return Schedule(s)
			}
			stepInspectionFixture(t, m)
			r := m.Store.View().Runners[id]
			if r.Phase != Cleaning || r.Terminated || r.Forced || len(allocation()) != 0 {
				t.Fatal("unconfirmed termination released the queued pool", r)
			}
			d.stopErr = nil
			base.live[id] = false
			if failure == "local cleanup" {
				base.cleanupErr = errors.New("cleanup unavailable")
			} else {
				remote.failure = errors.New("remote unavailable")
			}
			stepInspectionFixture(t, m)
			r = m.Store.View().Runners[id]
			if r.Phase != Cleaning || !r.Terminated || r.Forced || r.Problem == nil || pendingCleanup(m.Store.View()) != 1 || r.RemoteRemoved {
				t.Fatal("remaining cleanup was not retained durably", r)
			}
			if r.LocalCleaned != (failure == "remote removal") {
				t.Fatal("local cleanup progress was lost", r)
			}
			if got := allocation(); !reflect.DeepEqual(got, []string{other}) {
				t.Fatal("confirmed termination did not make capacity available to the queued pool", got)
			}
			// Allow real provisioning for the waiting pool without starting a
			// long-lived polling session; its demand is already committed above.
			m.RemoteFactory = func(Connection) (Remote, error) { return remote, nil }
			m.mu.Lock()
			m.poolLoops[other] = func() {}
			m.mu.Unlock()
			if err := m.Store.Update(func(s *Snapshot) error {
				s.Paused = false
				s.Pools[other].Phase = Ready
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			stepInspectionFixture(t, m)
			s := m.Store.View()
			if count, _ := liveCount(s, other); count != 1 || len(s.Runners) != 2 || s.Runners[id].Phase != Cleaning || pendingCleanup(s) != 1 {
				t.Fatal("queued pool did not receive capacity while cleanup remained pending", s.Runners)
			}
			for _, r := range s.Runners {
				if r.PoolID == other && (r.Phase != Idle || !base.live[r.ID]) {
					t.Fatal("waiting pool was not provisioned", r)
				}
			}
			base.cleanupErr, remote.failure = nil, nil
			stepInspectionFixture(t, m)
			r = m.Store.View().Runners[id]
			if r.Phase != Completed || !r.Terminated || !r.LocalCleaned || !r.RemoteRemoved || r.Forced || base.stops != 1 || pendingCleanup(m.Store.View()) != 0 {
				t.Fatal("cleanup did not resume idempotently", r)
			}
		})
	}
}
