// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/executionenv"
)

func TestOrdinaryGhConfigurationAfterAdapterRebuild(t *testing.T) {
	cfg := fixtureConfig(t, "ready")
	cfg.Mode = ThreadProtocol
	cfg.Process.Env = append(cfg.Process.Env, "HOME="+filepath.Dir(cfg.Home), "XDG_CONFIG_HOME="+filepath.Join(filepath.Dir(cfg.Home), "config"), "GH_TOKEN=foreign", "GH_CONFIG_DIR=foreign")
	directory := filepath.Join(t.TempDir(), "user-gh")
	cfg.OrdinaryTools = executionenv.Resolve("linux", []string{"GH_CONFIG_DIR=" + directory}, t.TempDir())
	configureOrdinaryTools(&cfg)
	assertOrdinaryGhTool(t, cfg.Process.Env, directory)
	for _, excluded := range []Config{{Mode: ProbeProtocol}, {Mode: SubscriptionProtocol}, {Mode: ThreadProtocol, Sidechat: ReadOnlySidechatV1}, {Mode: ThreadProtocol, ModelObservation: true}, {Mode: ThreadProtocol, API: &APIConfig{TitleProfile: true}}} {
		excluded.OrdinaryTools, excluded.Process.Env = cfg.OrdinaryTools, cfg.Process.Env
		configureOrdinaryTools(&excluded)
		if strings.Contains(strings.Join(excluded.Process.Env, "\n"), "GH_CONFIG_DIR=") {
			t.Fatal("auxiliary flow gained ordinary context")
		}
	}
}

// This offline gh subprocess reads only a temporary synthetic hosts.yml. It
// proves selector propagation, not installed-engine or OS credential-store use.
func assertOrdinaryGhTool(t *testing.T, env []string, directory string) {
	t.Helper()
	binary, err := exec.LookPath("gh")
	if err != nil {
		t.Skip("gh is unavailable for the offline synthetic fixture")
	}
	const token = "synthetic_gh_fixture_only_1857"
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "hosts.yml")
	raw := []byte("fixture.invalid:\n    oauth_token: " + token + "\n    user: fixture-user\n    git_protocol: https\n")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(values []string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "auth", "token", "--hostname", "fixture.invalid")
		command.Env = values
		return command.Output()
	}
	output, err := run(env)
	if err != nil || strings.TrimSpace(string(output)) != token {
		t.Fatal("ordinary tool did not locate its synthetic configuration")
	}
	// gh may migrate its own file during an explicitly invoked command.
	baseline, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	excluded := executionenv.Ordinary{}.Apply(env)
	if output, err := run(excluded); err == nil || strings.Contains(string(output), token) {
		t.Fatal("excluded flow borrowed user tool configuration")
	}
	after, err := os.ReadFile(file)
	if err != nil || string(after) != string(baseline) {
		t.Fatal("tool configuration was modified")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := run(env); err == nil {
		t.Fatal("missing login gained a credential fallback")
	}
}

func TestOrdinaryGhContextPreservesNativeCredentialExclusions(t *testing.T) {
	cfg := Config{Mode: ThreadProtocol, API: &APIConfig{ServerOrigin: "https://server.example", Token: apiFixtureToken()}}
	if _, err := configureAPI(&cfg); err != nil {
		t.Fatal(err)
	}
	originalArgs, originalProtected := append([]string(nil), cfg.Process.Args...), append([]string(nil), cfg.Process.ProtectedValues...)
	cfg.OrdinaryTools = executionenv.Resolve("linux", []string{"GH_CONFIG_DIR=" + t.TempDir()}, t.TempDir())
	configureOrdinaryTools(&cfg)
	if !reflect.DeepEqual(originalArgs, cfg.Process.Args) || !reflect.DeepEqual(originalProtected, cfg.Process.ProtectedValues) || !strings.Contains(strings.Join(cfg.Process.Args, " "), "shell_environment_policy.exclude=") {
		t.Fatal("ordinary tool context changed native credential exclusion authority")
	}
}
