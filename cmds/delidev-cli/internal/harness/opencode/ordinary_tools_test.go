// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/executionenv"
)

func TestOrdinaryGhConfigurationAfterAdapterRebuild(t *testing.T) {
	cfg := fixtureOwnedAPIConfig(t)
	directory := filepath.Join(t.TempDir(), "user-gh")
	cfg.OrdinaryTools = executionenv.Resolve("linux", []string{"GH_CONFIG_DIR=" + directory}, t.TempDir())
	cfg.Probe.Process.Env = append(cfg.Probe.Process.Env, "GH_TOKEN=foreign", "GH_CONFIG_DIR=foreign")
	env, _, err := prepareAPISession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(env, "\n"), "foreign") {
		t.Fatal("ambient native credentials survived")
	}
	assertOrdinaryGhTool(t, env, directory)
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
