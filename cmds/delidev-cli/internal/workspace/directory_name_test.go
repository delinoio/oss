// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestValidateDirectoryNamesRejectsCollisionsBeforeSideEffects(t *testing.T) {
	spec := func(name string) RepositorySpec {
		return RepositorySpec{ID: domain.NewID(), SourceKind: RemoteCloneSource, DirectoryName: name}
	}
	legacy := domain.NewID()
	for name, specs := range map[string][]RepositorySpec{
		"case fold":     {spec("oss"), spec("OSS")},
		"unicode":       {spec("é"), spec("é")},
		"metadata":      {spec("manifest.json")},
		"invalid":       {spec("a/b")},
		"not canonical": {spec("a ")},
		"legacy uuid":   {{ID: legacy, SourceKind: RemoteCloneSource}, spec(string(legacy))},
	} {
		if err := validateDirectoryNames(domain.Worktree, specs); err == nil {
			t.Fatalf("%s: collision or invalid name accepted", name)
		}
	}
	if err := validateDirectoryNames(domain.Worktree, []RepositorySpec{spec("oss"), spec("other"), {ID: domain.NewID(), SourceKind: RemoteCloneSource}}); err != nil {
		t.Fatal(err)
	}
	if err := validateDirectoryNames(domain.Local, []RepositorySpec{spec("oss")}); err == nil {
		t.Fatal("named directory accepted for Local workspace")
	}
	if domain.SafeError(validateDirectoryNames(domain.Worktree, []RepositorySpec{spec("a"), spec("A")})).Code != domain.Conflict {
		t.Fatal("collision did not return Conflict")
	}
}

func TestManagedCloneUsesRegisteredDirectoryName(t *testing.T) {
	m, input, _ := managedCloneFixture(t)
	input.Repositories[0].DirectoryName = "oss"
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	repo := manifest.Repositories[0]
	want := filepath.Join(m.Root, "workspaces", string(input.SessionID), "oss")
	if repo.Path != want || repo.DirectoryName != "oss" || manifest.PrimaryPath != want {
		t.Fatal("checkout did not use the frozen registered name", repo.Path)
	}
	if _, err := os.Stat(filepath.Join(m.Root, "workspaces", string(input.SessionID), string(repo.ID))); !os.IsNotExist(err) {
		t.Fatal("UUID-named checkout exists for a named repository")
	}
	if err := ValidateResult(input, manifest, runtime.GOOS); err != nil {
		t.Fatal(err)
	}
	tampered := manifest
	tampered.Repositories = append([]PreparedRepository(nil), manifest.Repositories...)
	tampered.Repositories[0].DirectoryName = "other"
	if ValidateResult(input, tampered, runtime.GOOS) == nil {
		t.Fatal("tampered directory name accepted")
	}
}
