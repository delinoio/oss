package runmoor

import "sort"

func usage(s Snapshot) (Resources, int, int) {
	var r Resources
	count, vms := 0, 0
	for _, v := range s.Runners {
		if v.Phase == Completed || v.Terminated {
			continue
		}
		r = r.Add(v.Resources)
		count++
		if v.Backend == Tart {
			vms++
		}
	}
	for _, im := range s.Images {
		if im.Phase == ImageOpen {
			r = r.Add(im.Resources)
			count++
			vms++
		}
	}
	return r, count, vms
}
func liveCount(s Snapshot, pool string) (total, busy int) {
	for _, r := range s.Runners {
		if r.PoolID == pool && r.Phase != Completed && !r.Terminated {
			total++
			if r.Phase == Busy {
				busy++
			}
		}
	}
	return
}
func logicalCount(s Snapshot, name string) int {
	n := 0
	for _, r := range s.Runners {
		p := s.Pools[r.PoolID]
		if p != nil && p.Spec.Name == name && r.Phase != Completed && !r.Terminated {
			n++
		}
	}
	return n
}
func eligible(s Snapshot, p *PoolState) bool {
	if s.Paused || s.Stopping || p.Phase != Ready || p.ScaleSetID == 0 || p.Session == "" {
		return false
	}
	for _, old := range s.Pools {
		if old.ID != p.ID && old.Phase != Retired && poolIdentity(old.Spec, old.Connection) == poolIdentity(p.Spec, p.Connection) {
			return false
		}
	}
	return true
}

// Schedule is pure: demand has a complete first pass before any warm capacity.
// It includes preparations, uncertain live resources, and image setup VMs.
func Schedule(s Snapshot) []string {
	ids := []string{}
	for id, p := range s.Pools {
		if eligible(s, p) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil
	}
	start := s.Cursor % len(ids)
	ids = append(ids[start:], ids[:start]...)
	used, total, vms := usage(s)
	counts := map[string]int{}
	logical := map[string]int{}
	for id, p := range s.Pools {
		counts[id], _ = liveCount(s, id)
		logical[p.Spec.Name] = logicalCount(s, p.Spec.Name)
	}
	var result []string
	for pass := 0; pass < 2; pass++ {
		for {
			progress := false
			for _, id := range ids {
				p := s.Pools[id]
				_, busy := liveCount(s, id)
				target := p.Demand
				if pass == 1 && busy+p.Spec.MinIdle > target {
					target = busy + p.Spec.MinIdle
				}
				if counts[id] >= target || logical[p.Spec.Name] >= p.Spec.MaxRunners {
					continue
				}
				cost := p.Spec.Cost()
				if total >= s.Config.Host.MaxRunners || used.CPU+cost.CPU > s.Config.Host.CPU || used.MemoryMiB+cost.MemoryMiB > s.Config.Host.MemoryMiB || (p.Spec.Backend == Tart && vms >= 2) {
					continue
				}
				result = append(result, id)
				counts[id]++
				logical[p.Spec.Name]++
				used = used.Add(cost)
				total++
				if p.Spec.Backend == Tart {
					vms++
				}
				progress = true
			}
			if !progress {
				break
			}
		}
	}
	return result
}
func retirementCandidates(s Snapshot) []string {
	_, _, vms := usage(s)
	needDocker, needTart := false, false
	for id, p := range s.Pools {
		n, _ := liveCount(s, id)
		if eligible(s, p) && n < p.Demand && logicalCount(s, p.Spec.Name) < p.Spec.MaxRunners {
			if p.Spec.Backend == Tart {
				needTart = true
			} else {
				needDocker = true
			}
		}
	}
	ids := []string{}
	for id, r := range s.Runners {
		if r.Phase == Idle {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	retiring := map[string]int{}
	var out []string
	for _, id := range ids {
		r := s.Runners[id]
		p := s.Pools[r.PoolID]
		if p == nil {
			continue
		}
		count, busy := liveCount(s, p.ID)
		// Retiring Docker capacity cannot release a slot at the Tart VM ceiling.
		remainingVMs := vms
		if r.Backend == Tart && !r.Terminated {
			remainingVMs--
		}
		needDemand := needDocker || (needTart && remainingVMs < 2)
		target := p.Demand
		if !needDemand && busy+p.Spec.MinIdle > target {
			target = busy + p.Spec.MinIdle
		}
		if s.Paused || s.Stopping || p.Phase != Ready {
			target = busy
		}
		if count-retiring[p.ID] > target {
			out = append(out, id)
			retiring[p.ID]++
		}
	}
	return out
}
