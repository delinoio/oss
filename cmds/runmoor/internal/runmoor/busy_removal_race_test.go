package runmoor

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/actions/scaleset"
)

type busyResponseRemote struct {
	*fakeRemote
	onBusy func()
	busy   *bool
}

func (r *busyResponseRemote) Remove(ctx context.Context, p PoolState, runner Runner) error {
	if r.busy != nil && *r.busy {
		*r.busy = false
		if r.onBusy != nil {
			r.onBusy()
		}
		return problem(ErrBusy, "Busy.", "Wait.")
	}
	return r.fakeRemote.Remove(ctx, p, runner)
}

func commitBusyRaceOutcome(m *Manager, id, outcome string) error {
	return m.Store.Update(func(s *Snapshot) error {
		r := s.Runners[id]
		switch outcome {
		case "completion":
			p := s.Pools[r.PoolID]
			return applyMessage(s, p.ID, p.Session, &scaleset.RunnerScaleSetMessage{
				MessageID:            p.LastMessage + 1,
				Statistics:           &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 0},
				JobCompletedMessages: []*scaleset.JobCompleted{{RunnerID: r.GitHubID, RunnerName: r.Name}},
			})
		case "forced cleanup":
			r.Phase, r.Forced = Cleaning, true
		case "remote removed":
			r.Phase, r.RemoteRemoved = Cleaning, true
		case "terminated":
			r.Phase, r.Terminated = Cleaning, true
		case "quarantined":
			r.Phase = Quarantined
			r.Problem = problem(ErrOwnership, "Retained quarantine.", "Verify ownership.")
		case "deleted":
			delete(s.Runners, id)
		case "later phase":
			r.Phase = Busy
			r.StartedAt = nowUTC()
			r.Deadline = r.StartedAt.Add(s.Generations[r.Generation].JobTimeout())
		default:
			return errors.New("unknown busy race outcome")
		}
		return nil
	})
}

func runBusyRemoval(m *Manager, id, path string) {
	switch path {
	case "inspection recovery":
		m.inspect(context.Background(), id)
	case "cleanup":
		m.cleanup(context.Background(), id)
	case "idle retirement":
		m.retireRunner(context.Background(), id)
	default:
		panic("unknown busy removal path")
	}
}

func TestLateBusyRemovalPreservesConcurrentLifecycle(t *testing.T) {
	paths := []struct {
		name  string
		phase RunnerPhase
	}{
		{"inspection recovery", Preparing},
		{"cleanup", Cleaning},
		{"idle retirement", Idle},
	}
	outcomes := []string{"completion", "forced cleanup", "remote removed", "terminated", "quarantined", "deleted", "later phase"}
	for _, path := range paths {
		for _, outcome := range outcomes {
			t.Run(path.name+"/"+outcome, func(t *testing.T) {
				m, _, remote, driver, pool := testManager(t)
				id := seedRunner(t, m, pool, path.phase)
				driver.live[id] = true
				var committed Snapshot
				busy := true
				m.RemoteFactory = func(Connection) (Remote, error) {
					return &busyResponseRemote{fakeRemote: remote, busy: &busy, onBusy: func() {
						if err := commitBusyRaceOutcome(m, id, outcome); err != nil {
							t.Fatal(err)
						}
						committed = m.Store.View()
					}}, nil
				}

				runBusyRemoval(m, id, path.name)
				if !reflect.DeepEqual(committed, m.Store.View()) {
					t.Fatal("late busy response changed the newer durable lifecycle")
				}
				if driver.stops != 0 {
					t.Fatal("busy response terminated a live execution")
				}

				if outcome == "completion" {
					r := m.Store.View().Runners[id]
					if r.Phase != Cleaning || !r.CompletedJob || r.Terminated {
						t.Fatal("committed completion was not retained", r)
					}
					if _, reservations, _ := usage(m.Store.View()); reservations != 1 {
						t.Fatal("completion released capacity before confirmed termination")
					}
					pauseInspectionFixture(t, m)
					stepInspectionFixture(t, m)
					r = m.Store.View().Runners[id]
					if r.Phase != Completed || !r.Terminated || !r.LocalCleaned || !r.RemoteRemoved || driver.stops != 1 {
						t.Fatal("completed job did not finish ordinary cleanup exactly once", r)
					}
					if _, reservations, _ := usage(m.Store.View()); reservations != 0 {
						t.Fatal("confirmed cleanup retained capacity")
					}
				}
			})
		}
	}
}

func TestBusyRemovalUsesOriginalDeadlineAccounting(t *testing.T) {
	paths := []struct {
		name  string
		phase RunnerPhase
	}{
		{"inspection recovery", Preparing},
		{"cleanup", Cleaning},
		{"idle retirement", Idle},
	}
	for _, path := range paths {
		for _, observedStart := range []bool{false, true} {
			label := "creation time fallback"
			if observedStart {
				label = "observed start retained"
			}
			t.Run(path.name+"/"+label, func(t *testing.T) {
				m, c, remote, driver, pool := testManager(t)
				id := seedRunner(t, m, pool, path.phase)
				created := nowUTC().Add(-2 * time.Hour)
				started := time.Time{}
				deadline := created.Add(c.JobTimeout())
				if observedStart {
					started = created.Add(20 * time.Minute)
					deadline = started.Add(c.JobTimeout())
				}
				if err := m.Store.Update(func(s *Snapshot) error {
					r := s.Runners[id]
					r.CreatedAt, r.StartedAt, r.Deadline = created, started, deadline
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				driver.live[id] = true
				busy := true
				m.RemoteFactory = func(Connection) (Remote, error) {
					return &busyResponseRemote{fakeRemote: remote, busy: &busy}, nil
				}

				runBusyRemoval(m, id, path.name)
				r := m.Store.View().Runners[id]
				if r.Phase != Busy || r.Forced || r.RemoteRemoved || r.Terminated || r.CompletedJob || driver.stops != 0 {
					t.Fatal("applicable busy result did not preserve the live runner", r)
				}
				if !r.StartedAt.Equal(func() time.Time {
					if observedStart {
						return started
					}
					return created
				}()) || !r.Deadline.Equal(deadline) {
					t.Fatal("busy result changed the original deadline accounting", r)
				}
				if _, reservations, _ := usage(m.Store.View()); reservations != 1 {
					t.Fatal("busy live runner lost its reservation")
				}
			})
		}
	}
}

func TestLateBusyCompletionCleanupRemainsRetryable(t *testing.T) {
	m, _, remote, driver, pool := testManager(t)
	id := seedRunner(t, m, pool, Cleaning)
	if err := m.Store.Update(func(s *Snapshot) error {
		s.Runners[id].DiagnosticsSaved = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	driver.live[id] = true
	var cleanupDriver inspectionDriver
	cleanupDriver.fakeDriver = driver
	m.Drivers = func(Backend) (Driver, error) { return &cleanupDriver, nil }
	busy := true
	m.RemoteFactory = func(Connection) (Remote, error) {
		return &busyResponseRemote{fakeRemote: remote, busy: &busy, onBusy: func() {
			completeInspectedRunner(t, m, id)
		}}, nil
	}
	cleanupDriver.stopErr = errors.New("temporary termination failure")
	m.cleanup(context.Background(), id)
	r := m.Store.View().Runners[id]
	if r.Phase != Cleaning || !r.CompletedJob || r.Terminated || driver.stops != 0 {
		t.Fatal("late busy response erased completion or claimed termination", r)
	}
	if _, reservations, _ := usage(m.Store.View()); reservations != 1 {
		t.Fatal("failed termination released capacity")
	}

	cleanupDriver.stopErr = nil
	driver.cleanupErr = errors.New("temporary local cleanup failure")
	m.cleanup(context.Background(), id)
	r = m.Store.View().Runners[id]
	if r.Phase != Cleaning || !r.Terminated || r.LocalCleaned || driver.stops != 1 {
		t.Fatal("local cleanup failure did not retain retry state", r)
	}
	if _, reservations, _ := usage(m.Store.View()); reservations != 0 || pendingCleanup(m.Store.View()) != 1 {
		t.Fatal("confirmed termination did not release capacity or local cleanup disappeared from pending work")
	}

	remote.failure = errors.New("temporary remote cleanup failure")
	driver.cleanupErr = nil
	m.cleanup(context.Background(), id)
	r = m.Store.View().Runners[id]
	if r.Phase != Cleaning || !r.Terminated || !r.LocalCleaned || r.RemoteRemoved || driver.stops != 1 || remote.removed != 1 {
		t.Fatalf("remote cleanup failure did not retain retry state (phase=%s terminated=%v local_cleaned=%v remote_removed=%v stops=%d removes=%d problem=%v)", r.Phase, r.Terminated, r.LocalCleaned, r.RemoteRemoved, driver.stops, remote.removed, r.Problem)
	}
	if pendingCleanup(m.Store.View()) != 1 {
		t.Fatal("failed remote cleanup disappeared from pending work")
	}

	remote.failure = nil
	m.cleanup(context.Background(), id)
	r = m.Store.View().Runners[id]
	if r.Phase != Completed || !r.Terminated || !r.LocalCleaned || !r.RemoteRemoved || driver.stops != 1 || remote.removed != 2 {
		t.Fatal("cleanup retry did not finish the committed job", r)
	}
	if _, reservations, _ := usage(m.Store.View()); reservations != 0 || pendingCleanup(m.Store.View()) != 0 {
		t.Fatal("finished cleanup retained a reservation or pending marker")
	}
}
