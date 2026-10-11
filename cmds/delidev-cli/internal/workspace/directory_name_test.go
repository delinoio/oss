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
)

func TestRepositoryDirectoryNamePortableConversion(t *testing.T) {
	for _, example := range []struct{ name, want string }{
		{"oss", "oss"}, {" OSS ", "OSS"}, {"a/b", "a_b"},
		{"a\\b:c*?\"<>|\nx", "a_b_c_______x"}, {"CON", "_CON"},
		{"con.txt", "_con.txt"}, {"LPT9", "_LPT9"}, {"... \t", "repository"},
		{"name. \t", "name"}, {"Cafe\u0301", "Café"},
		{strings.Repeat("é", 128), strings.Repeat("é", 127)},
		{"CON." + strings.Repeat("x", 251), "_CON." + strings.Repeat("x", 250)},
	} {
		t.Run(example.name, func(t *testing.T) {
			got, err := RepositoryDirectoryName(example.name)
			if err != nil || got != example.want || len(got) > 255 || !utf8.ValidString(got) || domain.ValidateRepositoryCloneDirectory(got) != nil {
				t.Fatalf("conversion: got %q, want %q, error %v", got, example.want, err)
			}
		})
	}
}

func TestNamedRepositoryCollisionRejectsBeforeGitOrWorkspaceCreation(t *testing.T) {
	for _, pair := range [][2]string{{"a/b", "a_b"}, {"OSS", "oss"}, {"Café", "Cafe\u0301"}, {"Straße", "STRASSE"}, {"manifest.json", "other"}} {
		t.Run(pair[0], func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "uncreated-worker")
			m := &Manager{Root: root, Git: Git{Executable: "must-not-run"}}
			input := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.Worktree}
			for _, name := range pair {
				component, err := RepositoryDirectoryName(name)
				if err != nil {
					t.Fatal(err)
				}
				input.Repositories = append(input.Repositories, RepositorySpec{ID: domain.NewID(), DirectoryName: component, SourceKind: RemoteCloneSource, RemoteURL: "https://github.com/fixture/repo.git"})
			}
			input.PrimaryRepository = input.Repositories[1].ID
			if _, err := m.Prepare(context.Background(), input); domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("collision did not fail at admission", err)
			}
			if _, err := os.Lstat(root); !os.IsNotExist(err) {
				t.Fatal("collision created Worker state", err)
			}
		})
	}
}

func TestDirectoryMetadataRejectsTraversalAndPreservesLegacyOmission(t *testing.T) {
	id := domain.NewID()
	legacy := RepositorySpec{ID: id}
	raw, _ := json.Marshal(legacy)
	if strings.Contains(string(raw), "directory_name") || ownedRepositoryPath("root", id, "") != filepath.Join("root", string(id)) {
		t.Fatal("historical serialization or path changed")
	}
	for _, name := range []string{"../foreign", "a/b", "CON", "a.", "Cafe\u0301", "\x00"} {
		if path := ownedRepositoryPath("root", id, name); path != "" {
			t.Fatal("invalid name produced authority", path)
		}
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "oss"), 0700); err != nil {
		t.Fatal(err)
	}
	input := PrepareRequest{Type: domain.Worktree, Repositories: []RepositorySpec{{ID: id, DirectoryName: "oss"}}}
	if err := requireRepositoryDestinationsAbsent(root, input); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("occupied destination was adopted", err)
	}
	if _, err := os.Stat(filepath.Join(root, "oss")); err != nil {
		t.Fatal("foreign destination removed", err)
	}
}

func TestNamedManagedRepositoryOriginalLifecycle(t *testing.T) {
	for _, name := range []string{"", "OSS"} {
		t.Run("component-"+name, func(t *testing.T) {
			m, input, _ := managedCloneFixture(t)
			input.Repositories[0].DirectoryName = name
			manifest, err := m.Prepare(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			want := repositoryDirectoryComponent(input.PrimaryRepository, name)
			if filepath.Base(manifest.PrimaryPath) != want || filepath.Base(filepath.Dir(manifest.PrimaryPath)) != string(input.SessionID) || ValidateResult(input, manifest, runtime.GOOS) != nil {
				t.Fatal("checkout layout or original result mismatch")
			}
			if err := m.WithTerminalDirectory(context.Background(), input, manifest, ReadRequest{ID: domain.NewID()}, func(cwd string) error {
				if cwd != manifest.PrimaryPath {
					t.Fatal("terminal lost frozen cwd")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			replacement := &Manager{Root: m.Root, Git: Git{Executable: m.Git.Executable}, Logger: m.Logger}
			replay, err := replacement.Prepare(context.Background(), input)
			if err != nil || manifestDigest(replay) != manifestDigest(manifest) {
				t.Fatal("original replay changed", err)
			}
			recovered, err := replacement.Recover(context.Background(), recoveryInput(input), false)
			if err != nil || recovered.Outcome != RecoveredReady {
				t.Fatal("original recovery failed", err)
			}
			changed := input
			changed.Repositories = append([]RepositorySpec(nil), input.Repositories...)
			changed.Repositories[0].DirectoryName = "renamed"
			if _, err := replacement.Prepare(context.Background(), changed); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("accepted name changed", err)
			}
			previous := closeFirstExecution(t, m, input, manifest)
			m.Git.Executable = ""
			childInput, err := m.ForkPreparation(context.Background(), manifest, domain.NewID(), domain.Worktree)
			if err != nil || childInput.Repositories[0].DirectoryName != name {
				t.Fatal("Fork did not inherit original component", err)
			}
			snapshot, err := m.InspectForkSnapshot(context.Background(), manifest, childInput)
			if err != nil {
				t.Fatal(err)
			}
			child, err := m.PrepareFork(context.Background(), childInput, snapshot)
			if err != nil || filepath.Base(child.PrimaryPath) != want {
				t.Fatal("independent Fork layout failed", err)
			}
			storage := StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StoragePreview, Preparation: input, Manifest: manifest}
			preview := storageDo(t, m, storage)
			storage.Action, storage.OperationID, storage.SnapshotID, storage.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
			cleaned := storageDo(t, m, storage)
			if _, err := os.Stat(child.PrimaryPath); err != nil {
				t.Fatal("parent cleanup removed independent child", err)
			}
			storage.Action, storage.OperationID, storage.SnapshotDigest = StorageRestore, domain.NewID(), cleaned.Snapshot.SHA256
			storageDo(t, m, storage)
			if _, err := os.Stat(manifest.PrimaryPath); err != nil {
				t.Fatal("exact frozen live layout not restored", err)
			}
			lease, err := m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), previous, input, manifest)
			if err != nil || lease.WorkingDirectory() != manifest.PrimaryPath {
				t.Fatal("continuation lost original layout", err)
			}
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			tampered := manifest
			tampered.Repositories = append([]PreparedRepository(nil), manifest.Repositories...)
			tampered.Repositories[0].DirectoryName = "foreign"
			if ValidateResult(input, tampered, runtime.GOOS) == nil {
				t.Fatal("tampered accepted component published")
			}
		})
	}
}

func TestNamedManagedReplacementCannotBecomeExecutionOrCleanupAuthority(t *testing.T) {
	for _, link := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign-directory", true: "replacement-link"}[link], func(t *testing.T) {
			m, input, _ := managedCloneFixture(t)
			input.Repositories[0].DirectoryName = "oss"
			manifest, err := m.Prepare(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			previous := closeFirstExecution(t, m, input, manifest)
			path := manifest.PrimaryPath
			if err := os.Rename(path, path+"-original"); err != nil {
				t.Fatal(err)
			}
			foreign := path
			if link {
				foreign = t.TempDir()
				if err := os.Symlink(foreign, path); err != nil {
					t.Skip("fixture cannot create a directory symlink", err)
				}
			} else if err := os.Mkdir(foreign, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(foreign, "preserve.txt")
			if err := os.WriteFile(marker, []byte("foreign bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			if lease, err := m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), previous, input, manifest); err == nil {
				lease.Close()
				t.Fatal("replacement acquired execution")
			}
			manifest.State = CleanupPending
			if err := m.cleanup(context.Background(), filepath.Dir(path), manifest); err == nil {
				t.Fatal("replacement acquired deletion authority")
			}
			if raw, err := os.ReadFile(marker); err != nil || string(raw) != "foreign bytes" {
				t.Fatal("foreign bytes removed", err)
			}
		})
	}
}
