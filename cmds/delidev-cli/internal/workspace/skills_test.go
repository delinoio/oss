// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"errors"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSkillReadRejectsExpiredOrUnboundedObservationBeforeFilesystemAccess(t *testing.T) {
	machine := domain.NewID()
	for _, deadline := range []time.Time{{}, time.Now().Add(-time.Minute), time.Now().Add(32 * time.Second)} {
		root := filepath.Join(t.TempDir(), "untouched")
		manager := Manager{Root: root}
		request := ReadRequest{Deadline: deadline, Preparation: PrepareRequest{MachineID: machine}, Skills: &domain.SkillReadRequest{MachineID: machine, ActorID: domain.NewID()}}
		if _, err := manager.ReadSkills(context.Background(), request); err == nil {
			t.Fatal("invalid observation deadline accepted")
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("invalid observation touched filesystem", err)
		}
	}
}

func skillScope(input PrepareRequest) domain.SkillReadRequest {
	return domain.SkillReadRequest{MachineID: input.MachineID, SessionID: input.SessionID, AgentID: domain.NewID(), ActorID: domain.NewID(), WorkerDeviceID: domain.NewID(), WorkerInstanceID: domain.NewID(), AgentRevision: 1}
}
func writeSkillFixture(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, ".agents", "skills", name)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(fmt.Sprintf("---\nname: %s\ndescription: Harmless isolated fixture\n---\nbody\n", name)), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestPreparedSkillInventoryUsesServerEnvelope(t *testing.T) {
	for _, kind := range []string{"chat", "local", "worktree", "multiple"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			var m *Manager
			var input PrepareRequest
			var manifest Manifest
			if kind == "chat" {
				m, input, manifest = chatExecutionFixture(t)
			} else {
				m = manager(t)
				input, _ = requestFor(skillRepositoryFixture(t))
				if kind == "local" {
					input.Type = domain.Local
				}
				if kind == "multiple" {
					input.Repositories = append(input.Repositories, RepositorySpec{ID: domain.NewID(), Checkout: skillRepositoryFixture(t), Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}})
				}
				var err error
				manifest, err = m.Prepare(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
			}
			writeSkillFixture(t, home, "user-fixture")
			roots := []string{manifest.PrimaryPath}
			if kind != "chat" {
				roots = nil
				for _, repo := range manifest.Repositories {
					roots = append(roots, repo.Path)
				}
			}
			for i, root := range roots {
				writeSkillFixture(t, root, fmt.Sprintf("project-%d", i))
			}
			scope := skillScope(input)
			request := ReadRequest{ID: domain.NewID(), Preparation: input, Manifest: manifest, Skills: &scope}
			for _, budget := range []time.Duration{30 * time.Second, 15 * time.Second} {
				request.Deadline = time.Now().Add(budget)
				result, err := m.ReadSkills(context.Background(), request)
				if err != nil || len(result.Entries) != len(roots)+1 {
					t.Fatalf("budget %s: %+v %v", budget, result, err)
				}
				user, project := 0, 0
				for _, entry := range result.Entries {
					switch entry.Provenance {
					case "user":
						user++
					case "project":
						project++
					default:
						t.Fatal(entry)
					}
				}
				if user != 1 || project != len(roots) {
					t.Fatal(result)
				}
			}
			ordinary := workspaceReadFixture(input, manifest, domain.WorkspaceDirectory, ".")
			ordinary.Deadline = time.Now().Add(30 * time.Second)
			if _, err := m.ReadWorkspace(context.Background(), ordinary); err == nil {
				t.Fatal("ordinary oversized deadline accepted")
			}
			request.Deadline = time.Now().Add(30 * time.Second)
			request.Manifest.InputDigest = "foreign"
			if _, err := m.ReadSkills(context.Background(), request); err == nil {
				t.Fatal("foreign manifest accepted")
			}
		})
	}
}
func TestSkillObservationSharesDeadlineWithoutRenewingOrMutatingCaller(t *testing.T) {
	for _, budget := range []time.Duration{30 * time.Second, 5 * time.Second} {
		t.Run(budget.String(), func(t *testing.T) {
			original := time.Now().Add(budget)
			request := ReadRequest{Deadline: original, Manifest: Manifest{Repositories: []PreparedRepository{{ID: domain.NewID()}, {ID: domain.NewID()}}}}
			started := time.Now()
			var first time.Time
			calls := 0
			roots, err := skillWorkspaceRoots(request, func(observation ReadRequest, id domain.ID) (string, error) {
				calls++
				if calls == 1 {
					first = observation.Deadline
				} else if observation.Deadline != first {
					t.Fatal("repository renewed deadline")
				}
				if observation.Deadline.After(original) || observation.Deadline.After(time.Now().Add(15*time.Second)) {
					t.Fatal("observation exceeds budget")
				}
				if id != request.Manifest.Repositories[calls-1].ID {
					t.Fatal("repository order changed")
				}
				return string(id), nil
			})
			if err != nil || len(roots) != 2 || calls != 2 {
				t.Fatal(roots, err, calls)
			}
			if budget < 15*time.Second && first != original {
				t.Fatal("shorter caller deadline extended")
			}
			if budget > 15*time.Second && first.Before(started.Add(15*time.Second)) {
				t.Fatal("incorrect nested budget")
			}
			if request.Deadline != original {
				t.Fatal("caller deadline changed")
			}
			sentinel := errors.New("observer failed")
			calls = 0
			if _, err = skillWorkspaceRoots(request, func(ReadRequest, domain.ID) (string, error) { calls++; return "", sentinel }); err != sentinel || calls != 1 {
				t.Fatal("continued after failure", err, calls)
			}
		})
	}
}
func TestSkillCreationPreparationAndCleanupKeepOriginalEnvelope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := manager(t)
	input := PrepareRequest{MachineID: domain.NewID()}
	scope := skillScope(input)
	source := writeSkillFixture(t, home, "user-fixture")
	read := func() (domain.SkillReadResult, error) {
		return m.ReadSkills(context.Background(), ReadRequest{Deadline: time.Now().Add(30 * time.Second), Preparation: input, Skills: &scope})
	}
	inventory, err := read()
	if err != nil || len(inventory.Entries) != 1 {
		t.Fatal(inventory, err)
	}
	// An unprepared session must still list user packages without touching a workspace.
	scope.SessionID = domain.NewID()
	inventory, err = read()
	if err != nil || len(inventory.Entries) != 1 {
		t.Fatal(inventory, err)
	}
	entry := inventory.Entries[0]
	snapshot := domain.NewID()
	scope.Selections = []domain.SkillBinding{{WorkerDeviceID: entry.WorkerDeviceID, InventoryID: entry.InventoryID, SkillID: entry.SkillID, ContentRevision: entry.ContentRevision, SnapshotID: snapshot}}
	scope.Preparation = &domain.SkillPreparationProof{ServerID: domain.NewID(), RequestID: snapshot, OriginalInstanceID: scope.WorkerInstanceID}
	scope.Preparation.ScopeDigest = domain.SkillPreparationDigest(scope)
	prepared, err := read()
	if err != nil || len(prepared.Entries) != 1 || prepared.Entries[0].ContentRevision != entry.ContentRevision {
		t.Fatal(prepared, err)
	}
	if err = os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("changed source"), 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err = read()
	if err != nil || len(prepared.Entries) != 1 || prepared.Entries[0].ContentRevision != entry.ContentRevision {
		t.Fatal("snapshot changed", prepared, err)
	}
	packages := skills.Manager{Root: m.Root, Home: home}
	if _, err = packages.Resolve(context.Background(), scope.Selections); err != nil {
		t.Fatal(err)
	}
	scope.Action = domain.CleanupSkillPreparation
	originalActor := scope.ActorID
	scope.ActorID = domain.NewID()
	if _, err = read(); err == nil {
		t.Fatal("foreign cleanup actor accepted")
	}
	scope.ActorID = originalActor
	for i := 0; i < 2; i++ {
		if _, err = read(); err != nil {
			t.Fatal("cleanup receipt retry", err)
		}
	}
	if _, err = packages.Resolve(context.Background(), scope.Selections); err == nil {
		t.Fatal("cleaned snapshot retained")
	}
	scope.Action = ""
	if _, err = read(); err == nil {
		t.Fatal("cleanup tombstone allowed recreation")
	}
}

func skillRepositoryFixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(repository(t))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
