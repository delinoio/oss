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
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestPortableRepositoryDirectoryConversion(t *testing.T) {
	for _, test := range []struct{ in, want string }{{" oss ", "oss"}, {"My Repo", "My Repo"}, {"e\u0301", "é"}, {"a<>:\"/\\|?*\x00b", "a__________b"}, {" ... ", "repository"}, {"NUL.txt", "_NUL.txt"}, {"COM1", "_COM1"}, {"name.  ", "name"}} {
		got, err := PortableRepositoryDirectory(test.in)
		if err != nil || got != test.want {
			t.Errorf("conversion %q: got %q, %v; want %q", test.in, got, err, test.want)
		}
	}
	reserved, err := PortableRepositoryDirectory("CON." + strings.Repeat("é", 140))
	if err != nil || len(reserved) > 255 || !strings.HasPrefix(reserved, "_CON.") {
		t.Fatal("long reserved name failed", len(reserved), err)
	}
	got, err := PortableRepositoryDirectory(strings.Repeat("é", 140))
	if err != nil || len(got) > 255 || !utf8.ValidString(got) || len(got) != 254 {
		t.Fatal(len(got), err)
	}
}
func TestNamedDirectoryCollisionsBeforeSideEffects(t *testing.T) {
	for _, names := range [][2]string{{"oss", "OSS"}, {"Straße", "STRASSE"}, {"é", "é"}, {"manifest.json", "safe"}, {"MANIFEST.JSON", "safe"}} {
		m := manager(t)
		input, _ := requestFor("/must-not-be-inspected")
		input.Repositories[0].DirectoryName = names[0]
		input.Repositories = append(input.Repositories, RepositorySpec{ID: domain.NewID(), Checkout: "/must-not-be-inspected", DirectoryName: names[1]})
		if _, err := m.Prepare(context.Background(), input); err == nil || domain.SafeError(err).Code != domain.Conflict {
			t.Fatal(names, err)
		}
		if _, err := os.Lstat(filepath.Join(m.Root, "workspaces", string(input.SessionID))); !os.IsNotExist(err) {
			t.Fatal("collision created workspace", err)
		}
	}
}
func TestNamedWorktreeFreezeValidationForkAndCleanup(t *testing.T) {
	m := manager(t)
	input, _ := requestFor(repository(t))
	input.Repositories[0].Checkout, _ = filepath.EvalSymlinks(input.Repositories[0].Checkout)
	input.Repositories[0].DirectoryName = "oss"
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.PrimaryPath != filepath.Join(m.Root, "workspaces", string(input.SessionID), "oss") || manifest.Repositories[0].DirectoryName != "oss" || ValidateResult(input, manifest, runtime.GOOS) != nil {
		t.Fatal("named path not accepted", manifest)
	}
	tampered := manifest
	tampered.Repositories = append([]PreparedRepository(nil), manifest.Repositories...)
	tampered.Repositories[0].DirectoryName = "renamed"
	if ValidateResult(input, tampered, runtime.GOOS) == nil {
		t.Fatal("tampered frozen name accepted")
	}
	childInput, err := m.ForkPreparation(context.Background(), manifest, domain.NewID(), domain.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	if childInput.Repositories[0].DirectoryName != "oss" {
		t.Fatal("fork lost frozen name")
	}
	child, err := m.Prepare(context.Background(), childInput)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(child.PrimaryPath) != "oss" || ValidateResult(childInput, child, runtime.GOOS) != nil {
		t.Fatal("fork layout changed")
	}
	if err := security.PrivateDir(filepath.Join(m.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteOwnedWorkspace(context.Background(), sidechatDeletionWork(manifest), false, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(child.PrimaryPath); err != nil {
		t.Fatal("parent cleanup removed child", err)
	}
	if err := m.DeleteOwnedWorkspace(context.Background(), sidechatDeletionWork(child), false, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}
func TestNamedDirectoryHistoricalOmissionAndUnsafeComponents(t *testing.T) {
	input, _ := requestFor("/original")
	raw, _ := json.Marshal(input)
	if strings.Contains(string(raw), "directory_name") {
		t.Fatal("legacy bytes changed")
	}
	if repositoryDirectory(input.Repositories[0].ID, "") != string(input.Repositories[0].ID) {
		t.Fatal("legacy path changed")
	}
	for _, name := range []string{"../foreign", "a/b", "e\u0301", strings.Repeat("a", 256), "NUL"} {
		input.Repositories[0].DirectoryName = name
		if ValidateDirectoryNames(input) == nil {
			t.Fatal("unsafe accepted", name)
		}
	}
}

func TestNamedSnapshotRestoresFrozenMultiRepositoryLayout(t *testing.T) {
	m := manager(t)
	first, _ := filepath.EvalSymlinks(repository(t))
	second, _ := filepath.EvalSymlinks(repository(t))
	preparation, _ := requestFor(first)
	preparation.Repositories[0].DirectoryName = "First"
	id := domain.NewID()
	preparation.Repositories = append(preparation.Repositories, RepositorySpec{ID: id, Checkout: second, DirectoryName: "oss", Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}})
	preparation.PrimaryRepository = id
	manifest, err := m.Prepare(context.Background(), preparation)
	if err != nil {
		t.Fatal(err)
	}
	input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, Preparation: preparation, Manifest: manifest, PreviousState: domain.WorkspacePresent}
	preview := storageDo(t, m, input)
	input.Action = StorageCleanup
	input.OperationID = domain.NewID()
	input.SnapshotID = domain.NewID()
	input.PreviewDigest = preview.PreviewDigest
	cleaned := storageDo(t, m, input)
	if cleaned.Snapshot == nil {
		t.Fatal("snapshot missing")
	}
	input.SnapshotDigest = cleaned.Snapshot.SHA256
	input.Action = StorageRestore
	input.OperationID = domain.NewID()
	input.PreviousState = domain.WorkspaceStored
	input.PreviousSnapshotID = input.SnapshotID
	input.PreviewDigest = ""
	storageDo(t, m, input)
	restored, err := m.Read(preparation.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.PrimaryPath != manifest.PrimaryPath || ValidateResult(preparation, restored, runtime.GOOS) != nil {
		t.Fatal("restore changed frozen primary", restored)
	}
	for i, repo := range restored.Repositories {
		if repo.Path != manifest.Repositories[i].Path || repo.DirectoryName != preparation.Repositories[i].DirectoryName {
			t.Fatal("restore changed layout", repo)
		}
		if _, err := os.Stat(filepath.Join(repo.Path, "tracked.txt")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNamedManagedCloneAndOccupiedDestination(t *testing.T) {
	m, input, _ := managedCloneFixture(t)
	input.Repositories[0].DirectoryName = "oss"
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(manifest.PrimaryPath) != "oss" || ValidateResult(input, manifest, runtime.GOOS) != nil {
		t.Fatal("managed clone lost frozen layout")
	}
	if _, err := m.verifyWorkspaceIdentity(context.Background(), input, manifest, preparationIdentity); err != nil {
		t.Fatal("named clone native identity invalid", err)
	}
	occupied, _ := requestFor("/must-not-be-inspected")
	occupied.Repositories[0].DirectoryName = "oss"
	root := filepath.Join(m.Root, "workspaces", string(occupied.SessionID))
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "foreign.txt")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prepare(context.Background(), occupied); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("occupied destination adopted", err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "preserve" {
		t.Fatal("foreign file changed", err)
	}
}
