// SPDX-License-Identifier: Apache-2.0
package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestNativeExecutableIdentityRejectsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(path, []byte("original fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	original, err := InspectExecutable(context.Background(), path)
	expected := sha256.Sum256([]byte("original fixture"))
	if err != nil || original != hex.EncodeToString(expected[:]) {
		t.Fatal("executable bytes were not pinned")
	}
	if err := os.WriteFile(path, []byte("replacement fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	current, err := InspectExecutable(context.Background(), path)
	if err != nil || current == original {
		t.Fatal("replacement reused the original identity")
	}
	link := filepath.Join(filepath.Dir(path), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Skip("symlink creation unavailable")
	}
	if _, err := InspectExecutable(context.Background(), link); err == nil {
		t.Fatal("mutable executable symlink accepted")
	}
}

func TestManualNativeModelsPrivateCopyAndCleanup(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_MODEL_EXECUTABLE")
	if binary == "" {
		t.Skip("opt-in Codex 0.151.0 owned observation and cleanup")
	}
	digest, err := InspectExecutable(context.Background(), binary)
	if err != nil {
		t.Fatal(err)
	}
	// Poison ambient authentication with an inert fixture; the native runtime
	// must receive only its reconstructed private environment.
	t.Setenv("OPENAI_API_KEY", "sk-unused-test-fixture")
	root := t.TempDir()
	scope := domain.NativeModelScope{Version: 1, MachineID: domain.NewID(), MachineRevision: 1, InstallationGeneration: 1, NativeVersion: domain.CodexProtocolVersion, Executable: binary, ExecutableSHA256: digest, AccountID: domain.NewID(), AccountRevision: 1, ConnectionID: domain.NewID(), ProviderID: domain.NewID(), Actor: domain.Principal{Type: domain.OwnerDevice}}
	result, err := DiscoverNativeModels(context.Background(), DiscoveryConfig{Root: root, OwnerID: domain.NewID(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, scope)
	if err != nil || result.Validate(false) != nil || len(result.Models) == 0 {
		t.Fatalf("owned native observation failed: count=%d error=%v", len(result.Models), err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "probes"))
	if err != nil || len(entries) != 0 {
		t.Fatal("private observation runtime survived successful cleanup")
	}
	if !result.CleanupVerified {
		t.Fatal("cleanup was not independently joined")
	}
	scope.ExecutableSHA256 = strings.Repeat("0", 64)
	if _, err := DiscoverNativeModels(context.Background(), DiscoveryConfig{Root: root, OwnerID: domain.NewID()}, scope); err == nil {
		t.Fatal("changed executable identity launched another observation")
	}
	t.Logf("owned private copy observed %d entries and removed its runtime", len(result.Models))
}
