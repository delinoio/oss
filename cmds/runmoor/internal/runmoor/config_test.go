package runmoor

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func fixtureConfig(t *testing.T) Config {
	t.Helper()
	tmp := os.TempDir()
	if runtime.GOOS != "windows" {
		var err error
		tmp, err = filepath.EvalSymlinks(tmp)
		if err != nil {
			t.Fatal(err)
		}
	}
	base, err := os.MkdirTemp(tmp, "rm-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })

	c := Config{SchemaVersion: 1, Storage: Storage{State: filepath.Join(base, "s"), Data: filepath.Join(base, "d")}, Host: Budget{MaxRunners: 4, CPU: 8, MemoryMiB: 8192, MinFreeDiskMiB: 1}, Connections: []Connection{{Name: "test", Target: "https://github.com/example/repo", Auth: PAT, Credential: SecretRef{Env: "RUNMOOR_TEST_CREDENTIAL"}}}, Pools: []Pool{{Name: "linux", Connection: "test", ScaleSet: "test-linux", Labels: []string{"test-linux"}, Backend: Docker, Mode: Plain, Arch: runtime.GOARCH, Image: "example/runner@sha256:" + strings.Repeat("a", 64), RunnerVersion: "2.337.0", Resources: Resources{CPU: 1, MemoryMiB: 128}, MaxRunners: 4}}}
	c, e := NormalizeConfig(c)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func fixtureStore(t *testing.T) (Config, *Store) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix host storage contract")
	}
	c := fixtureConfig(t)
	s, e := OpenStore(c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return c, s
}
func requireCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	p, ok := err.(*Problem)
	if !ok || p.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestConfigRejectsUnsafeOrImpossibleSettings(t *testing.T) {
	for name, change := range map[string]func(*Config){
		"schema": func(c *Config) { c.SchemaVersion = 2 }, "emulation": func(c *Config) { c.Pools[0].Arch = "mips" }, "remote engine": func(c *Config) { c.DockerSocket = "tcp://localhost:2375" }, "host budget": func(c *Config) { c.Host.CPU = 0 }, "floating image": func(c *Config) { c.Pools[0].Image = "runner:latest" }, "ambiguous credential": func(c *Config) { c.Connections[0].Credential.File = "/private/key" }, "plain daemon": func(c *Config) { c.Pools[0].DaemonResources = Resources{1, 10} }, "dind budget": func(c *Config) {
			p := &c.Pools[0]
			p.Mode = DinD
			p.DaemonImage = p.Image
			p.DaemonResources = Resources{8, 128}
		}, "minimum idle": func(c *Config) { c.Host.MaxRunners = 1; c.Pools[0].MinIdle = 2 }, "duplicate target": func(c *Config) { p := c.Pools[0]; p.Name = "other"; c.Pools = append(c.Pools, p) }, "repository group": func(c *Config) { c.Pools[0].RunnerGroup = "engineering" }, "credential URL": func(c *Config) { c.Connections[0].Target = "https://secret@github.com/example/repo" }, "reserved label": func(c *Config) { c.Pools[0].Labels = []string{"runmoor-owner-foreign"} }, "negative timeout": func(c *Config) { c.Timeouts.Job = "-1s" },
	} {
		t.Run(name, func(t *testing.T) {
			c := fixtureConfig(t)
			change(&c)
			_, e := NormalizeConfig(c)
			requireCode(t, e, ErrConfig)
		})
	}
}
func TestStrictTOMLAndProtectedCredentials(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions")
	}
	c := fixtureConfig(t)
	data, e := toml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(filepath.Dir(c.Storage.State), "config.toml")
	if e = os.WriteFile(p, data, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadConfig(p); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p, append([]byte("unknown = true\n"), data...), 0600); e != nil {
		t.Fatal(e)
	}
	_, e = LoadConfig(p)
	requireCode(t, e, ErrConfig)
	key := filepath.Join(filepath.Dir(p), "credential")
	if e = os.WriteFile(key, []byte("test-private-value"), 0644); e != nil {
		t.Fatal(e)
	}
	_, e = ResolveSecret(SecretRef{File: key})
	requireCode(t, e, ErrAuth)
	if e = os.Chmod(key, 0600); e != nil {
		t.Fatal(e)
	}
	value, e := ResolveSecret(SecretRef{File: key})
	if e != nil || value != "test-private-value" {
		t.Fatal("secure credential read failed")
	}
	link := key + "-link"
	if e = os.Symlink(key, link); e != nil {
		t.Fatal(e)
	}
	_, e = ResolveSecret(SecretRef{File: link})
	requireCode(t, e, ErrAuth)
}
func TestCLIInitAndErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix CLI installation")
	}
	c := fixtureConfig(t)
	p := filepath.Join(filepath.Dir(c.Storage.State), "config.toml")
	var out, errs bytes.Buffer
	if code := Execute([]string{"init", "--config", p}, &out, &errs); code != 0 {
		t.Fatal(errs.String())
	}
	if code := Execute([]string{"init", "--config", p}, &out, &errs); code == 0 {
		t.Fatal("overwrote config")
	}
	errs.Reset()
	if code := Execute([]string{"config", "validate", "--config", p, "--json"}, &out, &errs); code != 2 {
		t.Fatalf("incomplete skeleton accepted: %d", code)
	}
	if !strings.Contains(errs.String(), `"schema_version": 1`) || !strings.Contains(errs.String(), `"code": "CONFIG_INVALID"`) {
		t.Fatal(errs.String())
	}
	out.Reset()
	Execute([]string{"version"}, &out, &errs)
	if !strings.Contains(out.String(), Version+" preview") {
		t.Fatal(out.String())
	}
}
