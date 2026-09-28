package runmoor

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
		if config.Connections[0].Credential.Env != "MY_RUNNER_PAT" || config.Pools[0].RunnerVersion != LatestRunner || !strings.Contains(output.String(), "runs-on: runmoor-linux") {
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
