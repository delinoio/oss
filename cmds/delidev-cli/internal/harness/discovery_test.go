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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
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
	result, err := Discover(context.Background(), DiscoveryConfig{Root: root, Profiles: testProfiles(t, root), OwnerID: owner}, input)
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
	// The accountless probe profile is persistent and is reused, not recreated.
	profile, ok, err := testProfiles(t, root).Lookup(context.Background(), nativeprofile.Key{Harness: domain.Codex, Purpose: nativeprofile.PurposeDiscovery})
	if err != nil || !ok {
		t.Fatalf("probe profile was not retained: %v", err)
	}
	if _, err := os.Stat(profile.Root); err != nil {
		t.Fatal("persistent probe profile was removed")
	}
	if _, err := os.Stat(filepath.Join(root, "probes")); err == nil {
		t.Fatal("a disposable probe directory was allocated")
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
		discoveryDir := discoveryRoot(t)
		result, err := Discover(context.Background(), DiscoveryConfig{Root: discoveryDir, Profiles: testProfiles(t, discoveryDir), OwnerID: domain.NewID()}, input)
		if err != nil {
			t.Fatal(err)
		}
		if result.Installations[0].State != test.state || result.Installations[0].Version != "" {
			t.Fatalf("invalid explicit path fell back: %+v", result.Installations[0])
		}
	}
	input := allMissing(t)
	input.Selections.Executables[0].Path = ""
	discoveryDir := discoveryRoot(t)
	result, err := Discover(context.Background(), DiscoveryConfig{Root: discoveryDir, Profiles: testProfiles(t, discoveryDir), OwnerID: domain.NewID()}, input)
	if err != nil || result.Installations[0].Version != "9.9.9" {
		t.Fatalf("unset path was not searched: %+v %v", result, err)
	}
}

// TestOpenCodeDiscoveryCausesDistinguishVersionFromIncompatibleInstallation
// enumerates every discovery path that leaves an OpenCode installation
// unusable and pins the record shape the desktop classifies. Only an
// under-baseline detected version (installation detected with a version and an
// unsupported protocol) is fixed by updating OpenCode; every `incompatible`
// installation state carries no version, so it can never be presented as an
// outdated OpenCode. The desktop mirror is
// apps/delidev/src/runner-observation.test.ts.
func TestOpenCodeDiscoveryCausesDistinguishVersionFromIncompatibleInstallation(t *testing.T) {
	notExecutable := filepath.Join(t.TempDir(), "opencode")
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(notExecutable, []byte("not a program\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	type want struct {
		state    domain.InstallationState
		version  string
		protocol domain.ProtocolState
	}
	for _, test := range []struct {
		name string
		path func(*testing.T) string
		want want
	}{
		{"relative explicit path", func(*testing.T) string { return "./opencode" }, want{state: domain.InstallationIncompatible}},
		{"non-regular executable", func(t *testing.T) string { return t.TempDir() }, want{state: domain.InstallationIncompatible}},
		{"unparseable version output", func(t *testing.T) string { return fixture(t, "printf 'not a version\\n'\n") }, want{state: domain.InstallationIncompatible}},
		{"unrunnable executable", func(t *testing.T) string {
			if runtime.GOOS == "windows" {
				t.Skip("Unix exec-format fixture")
			}
			return notExecutable
		}, want{state: domain.InstallationIncompatible}},
		{"windows shell wrapper", func(t *testing.T) string {
			if runtime.GOOS != "windows" {
				t.Skip("Only Windows rejects shell wrappers")
			}
			path := filepath.Join(t.TempDir(), "opencode.cmd")
			if err := os.WriteFile(path, []byte("@echo off\r\n"), 0700); err != nil {
				t.Fatal(err)
			}
			return path
		}, want{state: domain.InstallationIncompatible}},
		{"below-baseline version", func(t *testing.T) string { return fixture(t, "printf '1.18.20\\n'\n") }, want{state: domain.InstallationDetected, version: "1.18.20", protocol: domain.ProtocolUnsupported}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := allMissing(t)
			input.VerifyProtocol = true
			input.Selections.Executables[2].Path = test.path(t)
			root := discoveryRoot(t)
			result, err := Discover(context.Background(), DiscoveryConfig{Root: root, Profiles: testProfiles(t, root), OwnerID: domain.NewID()}, input)
			if err != nil {
				t.Fatal(err)
			}
			got := result.Installations[2]
			if got.Harness != domain.OpenCode || got.State != test.want.state || got.Version != test.want.version {
				t.Fatalf("unexpected installation: %+v", got)
			}
			if test.want.protocol == "" {
				if got.Protocol != nil {
					t.Fatalf("protocol observed for an installation that was not detected: %+v", got.Protocol)
				}
				return
			}
			if got.Protocol == nil || got.Protocol.State != test.want.protocol || got.ProtocolVerified {
				t.Fatalf("unexpected protocol: %+v", got.Protocol)
			}
		})
	}
}

func TestDiscoveryBoundsAndGrokUpdateSuppression(t *testing.T) {
	input := allMissing(t)
	input.Selections.Executables[0].Path = fixture(t, "printf 'secret-text 1.2.3\\n'\n")
	input.Selections.Executables[1].Path = fixture(t, "while :; do printf 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx'; done\n")
	input.Selections.Executables[3].Path = fixture(t, "test \"$1\" = --no-auto-update\ntest \"$2\" = --version\nprintf 'grok 1.2.3\\n'\n")
	root, owner := discoveryRoot(t), domain.NewID()
	result, err := Discover(context.Background(), DiscoveryConfig{Root: root, Profiles: testProfiles(t, root), OwnerID: owner}, input)
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

func TestCanceledDiscoveryReapsOwnedProcessesAndKeepsProfile(t *testing.T) {
	input := allMissing(t)
	input.Selections.Executables[0].Path = fixture(t, "while :; do sleep 1; done\n")
	root, owner := discoveryRoot(t), domain.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Discover(ctx, DiscoveryConfig{Root: root, Profiles: testProfiles(t, root), OwnerID: owner}, input)
	if err == nil {
		t.Fatal("canceled probe succeeded")
	}
	if err := process.ReconcileOwner(filepath.Join(root, "processes"), owner); err != nil {
		t.Fatal(err)
	}
	// A canceled probe retains its persistent profile; only owned processes are joined.
	if _, err := os.Stat(filepath.Join(root, "probes")); err == nil {
		t.Fatal("a disposable probe directory was allocated")
	}
}

func TestDiscoveryReusesOnePersistentProfileAcrossRequestsAndRestart(t *testing.T) {
	input := allMissing(t)
	// The native leaves a cache entry and a helper link behind, as real natives do.
	input.Selections.Executables[0].Path = fixture(t, `
mkdir -p "$HOME/cache"
printf retained > "$HOME/cache/native-cache"
ln -sf "$0" "$HOME/cache/helper-link"
printf 'codex-cli 0.151.0\n'
`)
	root := discoveryRoot(t)
	var firstRoot string
	for attempt := 0; attempt < 3; attempt++ {
		registry := testProfiles(t, root)
		if attempt == 2 {
			// An owner restart opens a new registry over the same storage root.
			reopened, err := nativeprofile.Open(nativeprofile.Config{Root: root, Owner: nativeprofile.OwnerWorker, Machine: registryMachine(t, registry)})
			if err != nil {
				t.Fatal(err)
			}
			registry = reopened
		}
		owner := domain.NewID()
		result, err := Discover(context.Background(), DiscoveryConfig{Root: root, Profiles: registry, OwnerID: owner}, input)
		if err != nil || result.Installations[0].State != domain.InstallationDetected {
			t.Fatalf("attempt %d: %+v %v", attempt, result, err)
		}
		info, ok, err := registry.Lookup(context.Background(), nativeprofile.Key{Harness: domain.Codex, Purpose: nativeprofile.PurposeDiscovery})
		if err != nil || !ok {
			t.Fatal("profile missing", err)
		}
		if firstRoot == "" {
			firstRoot = info.Root
		} else if info.Root != firstRoot {
			t.Fatalf("a different request or restart allocated a new native home: %s", info.Root)
		}
		if raw, err := os.ReadFile(filepath.Join(info.Root, "cache", "native-cache")); err != nil || string(raw) != "retained" {
			t.Fatal("native cache was not retained", err)
		}
		if _, err := os.Lstat(filepath.Join(info.Root, "cache", "helper-link")); err != nil {
			t.Fatal("helper link was removed")
		}
		// Each request keeps its own fresh process journal.
		if _, err := os.Stat(filepath.Join(root, "processes", string(owner))); err != nil {
			t.Fatal("operation journal is missing", err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "native-profiles", "p"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one profile directory: %v %v", entries, err)
	}
}

func TestDiscoveryRequiresPersistentProfileRegistry(t *testing.T) {
	root := discoveryRoot(t)
	if _, err := Discover(context.Background(), DiscoveryConfig{Root: root, OwnerID: domain.NewID()}, allMissing(t)); err == nil {
		t.Fatal("discovery fell back to a temporary home")
	}
	if _, err := os.Stat(filepath.Join(root, "probes")); err == nil {
		t.Fatal("a disposable probe directory was allocated")
	}
}
