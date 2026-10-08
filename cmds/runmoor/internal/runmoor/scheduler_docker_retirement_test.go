package runmoor

import (
	"fmt"
	"testing"
)

func dockerRetirementFixture(t *testing.T, warm Backend, budget Resources) Snapshot {
	t.Helper()
	s := schedulingState(t)
	delete(s.Pools, "c")
	s.Config.Host.CPU, s.Config.Host.MemoryMiB, s.Config.Host.MaxRunners = 8, 8192, 4
	s.Config.DockerBudget = budget
	s.Pools["a"].Spec.Backend = warm
	s.Pools["a"].Spec.MinIdle = 1
	s.Pools["a"].Spec.Resources = Resources{1, 128}
	s.Pools["b"].Spec.Backend = Docker
	s.Pools["b"].Spec.Resources = Resources{1, 128}
	s.Pools["b"].Spec.MaxRunners = 4
	s.Pools["b"].Demand = 2
	s.Runners["warm"] = &Runner{ID: "warm", PoolID: "a", Phase: Idle, Backend: warm, Resources: Resources{1, 128}}
	s.Runners["busy"] = &Runner{ID: "busy", PoolID: "b", Phase: Busy, Backend: Docker, Resources: Resources{1, 128}}
	return s
}
func TestDockerEngineWaitPreservesUnrelatedWarmRunners(t *testing.T) {
	for _, backend := range []Backend{Tart, Host} {
		for _, budget := range []Resources{{1, 8192}, {8, 128}} {
			t.Run(fmt.Sprintf("%s/%+v", backend, budget), func(t *testing.T) {
				s := dockerRetirementFixture(t, backend, budget)
				before := fingerprint(s)
				for tick := 0; tick < 3; tick++ {
					if got := Schedule(s); len(got) != 0 {
						t.Fatalf("engine-blocked demand allocated: %v", got)
					}
					if got := retirementCandidates(s); len(got) != 0 {
						t.Fatalf("engine wait evicted unrelated warm capacity: %v", got)
					}
				}
				if fingerprint(s) != before {
					t.Fatal("retirement planning mutated actual reservations")
				}
				s.Config.DockerBudget = Resources{8, 8192}
				if got := Schedule(s); len(got) != 1 || got[0] != "b" {
					t.Fatalf("available engine capacity did not satisfy demand: %v", got)
				}
				if got := retirementCandidates(s); len(got) != 0 {
					t.Fatalf("available engine capacity needlessly displaced warm runner: %v", got)
				}
			})
		}
	}
}
func TestDockerRetirementCountsSyntheticDemand(t *testing.T) {
	for _, backend := range []Backend{Tart, Host} {
		s := dockerRetirementFixture(t, backend, Resources{2, 256})
		s.Pools["b"].Demand = 3
		if got := Schedule(s); len(got) != 1 || got[0] != "b" {
			t.Fatalf("expected one engine-bounded allocation: %v", got)
		}
		if got := retirementCandidates(s); len(got) != 0 {
			t.Fatalf("unallocated engine-blocked demand evicted warm runner after synthetic allocation: %v", got)
		}
	}
}
func TestDockerRetirementCountsSelectedAndDeregisteredCapacity(t *testing.T) {
	s := dockerRetirementFixture(t, Docker, Resources{2, 256})
	s.Pools["b"].Demand = 3
	// An earlier useful Docker retirement fills the engine with synthetic demand;
	// the later host runner must not yield to remaining engine-blocked demand.
	p := *s.Pools["a"]
	p.ID, p.Spec.Name, p.Spec.ScaleSet = "c", "c", "c"
	p.Spec.Backend = Host
	s.Pools["c"] = &p
	s.Runners["z-host"] = &Runner{ID: "z-host", PoolID: "c", Phase: Idle, Backend: Host, Resources: Resources{1, 128}}
	before := fingerprint(s)
	if got := retirementCandidates(s); len(got) != 1 || got[0] != "warm" {
		t.Fatalf("useful Docker retirement or later host preservation lost: %v", got)
	}
	if fingerprint(s) != before {
		t.Fatal("planning changed actual reservations")
	}
	s.Runners["warm"].Phase, s.Runners["warm"].RemoteRemoved = Cleaning, true
	if got := retirementCandidates(s); len(got) != 0 {
		t.Fatalf("pending Docker cleanup evicted another warm runner: %v", got)
	}
	if got := Schedule(s); len(got) != 0 {
		t.Fatalf("pending cleanup released actual engine capacity: %v", got)
	}
	if used := dockerUsage(s); used != (Resources{2, 256}) {
		t.Fatalf("unconfirmed reservations changed: %+v", used)
	}
	s.Runners["warm"].Terminated = true
	if got := Schedule(s); len(got) != 1 || got[0] != "b" {
		t.Fatalf("confirmed Docker termination did not satisfy demand: %v", got)
	}
	if s.Runners["busy"].Phase != Busy || s.Runners["busy"].Terminated {
		t.Fatal("planning changed busy work")
	}
}
func TestDockerDemandCanRetireMultipleSharedHostReservations(t *testing.T) {
	for _, resource := range []string{"cpu", "memory"} {
		t.Run(resource, func(t *testing.T) {
			s := dockerRetirementFixture(t, Host, Resources{8, 8192})
			s.Runners["warm-2"] = &Runner{ID: "warm-2", PoolID: "a", Phase: Idle, Backend: Host, Resources: Resources{1, 128}}
			s.Pools["a"].Spec.MinIdle = 2
			if resource == "cpu" {
				s.Config.Host.CPU = 3
				s.Pools["b"].Spec.Resources.CPU = 2
			} else {
				s.Config.Host.MemoryMiB = 384
				s.Pools["b"].Spec.Resources.MemoryMiB = 256
			}
			before := fingerprint(s)
			got := retirementCandidates(s)
			if len(got) != 2 || got[0] != "warm" || got[1] != "warm-2" {
				t.Fatalf("shared host constraint did not select both useful idle retirements: %v", got)
			}
			if fingerprint(s) != before {
				t.Fatal("planning released shared host reservations")
			}
			for _, id := range got {
				s.Runners[id].Phase = Cleaning
				s.Runners[id].RemoteRemoved = true
			}
			if got := Schedule(s); len(got) != 0 {
				t.Fatalf("unconfirmed retirement allocated demand: %v", got)
			}
			if got := retirementCandidates(s); len(got) != 0 {
				t.Fatalf("pending cleanup selected further capacity: %v", got)
			}
			for _, id := range got {
				s.Runners[id].Terminated = true
			}
			if got := Schedule(s); len(got) != 1 || got[0] != "b" {
				t.Fatalf("confirmed shared capacity did not satisfy demand: %v", got)
			}
		})
	}
}
func TestDockerArtifactReservationPreservesWarmHostCapacity(t *testing.T) {
	s := dockerRetirementFixture(t, Host, Resources{1, 128})
	delete(s.Runners, "busy")
	s.Artifacts = map[string]*RunnerArtifact{"build": {ID: "build", Backend: Docker, Reserved: true, Resources: Resources{1, 128}}}
	if got := retirementCandidates(s); len(got) != 0 {
		t.Fatalf("engine reservation evicted unrelated host runner: %v", got)
	}
	if got := Schedule(s); len(got) != 0 {
		t.Fatalf("engine reservation was ignored: %v", got)
	}
}

func TestDockerEngineDemandCanRetireMultipleDockerRunners(t *testing.T) {
	s := dockerRetirementFixture(t, Docker, Resources{3, 8192})
	s.Pools["a"].Spec.MinIdle = 2
	s.Pools["b"].Spec.Resources.CPU = 2
	s.Runners["warm-2"] = &Runner{ID: "warm-2", PoolID: "a", Phase: Idle, Backend: Docker, Resources: Resources{1, 128}}
	got := retirementCandidates(s)
	if len(got) != 2 || got[0] != "warm" || got[1] != "warm-2" {
		t.Fatalf("partial useful engine retirements were lost: %v", got)
	}
	for _, id := range got {
		s.Runners[id].Terminated = true
	}
	if allocated := Schedule(s); len(allocated) != 1 || allocated[0] != "b" {
		t.Fatalf("released engine capacity did not satisfy demand: %v", allocated)
	}
}
