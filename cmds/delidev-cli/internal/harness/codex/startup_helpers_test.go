// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// Managed Thread fixture modes that create native-style helper aliases during
// initialization. They differ only in where startup stops.
const (
	helperFixturePrefix     = "thread-managed-helpers-"
	helperFixtureReady      = helperFixturePrefix + "ready"
	helperFixturePreCapture = helperFixturePrefix + "precapture"
	helperFixtureFailure    = helperFixturePrefix + "failure"
	helperFixtureTamper     = helperFixtureFailure + "-tamper"
	helperFixtureHang       = helperFixtureFailure + "-hang"
	helperFixtureDirectory  = "native-helpers"
)

func helperFixtureDirectoryPath() string {
	return filepath.Join(os.Getenv("CODEX_HOME"), "tmp", "arg0", helperFixtureDirectory)
}

// createHelperFixtureAliases runs in the fixture native process and mimics the
// temporary aliases Codex creates after initialization.
func createHelperFixtureAliases() bool {
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	if executable, err = filepath.EvalSymlinks(executable); err != nil {
		return false
	}
	directory := helperFixtureDirectoryPath()
	if os.MkdirAll(directory, 0700) != nil {
		return false
	}
	for _, name := range executionHelperNames {
		if os.Symlink(executable, filepath.Join(directory, name)) != nil {
			return false
		}
	}
	return true
}

// tamperHelperFixtureAlias replaces one original alias with a foreign target
// so retirement cannot confirm the captured identity.
func tamperHelperFixtureAlias() {
	alias := filepath.Join(helperFixtureDirectoryPath(), executionHelperNames[0])
	if os.Remove(alias) != nil || os.Symlink(filepath.Join(os.Getenv("CODEX_HOME"), "foreign-target"), alias) != nil {
		os.Exit(45)
	}
}

func helperStartupConfig(t *testing.T, mode string) (Config, *bytes.Buffer) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix helper aliases are not created on Windows")
	}
	config := fixtureConfig(t, mode)
	config.Mode, config.ManagedAuthentication = ThreadProtocol, true
	var logs bytes.Buffer
	config.Process.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	return config, &logs
}

func openHelperStartup(t *testing.T, config Config) (*Client, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return Open(ctx, config)
}

// requireHelperAliases asserts that every original alias still exists (present)
// or is absent, never following the link target.
func requireHelperAliases(t *testing.T, config Config, present bool) {
	t.Helper()
	directory := filepath.Join(config.Home, "tmp", "arg0", helperFixtureDirectory)
	if _, err := os.Lstat(directory); err != nil {
		t.Fatal("helper directory must remain; only aliases are retired", err)
	}
	for _, name := range executionHelperNames {
		_, err := os.Lstat(filepath.Join(directory, name))
		if present && err != nil {
			t.Fatal("alias was removed without retirement authority", name, err)
		}
		if !present && !errors.Is(err, os.ErrNotExist) {
			t.Fatal("alias survived retirement", name, err)
		}
	}
}

func retirementLogs(logs *bytes.Buffer) int {
	return strings.Count(logs.String(), `"stage":"retirement"`)
}

func TestOpenSuccessRetiresHelpersOnceOnClose(t *testing.T) {
	config, logs := helperStartupConfig(t, helperFixtureReady)
	client, err := openHelperStartup(t, config)
	if err != nil {
		t.Fatal(err)
	}
	if retirementLogs(logs) != 0 {
		t.Fatal("successful startup retired helpers before Close")
	}
	requireHelperAliases(t, config, true)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	requireHelperAliases(t, config, false)
	if retirementLogs(logs) != 1 {
		t.Fatal("ordinary Close must retire helpers exactly once")
	}
}

func TestOpenPostCaptureFailureRetiresCapturedHelpersAfterJoin(t *testing.T) {
	config, logs := helperStartupConfig(t, helperFixtureFailure)
	client, err := openHelperStartup(t, config)
	if client != nil {
		_ = client.Close()
		t.Fatal("failed profile validation returned a client")
	}
	if code := domain.SafeError(err).Code; code != domain.Unsupported {
		t.Fatalf("original startup failure was replaced: %v", err)
	}
	requireHelperAliases(t, config, false)
	if retirementLogs(logs) != 1 || !strings.Contains(logs.String(), `"reason":"verified"`) {
		t.Fatal("failed startup must retire the captured proof exactly once")
	}
	if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
		t.Fatal("failed startup left the original native owner unjoined", err)
	}
}

func TestOpenPostCaptureFailureKeepsRetirementFailureRecoveryRequired(t *testing.T) {
	config, logs := helperStartupConfig(t, helperFixtureTamper)
	_, err := openHelperStartup(t, config)
	if domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("retirement failure did not retain recovery ownership: %v", err)
	}
	if diagnostic := domain.CodexErrorDiagnostic(err); diagnostic == nil || diagnostic.Code != domain.Unsupported {
		t.Fatal("original startup failure was not preserved independently", err)
	}
	if retirementLogs(logs) != 1 || !strings.Contains(logs.String(), `"reason":"alias-changed"`) {
		t.Fatal("changed alias did not produce one retirement failure")
	}
	directory := filepath.Join(config.Home, "tmp", "arg0", helperFixtureDirectory)
	// Retirement validates every original before its first unlink, so the
	// untouched aliases and the replaced entry all remain.
	for _, name := range executionHelperNames {
		if _, err := os.Lstat(filepath.Join(directory, name)); err != nil {
			t.Fatal("retirement removed an entry despite an unconfirmed proof", name, err)
		}
	}
	if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
		t.Fatal(err)
	}
}

func TestOpenPostCaptureFailureRetiresAfterForcedJoin(t *testing.T) {
	// The fixture ignores stdin EOF, so graceful close expires and forces the
	// original process. A confirmed forced join still permits captured retirement.
	config, logs := helperStartupConfig(t, helperFixtureHang)
	_, err := openHelperStartup(t, config)
	if domain.SafeError(err).Code != domain.Unsupported {
		t.Fatalf("original startup failure was replaced: %v", err)
	}
	if retirementLogs(logs) != 1 {
		t.Fatal("forced joined cleanup did not retire the captured proof once")
	}
	requireHelperAliases(t, config, false)
}

func TestOpenPreCaptureFailureHoldsNoRetirementAuthority(t *testing.T) {
	config, logs := helperStartupConfig(t, helperFixturePreCapture)
	_, err := openHelperStartup(t, config)
	if domain.SafeError(err).Code != domain.Unsupported {
		t.Fatalf("unexpected pre-capture failure: %v", err)
	}
	if retirementLogs(logs) != 0 {
		t.Fatal("pre-capture failure invoked retirement")
	}
	requireHelperAliases(t, config, true)
	if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
		t.Fatal(err)
	}
}
