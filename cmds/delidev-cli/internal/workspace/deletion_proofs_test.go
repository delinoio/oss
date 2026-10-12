// SPDX-License-Identifier: Apache-2.0
package workspace

import (
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
			err := ImportLegacyDeletionWorkspaceProof(root, work)
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
	root := t.TempDir()
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
	if err := ImportLegacyDeletionWorkspaceProof(manager.Root, work); err != nil {
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
	if ImportLegacyDeletionWorkspaceProof(root, work) == nil {
		t.Fatal("foreign original manifest acquired keyed authority")
	}
	after, err := security.ReadPrivate(path, 1<<20)
	if err != nil || string(after) != string(raw) {
		t.Fatal("foreign legacy proof changed", err)
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
