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
