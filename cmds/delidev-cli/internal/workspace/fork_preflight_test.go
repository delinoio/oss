// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestForkPreparationRejectsAllRepositoriesBeforeCreatingChildScope(t *testing.T) {
	for _, kind := range []domain.WorkspaceType{domain.Worktree, domain.Local} {
		for _, scenario := range []string{"second-unborn", "duplicate-repository", "missing-primary"} {
			t.Run(string(kind)+"/"+scenario, func(t *testing.T) {
				m := manager(t)
				first := repository(t)
				var second string
				if scenario == "second-unborn" {
					second = t.TempDir()
					gitTest(t, second, "init", "-b", "main")
				} else {
					second = repository(t)
				}
				input, _ := requestFor(first)
				input.Type, input.OriginMachineID = domain.Local, input.MachineID
				input.Repositories[0].Starting = domain.Reference{}
				input.Repositories = append(input.Repositories, RepositorySpec{ID: domain.NewID(), Checkout: second})
				source, err := m.Prepare(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(first, "tracked.txt"), []byte("staged source edits\n"), 0600); err != nil {
					t.Fatal(err)
				}
				gitTest(t, first, "add", "tracked.txt")
				if err := os.WriteFile(filepath.Join(first, "tracked.txt"), []byte("unstaged source edits\n"), 0600); err != nil {
					t.Fatal(err)
				}
				before, _ := json.Marshal(source)
				var statuses []string
				for _, repo := range source.Repositories {
					statuses = append(statuses, gitTest(t, repo.Path, "status", "--porcelain=v1", "--untracked-files=all"))
				}
				expected := domain.InvalidArgument
				switch scenario {
				case "second-unborn":
					if source.Repositories[1].LocalHEAD != LocalHEADUnborn {
						t.Fatal("fixture did not prepare an unborn second repository")
					}
					expected = domain.Unsupported
				case "duplicate-repository":
					source.Repositories[1].ID = source.Repositories[0].ID
				case "missing-primary":
					source.PrimaryPath = filepath.Join(m.Root, "missing-primary")
				}
				// Repeated definite failures must not accumulate unpublished scopes.
				for range 2 {
					child := domain.NewID()
					if _, err := m.ForkPreparation(context.Background(), source, child, kind); domain.SafeError(err).Code != expected {
						t.Fatal("unexpected preflight result", err)
					}
					for _, directory := range []string{"processes", "workspaces"} {
						if _, err := os.Lstat(filepath.Join(m.Root, directory, string(child))); !os.IsNotExist(err) {
							t.Fatal("definite rejection retained a child scope", directory, err)
						}
					}
				}
				stored, err := m.Read(source.SessionID)
				after, _ := json.Marshal(stored)
				if err != nil || string(before) != string(after) {
					t.Fatal("rejection changed the stored source manifest", err)
				}
				for i, repo := range stored.Repositories {
					if gitTest(t, repo.Path, "status", "--porcelain=v1", "--untracked-files=all") != statuses[i] {
						t.Fatal("rejection changed source Git state")
					}
				}
				if raw, err := os.ReadFile(filepath.Join(first, "tracked.txt")); err != nil || string(raw) != "unstaged source edits\n" {
					t.Fatal("rejection changed source file bytes", err)
				}
			})
		}
	}
}

func TestForkPreparationRetainsScopeAfterNativeHEADFailure(t *testing.T) {
	m := manager(t)
	first, second := repository(t), repository(t)
	input, _ := requestFor(first)
	input.Type, input.OriginMachineID = domain.Local, input.MachineID
	input.Repositories[0].Starting = domain.Reference{}
	input.Repositories = append(input.Repositories, RepositorySpec{ID: domain.NewID(), Checkout: second})
	source, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	before := gitTest(t, first, "rev-parse", "HEAD")
	// The manifest remains structurally valid, but the second native read fails
	// after the first repository has already created process ownership evidence.
	if err := os.Rename(second, second+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Rename(second+"-unavailable", second); err != nil {
			t.Error(err)
		}
	})
	child := domain.NewID()
	if _, err := m.ForkPreparation(context.Background(), source, child, domain.Worktree); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("native HEAD failure became a definite rejection", err)
	}
	entries, err := os.ReadDir(filepath.Join(m.Root, "processes", string(child)))
	if err != nil || len(entries) == 0 {
		t.Fatal("uncertain native process evidence was removed", err)
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "workspaces", string(child))); !os.IsNotExist(err) {
		t.Fatal("native HEAD failure created a child workspace", err)
	}
	if gitTest(t, first, "rev-parse", "HEAD") != before {
		t.Fatal("native HEAD failure changed the first source HEAD")
	}
}

func TestForkPreparationRetainsOriginalIndexIdentityAndRejectsForeignOwners(t *testing.T) {
	m := manager(t)
	input, _ := requestFor(repository(t))
	source, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	child := domain.NewID()
	fork, err := m.ForkPreparation(context.Background(), source, child, domain.Worktree)
	if err != nil || fork.ForkProcessIdentity() == nil {
		t.Fatal("original index not retained", err)
	}
	raw, err := json.Marshal(fork)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PrepareRequest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ForkProcessIdentity() != nil {
		t.Fatal("serialized metadata recreated native proof")
	}
	if _, err := m.ForkPreparation(context.Background(), source, child, domain.Worktree); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("existing child owner adopted", err)
	}
	current, err := os.Lstat(filepath.Join(m.Root, "processes", string(child)))
	if err != nil || !os.SameFile(fork.ForkProcessIdentity(), current) {
		t.Fatal("original owner changed", err)
	}
}
