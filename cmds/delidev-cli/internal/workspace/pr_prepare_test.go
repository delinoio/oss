package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func fixtureShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type prPreparationFixture struct {
	manager                                               *Manager
	request                                               PrepareRequest
	source, fork, checkout, marker, log, base, head, next string
}

// The fixture delegates all Git operations to real Git. Only the two exact
// synthetic network destinations are replaced with private local repositories;
// no live network, user's Git configuration or credential helper is used.
func newPRPreparationFixture(t *testing.T) prPreparationFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("this local transport fixture uses a POSIX shell; no Windows runtime claim")
	}
	home := t.TempDir()
	for key, value := range map[string]string{"HOME": home, "USERPROFILE": home, "GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_SYSTEM": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1", "XDG_CONFIG_HOME": home, "SSH_AUTH_SOCK": "", "GIT_SSH": "", "GIT_SSH_COMMAND": ""} {
		t.Setenv(key, value)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	f := prPreparationFixture{manager: manager(t), source: repository(t), fork: filepath.Join(t.TempDir(), "fork"), checkout: filepath.Join(t.TempDir(), "checkout"), marker: filepath.Join(home, "behavior"), log: filepath.Join(home, "network.log")}
	f.base = gitTest(t, f.source, "rev-parse", "HEAD")
	gitTest(t, home, "clone", f.source, f.fork)
	gitTest(t, f.fork, "switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(f.fork, "tracked.txt"), []byte("PR head\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, f.fork, "commit", "-am", "PR change")
	f.head = gitTest(t, f.fork, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(f.fork, "tracked.txt"), []byte("Later PR head\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, f.fork, "commit", "-am", "later change")
	f.next = gitTest(t, f.fork, "rev-parse", "HEAD")
	gitTest(t, f.fork, "reset", "--hard", f.head)
	gitTest(t, home, "clone", f.source, f.checkout)
	f.checkout, err = filepath.EvalSymlinks(f.checkout)
	if err != nil {
		t.Fatal(err)
	}
	gitTest(t, f.checkout, "fetch", "origin")
	gitTest(t, f.checkout, "remote", "set-url", "origin", "https://github.com/fixture-owner/repo.git")
	// Existing fetch policy must not prune tags/tracking refs in PR preparation.
	gitTest(t, f.checkout, "config", "fetch.prune", "true")
	gitTest(t, f.checkout, "config", "fetch.pruneTags", "true")
	gitTest(t, f.checkout, "tag", "local-only-tag")
	gitTest(t, f.checkout, "update-ref", "refs/remotes/origin/retained", f.base)
	f.request, _ = requestFor(f.checkout)
	target := domain.PRGitTarget{Version: 1, Target: domain.SessionPullRequest{Version: 1, Provider: domain.GitHubCom, RepositoryID: f.request.PrimaryRepository, RemoteRepositoryID: "37", RepositoryNodeID: "R_37", Owner: "fixture-owner", Name: "repo", PullRequestID: "53", PullRequestNodeID: "PR_53", Number: "17", Title: "Fixture PR", ObservedAt: time.Now().UTC()}, HeadRepository: domain.RemoteRepository{Provider: domain.GitHubCom, ID: "9007199254740993", NodeID: "FORK_1", Owner: "fixture-author", Name: "fork-repo", Private: true, DefaultBranch: "main"}, BaseRef: "main", BaseSHA: f.base, HeadRef: "feature", HeadSHA: f.head}
	spec := &f.request.Repositories[0]
	spec.PRTarget, spec.AutoFetch, spec.PreferredRemote = &target, true, "origin"
	spec.Starting = domain.Reference{Type: domain.CommitReference, Name: f.head}
	spec.Base = domain.Reference{Type: domain.CommitReference, Name: f.base}
	// Keep argv intact through shell rotation. The marker is test-owned and
	// changes only these fixture refs after an actual successful head fetch.
	script := fmt.Sprintf(`#!/bin/sh
network=no
fetch=no
geturl=no
head=no
for arg do
  case "$arg" in fetch) network=yes; fetch=yes;; ls-remote) network=yes;; --get-url) geturl=yes;; esac
done
if [ "$network" = yes ] && [ "$geturl" = no ]; then
  printf 'network:%%s\n' "$fetch" >> %s
  if [ -f %s ] && [ "$(cat %s)" = fail ]; then exit 128; fi
  for arg do
    case "$arg" in
      https://github.com/fixture-owner/repo.git|git@github.com:fixture-owner/repo.git|ssh://git@github.com/fixture-owner/repo.git) arg=%s;;
      https://github.com/fixture-author/fork-repo.git|git@github.com:fixture-author/fork-repo.git|ssh://git@github.com/fixture-author/fork-repo.git) arg=%s; head=yes;;
    esac
    shift
    set -- "$@" "$arg"
  done
  %s "$@" || exit $?
  if [ "$fetch" = yes ] && [ "$head" = yes ] && [ -f %s ]; then
    case "$(cat %s)" in
      move) %s -C %s update-ref refs/heads/feature %s;;
      base-move) %s -C %s update-ref refs/heads/main %s;;
      remote-change) %s -C %s remote set-url origin https://github.com/unrelated/repo.git;;
    esac
  fi
  exit 0
fi
exec %s "$@"
`, fixtureShellQuote(f.log), fixtureShellQuote(f.marker), fixtureShellQuote(f.marker), fixtureShellQuote(f.source), fixtureShellQuote(f.fork), fixtureShellQuote(git), fixtureShellQuote(f.marker), fixtureShellQuote(f.marker), fixtureShellQuote(git), fixtureShellQuote(f.fork), fixtureShellQuote(f.next), fixtureShellQuote(git), fixtureShellQuote(f.source), fixtureShellQuote(f.head), fixtureShellQuote(git), fixtureShellQuote(f.checkout), fixtureShellQuote(git))
	// The base-change scenario needs its replacement object in the base fixture.
	gitTest(t, f.source, "fetch", f.fork, f.head)
	executable := filepath.Join(home, "fixture-git")
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	f.manager.Git.Executable = executable
	return f
}

func TestPRPreparationCreatesExactForkWorktreeAndPreservesOriginalWork(t *testing.T) {
	f := newPRPreparationFixture(t)
	tracked := filepath.Join(f.checkout, "tracked.txt")
	os.WriteFile(tracked, []byte("staged\n"), 0600)
	gitTest(t, f.checkout, "add", "tracked.txt")
	os.WriteFile(tracked, []byte("unstaged\n"), 0600)
	os.WriteFile(filepath.Join(f.checkout, "untracked"), []byte("keep\n"), 0600)
	index, err := os.ReadFile(filepath.Join(f.checkout, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	fetchHead, err := os.ReadFile(filepath.Join(f.checkout, ".git", "FETCH_HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	refs := gitTest(t, f.checkout, "show-ref")
	result, err := f.manager.Prepare(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateResult(f.request, result, runtime.GOOS); err != nil {
		t.Fatal("publication", err)
	}
	prepared := result.Repositories[0]
	if !samePRTarget(f.request.Repositories[0].PRTarget, prepared.PRTarget) || gitTest(t, prepared.Path, "rev-parse", "HEAD") != f.head || gitTest(t, prepared.Path, "branch", "--show-current") != "" || gitTest(t, f.checkout, "rev-parse", "HEAD") != f.base {
		t.Fatal("wrong original/fork commit or branch")
	}
	if gitTest(t, f.checkout, "show-ref") != refs {
		t.Fatal("unrelated references changed")
	}
	for path, expected := range map[string]string{filepath.Join(f.checkout, ".git", "index"): string(index), filepath.Join(f.checkout, ".git", "FETCH_HEAD"): string(fetchHead), tracked: "unstaged\n", filepath.Join(f.checkout, "untracked"): "keep\n"} {
		actual, err := os.ReadFile(path)
		if err != nil || string(actual) != expected {
			t.Fatal("original work changed", filepath.Base(path), err)
		}
	}
	before, _ := os.ReadFile(f.log)
	if _, err := f.manager.Prepare(context.Background(), f.request); err != nil {
		t.Fatal("original retry", err)
	}
	after, _ := os.ReadFile(f.log)
	if string(before) != string(after) {
		t.Fatal("ready retry performed another fetch")
	}
	recovered, err := f.manager.Recover(context.Background(), recoveryInput(f.request), false)
	if err != nil || recovered.Outcome != RecoveredReady || !samePRTarget(recovered.Manifest.Repositories[0].PRTarget, prepared.PRTarget) {
		t.Fatal("original PR recovery", err)
	}
	lease, err := f.manager.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), f.request, result)
	if err != nil {
		t.Fatal("original PR execution ownership", err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal("execution ownership cleanup", err)
	}
	mutated := result
	mutated.Repositories = append([]PreparedRepository(nil), result.Repositories...)
	copy := *prepared.PRTarget
	copy.HeadRepository.ID = "99"
	mutated.Repositories[0].PRTarget = &copy
	if ValidateResult(f.request, mutated, runtime.GOOS) == nil {
		t.Fatal("changed source accepted for execution")
	}
	mutated.Repositories[0].PRTarget = nil
	if ValidateResult(f.request, mutated, runtime.GOOS) == nil {
		t.Fatal("missing original source accepted")
	}
	if f.request.Repositories[0].PRTarget.HeadRepository.ID != "9007199254740993" {
		t.Fatal("result aliases input")
	}
}

func TestPRPreparationRejectsChangedRemoteWithoutAlteringOriginalCheckout(t *testing.T) {
	for _, scenario := range []string{"move", "base-move", "remote-change", "fail", "deleted", "foreign", "multiple", "rewrite", "invalid-ref", "partial-project"} {
		t.Run(scenario, func(t *testing.T) {
			f := newPRPreparationFixture(t)
			if err := os.WriteFile(filepath.Join(f.checkout, "unrelated"), []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "move", "base-move", "remote-change", "fail":
				os.WriteFile(f.marker, []byte(scenario), 0600)
			case "deleted":
				gitTest(t, f.fork, "update-ref", "-d", "refs/heads/feature")
			case "foreign":
				gitTest(t, f.checkout, "remote", "set-url", "origin", "https://github.com/other/repo.git")
			case "multiple":
				gitTest(t, f.checkout, "remote", "set-url", "--add", "origin", "https://github.com/fixture-owner/repo.git")
			case "rewrite":
				gitTest(t, f.checkout, "config", "url.https://other.invalid/.insteadOf", "https://github.com/fixture-author/")
			case "invalid-ref":
				f.request.Repositories[0].PRTarget.HeadRef = "feature:other"
			case "partial-project":
				f.request.Repositories = append(f.request.Repositories, RepositorySpec{ID: domain.NewID(), Checkout: f.checkout, Starting: domain.Reference{Type: domain.LocalBranch, Name: "missing"}})
			}
			beforeRefs := gitTest(t, f.checkout, "show-ref")
			_, err := f.manager.Prepare(context.Background(), f.request)
			if err == nil {
				t.Fatal("invalid or moved target accepted")
			}
			if gitTest(t, f.checkout, "show-ref") != beforeRefs || gitTest(t, f.checkout, "rev-parse", "HEAD") != f.base || strings.Count(gitTest(t, f.checkout, "worktree", "list", "--porcelain"), "worktree ") != 1 {
				t.Fatal("original refs changed or partial worktree survived")
			}
			value, err := os.ReadFile(filepath.Join(f.checkout, "unrelated"))
			if err != nil || string(value) != "keep" {
				t.Fatal("unrelated file changed")
			}
			if _, err = os.Stat(filepath.Join(f.manager.Root, "workspaces", string(f.request.SessionID))); !os.IsNotExist(err) {
				t.Fatal("failed preparation not cleaned", err)
			}
			if scenario == "foreign" || scenario == "multiple" || scenario == "invalid-ref" {
				if _, err := os.Stat(f.log); !os.IsNotExist(err) {
					t.Fatal("invalid target acquired network access")
				}
			}
		})
	}
}

func TestPRPreparationRejectsMixedOrLocalInputsBeforeGit(t *testing.T) {
	f := newPRPreparationFixture(t)
	for _, scenario := range []string{"local", "repository", "no-fetch", "base", "starting", "two-targets"} {
		r := f.request
		r.Repositories = append([]RepositorySpec(nil), f.request.Repositories...)
		switch scenario {
		case "local":
			r.Type = domain.Local
		case "repository":
			r.Repositories[0].ID = domain.NewID()
		case "no-fetch":
			r.Repositories[0].AutoFetch = false
		case "base":
			r.Repositories[0].Base = domain.Reference{Type: domain.LocalBranch, Name: "main"}
		case "starting":
			r.Repositories[0].Starting.Name = f.base
		case "two-targets":
			r.Repositories = append(r.Repositories, r.Repositories[0])
		}
		if _, err := f.manager.Prepare(context.Background(), r); err == nil {
			t.Fatal("mixed input accepted", scenario)
		}
	}
	if _, err := os.Stat(f.log); !os.IsNotExist(err) {
		t.Fatal("invalid preparation acquired network access")
	}
}

func TestPRPreparationUsesSelectedSSHTransportWithoutChangingRemotes(t *testing.T) {
	for _, address := range []string{"git@github.com:fixture-owner/repo.git", "ssh://git@github.com/fixture-owner/repo"} {
		t.Run(address, func(t *testing.T) {
			f := newPRPreparationFixture(t)
			gitTest(t, f.checkout, "remote", "set-url", "origin", address)
			result, err := f.manager.Prepare(context.Background(), f.request)
			if err != nil || ValidateResult(f.request, result, runtime.GOOS) != nil {
				t.Fatal("SSH transport preparation", err)
			}
			if got := gitTest(t, f.checkout, "remote", "get-url", "origin"); got != address {
				t.Fatal("native remote configuration changed")
			}
			if got := gitTest(t, result.PrimaryPath, "rev-parse", "HEAD"); got != f.head {
				t.Fatal("wrong fork head")
			}
		})
	}
}

func TestPRPreparationCancellationJoinsFetchBeforeRollback(t *testing.T) {
	f := newPRPreparationFixture(t)
	script, err := os.ReadFile(f.manager.Git.Executable)
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(filepath.Dir(f.marker), "fetch-ready")
	barrier := "  if [ \"$fetch\" = yes ]; then\n    printf ready > " + fixtureShellQuote(ready) + "\n    sleep 60\n  fi\n  for arg do\n"
	if err := os.WriteFile(f.manager.Git.Executable, []byte(strings.Replace(string(script), "  for arg do\n", barrier, 1)), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := f.manager.Prepare(ctx, f.request); done <- err }()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	waiting := true
	for waiting {
		select {
		case err := <-done:
			t.Fatal("preparation ended before original fetch barrier", err)
		case <-deadline.C:
			cancel()
			t.Fatal("fetch barrier timed out")
		case <-ticker.C:
			if _, err := os.Stat(ready); err == nil {
				waiting = false
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		if domain.SafeError(err).Code != domain.Canceled {
			t.Fatal("original cancellation classification", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("canceled fetch was not joined")
	}
	if _, err := os.Stat(filepath.Join(f.manager.Root, "workspaces", string(f.request.SessionID))); !os.IsNotExist(err) {
		t.Fatal("canceled preparation not rolled back", err)
	}
	if gitTest(t, f.checkout, "rev-parse", "HEAD") != f.base || strings.Count(gitTest(t, f.checkout, "worktree", "list", "--porcelain"), "worktree ") != 1 {
		t.Fatal("cancellation changed original ownership")
	}
}
