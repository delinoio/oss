package runmoor

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func decodeDefaults(t *testing.T, body string, capacity Resources) (Config, error) {
	t.Helper()
	var c Config
	if err := toml.Unmarshal([]byte(body), &c); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := toml.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatal(err)
	}
	markAutomatic(&c, fields)
	return resolveDefaults(c, capacity)
}

const minimalConfig = `schema_version = 1
[[connections]]
name = "project"
target = "https://github.com/example/repo"
auth = "pat"
credential = {env = "TEST_PAT"}
[[pools]]
name = "linux"
connection = "project"
scale_set = "runmoor-linux"
backend = "docker"
`

func TestAutomaticDefaultsAndExplicitInvalidValues(t *testing.T) {
	c, err := decodeDefaults(t, minimalConfig, Resources{12, 32768})
	if err != nil {
		t.Fatal(err)
	}
	p := c.Pools[0]
	if c.Host.CPU != 12 || c.Host.MemoryMiB != 32768 || c.Host.MaxRunners != 6 || p.Resources != (Resources{2, 4096}) || p.MaxRunners != 6 || p.Arch != runtime.GOARCH || p.RunnerVersion != LatestRunner {
		t.Fatalf("unexpected defaults: %+v %+v", c, p)
	}
	for _, line := range []string{"resources = {cpu = 0}", "resources = {memory_mib = 0}", "max_runners = 0", "resources = {cpu = -1}"} {
		_, err = decodeDefaults(t, minimalConfig+line+"\n", Resources{12, 32768})
		if err == nil {
			t.Fatalf("accepted explicit %s", line)
		}
	}
	c, err = decodeDefaults(t, minimalConfig, Resources{1, 2048})
	if err != nil || c.Pools[0].Resources != (Resources{1, 2048}) || c.Pools[0].MaxRunners != 1 {
		t.Fatalf("small host: %+v %v", c, err)
	}
	_, err = decodeDefaults(t, minimalConfig, Resources{1, 512})
	requireCode(t, err, ErrCapacity)
	c, err = decodeDefaults(t, minimalConfig+"resources = {cpu = 3}\n", Resources{12, 32768})
	if err != nil || c.Pools[0].Resources != (Resources{3, 4096}) || c.Pools[0].MaxRunners != 4 {
		t.Fatalf("partial override: %+v %v", c, err)
	}
}
func TestDockerDefaultsRespectIndependentCeiling(t *testing.T) {
	c, err := decodeDefaults(t, minimalConfig, Resources{16, 32768})
	if err != nil {
		t.Fatal(err)
	}
	c.DockerBudget = Resources{3, 6144}
	c, err = resolveDefaults(c, Resources{16, 32768})
	if err != nil || c.Host.CPU != 16 || c.Pools[0].MaxRunners != 1 {
		t.Fatalf("engine ceiling: %+v %v", c, err)
	}
}
func TestLoadMinimalConfigKeepsOmissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("supported host")
	}
	c := fixtureConfig(t)
	file := filepath.Join(filepath.Dir(c.Storage.State), "auto.toml")
	if err := os.WriteFile(file, []byte(minimalConfig), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Automatic["pool.linux.resources.cpu"] || loaded.Pools[0].MaxRunners == 0 {
		t.Fatalf("missing omission metadata: %+v", loaded)
	}
}

func TestAutomaticDinDAndMixedBackendBudgets(t *testing.T) {
	c, err := decodeDefaults(t, minimalConfig+`mode = "dind"
daemon_image = "docker@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`, Resources{8, 16384})
	if err != nil {
		t.Fatal(err)
	}
	p := c.Pools[0]
	if p.DaemonResources != (Resources{1, 1024}) || p.MaxRunners != 2 {
		t.Fatalf("DinD omitted allocations not combined: %+v", p)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	tart := p
	tart.Name = "macos"
	tart.ScaleSet = "runmoor-macos"
	tart.Backend = Tart
	tart.Mode = Plain
	tart.DaemonImage = ""
	tart.DaemonResources = Resources{}
	tart.Image = newID()
	tart.RunnerPath = "/Users/runner/actions-runner"
	tart.Resources = Resources{4, 8192}
	tart.MaxRunners = 2
	c.Pools = append(c.Pools, tart)
	c.Host = Budget{CPU: 16, MemoryMiB: 32768, MaxRunners: 4, MinFreeDiskMiB: 1}
	c.DockerBudget = Resources{3, 6144}
	c, err = NormalizeConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	s := Snapshot{Config: c, Pools: map[string]*PoolState{}, Runners: map[string]*Runner{}, Images: map[string]*Image{}, Artifacts: map[string]*RunnerArtifact{}}
	for _, p := range c.Pools {
		s.Pools[p.Name] = &PoolState{ID: p.Name, Spec: p, Phase: Ready, ScaleSetID: 1, Session: "fixture", Demand: 10}
	}
	allocated := Schedule(s)
	counts := map[string]int{}
	for _, pool := range allocated {
		counts[pool]++
	}
	if counts["linux"] != 1 || counts["macos"] != 2 {
		t.Fatalf("host/engine/VM ceilings not independent: %v", counts)
	}
}
