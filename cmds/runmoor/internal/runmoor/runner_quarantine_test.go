package runmoor

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCompletionPreservesIdentityQuarantine(t *testing.T) {
	for _, backend := range []Backend{Docker, Tart} {
		for _, boundary := range []string{"backend", "registration"} {
			t.Run(string(backend)+"/"+boundary, func(t *testing.T) {
				m, _, remote, driver, pool := testManager(t)
				id := seedRunner(t, m, pool, Busy)
				driver.live[id] = true
				if err := m.Store.Update(func(s *Snapshot) error { s.Runners[id].Backend = backend; return nil }); err != nil {
					t.Fatal(err)
				}
				conflict := problem(ErrOwnership, "Foreign identity.", "Repair original ownership.")
				if boundary == "backend" {
					m.Drivers = func(Backend) (Driver, error) {
						return &inspectionDriver{fakeDriver: driver, inspect: func(context.Context) (Observation, error) { return Observation{}, conflict }}, nil
					}
				} else {
					m.RemoteFactory = func(Connection) (Remote, error) {
						return &inspectionRemote{fakeRemote: remote, find: func(context.Context) (bool, error) { return false, conflict }}, nil
					}
				}
				m.inspect(context.Background(), id)
				before := m.Store.View().Runners[id]
				completeInspectedRunner(t, m, id)
				pauseInspectionFixture(t, m)
				stepInspectionFixture(t, m)
				s := m.Store.View()
				r := s.Runners[id]
				if r.Phase != Quarantined || !r.CompletedJob || r.Forced || r.Terminated || r.LocalCleaned || r.RemoteRemoved || !reflect.DeepEqual(r.Problem, before.Problem) || runnerQuarantineCause(s, id) != QuarantineIdentityConflict {
					t.Fatal("completion changed ownership quarantine", r)
				}
				if r.ID != before.ID || r.Name != before.Name || r.GitHubID != before.GitHubID || r.Generation != before.Generation || driver.stops != 0 || remote.removed != 0 {
					t.Fatal("completion changed original authority or ran cleanup", r)
				}
				if _, reserved, _ := usage(s); reserved != 1 {
					t.Fatal("quarantine released reservation")
				}
			})
		}
	}
}

func TestLegacyQuarantineRestartAndVerifiedRecovery(t *testing.T) {
	for _, cause := range []RunnerQuarantineCause{"", "future_cause", QuarantineIdentityConflict, QuarantineRegistrationAbsent} {
		t.Run(string(cause), func(t *testing.T) {
			m, c, remote, driver, pool := testManager(t)
			id := seedRunner(t, m, pool, Quarantined)
			driver.live[id] = true
			conflict := problem(ErrOwnership, "Retained original diagnosis.", "Repair original ownership.")
			if err := m.Store.Update(func(s *Snapshot) error {
				s.Runners[id].Problem = conflict
				if cause != "" {
					s.RunnerQuarantines = map[string]RunnerQuarantineCause{id: cause}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := m.Store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenStore(c)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reopened.Close() })
			m.Store = reopened
			if err := m.accept(c, true); err != nil {
				t.Fatal(err)
			}
			want := cause
			if cause == "" || cause == "future_cause" {
				want = QuarantineUnknown
			}
			if got := m.Store.View().RunnerQuarantines[id]; got != want {
				t.Fatal("restart did not persist conservative cause", got)
			}
			completeInspectedRunner(t, m, id)
			r := m.Store.View().Runners[id]
			if !r.CompletedJob || !reflect.DeepEqual(r.Problem, conflict) || (want != QuarantineRegistrationAbsent && r.Phase != Quarantined) || (want == QuarantineRegistrationAbsent && r.Phase != Cleaning) {
				t.Fatal("restart completion changed quarantine authority", r)
			}
			if want != QuarantineRegistrationAbsent {
				pauseInspectionFixture(t, m)
				stepInspectionFixture(t, m)
				if _, reserved, _ := usage(m.Store.View()); reserved != 1 || driver.stops != 0 || remote.removed != 0 {
					t.Fatal("legacy quarantine allowed automatic cleanup")
				}
			}
			public, err := json.Marshal(statusOf(m.Store.View(), true))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(public), "runner_quarantines") || strings.Contains(string(public), "quarantine_cause") {
				t.Fatal("private cause leaked into status", string(public))
			}
			// Explicit force still checks the original backend identity and must fail
			// closed until the operator repairs that identity.
			checked := &inspectionDriver{fakeDriver: driver, stopErr: conflict}
			m.Drivers = func(Backend) (Driver, error) { return checked, nil }
			if err := m.Stop(true); err != nil {
				t.Fatal(err)
			}
			m.cleanup(context.Background(), id)
			s := m.Store.View()
			if s.Runners[id].Phase != Quarantined || s.Runners[id].Terminated || driver.stops != 0 || remote.removed != 0 || runnerQuarantineCause(s, id) != QuarantineIdentityConflict {
				t.Fatal("force bypassed original ownership", s.Runners[id])
			}
			if _, reserved, _ := usage(s); reserved != 1 {
				t.Fatal("failed force released reservation")
			}
			checked.stopErr = nil
			if err := m.Stop(true); err != nil {
				t.Fatal(err)
			}
			m.cleanup(context.Background(), id)
			s = m.Store.View()
			if s.Runners[id].Phase != Completed || !s.Runners[id].Terminated || !s.Runners[id].LocalCleaned || !s.Runners[id].RemoteRemoved || driver.stops != 1 || remote.removed != 1 {
				t.Fatal("verified repair did not finish cleanup", s.Runners[id])
			}
			if _, ok := s.RunnerQuarantines[id]; ok {
				t.Fatal("completed cleanup retained private cause")
			}
		})
	}
}
