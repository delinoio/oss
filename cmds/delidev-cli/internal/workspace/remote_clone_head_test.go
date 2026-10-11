// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func remoteHeadQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func remoteHeadFixture(t *testing.T, observation, beforeObservation string) (Git, string, RepositorySpec) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable fixture; native Windows acceptance is separate")
	}
	root := repository(t)
	gitTest(t, root, "remote", "add", "origin", "https://github.com/fixture/repo.git")
	gitTest(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitTest(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	binary, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(t.TempDir(), "git")
	body := "#!/bin/sh\ncase \" $* \" in\n" +
		"*' symbolic-ref --quiet refs/remotes/origin/HEAD '*) " + observation + " ;;\n"
	if beforeObservation != "" {
		body += "*' for-each-ref '*) " + remoteHeadQuote(binary) + " \"$@\"\nstatus=$?\n" + beforeObservation + "\nexit \"$status\" ;;\n"
	}
	body += "*' fetch '*|*' clone '*) exit 77 ;;\n*) exec " + remoteHeadQuote(binary) + " \"$@\" ;;\nesac\n"
	if err := os.WriteFile(wrapper, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	git := Git{Executable: wrapper, ProcessRoot: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID(), cloneDiagnostics: true}
	spec := RepositorySpec{PreferredRemote: "upstream", Starting: domain.Reference{Type: domain.RemoteBranch, Remote: "backup", Name: "main"}}
	return git, root, spec
}

func TestManagedCloneRemoteHeadObservation(t *testing.T) {
	for _, test := range []struct {
		name, observation, before, cause string
		code                             domain.Code
	}{
		{name: "absent", observation: "exit 1"},
		{name: "fatal", observation: "exit 128", code: domain.Unavailable, cause: "git_exit"},
		{name: "authentication status one", observation: "printf 'fatal: Authentication failed for private-content\\n' >&2; exit 1", code: domain.PermissionDenied, cause: "clone_authentication"},
		{name: "launch", observation: "exit 1", before: "rm -- \"$0\"", code: domain.Unavailable, cause: "git_launch"},
		{name: "empty", observation: "exit 0", code: domain.RecoveryRequired},
		{name: "foreign", observation: "printf 'refs/heads/main\\n'", code: domain.RecoveryRequired},
		{name: "empty branch", observation: "printf 'refs/remotes/origin/\\n'", code: domain.RecoveryRequired},
		{name: "self", observation: "printf 'refs/remotes/origin/HEAD\\n'", code: domain.RecoveryRequired},
		{name: "multiple", observation: "printf 'refs/remotes/origin/main\\nrefs/remotes/origin/other\\n'", code: domain.RecoveryRequired},
		{name: "embedded carriage return", observation: "printf 'refs/remotes/origin/main\\rrefs/remotes/origin/other\\n'", code: domain.RecoveryRequired},
		{name: "malformed", observation: "printf 'refs/remotes/origin/bad..name\\n'", code: domain.Unavailable, cause: "git_exit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			git, root, spec := remoteHeadFixture(t, test.observation, test.before)
			err := provisionManagedCloneRemotes(context.Background(), git, root, "https://github.com/fixture/repo.git", spec, "origin")
			if test.code == "" {
				if err != nil {
					t.Fatal("ordinary non-symbolic status was not accepted", err)
				}
				return
			}
			problem := domain.SafeError(err)
			if err == nil || problem.Code != test.code || problem.Cause != test.cause || strings.Contains(problem.Error(), "private-content") {
				t.Fatalf("unexpected observation outcome: %v", err)
			}
		})
	}
	t.Run("nested target and every alias", func(t *testing.T) {
		git, root, spec := remoteHeadFixture(t, "printf 'refs/remotes/origin/feature/nested\\n'", "")
		gitTest(t, root, "update-ref", "refs/remotes/origin/feature/nested", "HEAD")
		if err := provisionManagedCloneRemotes(context.Background(), git, root, "https://github.com/fixture/repo.git", spec, "origin"); err != nil {
			t.Fatal(err)
		}
		for _, remote := range []string{"upstream", "backup"} {
			if got := gitTest(t, root, "symbolic-ref", "refs/remotes/"+remote+"/HEAD"); got != "refs/remotes/"+remote+"/feature/nested" {
				t.Fatal("alias lost the exact original target", remote, got)
			}
		}
	})
}

func TestManagedCloneRemoteHeadInterruptedObservation(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "timeout"}[timeout], func(t *testing.T) {
			started := filepath.Join(t.TempDir(), "started")
			git, root, spec := remoteHeadFixture(t, "printf started > "+remoteHeadQuote(started)+"; exec sleep 20", "")
			git.Timeout = 5 * time.Second
			if timeout {
				git.Timeout = time.Second
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				result <- provisionManagedCloneRemotes(ctx, git, root, "https://github.com/fixture/repo.git", spec, "origin")
			}()
			if !timeout {
				deadline := time.Now().Add(5 * time.Second)
				for {
					if _, err := os.Stat(started); err == nil {
						cancel()
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("symbolic observation did not start")
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			select {
			case err := <-result:
				if _, startedErr := os.Stat(started); startedErr != nil {
					t.Fatal("interruption preceded symbolic observation", startedErr)
				}
				problem := domain.SafeError(err)
				if timeout {
					if problem.Code != domain.Unavailable || problem.Cause != "clone_timeout" {
						t.Fatal("timeout became symbolic absence", err)
					}
				} else if problem.Code != domain.Canceled {
					t.Fatal("cancellation became symbolic absence", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("original Git process did not join")
			}
		})
	}
}

func TestManagedCloneRemoteHeadFailureBlocksReady(t *testing.T) {
	for _, observation := range []string{"exit 128", "printf 'fatal: Authentication failed\\n' >&2; exit 1", "printf 'refs/heads/main\\n'"} {
		t.Run(observation, func(t *testing.T) {
			manager, input, marker := managedCloneFixture(t)
			input.Repositories[0].Starting = domain.Reference{Type: domain.RemoteBranch, Remote: "upstream", Name: "main"}
			input.Repositories[0].AutoFetch = false
			body, err := os.ReadFile(manager.Git.Executable)
			if err != nil {
				t.Fatal(err)
			}
			branch := "case \" $* \" in\n*' symbolic-ref --quiet refs/remotes/origin/HEAD '*) " + observation + " ;;\n*' fetch '*) exit 77 ;;\n"
			body = []byte(strings.Replace(string(body), "case \" $* \" in\n", branch, 1))
			if err := os.WriteFile(manager.Git.Executable, body, 0700); err != nil {
				t.Fatal(err)
			}
			manifest, err := manager.Prepare(context.Background(), input)
			if err == nil || manifest.State == Ready {
				t.Fatal("failed symbolic observation published readiness")
			}
			if _, err := os.Stat(filepath.Join(manager.Root, "workspaces", string(input.SessionID))); !os.IsNotExist(err) {
				t.Fatal("joined failed preparation retained its workspace", err)
			}
			if raw, err := os.ReadFile(marker); err != nil || string(raw) != "invoked" {
				t.Fatal("failure repeated the original clone", string(raw), err)
			}
		})
	}
}
