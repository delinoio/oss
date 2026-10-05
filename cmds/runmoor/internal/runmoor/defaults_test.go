package runmoor

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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
	if len(loaded.Pools[0].Labels) != 0 {
		t.Fatalf("omitted routing labels changed: %v", loaded.Pools[0].Labels)
	}
	explicit := filepath.Join(filepath.Dir(c.Storage.State), "explicit-labels.toml")
	if err := os.WriteFile(explicit, []byte(minimalConfig+"labels = [\"custom\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadConfig(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(loaded.Pools[0].Labels, []string{"custom"}) {
		t.Fatalf("explicit routing labels changed: %v", loaded.Pools[0].Labels)
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
	if p.DaemonResources != (Resources{1, 1024}) || p.MaxRunners != 3 {
		t.Fatalf("DinD defaults did not reserve runner CPU and combined memory: %+v", p)
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

func TestDinDRunnerOnlyCPUAdmission(t *testing.T) {
	body := minimalConfig + `mode = "dind"
daemon_image = "docker@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
min_idle = 12
max_runners = 15
resources = {cpu = 2, memory_mib = 16384}
daemon_resources = {cpu = 2, memory_mib = 2048}
`
	c, err := decodeDefaults(t, body, Resources{32, 524288})
	if err != nil {
		t.Fatal(err)
	}
	c.DockerBudget = Resources{32, 524288}
	c, err = resolveDefaults(c, Resources{32, 524288})
	if err != nil {
		t.Fatal(err)
	}
	p := c.Pools[0]
	s := Snapshot{Config: c, Pools: map[string]*PoolState{
		p.Name: {ID: p.Name, Spec: p, Phase: Ready, ScaleSetID: 1, Session: "fixture"},
	}, Runners: map[string]*Runner{}}
	initializeManaged(&s, c)
	if s.Managed[p.Name].Resources != (Resources{2, 18432}) {
		t.Fatal("managed status cost included daemon CPU")
	}
	if got := Schedule(s); len(got) != 12 {
		t.Fatalf("minimum idle allocation: got %d, want 12", len(got))
	}
	s.Pools[p.Name].Demand = 100
	if got := acquisitionCapacity(s, s.Pools[p.Name]); got != 15 {
		t.Fatalf("acquisition capacity: got %d, want 15", got)
	}
	for _, pool := range Schedule(s) {
		id := newID()
		s.Runners[id] = &Runner{ID: id, PoolID: pool, Backend: Docker, Phase: Preparing, Resources: p.Cost()}
	}
	status := statusOf(s, false)
	if status.Active != 15 || status.Reserved != (Resources{30, 276480}) || status.Pools[0].Resources != (Resources{2, 18432}) {
		t.Fatalf("wrong admission reservations in status: %+v", status)
	}
	if got := Schedule(s); len(got) != 0 {
		t.Fatalf("pool cap exceeded: %v", got)
	}

	// Omitting the pool cap uses 32/2 CPUs, without adding the daemon CPU
	// to either the available budget or the per-runner reservation.
	automatic := strings.Replace(body, "max_runners = 15\n", "", 1)
	c, err = decodeDefaults(t, automatic, Resources{32, 524288})
	if err != nil || c.Pools[0].MaxRunners != 16 || c.Host.MaxRunners != 16 {
		t.Fatalf("automatic CPU ceiling: %+v, %v", c, err)
	}
	for _, capacity := range []Resources{{23, 524288}, {32, 215 * 1024}} {
		_, err := decodeDefaults(t, body, capacity)
		requireCode(t, err, ErrConfig)
	}
}

func TestDinDDefaultsUseFullCPUAndReserveDaemonMemory(t *testing.T) {
	body := minimalConfig + `mode = "dind"
daemon_image = "docker@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`
	for _, tc := range []struct {
		name                 string
		host, engine, runner Resources
		max                  int
	}{
		{"one CPU host", Resources{1, 2048}, Resources{}, Resources{1, 1024}, 1},
		{"CPU limited engine", Resources{32, 524288}, Resources{4, 65536}, Resources{2, 4096}, 2},
		{"memory limited engine", Resources{32, 524288}, Resources{32, 10240}, Resources{2, 4096}, 2},
		{"one CPU engine", Resources{32, 524288}, Resources{1, 2048}, Resources{1, 1024}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := decodeDefaults(t, body, tc.host)
			if err == nil && validResources(tc.engine) {
				c.DockerBudget = tc.engine
				c, err = resolveDefaults(c, tc.host)
			}
			if err != nil {
				t.Fatal(err)
			}
			p := c.Pools[0]
			if p.Resources != tc.runner || p.DaemonResources != (Resources{1, 1024}) || p.MaxRunners != tc.max {
				t.Fatalf("wrong DinD resource defaults: %+v", p)
			}
		})
	}
	_, err := decodeDefaults(t, body, Resources{1, 1536})
	requireCode(t, err, ErrCapacity)
}
