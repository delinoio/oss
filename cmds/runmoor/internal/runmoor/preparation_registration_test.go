package runmoor

import (
	"context"
	"testing"
)

type registrationRaceRemote struct {
	*fakeRemote
	registered func()
	removedID  int
}

func (r *registrationRaceRemote) JIT(context.Context, PoolState, Runner) (int, string, error) {
	r.registered()
	return 42, "fixture-jit", nil
}
func (r *registrationRaceRemote) Remove(ctx context.Context, p PoolState, v Runner) error {
	r.removedID = v.GitHubID
	return r.fakeRemote.Remove(ctx, p, v)
}

func TestPreparationRechecksStateAfterJITRegistration(t *testing.T) {
	for _, boundary := range []string{"JIT registration", "backend selection"} {
		for _, phase := range []RunnerPhase{Preparing, Cleaning, Busy, Quarantined, Completed} {
			t.Run(boundary+"/"+string(phase), func(t *testing.T) {
				m, _, baseRemote, driver, pool := testManager(t)
				id := seedRunner(t, m, pool, Preparing)
				forced := phase == Preparing
				if err := m.Store.Update(func(s *Snapshot) error {
					s.Runners[id].GitHubID = 0
					s.Pools[pool].PreparationFailures = 2
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				transition := func() {
					// Model the durable stop commit before cancelForcedWorkers can
					// reach this worker, or a concurrent lifecycle message.
					if err := m.Store.Update(func(s *Snapshot) error {
						r := s.Runners[id]
						r.Forced, r.Phase = forced, phase
						if forced {
							r.Phase = Cleaning
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				remote := &registrationRaceRemote{fakeRemote: baseRemote, registered: func() {}}
				m.RemoteFactory = func(Connection) (Remote, error) { return remote, nil }
				if boundary == "JIT registration" {
					remote.registered = transition
				} else {
					m.Drivers = func(Backend) (Driver, error) { transition(); return driver, nil }
				}
				// There is deliberately no cancellation signal to mask a stale
				// state read. Backend work must be rejected from the journal alone.
				m.prepare(context.Background(), id)
				s := m.Store.View()
				r := s.Runners[id]
				want := phase
				if forced {
					want = Cleaning
				}
				if r.GitHubID != 42 || r.Phase != want || r.Forced != forced || len(driver.live) != 0 {
					t.Fatal("registration race provisioned resources or lost lifecycle state", r)
				}
				if s.Pools[pool].PreparationFailures != 2 || s.Pools[pool].Phase != Ready || r.Problem != nil {
					t.Fatal("skipped preparation changed the failure circuit")
				}
				if forced {
					m.Drivers = func(Backend) (Driver, error) { return driver, nil }
					m.cleanup(context.Background(), id)
					if remote.removedID != 42 || m.Store.View().Runners[id].Phase != Completed {
						t.Fatal("returned registration was not retained for cleanup")
					}
				}
			})
		}
	}
}
