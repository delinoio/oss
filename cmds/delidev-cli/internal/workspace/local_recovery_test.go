package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestLocalPreparationRecoveryPreservesReadyCheckout(t *testing.T) {
	for _, unborn := range []bool{false, true} {
		name := "committed"
		if unborn {
			name = "unborn"
		}
		t.Run(name, func(t *testing.T) {
			fixture := localExecutionFixture
			if unborn {
				fixture = unbornLocalFixture
			}
			m, input, manifest := fixture(t)
			request := recoveryInput(input)
			request.Action = CleanupPreparation
			recovered, err := m.Recover(context.Background(), request, false)
			if err != nil || recovered.Outcome != RecoveredReady || recovered.Manifest == nil {
				t.Fatal("ready Local recovery failed", err)
			}
			before, _ := json.Marshal(manifest)
			after, _ := json.Marshal(recovered.Manifest)
			if string(before) != string(after) {
				t.Fatal("recovery rewrote ready manifest")
			}
			if unborn {
				gitTest(t, manifest.PrimaryPath, "commit", "-m", "user first commit after preparation")
			}
			recovered, err = m.Recover(context.Background(), request, false)
			if err != nil || recovered.Outcome != RecoveredReady {
				t.Fatal("user commit invalidated Local ready recovery", err)
			}
			// A redirected or missing Git administration cannot become ready authority.
			if err := os.Rename(filepath.Join(manifest.PrimaryPath, ".git"), filepath.Join(manifest.PrimaryPath, ".git.saved")); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Recover(context.Background(), request, false); err == nil {
				t.Fatal("missing Git identity accepted")
			}
			if _, err := os.Stat(manifest.PrimaryPath); err != nil {
				t.Fatal("ready recovery deleted original tree", err)
			}
		})
	}
}

func TestLocalPartialRecoveryDeletesOnlyMetadataAndRetainsProof(t *testing.T) {
	for _, prefix := range []int{0, 1, 2} {
		t.Run(string(rune('0'+prefix)), func(t *testing.T) {
			m, input, manifest := unbornLocalFixture(t)
			// Extend the accepted input to prove cleanup of every possible recorded prefix.
			_, otherInput, other := localExecutionFixture(t)
			input.Repositories = append(input.Repositories, otherInput.Repositories[0])
			input.PrimaryRepository = input.Repositories[1].ID
			manifest.InputDigest = preparationDigest(input)
			manifest.Repositories = append(manifest.Repositories, other.Repositories[0])
			manifest.PrimaryPath = other.PrimaryPath
			originalPaths := []string{manifest.Repositories[0].Path, manifest.Repositories[1].Path}
			manifest.Repositories = manifest.Repositories[:prefix]
			manifest.State = CleanupPending
			if prefix < 2 {
				manifest.PrimaryPath = ""
			}
			path := filepath.Join(m.Root, "workspaces", string(input.SessionID), "manifest.json")
			raw, _ := json.Marshal(manifest)
			if err := security.WriteAtomic(path, raw); err != nil {
				t.Fatal(err)
			}
			request := recoveryInput(input)
			if _, err := m.Recover(context.Background(), request, false); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("inspect performed implicit cleanup")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("inspect removed metadata", err)
			}
			request.Action = CleanupPreparation
			result, err := m.Recover(context.Background(), request, false)
			if err != nil || result.Outcome != RecoveredClean {
				t.Fatal("explicit Local cleanup failed", err)
			}
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatal("metadata remains", err)
			}
			if gitTest(t, originalPaths[0], "status", "--porcelain") != "A  staged.txt\n?? untracked.txt" {
				t.Fatal("cleanup changed unborn index/files")
			}
			if gitTest(t, originalPaths[1], "branch", "--show-current") != "main" {
				t.Fatal("cleanup changed committed branch")
			}
			proofPath := filepath.Join(m.Root, "workspace-recovery", string(request.JobID)+".json")
			proofRaw, err := security.ReadPrivate(proofPath, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			var proof cleanupProof
			if err := domain.Decode(proofRaw, &proof); err != nil {
				t.Fatal(err)
			}
			proof.Complete = false
			if err := writeCleanupProof(proofPath, proof); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				result, err = m.Recover(context.Background(), request, false)
				if err != nil || result.Outcome != RecoveredClean {
					t.Fatal("interrupted Local cleanup lost proof", err)
				}
			}
			request.JobID = domain.NewID()
			if _, err := m.Recover(context.Background(), request, false); err == nil {
				t.Fatal("another job reused cleanup proof")
			}
		})
	}
}

func TestLocalPartialRecoveryRejectsForeignOrOwnedManifest(t *testing.T) {
	for _, scenario := range []string{"owned", "path", "source", "identity", "origin", "unborn-shape", "repository-order"} {
		t.Run(scenario, func(t *testing.T) {
			m, input, manifest := unbornLocalFixture(t)
			original := manifest.PrimaryPath
			manifest.State = CleanupPending
			switch scenario {
			case "owned":
				manifest.Repositories[0].Owned = true
			case "path":
				manifest.Repositories[0].Path = m.Root
			case "source":
				manifest.Repositories[0].Source = m.Root
			case "identity":
				manifest.Repositories[0].LocalIdentityDigest = ""
			case "origin":
				input.OriginMachineID = domain.NewID()
				manifest.InputDigest = preparationDigest(input)
			case "unborn-shape":
				manifest.Repositories[0].LocalHEAD = LocalHEADCommitted
			case "repository-order":
				manifest.Repositories[0].ID = domain.NewID()
			}
			path := filepath.Join(m.Root, "workspaces", string(input.SessionID), "manifest.json")
			raw, _ := json.Marshal(manifest)
			if err := security.WriteAtomic(path, raw); err != nil {
				t.Fatal(err)
			}
			request := recoveryInput(input)
			request.Action = CleanupPreparation
			if _, err := m.Recover(context.Background(), request, false); err == nil {
				t.Fatal("unproven cleanup accepted")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("unproven cleanup removed manifest", err)
			}
			if raw, err := os.ReadFile(filepath.Join(original, "staged.txt")); err != nil || string(raw) != "staged content" {
				t.Fatal("unproven cleanup changed original", err)
			}
		})
	}
}
