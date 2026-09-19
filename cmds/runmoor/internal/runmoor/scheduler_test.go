package runmoor

import (
	"testing"
	"time"
)

func schedulingState(t *testing.T) Snapshot {
	c := fixtureConfig(t)
	s := Snapshot{Config: c, Pools: map[string]*PoolState{}, Runners: map[string]*Runner{}, Images: map[string]*Image{}}
	for _, id := range []string{"a", "b", "c"} {
		p := c.Pools[0]
		p.Name = id
		p.ScaleSet = id
		s.Pools[id] = &PoolState{ID: id, Spec: p, Connection: c.Connections[0], Phase: Ready, ScaleSetID: 1, Session: "session"}
	}
	return s
}
func TestDemandPrecedesWarmAndRoundRobin(t *testing.T) {
	s := schedulingState(t)
	s.Config.Host.MaxRunners = 3
	s.Pools["a"].Spec.MinIdle = 3
	s.Pools["b"].Demand = 2
	s.Pools["c"].Demand = 2
	got := Schedule(s)
	if len(got) != 3 || got[0] != "b" || got[1] != "c" || got[2] != "b" {
		t.Fatalf("unfair or warm-first allocation: %v", got)
	}
	s.Cursor = 2
	got = Schedule(s)
	if got[0] != "c" {
		t.Fatal("round-robin cursor was ignored")
	}
}
func TestReservationsIncludeDindAndUncertainResources(t *testing.T) {
	s := schedulingState(t)
	s.Config.Host.CPU = 3
	s.Pools["a"].Spec.DaemonResources = Resources{CPU: 2, MemoryMiB: 256}
	s.Pools["a"].Demand = 3
	s.Runners["uncertain"] = &Runner{PoolID: "b", Phase: Quarantined, Resources: Resources{CPU: 1, MemoryMiB: 128}}
	if got := Schedule(s); len(got) != 0 {
		t.Fatalf("overcommitted daemon or uncertain work: %v", got)
	}
	s.Runners["uncertain"].Terminated = true
	if got := Schedule(s); len(got) != 1 {
		t.Fatalf("confirmed termination did not release reservation: %v", got)
	}
}
func TestWarmRetirementPreservesDemand(t *testing.T) {
	s := schedulingState(t)
	s.Config.Host.MaxRunners = 3
	s.Pools["a"].Spec.MinIdle = 2
	s.Pools["b"].Demand = 1
	s.Runners["warm"] = &Runner{ID: "warm", PoolID: "a", Phase: Idle}
	s.Runners["busy"] = &Runner{ID: "busy", PoolID: "a", Phase: Busy}
	s.Runners["assigned"] = &Runner{ID: "assigned", PoolID: "b", Phase: Idle}
	s.Pools["c"].Demand = 1
	got := retirementCandidates(s)
	if len(got) != 1 || got[0] != "warm" {
		t.Fatalf("retired busy/demanded runner: %v", got)
	}
}

func TestCappedDemandDoesNotChurnWarmRunners(t *testing.T) {
	for _, oldGeneration := range []bool{false, true} {
		s := schedulingState(t)
		s.Config.Host.MaxRunners = 2
		s.Pools["a"].Spec.MinIdle = 1
		s.Pools["b"].Demand = 10
		s.Pools["b"].Spec.MaxRunners = 1
		s.Runners["warm"] = &Runner{ID: "warm", PoolID: "a", Phase: Idle, Resources: Resources{1, 128}}
		pool := "b"
		if oldGeneration {
			old := *s.Pools["b"]
			old.ID, old.Phase, old.Spec.ScaleSet = "old", Draining, "old-remote"
			s.Pools["old"] = &old
			pool = "old"
		}
		s.Runners["busy"] = &Runner{ID: "busy", PoolID: pool, Phase: Busy, Resources: Resources{1, 128}}
		for tick := 0; tick < 3; tick++ {
			if got := retirementCandidates(s); len(got) != 0 {
				t.Fatalf("capped demand evicted warm capacity (old=%t): %v", oldGeneration, got)
			}
			if got := Schedule(s); len(got) != 0 {
				t.Fatalf("capped pool admitted excess capacity: %v", got)
			}
			s.Cursor++
		}
		// Once the pool cap permits demand to use that slot, warm capacity must
		// still yield without terminating the existing busy execution.
		s.Pools["b"].Spec.MaxRunners = 2
		if got := retirementCandidates(s); len(got) != 1 || got[0] != "warm" {
			t.Fatalf("admissible demand did not displace idle capacity: %v", got)
		}
		delete(s.Runners, "warm")
		if got := Schedule(s); len(got) != 1 || got[0] != "b" {
			t.Fatalf("released slot did not satisfy demand: %v", got)
		}
	}
}
func TestVMCappedDemandPreservesUnrelatedWarmCapacity(t *testing.T) {
	for _, occupied := range []string{"busy", "preparing", "quarantined", "setup"} {
		t.Run(occupied, func(t *testing.T) {
			s := schedulingState(t)
			s.Pools["a"].Spec.MinIdle = 1
			s.Pools["b"].Spec.Backend = Tart
			s.Pools["b"].Demand = 1
			s.Runners["warm"] = &Runner{ID: "warm", PoolID: "a", Phase: Idle, Backend: Docker}
			for _, id := range []string{"vm1", "vm2"} {
				if occupied == "setup" {
					s.Images[id] = &Image{Phase: ImageOpen}
				} else {
					phase := Busy
					if occupied == "preparing" {
						phase = Preparing
					} else if occupied == "quarantined" {
						phase = Quarantined
					}
					s.Runners[id] = &Runner{ID: id, PoolID: "c", Phase: phase, Backend: Tart}
				}
			}
			for tick := 0; tick < 3; tick++ {
				if got := retirementCandidates(s); len(got) != 0 {
					t.Fatalf("VM-capped demand evicted warm Docker runner: %v", got)
				}
				if got := Schedule(s); len(got) != 0 {
					t.Fatalf("VM-capped demand admitted: %v", got)
				}
				s.Cursor++
			}
			delete(s.Images, "vm2")
			delete(s.Runners, "vm2")
			if got := retirementCandidates(s); len(got) != 0 {
				t.Fatalf("available VM slot needlessly displaced warm capacity: %v", got)
			}
			if got := Schedule(s); len(got) != 1 || got[0] != "b" {
				t.Fatalf("available VM slot did not satisfy Tart demand: %v", got)
			}
		})
	}
}

func TestVMCappedDemandCanRetireWarmVM(t *testing.T) {
	s := schedulingState(t)
	s.Pools["a"].Spec.MinIdle = 1
	s.Pools["b"].Spec.Backend = Tart
	s.Pools["b"].Demand = 1
	s.Pools["c"].Spec.Backend = Tart
	s.Pools["c"].Spec.MinIdle = 1
	s.Runners["docker"] = &Runner{ID: "docker", PoolID: "a", Phase: Idle, Backend: Docker}
	s.Runners["tart"] = &Runner{ID: "tart", PoolID: "c", Phase: Idle, Backend: Tart}
	s.Images["setup"] = &Image{Phase: ImageOpen}
	if got := retirementCandidates(s); len(got) != 1 || got[0] != "tart" {
		t.Fatalf("only a warm VM can release the required slot: %v", got)
	}
	delete(s.Runners, "tart")
	if got := Schedule(s); len(got) != 1 || got[0] != "b" {
		t.Fatalf("released VM slot did not satisfy demand: %v", got)
	}
	// Docker demand can still displace warm Docker capacity at the VM ceiling.
	s.Config.Host.MaxRunners = 3
	s.Images["setup2"] = &Image{Phase: ImageOpen}
	s.Pools["b"].Spec.Backend = Docker
	if got := retirementCandidates(s); len(got) != 1 || got[0] != "docker" {
		t.Fatalf("VM ceiling blocked Docker demand retirement: %v", got)
	}
}

func TestImagePreparationSharesTwoVMLimit(t *testing.T) {
	s := schedulingState(t)
	s.Pools["a"].Spec.Backend = Tart
	s.Pools["a"].Demand = 3
	s.Images["setup"] = &Image{Phase: ImageOpen, Resources: Resources{CPU: 1, MemoryMiB: 128}}
	got := Schedule(s)
	if len(got) != 1 {
		t.Fatalf("setup VM ignored by limit: %v", got)
	}
}
func TestBackoffAndRateLimit(t *testing.T) {
	for i := 0; i < 20; i++ {
		d := retryDelay(i, nil)
		if d <= 0 || d > 60*time.Second {
			t.Fatalf("invalid backoff %s", d)
		}
	}
	p := &Problem{RetryAt: time.Now().Add(2 * time.Minute)}
	if retryDelay(0, p) < 119*time.Second {
		t.Fatal("rate limit reset ignored")
	}
}

func TestWarmVMRetirementAccountsForSelectedCapacity(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		demand, cpu           int
		memory                int64
		want                  int
		samePool, poolLimited bool
	}{
		{name: "one demand", demand: 1, cpu: 1, memory: 128, want: 1},
		{name: "two demands", demand: 2, cpu: 1, memory: 128, want: 2},
		{name: "CPU needs both", demand: 1, cpu: 2, memory: 128, want: 2},
		{name: "memory needs both", demand: 1, cpu: 1, memory: 256, want: 2},
		{name: "same warm pool", demand: 1, cpu: 1, memory: 128, want: 1, samePool: true},
		{name: "bounded by pool cap", demand: 10, cpu: 1, memory: 128, want: 1, poolLimited: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := schedulingState(t)
			s.Config.Host.MaxRunners, s.Config.Host.CPU, s.Config.Host.MemoryMiB = 2, 2, 256
			for _, p := range s.Pools {
				p.Spec.Backend = Tart
			}
			s.Pools["a"].Spec.MinIdle, s.Pools["b"].Spec.MinIdle = 1, 1
			s.Pools["c"].Demand = tc.demand
			s.Pools["c"].Spec.Resources = Resources{CPU: tc.cpu, MemoryMiB: tc.memory}
			if tc.poolLimited {
				s.Pools["c"].Spec.MaxRunners = 1
			}
			s.Runners["warm-a"] = &Runner{ID: "warm-a", PoolID: "a", Phase: Idle, Backend: Tart, Resources: Resources{1, 128}}
			s.Runners["warm-b"] = &Runner{ID: "warm-b", PoolID: "b", Phase: Idle, Backend: Tart, Resources: Resources{1, 128}}
			if tc.samePool {
				s.Runners["warm-b"].PoolID = "a"
				s.Pools["a"].Spec.MinIdle, s.Pools["b"].Spec.MinIdle = 2, 0
			}
			got := retirementCandidates(s)
			if len(got) != tc.want || got[0] != "warm-a" {
				t.Fatalf("retired more or less than demand needs: %v", got)
			}
			if len(s.Runners) != 2 || s.Runners["warm-a"].Phase != Idle || s.Pools["a"].Spec.MinIdle == 0 {
				t.Fatal("planning mutated the input snapshot")
			}
			for _, id := range got {
				r := s.Runners[id]
				r.Phase, r.RemoteRemoved = Cleaning, true
			}
			for tick := 0; tick < 3; tick++ {
				if more := retirementCandidates(s); len(more) != 0 {
					t.Fatalf("pending cleanup evicted another warm VM: %v", more)
				}
				if allocated := Schedule(s); len(allocated) != 0 {
					t.Fatalf("planning released actual reservations early: %v", allocated)
				}
				s.Cursor++
			}
			for _, id := range got {
				s.Runners[id].Terminated = true
			}
			allocated := Schedule(s)
			want := min(tc.demand, s.Pools["c"].Spec.MaxRunners)
			if len(allocated) != want || allocated[0] != "c" {
				t.Fatalf("confirmed capacity did not satisfy demand: %v", allocated)
			}
		})
	}
}

func TestAvailableDemandCapacityPreservesWarmRunners(t *testing.T) {
	s := schedulingState(t)
	s.Pools["a"].Spec.MinIdle = 1
	s.Runners["warm"] = &Runner{ID: "warm", PoolID: "a", Phase: Idle, Backend: Docker, Resources: Resources{1, 128}}
	s.Pools["b"].Demand = 1
	if got := retirementCandidates(s); len(got) != 0 {
		t.Fatalf("available capacity still evicted a minimum-idle runner: %v", got)
	}
	if got := Schedule(s); len(got) != 1 || got[0] != "b" {
		t.Fatalf("available capacity did not serve demand: %v", got)
	}
}

func TestDeregisteredRetirementDoesNotCountTowardWarmExcess(t *testing.T) {
	s := schedulingState(t)
	s.Pools["a"].Spec.MinIdle = 2
	for _, id := range []string{"retiring", "warm-1", "warm-2"} {
		s.Runners[id] = &Runner{ID: id, PoolID: "a", Phase: Idle, Backend: Docker, Resources: Resources{1, 128}}
	}
	s.Runners["retiring"].Phase, s.Runners["retiring"].RemoteRemoved = Cleaning, true
	if got := retirementCandidates(s); len(got) != 0 {
		t.Fatalf("pending retirement caused another minimum-idle eviction: %v", got)
	}
	if _, reserved, _ := usage(s); reserved != 3 {
		t.Fatal("retirement planning released a reservation before termination")
	}
}
