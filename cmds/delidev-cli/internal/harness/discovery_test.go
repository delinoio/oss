package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func fixture(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix script fixture; native Windows evidence is separate")
	}
	path := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func allMissing(t *testing.T) domain.HarnessDiscoveryInput {
	t.Helper()
	input := domain.HarnessDiscoveryInput{Revision: 1}
	for _, harness := range domain.Harnesses() {
		input.Selections.Executables = append(input.Selections.Executables, domain.ExecutableSelection{Harness: harness, Path: filepath.Join(t.TempDir(), "absent")})
	}
	return input
}
func discoveryRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "worker")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDiscoveryUsesIsolatedEnvironmentAndOwnedProcesses(t *testing.T) {
	for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "XAI_API_KEY", "SSH_AUTH_SOCK", "HTTP_PROXY", "NODE_OPTIONS", "OPENCODE_CONFIG", "CLAUDE_CODE_OAUTH_TOKEN"} {
		t.Setenv(name, "private-test-sentinel")
	}
	input := allMissing(t)
	path := fixture(t, `
test "$#" = 1
test "$1" = --version
test -z "${OPENAI_API_KEY-}${ANTHROPIC_API_KEY-}${XAI_API_KEY-}${SSH_AUTH_SOCK-}${HTTP_PROXY-}${NODE_OPTIONS-}${OPENCODE_CONFIG-}${CLAUDE_CODE_OAUTH_TOKEN-}"
test "$PWD" = "$HOME"
test "$CLAUDE_CONFIG_DIR" = "$HOME/claude"
test "$CODEX_HOME" = "$HOME/codex"
test "$OPENCODE_CONFIG_DIR" = "$HOME/opencode"
test "$DISABLE_AUTOUPDATER" = 1
test "$OPENCODE_DISABLE_AUTOUPDATE" = true
test -d "$TMPDIR"
printf 'untrusted-stderr-sentinel' >&2
printf 'codex-cli 0.151.0\n'
`)
	input.Selections.Executables[0].Path = path
	root, owner := discoveryRoot(t), domain.NewID()
	result, err := Discover(context.Background(), DiscoveryConfig{Root: root, OwnerID: owner}, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := result.ValidateDiscovery(input); err != nil {
		t.Fatal(err)
	}
	detected := result.Installations[0]
	if detected.State != domain.InstallationDetected || detected.Version != "0.151.0" || detected.ProtocolVerified || len(detected.Capabilities) != 0 {
		t.Fatalf("unverified discovery: %+v", detected)
	}
	if err := process.ReconcileOwner(filepath.Join(root, "processes"), owner); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "probes"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("retained completed probe: %v %v", entries, err)
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), "private-test-sentinel") || strings.Contains(string(raw), "untrusted-stderr-sentinel") {
			t.Fatal("probe content entered persistent ownership metadata")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExplicitPathFailuresNeverFallBack(t *testing.T) {
	path := fixture(t, "printf 'codex-cli 9.9.9\\n'\n")
	bin := t.TempDir()
	if err := os.Symlink(path, filepath.Join(bin, "codex")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	denied := filepath.Join(t.TempDir(), "denied")
	if err := os.WriteFile(denied, []byte("#!/bin/sh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path  string
		state domain.InstallationState
	}{
		{filepath.Join(t.TempDir(), "missing"), domain.InstallationMissing},
		{denied, domain.InstallationDenied},
		{t.TempDir(), domain.InstallationIncompatible},
		{"./codex", domain.InstallationIncompatible},
	}
	for _, test := range cases {
		input := allMissing(t)
		input.Selections.Executables[0].Path = test.path
		result, err := Discover(context.Background(), DiscoveryConfig{Root: discoveryRoot(t), OwnerID: domain.NewID()}, input)
		if err != nil {
			t.Fatal(err)
		}
		if result.Installations[0].State != test.state || result.Installations[0].Version != "" {
			t.Fatalf("invalid explicit path fell back: %+v", result.Installations[0])
		}
	}
	input := allMissing(t)
	input.Selections.Executables[0].Path = ""
	result, err := Discover(context.Background(), DiscoveryConfig{Root: discoveryRoot(t), OwnerID: domain.NewID()}, input)
	if err != nil || result.Installations[0].Version != "9.9.9" {
		t.Fatalf("unset path was not searched: %+v %v", result, err)
	}
}

func TestDiscoveryBoundsAndGrokUpdateSuppression(t *testing.T) {
	input := allMissing(t)
	input.Selections.Executables[0].Path = fixture(t, "printf 'secret-text 1.2.3\\n'\n")
	input.Selections.Executables[1].Path = fixture(t, "while :; do printf 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx'; done\n")
	input.Selections.Executables[3].Path = fixture(t, "test \"$1\" = --no-auto-update\ntest \"$2\" = --version\nprintf 'grok 1.2.3\\n'\n")
	root, owner := discoveryRoot(t), domain.NewID()
	result, err := Discover(context.Background(), DiscoveryConfig{Root: root, OwnerID: owner}, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Installations[0].State != domain.InstallationIncompatible || result.Installations[1].State != domain.InstallationFailed || result.Installations[3].Version != "1.2.3" {
		t.Fatalf("bad classifications: %+v", result)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "secret-text") {
		t.Fatal("raw output exposed")
	}
	if err := process.ReconcileOwner(filepath.Join(root, "processes"), owner); err != nil {
		t.Fatal(err)
	}
}

func TestCanceledDiscoveryReapsBeforeRemovingRuntime(t *testing.T) {
	input := allMissing(t)
	input.Selections.Executables[0].Path = fixture(t, "while :; do sleep 1; done\n")
	root, owner := discoveryRoot(t), domain.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Discover(ctx, DiscoveryConfig{Root: root, OwnerID: owner}, input)
	if err == nil {
		t.Fatal("canceled probe succeeded")
	}
	if err := process.ReconcileOwner(filepath.Join(root, "processes"), owner); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "probes"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled probe runtime: %v %v", entries, err)
	}
}
