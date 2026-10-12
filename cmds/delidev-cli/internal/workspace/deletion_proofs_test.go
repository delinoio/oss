// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func deletionProofWork() domain.SessionDeletionWork {
	return domain.SessionDeletionWork{Version: 1, ServerID: domain.NewID(), SessionID: domain.NewID(), DeletionID: domain.NewID(), MachineID: domain.NewID(), DeviceID: domain.NewID(), Copies: []domain.SessionDeletionCopy{{JobID: domain.NewID(), Type: domain.PrepareWorkspaceJob, Revision: 1, InstanceID: domain.NewID(), Digest: strings.Repeat("a", 64)}}}
}

func TestStorageOnlyDeletionCannotUseSnapshotDigestToRemoveWorkspace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "worker")
	manager := Manager{Root: root}
	work := deletionProofWork()
	input := PrepareRequest{SessionID: work.SessionID, MachineID: work.MachineID, OriginMachineID: work.MachineID, Type: domain.GeneralChat, Repositories: []RepositorySpec{}}
	manifest, err := manager.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	work.Copies = []domain.SessionDeletionCopy{{JobID: domain.NewID(), SnapshotID: domain.NewID(), Type: domain.WorkspaceStorageJob, Revision: 1, InstanceID: domain.NewID(), Digest: strings.Repeat("b", 64)}}
	work.PreparationDigests = []string{manifest.InputDigest}
	work.SnapshotPreparationDigests = []string{manifest.InputDigest}
	work.SharedCleanupOwnerDeviceID = work.DeviceID
	called := false
	err = manager.DeleteOwnedWorkspace(context.Background(), work, true, func() error { called = true; return nil })
	if domain.SafeError(err).Code != domain.RecoveryRequired || called {
		t.Fatal("storage-only proof removed a workspace owned by another obligation", err)
	}
	if current, err := manager.Read(work.SessionID); err != nil || manifestDigest(current) != manifestDigest(manifest) {
		t.Fatal("storage-only deletion changed the original workspace", err)
	}
}

func TestDeletionWorkspaceProofLegacyImportsOnlyExactEqualPairs(t *testing.T) {
	for _, stage := range []string{"original", "removed", "equal-pair", "changed-pair", "foreign-work"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			work := deletionProofWork()
			if err := security.PrivateDir(filepath.Join(root, "session-deletions")); err != nil {
				t.Fatal(err)
			}
			legacy := deletionWorkspaceProof{Version: 1, Digest: work.Digest(), Removed: stage == "removed"}
			expected := legacy
			expected.Version = 2
			expected.Identity = work.Identity()
			paths, _ := SessionDeletionProofPaths(root, work)
			if stage == "foreign-work" {
				legacy.Digest = deletionProofWork().Digest()
			}
			if err := writeDeletionWorkspaceProof(legacyDeletionWorkspacePath(root, work), legacy); err != nil {
				t.Fatal(err)
			}
			if stage == "equal-pair" || stage == "changed-pair" {
				pair := expected
				if stage == "changed-pair" {
					pair.Removed = true
				}
				if err := writeDeletionWorkspaceProof(paths.Workspace, pair); err != nil {
					t.Fatal(err)
				}
			}
			err := ImportLegacyDeletionWorkspaceProof(root, work, false)
			accepted := stage == "original" || stage == "removed" || stage == "equal-pair"
			if (err == nil) != accepted {
				t.Fatalf("workspace legacy %s: %v", stage, err)
			}
			if accepted {
				raw, err := security.ReadPrivate(paths.Workspace, 1<<20)
				var actual deletionWorkspaceProof
				if err != nil || domain.Decode(raw, &actual) != nil || actual.Identity != expected.Identity || actual.Digest != expected.Digest || actual.Removed != expected.Removed {
					t.Fatal("original workspace stage changed", err)
				}
				if _, err := os.Lstat(legacyDeletionWorkspacePath(root, work)); !os.IsNotExist(err) {
					t.Fatal("legacy proof not retired", err)
				}
			} else if _, err := os.Lstat(legacyDeletionWorkspacePath(root, work)); err != nil {
				t.Fatal("foreign legacy proof changed", err)
			}
		})
	}
}

func TestDeletionLegacyAbsentWorkspaceCannotAdoptRestoredContent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "worker")
	work := deletionProofWork()
	manager := Manager{Root: root}
	if err := manager.initialize(); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(manager.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	legacy := deletionWorkspaceProof{Version: 1, Digest: work.Digest()}
	if err := writeDeletionWorkspaceProof(legacyDeletionWorkspacePath(manager.Root, work), legacy); err != nil {
		t.Fatal(err)
	}
	if err := ImportLegacyDeletionWorkspaceProof(manager.Root, work, false); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(manager.Root, "workspaces", string(work.SessionID))
	if err := os.Mkdir(restored, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(restored, "foreign")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	invoked := false
	err := manager.DeleteOwnedWorkspace(context.Background(), work, true, func() error { invoked = true; return nil })
	if err == nil || invoked {
		t.Fatal("absence proof adopted restored workspace", err)
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "preserve" {
		t.Fatal("restored content was removed", err)
	}
}

func TestDeletionLegacyAbsentWorkspaceRejectsOrphanedKeyedProof(t *testing.T) {
	root := t.TempDir()
	work := deletionProofWork()
	if err := security.PrivateDir(filepath.Join(root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	paths, err := SessionDeletionProofPaths(root, work)
	if err != nil {
		t.Fatal(err)
	}
	orphan := deletionWorkspaceProof{Version: 2, Identity: work.Identity(), Digest: work.Digest()}
	if err := writeDeletionWorkspaceProof(paths.Workspace, orphan); err != nil {
		t.Fatal(err)
	}
	before, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := ImportLegacyDeletionWorkspaceProof(root, work, false); err == nil {
		t.Fatal("fresh admission adopted an orphaned keyed workspace proof")
	}
	after, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err != nil || string(after) != string(before) {
		t.Fatal("orphaned keyed workspace proof changed", err)
	}
	if _, err := os.Lstat(legacyDeletionWorkspacePath(root, work)); !os.IsNotExist(err) {
		t.Fatal("fresh admission created legacy workspace evidence", err)
	}
}

func TestFreshDeletionWorkspaceProofValidationIsReadOnly(t *testing.T) {
	root := t.TempDir()
	work := deletionProofWork()
	if err := security.PrivateDir(filepath.Join(root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	legacy := deletionWorkspaceProof{Version: 1, Digest: work.Digest(), Removed: true}
	legacyPath := legacyDeletionWorkspacePath(root, work)
	if err := writeDeletionWorkspaceProof(legacyPath, legacy); err != nil {
		t.Fatal(err)
	}
	before, err := security.ReadPrivate(legacyPath, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	hasLegacy, err := ValidateFreshDeletionWorkspaceProof(root, work)
	if err != nil || !hasLegacy {
		t.Fatal("valid legacy proof was not admitted for fenced import", err)
	}
	after, err := security.ReadPrivate(legacyPath, 1<<20)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatal("fresh proof validation changed legacy evidence", err)
	}
}

func TestInitialDeletionWorkspaceRecoveryRequiresExactKeyedProof(t *testing.T) {
	root := t.TempDir()
	work := deletionProofWork()
	if err := security.PrivateDir(filepath.Join(root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	paths, err := SessionDeletionProofPaths(root, work)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDeletionWorkspaceProof(paths.Workspace, deletionWorkspaceProof{Version: 2, Identity: work.Identity(), Digest: work.Digest(), Removed: true}); err != nil {
		t.Fatal(err)
	}
	if err := RecoverInitialDeletionWorkspaceProof(root, work, false); err != nil {
		t.Fatalf("exact keyed proof did not recover an admitted import: %v", err)
	}
	if err := os.Remove(paths.Workspace); err != nil {
		t.Fatal(err)
	}
	if err := RecoverInitialDeletionWorkspaceProof(root, work, false); err == nil {
		t.Fatal("initial import recovered after all original evidence disappeared")
	}
}

func TestDeletionLegacyWorkspaceProofCannotImportForeignManifestScope(t *testing.T) {
	root := t.TempDir()
	work := deletionProofWork()
	if err := security.PrivateDir(filepath.Join(root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	legacy := deletionWorkspaceProof{Version: 1, Digest: work.Digest(), Manifest: &Manifest{SessionID: domain.NewID(), MachineID: work.MachineID, Type: domain.GeneralChat, InputDigest: strings.Repeat("a", 64)}}
	path := legacyDeletionWorkspacePath(root, work)
	if err := writeDeletionWorkspaceProof(path, legacy); err != nil {
		t.Fatal(err)
	}
	raw, err := security.ReadPrivate(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if ImportLegacyDeletionWorkspaceProof(root, work, false) == nil {
		t.Fatal("foreign original manifest acquired keyed authority")
	}
	after, err := security.ReadPrivate(path, 1<<20)
	if err != nil || string(after) != string(raw) {
		t.Fatal("foreign legacy proof changed", err)
	}
}

func TestDeletionWorkspaceProofStartedLegacyRequiresHistoricalOrKeyedEvidence(t *testing.T) {
	root := t.TempDir()
	work := deletionProofWork()
	if err := security.PrivateDir(filepath.Join(root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	if err := ImportLegacyDeletionWorkspaceProof(root, work, true); err == nil {
		t.Fatal("started legacy cleanup accepted missing workspace evidence")
	}

	paths, err := SessionDeletionProofPaths(root, work)
	if err != nil {
		t.Fatal(err)
	}
	keyed := deletionWorkspaceProof{Version: 2, Identity: work.Identity(), Digest: work.Digest(), Removed: true}
	if err := writeDeletionWorkspaceProof(paths.Workspace, keyed); err != nil {
		t.Fatal(err)
	}
	if err := ImportLegacyDeletionWorkspaceProof(root, work, true); err != nil {
		t.Fatalf("exact keyed evidence did not recover the interrupted import: %v", err)
	}

	if err := os.Remove(paths.Workspace); err != nil {
		t.Fatal(err)
	}
	foreign := keyed
	foreign.Identity.DeviceID = domain.NewID()
	if err := writeDeletionWorkspaceProof(paths.Workspace, foreign); err != nil {
		t.Fatal(err)
	}
	if err := ImportLegacyDeletionWorkspaceProof(root, work, true); err == nil {
		t.Fatal("foreign keyed evidence satisfied started legacy cleanup")
	}
}

func TestDeletionWorkspaceProofStartedRequiresRemovedStage(t *testing.T) {
	root := t.TempDir()
	work := deletionProofWork()
	if err := security.PrivateDir(filepath.Join(root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	paths, err := SessionDeletionProofPaths(root, work)
	if err != nil {
		t.Fatal(err)
	}
	stale := deletionWorkspaceProof{Version: 2, Identity: work.Identity(), Digest: work.Digest(), Removed: false}
	if err := writeDeletionWorkspaceProof(paths.Workspace, stale); err != nil {
		t.Fatal(err)
	}
	if err := RequireKeyedDeletionWorkspaceProof(root, work); err == nil {
		t.Fatal("started cleanup accepted workspace proof from before removal")
	}
	if err := os.Remove(paths.Workspace); err != nil {
		t.Fatal(err)
	}
	legacy := stale
	legacy.Version = 1
	legacy.Identity = domain.SessionDeletionIdentity{}
	legacyPath := legacyDeletionWorkspacePath(root, work)
	if err := writeDeletionWorkspaceProof(legacyPath, legacy); err != nil {
		t.Fatal(err)
	}
	if err := ImportLegacyDeletionWorkspaceProof(root, work, true); err == nil {
		t.Fatal("started legacy cleanup imported workspace proof from before removal")
	}
	if _, err := os.Lstat(legacyPath); err != nil {
		t.Fatal("stale historical workspace proof was retired", err)
	}
	if _, err := os.Lstat(paths.Workspace); !os.IsNotExist(err) {
		t.Fatal("stale historical proof was copied to the keyed path", err)
	}
}

func TestPermanentDeletionCurrentFenceBlocksWorkspaceAdmission(t *testing.T) {
	for _, operation := range []string{"preparation", "execution", "read", "sidechat"} {
		t.Run(operation, func(t *testing.T) {
			m, input, manifest := chatExecutionFixture(t)
			work := sidechatDeletionWork(manifest)
			marker := struct {
				Version    uint32    `json:"version"`
				ServerID   domain.ID `json:"server_id"`
				SessionID  domain.ID `json:"session_id"`
				DeletionID domain.ID `json:"deletion_id"`
				MachineID  domain.ID `json:"machine_id"`
			}{2, work.ServerID, work.SessionID, work.DeletionID, work.MachineID}
			raw, err := json.Marshal(marker)
			if err != nil {
				t.Fatal(err)
			}
			if err := security.PrivateDir(filepath.Join(m.Root, "session-deletions")); err != nil {
				t.Fatal(err)
			}
			if err := security.WriteAtomic(filepath.Join(m.Root, "session-deletions", string(work.SessionID)+".json"), raw); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			switch operation {
			case "preparation":
				_, err = m.Prepare(ctx, input)
			case "execution":
				var lease *ExecutionLease
				lease, err = m.ClaimFirstExecution(ctx, domain.NewID(), domain.NewID(), input, manifest)
				if lease != nil {
					lease.Close()
				}
			case "read":
				request := workspaceReadFixture(input, manifest, domain.WorkspaceFile, "keep")
				err = m.observeWorkspace(ctx, request, input.PrimaryRepository, true, func(context.Context, Git, Manifest, *os.Root, string) error {
					t.Fatal("current deletion fence admitted a file read")
					return nil
				})
			case "sidechat":
				child := domain.NewID()
				_, _, err = m.PrepareSidechatReference(ctx, domain.NewID(), child, input, manifest)
				if _, statErr := os.Lstat(filepath.Join(m.Root, "workspaces", string(child))); !os.IsNotExist(statErr) {
					t.Fatal("current deletion fence published child metadata", statErr)
				}
			}
			if domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("current deletion fence lost admission ownership", err)
			}
			if _, err := os.Lstat(filepath.Join(m.Root, "execution-claims", string(work.SessionID)+".json")); !os.IsNotExist(err) {
				t.Fatal("current deletion fence admitted an execution claim", err)
			}
			retained, err := m.Read(input.SessionID)
			if err != nil || manifestDigest(retained) != manifestDigest(manifest) {
				t.Fatal("current deletion fence changed retained workspace", err)
			}
		})
	}
}
