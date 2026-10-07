// SPDX-License-Identifier: Apache-2.0
package workspace

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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func sidechatDeletionWork(manifest Manifest) domain.SessionDeletionWork {
	return domain.SessionDeletionWork{Version: 1, DeletionID: domain.NewID(), ServerID: domain.NewID(), SessionID: manifest.SessionID, MachineID: manifest.MachineID, DeviceID: domain.NewID(), Copies: []domain.SessionDeletionCopy{{JobID: domain.NewID(), Type: domain.PrepareWorkspaceJob, Revision: 2, Digest: strings.Repeat("ab", 32), InstanceID: domain.NewID()}}, PreparationDigests: []string{manifest.InputDigest}}
}

func TestSidechatReferenceRetainsParentOwnershipUntilChildDeletion(t *testing.T) {
	for _, kind := range []domain.WorkspaceType{domain.Worktree, domain.Local, domain.GeneralChat} {
		t.Run(string(kind), func(t *testing.T) {
			ctx := context.Background()
			m := manager(t)
			checkout, err := filepath.EvalSymlinks(repository(t))
			if err != nil {
				t.Fatal(err)
			}
			sourceInput, _ := requestFor(checkout)
			sourceInput.Type = kind
			if kind == domain.GeneralChat {
				sourceInput.Repositories = []RepositorySpec{}
				sourceInput.PrimaryRepository = ""
			}
			source, err := m.Prepare(ctx, sourceInput)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(source.PrimaryPath, "sidechat-reference-fixture.txt")
			if err := os.WriteFile(file, []byte("before\n"), 0600); err != nil {
				t.Fatal(err)
			}
			input, child, err := m.PrepareSidechatReference(ctx, domain.NewID(), domain.NewID(), sourceInput, source)
			if err != nil {
				t.Fatal(err)
			}
			if ValidateResult(input, child, runtime.GOOS) != nil || child.PrimaryPath != source.PrimaryPath || child.Reference == nil {
				t.Fatal("reference publication mismatch")
			}
			for _, repo := range child.Repositories {
				if repo.Owned {
					t.Fatal("Sidechat adopted parent deletion ownership")
				}
			}
			entries, err := os.ReadDir(filepath.Join(m.Root, "workspaces", string(child.SessionID)))
			if err != nil || len(entries) != 1 || entries[0].Name() != "manifest.json" {
				t.Fatal("Sidechat copied workspace data")
			}
			if _, err := m.Prepare(ctx, input); domain.SafeError(err).Code != domain.PermissionDenied {
				t.Fatal("ordinary preparation accepted a reference")
			}
			first, err := m.verifyWorkspaceIdentity(ctx, input, child, continuationIdentity)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("after\n"), 0600); err != nil {
				t.Fatal(err)
			}
			second, err := m.verifyWorkspaceIdentity(ctx, input, child, continuationIdentity)
			if err != nil || first != second {
				t.Fatal("ordinary parent edits changed reference identity", err)
			}
			query := domain.WorkspaceReadQuery{Operation: domain.WorkspaceFile, Path: "sidechat-reference-fixture.txt", RepositoryID: input.PrimaryRepository}
			result, err := m.ReadWorkspace(ctx, ReadRequest{ID: domain.NewID(), Deadline: time.Now().Add(10 * time.Second), Preparation: input, Manifest: child, Query: query})
			if err != nil {
				t.Fatal("referenced file unavailable", err)
			}
			if result.Text != "after\n" || result.Truncated || result.Binary {
				t.Fatal("referenced read lost current parent contents")
			}
			called := false
			err = m.WithTerminalDirectory(ctx, input, child, ReadRequest{ID: domain.NewID()}, func(string) error { called = true; return nil })
			if domain.SafeError(err).Code != domain.PermissionDenied || called {
				t.Fatal("Sidechat opened a native terminal")
			}
			storage := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, Preparation: input, Manifest: child, PreviousState: domain.WorkspacePresent}
			if _, err := m.Storage(ctx, storage); domain.SafeError(err).Code != domain.PermissionDenied {
				t.Fatal("Sidechat opened workspace storage controls", err)
			}
			if kind != domain.Local {
				storage = StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StorageCleanup, Preparation: sourceInput, Manifest: source, PreviousState: domain.WorkspacePresent, SnapshotID: domain.NewID(), PreviewDigest: strings.Repeat("ab", 32)}
				if _, err := m.Storage(ctx, storage); domain.SafeError(err).Code != domain.Conflict {
					t.Fatal("parent storage crossed dependent cleanup", err)
				}
			}
			if err := security.PrivateDir(filepath.Join(m.Root, "session-deletions")); err != nil {
				t.Fatal(err)
			}
			parentDeletion := sidechatDeletionWork(source)
			if err := m.requireNoSidechatReferences(ctx, source.SessionID); err != nil {
				t.Fatal("dependent reference blocked parent admission", err)
			}
			if _, err := os.Stat(file); err != nil {
				t.Fatal("parent file changed during pending cleanup")
			}
			if err := m.DeleteOwnedWorkspace(ctx, sidechatDeletionWork(child), false, func() error { return nil }); err != nil {
				t.Fatal("child metadata cleanup", err)
			}
			if raw, err := os.ReadFile(file); err != nil || string(raw) != "after\n" {
				t.Fatal("child deletion removed parent files")
			}
			if err := m.DeleteOwnedWorkspace(ctx, parentDeletion, false, func() error { return nil }); err != nil {
				t.Fatal("parent cleanup remained blocked", err)
			}
			if kind == domain.Local {
				if _, err := os.Stat(file); err != nil {
					t.Fatal("original Local checkout removed")
				}
			} else {
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Fatal("managed parent files retained after complete cleanup")
				}
			}
		})
	}
}

func TestSidechatReferenceRejectsChangedOrReplacedAuthority(t *testing.T) {
	for _, change := range []string{"owned", "wrong-parent", "nested", "changed-manifest", "source-replacement", "metadata-replacement", "extra-metadata"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			m := manager(t)
			sourceInput := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat, Repositories: []RepositorySpec{}}
			source, err := m.Prepare(ctx, sourceInput)
			if err != nil {
				t.Fatal(err)
			}
			input, child, err := m.PrepareSidechatReference(ctx, domain.NewID(), domain.NewID(), sourceInput, source)
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(m.Root, "workspaces", string(child.SessionID))
			switch change {
			case "owned":
				child.Repositories = append(child.Repositories, PreparedRepository{ID: domain.NewID(), Owned: true})
			case "wrong-parent":
				child.Reference.SessionID = domain.NewID()
			case "nested":
				input.SidechatSource.Preparation.SidechatSource = &SidechatSource{}
			case "changed-manifest":
				source.CreatedAt = source.CreatedAt.Add(time.Second)
				raw, _ := json.Marshal(source)
				if err := security.WriteAtomic(filepath.Join(m.Root, "workspaces", string(source.SessionID), "manifest.json"), raw); err != nil {
					t.Fatal(err)
				}
			case "source-replacement":
				if err := os.Rename(source.PrimaryPath, source.PrimaryPath+"-retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(source.PrimaryPath, 0700); err != nil {
					t.Fatal(err)
				}
			case "metadata-replacement":
				if err := os.Rename(root, root+"-retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(child)
				if err := security.WriteAtomic(filepath.Join(root, "manifest.json"), raw); err != nil {
					t.Fatal(err)
				}
			case "extra-metadata":
				if err := os.WriteFile(filepath.Join(root, "unknown"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if change == "wrong-parent" {
				if _, err := m.verifyWorkspaceIdentity(ctx, input, child, continuationIdentity); err != nil {
					t.Fatal("parent metadata blocked selected reference", err)
				}
			} else if change == "extra-metadata" {
				if err := m.removeSidechatMetadata(ctx, root, child); domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("unknown metadata was removed", err)
				}
			} else {
				if _, err := m.verifyWorkspaceIdentity(ctx, input, child, continuationIdentity); err == nil {
					t.Fatal("changed original authority was adopted")
				}
			}
		})
	}
}
