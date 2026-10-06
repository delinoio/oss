// SPDX-License-Identifier: Apache-2.0
package harness

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// Explicit opt-in uses empty temporary homes, no account login or inference.
// A real incompatible response is evidence of an attempted profile, not success.
func TestInstalledCodexInitializationDiagnostics(t *testing.T) {
	executable := os.Getenv("DELIDEV_NATIVE_INITIALIZE_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit installed Codex initialization opt-in required")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("absolute native executable required")
	}
	t.Setenv("PATH", filepath.Dir(executable)+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	root, owner := t.TempDir(), domain.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	installation, err := DiscoverCodex(ctx, DiscoveryConfig{Root: root, OwnerID: owner})
	if err != nil {
		t.Fatal(domain.SafeError(err).Code)
	}
	if installation.State != domain.InstallationDetected || !domain.CodexVersionAllowed(installation.Version) {
		t.Fatal("installed version was not admitted")
	}
	t.Logf("installed discovery version=%s minimum=%s protocol=%s", installation.Version, domain.CodexMinimumVersion, installation.Protocol.State)
	if !installation.ProtocolVerified {
		d := installation.Protocol.Diagnostic
		if d == nil || d.Validate() != nil || d.DetectedVersion != installation.Version || d.Phase == domain.CodexVersion {
			t.Fatal("actual attempted failure metadata missing")
		}
		t.Logf("installed initialization failed phase=%s code=%s", d.Phase, d.Code)
		return
	}
	home := filepath.Join(root, "managed")
	env, err := PrivateRuntimeEnvironment(home)
	if err != nil {
		t.Fatal(domain.SafeError(err).Code)
	}
	client, err := codex.Open(ctx, codex.Config{Version: installation.Version, Mode: codex.SubscriptionProtocol, ManagedAuthentication: true, Home: filepath.Join(home, "codex"), Process: process.Config{Directory: filepath.Join(root, "processes"), OwnerID: domain.NewID(), Executable: installation.ResolvedPath, Cwd: home, Env: env}})
	if err != nil {
		d := domain.CodexErrorDiagnostic(err)
		if d == nil || d.Validate() != nil || d.DetectedVersion != installation.Version || d.Phase == domain.CodexVersion || domain.SafeError(err).Code == domain.RecoveryRequired {
			t.Fatal("isolated native failure or cleanup was not verified")
		}
		t.Logf("installed managed initialization failed phase=%s code=%s", d.Phase, d.Code)
		return
	}
	if client.Version() != installation.Version || client.Close() != nil {
		t.Fatal("installed initialization attribution/cleanup failed")
	}
	t.Logf("installed managed initialization succeeded version=%s; no account login or inference", installation.Version)
}
