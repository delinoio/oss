package runmoor

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

// Automatic records omissions, never zero values. It is persisted with the
// requested configuration so a restart cannot turn a detected value into a pin.
func markAutomatic(c *Config, fields map[string]any) {
	c.Automatic = map[string]bool{}
	host, _ := fields["host"].(map[string]any)
	for _, key := range []string{"cpu", "memory_mib", "max_runners", "min_free_disk_mib"} {
		_, found := host[key]
		c.Automatic["host."+key] = !found
	}
	pools, _ := fields["pools"].([]any)
	for i, p := range c.Pools {
		var raw map[string]any
		if i < len(pools) {
			raw, _ = pools[i].(map[string]any)
		}
		for _, key := range []string{"arch", "max_runners"} {
			_, found := raw[key]
			c.Automatic[poolDefaultKey(p.Name, key)] = !found
		}
		for _, group := range []string{"resources", "daemon_resources"} {
			r, _ := raw[group].(map[string]any)
			for _, key := range []string{"cpu", "memory_mib"} {
				_, found := r[key]
				c.Automatic[poolDefaultKey(p.Name, group+"."+key)] = !found
			}
		}
	}
}
func poolDefaultKey(name, field string) string { return "pool." + name + "." + field }
func needsCapacity(c Config) bool {
	for _, automatic := range c.Automatic {
		if automatic {
			return true
		}
	}
	return false
}
func validImageSource(s ImageSource) bool {
	if s.From == "" || strings.ContainsAny(s.From+s.SourceHome, "\x00\r\n") {
		return false
	}
	if s.SourceHome != "" && !filepath.IsAbs(s.SourceHome) {
		return false
	}
	return validID(s.From) || safeName.MatchString(s.From) || (filepath.IsAbs(s.From) && strings.HasSuffix(s.From, ".tvm")) || (strings.HasPrefix(s.From, "oci://") && strings.Contains(strings.TrimPrefix(s.From, "oci://"), "/"))
}
func resolveDefaults(c Config, capacity Resources) (Config, error) {
	auto := c.Automatic
	if auto["host.cpu"] {
		c.Host.CPU = capacity.CPU
	}
	if auto["host.memory_mib"] {
		c.Host.MemoryMiB = capacity.MemoryMiB
	}
	if auto["host.min_free_disk_mib"] {
		c.Host.MinFreeDiskMiB = 10240
		for _, p := range c.Pools {
			if p.Backend == Tart {
				c.Host.MinFreeDiskMiB = 20480
			}
		}
	}
	total := 0
	for i := range c.Pools {
		p := &c.Pools[i]
		if auto[poolDefaultKey(p.Name, "arch")] {
			p.Arch = runtime.GOARCH
		}
		available := Resources{c.Host.CPU, c.Host.MemoryMiB}
		if p.Backend == Docker && validResources(c.DockerBudget) {
			available.CPU = min(available.CPU, c.DockerBudget.CPU)
			available.MemoryMiB = min(available.MemoryMiB, c.DockerBudget.MemoryMiB)
		}
		desired, floor := Resources{2, 4096}, Resources{1, 1024}
		if p.Backend == Tart {
			desired, floor = Resources{4, 8192}, Resources{2, 4096}
		}
		if p.Mode == DinD {
			if auto[poolDefaultKey(p.Name, "daemon_resources.cpu")] {
				p.DaemonResources.CPU = 1
			}
			if auto[poolDefaultKey(p.Name, "daemon_resources.memory_mib")] {
				p.DaemonResources.MemoryMiB = 1024
			}
		}
		available.MemoryMiB -= p.DaemonResources.MemoryMiB
		if auto[poolDefaultKey(p.Name, "resources.cpu")] {
			p.Resources.CPU = min(desired.CPU, available.CPU)
			if p.Resources.CPU < floor.CPU {
				return c, problem(ErrCapacity, "Automatic runner CPU allocation is below the backend minimum.", "Increase the available CPU budget.")
			}
		}
		if auto[poolDefaultKey(p.Name, "resources.memory_mib")] {
			p.Resources.MemoryMiB = min(desired.MemoryMiB, available.MemoryMiB)
			if p.Resources.MemoryMiB < floor.MemoryMiB {
				return c, problem(ErrCapacity, "Automatic runner memory allocation is below the backend minimum.", "Increase the available memory budget.")
			}
		}
		cost := p.Cost()
		if auto[poolDefaultKey(p.Name, "max_runners")] && validResources(cost) {
			limit := Resources{available.CPU, available.MemoryMiB + p.DaemonResources.MemoryMiB}
			p.MaxRunners = min(limit.CPU/cost.CPU, int(limit.MemoryMiB/cost.MemoryMiB), 10000)
			if p.Backend == Tart {
				p.MaxRunners = min(p.MaxRunners, 2)
			}
		}
		total += p.MaxRunners
	}
	if auto["host.max_runners"] {
		c.Host.MaxRunners = max(1, min(total, 10000))
		if len(c.Pools) == 0 {
			c.Host.MaxRunners = 2
		}
	}
	return NormalizeConfig(c)
}

// Docker capacity is a second ceiling, not a replacement for the host ceiling.
// In particular, a Docker Desktop VM must not limit unrelated Tart allocations.
func resolveDockerCapacity(ctx context.Context, c Config) (Config, error) {
	found := false
	for _, p := range c.Pools {
		found = found || p.Backend == Docker
	}
	if !found {
		return c, nil
	}
	cli, err := dockerClient(ctx, c)
	if err != nil {
		return c, err
	}
	defer cli.Close()
	info, err := cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return c, dockerProblem()
	}
	c.DockerBudget = Resources{info.Info.NCPU, info.Info.MemTotal / (1024 * 1024)}
	c.DockerCapacityPending = false
	return resolveDefaults(c, Resources{c.Host.CPU, c.Host.MemoryMiB})
}

func (m *Manager) retryDockerCapacity(ctx context.Context) error {
	m.imageMu.Lock()
	defer m.imageMu.Unlock()
	s := m.Store.View()
	if !s.Requested.DockerCapacityPending {
		return nil
	}
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := guardDockerArtifactEndpoint(probe, s.Requested, s); err != nil {
		return err
	}
	c, err := m.ResolveCapacity(probe, s.Requested)
	if err != nil {
		return err
	}
	return m.Store.Update(func(v *Snapshot) error {
		if v.Stopping || fingerprint(v.Requested) != fingerprint(s.Requested) {
			return staleUpdate()
		}
		initializeManaged(v, c)
		return acceptSnapshot(v, managedConfig(*v), false)
	})
}
func defaultImageResources(c Config, r Resources) (Resources, error) {
	if r.CPU == 0 {
		r.CPU = min(4, c.Host.CPU)
		if r.CPU < 2 {
			return r, problem(ErrCapacity, "Automatic Tart setup needs two CPUs.", "Increase the host budget.")
		}
	}
	if r.MemoryMiB == 0 {
		r.MemoryMiB = min(int64(8192), c.Host.MemoryMiB)
		if r.MemoryMiB < 4096 {
			return r, problem(ErrCapacity, "Automatic Tart setup needs 4096 MiB.", "Increase the host budget.")
		}
	}
	if !validResources(r) {
		return r, problem(ErrCapacity, "Tart setup requires at least two CPUs and 4096 MiB.", "Increase the host or setup budget.")
	}
	return r, nil
}
func allocationSummary(c Config) string {
	return fmt.Sprintf("Host: %d CPUs, %d MiB; up to %d concurrent runners.", c.Host.CPU, c.Host.MemoryMiB, c.Host.MaxRunners)
}
