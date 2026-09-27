package server

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type openCodeProjectFixtureKind uint8

const (
	openCodeCommittedProject openCodeProjectFixtureKind = iota
	openCodeUnbornProject
	openCodeFirstCommitProject
)

func TestManualNativeOpenCodeUnbornPublicContinuation(t *testing.T) {
	for _, scenario := range []string{"unborn", "first-commit"} {
		t.Run(scenario, func(t *testing.T) {
			nativeOpenCodePublicDispatchProfile(t, 3, false, "", "project-local:"+scenario, true)
		})
	}
}

func TestManualNativeOpenCodeUnbornCompletionRecovery(t *testing.T) {
	binary := nativeOpenCodeRecoveryExecutable(t)
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, scenario := range []string{"unborn-resumed", "first-commit-resumed"} {
			t.Run(string(mode)+"/"+scenario, func(t *testing.T) {
				nativeOpenCodeRecovery(t, binary, mode, "project-local:"+scenario)
			})
		}
	}
}

func openCodeProjectFixtureProfile(profile string) (domain.WorkspaceType, string) {
	for _, kind := range []domain.WorkspaceType{domain.Worktree, domain.Local} {
		prefix := "project-" + string(kind) + ":"
		if strings.HasPrefix(profile, prefix) {
			return kind, strings.TrimPrefix(profile, prefix)
		}
	}
	return domain.GeneralChat, profile
}

func prepareOpenCodeProjectFixture(t *testing.T, f *firstDispatchFixture, kind domain.WorkspaceType, profiles ...openCodeProjectFixtureKind) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	if kind != domain.Worktree && kind != domain.Local {
		t.Fatal("unsupported project workspace fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(root, "checkout")
	if err := os.Mkdir(checkout, 0700); err != nil {
		t.Fatal(err)
	}
	env, err := harness.PrivateRuntimeEnvironment(filepath.Join(root, "git-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	env = append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	if err := os.WriteFile(filepath.Join(checkout, "tracked.txt"), []byte("Original project fixture.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{{"init", "-q", "--initial-branch=main"}, {"config", "core.hooksPath", filepath.Join(root, "no-hooks")}, {"add", "tracked.txt"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "Private project fixture"}}
	if len(profiles) == 1 && profiles[0] != openCodeCommittedProject {
		if kind != domain.Local {
			t.Fatal("unborn fixture must use authenticated Local")
		}
		commands = commands[:len(commands)-1]
	}
	for _, args := range commands {
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir, command.Env = checkout, env
		command.Stdout, command.Stderr = io.Discard, io.Discard
		if err := command.Run(); err != nil {
			t.Fatal("private project Git setup", err)
		}
	}
	// Repository metadata is a fixture. Public project/session creation, real
	// owned Git preparation and native execution are independently exercised.
	repository := domain.NewID()
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.project-repository", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.RepositoryKind, repository, 0, "", "", domain.Repository{Name: "Private project fixture", Checkouts: []domain.Checkout{{MachineID: f.selection.MachineID, Path: checkout}}, Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}, AutoFetch: false})
	})
	if err != nil {
		t.Fatal(err)
	}
	project := f.save(pb.EntityKind_ENTITY_KIND_PROJECT, domain.Project{Name: "Private project fixture", Repositories: []domain.ID{repository}, PrimaryRepository: repository})
	f.selection.ProjectID, f.selection.Workspace = domain.ID(project.Id), kind
}

func TestManualNativeOpenCodeProjectPublicContinuation(t *testing.T) {
	for _, kind := range []domain.WorkspaceType{domain.Worktree, domain.Local} {
		t.Run(string(kind), func(t *testing.T) {
			nativeOpenCodePublicDispatchProfile(t, 3, false, "", "project-"+string(kind)+":", true)
		})
	}
}

func TestManualNativeOpenCodeProjectPublicFileContinuation(t *testing.T) {
	for _, kind := range []domain.WorkspaceType{domain.Worktree, domain.Local} {
		for _, tool := range []string{"write", "edit", "apply_patch"} {
			t.Run(string(kind)+"/"+tool, func(t *testing.T) {
				nativeOpenCodePublicDispatchProfile(t, 3, false, "", "project-"+string(kind)+":"+tool)
			})
		}
	}
}

func TestManualNativeOpenCodeProjectCompletionRecovery(t *testing.T) {
	binary := nativeOpenCodeRecoveryExecutable(t)
	for _, kind := range []domain.WorkspaceType{domain.Worktree, domain.Local} {
		for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
			for _, scenario := range []string{"success", "resumed"} {
				t.Run(string(kind)+"/"+string(mode)+"/"+scenario, func(t *testing.T) { nativeOpenCodeRecovery(t, binary, mode, "project-"+string(kind)+":"+scenario) })
			}
		}
	}
}

func TestManualNativeOpenCodeProjectRefusesChangedSnapshot(t *testing.T) {
	for _, kind := range []domain.WorkspaceType{domain.Worktree, domain.Local} {
		t.Run(string(kind), func(t *testing.T) {
			nativeOpenCodePublicDispatchProfile(t, 2, false, "snapshot", "project-"+string(kind)+":")
		})
	}
}

func TestManualNativeOpenCodeProjectFailedContinuation(t *testing.T) {
	for _, kind := range []domain.WorkspaceType{domain.Worktree, domain.Local} {
		t.Run(string(kind), func(t *testing.T) { nativeOpenCodePublicDispatchProfile(t, 2, true, "", "project-"+string(kind)+":") })
	}
}

func commitOpenCodeProjectFixture(t *testing.T, ctx context.Context, directory string) {
	t.Helper()
	env, err := harness.PrivateRuntimeEnvironment(filepath.Join(t.TempDir(), "git-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "First private fixture commit")
	command.Dir = directory
	command.Env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("first private project commit: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(directory, "tracked.txt"), []byte("Later independent Local changes.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "untracked-after-first-commit.txt"), []byte("Retain untracked Local content.\n"), 0600); err != nil {
		t.Fatal(err)
	}
}
