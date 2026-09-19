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
