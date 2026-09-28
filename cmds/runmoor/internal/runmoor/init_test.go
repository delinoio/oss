package runmoor

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestInitialPoolLabels(t *testing.T) {
	for _, tc := range []struct {
		backend Backend
		arch    string
		name    string
		labels  []string
	}{
		{Docker, "amd64", "linux", []string{"runmoor-linux", "linux", "x64"}},
		{Docker, "arm64", "linux", []string{"runmoor-linux", "linux", "ARM64"}},
		{Tart, "arm64", "macos", []string{"runmoor-macos", "macOS", "ARM64"}},
	} {
		t.Run(string(tc.backend)+"/"+tc.arch, func(t *testing.T) {
			name, labels, err := initialPoolLabels(tc.backend, tc.arch)
			if err != nil || name != tc.name || !slices.Equal(labels, tc.labels) {
				t.Fatalf("pool labels: name=%q labels=%v error=%v", name, labels, err)
			}
		})
	}
	for _, tc := range []struct {
		backend Backend
		arch    string
	}{{Docker, "386"}, {Tart, "amd64"}, {Backend("unknown"), "arm64"}} {
		if _, _, err := initialPoolLabels(tc.backend, tc.arch); err == nil {
			t.Fatalf("accepted unsupported backend/architecture: %s/%s", tc.backend, tc.arch)
		}
	}
}

func TestInitInteractiveAndNoninteractiveReferences(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("supported host initialization")
	}
	c := fixtureConfig(t)
	for _, interactive := range []bool{false, true} {
		path := filepath.Join(filepath.Dir(c.Storage.State), newID()+".toml")
		opts := InitOptions{Backend: "docker"}
		input := ""
		if interactive {
			input = "https://github.com/example/repo\npat\nenv\nMY_RUNNER_PAT\n"
		} else {
			opts.Target = "https://github.com/example/repo"
			opts.CredentialEnv = "MY_RUNNER_PAT"
		}
		var output bytes.Buffer
		if err := initialize(path, opts, strings.NewReader(input), &output, interactive); err != nil {
			t.Fatal(err)
		}
		config, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		_, wantLabels, err := initialPoolLabels(Docker, runtime.GOARCH)
		if err != nil {
			t.Fatal(err)
		}
		if config.Connections[0].Credential.Env != "MY_RUNNER_PAT" || config.Pools[0].RunnerVersion != LatestRunner || !slices.Equal(config.Pools[0].Labels, wantLabels) || !strings.Contains(output.String(), "runs-on: runmoor-linux") {
			t.Fatal("initializer failed to create minimal managed configuration")
		}
		original, _ := os.ReadFile(path)
		if err = initialize(path, opts, strings.NewReader(input), &output, interactive); err == nil {
			t.Fatal("overwrote configuration")
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(original, after) {
			t.Fatal("existing file changed")
		}
	}
	path := filepath.Join(filepath.Dir(c.Storage.State), "missing.toml")
	if err := initialize(path, InitOptions{}, strings.NewReader(""), &bytes.Buffer{}, false); err == nil {
		t.Fatal("missing mandatory input accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("incomplete configuration was written")
	}
	if err := initialize(path, InitOptions{Backend: "docker"}, strings.NewReader(""), &bytes.Buffer{}, true); err == nil {
		t.Fatal("EOF accepted")
	}
}

func TestInitTartLabelsRoundTrip(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Tart initialization requires macOS arm64")
	}
	c := fixtureConfig(t)
	path := filepath.Join(filepath.Dir(c.Storage.State), "tart-labels.toml")
	opts := InitOptions{Backend: string(Tart), Target: "https://github.com/example/repo", CredentialEnv: "MY_RUNNER_PAT", Image: newID()}
	if err := initialize(path, opts, strings.NewReader(""), &bytes.Buffer{}, false); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Pools) != 1 || config.Pools[0].ScaleSet != "runmoor-macos" || !slices.Equal(config.Pools[0].Labels, []string{"runmoor-macos", "macOS", "ARM64"}) {
		t.Fatalf("Tart labels did not survive TOML round trip: %+v", config.Pools)
	}
}
