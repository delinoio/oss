package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestPRFirstExecutionRechecksRemoteAndPreservesOriginalPreparation(t *testing.T) {
	for _, scenario := range []string{"head-moved", "base-moved", "deleted", "dirty", "untracked", "ignored", "worktree-remote"} {
		t.Run(scenario, func(t *testing.T) {
			f := newPRPreparationFixture(t)
			manifest, err := f.manager.Prepare(context.Background(), f.request)
			if err != nil {
				t.Fatal(err)
			}
			root := manifest.PrimaryPath
			before, err := os.ReadFile(filepath.Join(f.manager.Root, "workspaces", string(f.request.SessionID), "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			repair := func() {}
			switch scenario {
			case "head-moved":
				gitTest(t, f.fork, "update-ref", "refs/heads/feature", f.next)
				repair = func() { gitTest(t, f.fork, "update-ref", "refs/heads/feature", f.head) }
			case "base-moved":
				gitTest(t, f.source, "update-ref", "refs/heads/main", f.head)
				repair = func() { gitTest(t, f.source, "update-ref", "refs/heads/main", f.base) }
			case "deleted":
				gitTest(t, f.fork, "update-ref", "-d", "refs/heads/feature")
				repair = func() { gitTest(t, f.fork, "update-ref", "refs/heads/feature", f.head) }
			case "dirty":
				os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("unrelated local edit\n"), 0600)
				repair = func() { os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("PR head\n"), 0600) }
			case "untracked", "ignored":
				os.WriteFile(filepath.Join(root, "unrelated"), []byte("preserve unrelated\n"), 0600)
				if scenario == "ignored" {
					os.WriteFile(filepath.Join(f.checkout, ".git", "info", "exclude"), []byte("unrelated\n"), 0600)
				}
				repair = func() { os.Remove(filepath.Join(root, "unrelated")) }
			case "worktree-remote":
				gitTest(t, root, "config", "extensions.worktreeConfig", "true")
				gitTest(t, root, "config", "--worktree", "remote.origin.url", "https://github.com/other/repo.git")
				repair = func() { gitTest(t, root, "config", "--worktree", "--unset", "remote.origin.url") }
			}
			job, execution := domain.NewID(), domain.NewID()
			_, firstErr := f.manager.ClaimFirstExecution(context.Background(), job, execution, f.request, manifest)
			var rejected *prStartupRejection
			if !errors.As(firstErr, &rejected) || rejected.record.Phase != prStartupRejected {
				t.Fatal("changed original PR lacks a retained pre-native rejection", firstErr)
			}
			proof, err := f.manager.ReadPRStartupRejection(context.Background(), job, execution, f.request, manifest, runtime.GOOS)
			if err != nil || !MatchesPRStartupRejection(firstErr, proof) || ValidatePRStartupRejection(f.request, manifest, proof, runtime.GOOS) != nil {
				t.Fatal("original rejection comparison failed", err)
			}
			changedProof := proof
			changedProof.Reason = domain.Canceled
			if changedProof.Reason == proof.Reason {
				changedProof.Reason = domain.Conflict
			}
			if ValidatePRStartupRejection(f.request, manifest, changedProof, runtime.GOOS) == nil || MatchesPRStartupRejection(firstErr, changedProof) || MatchesPRStartupRejection(domain.SafeError(firstErr), proof) {
				t.Fatal("changed or reconstructed rejection gained proof authority")
			}
			if _, err := os.Stat(f.manager.executionClaimPath(f.request.SessionID)); !os.IsNotExist(err) {
				t.Fatal("failed preflight created an execution claim", err)
			}
			if _, err := os.Stat(filepath.Join(f.manager.Git.ProcessRoot, string(job))); !os.IsNotExist(err) {
				t.Fatal("failed preflight created a native execution process scope", err)
			}
			after, err := os.ReadFile(filepath.Join(f.manager.Root, "workspaces", string(f.request.SessionID), "manifest.json"))
			if err != nil || string(before) != string(after) {
				t.Fatal("preflight rewrote original preparation", err)
			}
			if scenario == "dirty" {
				raw, err := os.ReadFile(filepath.Join(root, "tracked.txt"))
				if err != nil || string(raw) != "unrelated local edit\n" {
					t.Fatal("dirty bytes discarded")
				}
			}
			if scenario == "untracked" || scenario == "ignored" {
				raw, err := os.ReadFile(filepath.Join(root, "unrelated"))
				if err != nil || string(raw) != "preserve unrelated\n" {
					t.Fatal("unrelated file discarded")
				}
			}
			repair()
			journalPath := f.manager.prStartupPath(f.request.SessionID, execution)
			retained := readPRStartupTestFile(t, journalPath)
			network := readPRStartupTestFile(t, f.log)
			// Restoration cannot revive the original execution, including after
			// manager replacement. Its immutable rejection performs no new Git IO.
			for _, manager := range []*Manager{f.manager, {Root: f.manager.Root, Git: f.manager.Git}} {
				_, err := manager.ClaimFirstExecution(context.Background(), job, execution, f.request, manifest)
				var replay *prStartupRejection
				if !errors.As(err, &replay) || !reflect.DeepEqual(replay.record, rejected.record) {
					t.Fatal("original rejection was not retained", err)
				}
				if readPRStartupTestFile(t, journalPath) != retained || readPRStartupTestFile(t, f.log) != network {
					t.Fatal("original rejection replay rewrote evidence or repeated networking")
				}
			}
			// A distinct private claim needs new identities and current reads.
			// This is not public authorization to resend an accepted server job.
			lease, err := f.manager.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), f.request, manifest)
			if err != nil {
				t.Fatal("restored original preflight", err)
			}
			if err = lease.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPRFirstExecutionPreflightIsReadOnlyAndDisablesFSMonitor(t *testing.T) {
	f := newPRPreparationFixture(t)
	manifest, err := f.manager.Prepare(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(filepath.Dir(f.marker), "fsmonitor-ran")
	hook := filepath.Join(filepath.Dir(f.marker), "fsmonitor")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nprintf ran > "+fixtureShellQuote(marker)+"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	gitTest(t, manifest.PrimaryPath, "config", "core.fsmonitor", hook)
	refs := gitTest(t, f.checkout, "show-ref")
	fetchHead, err := os.ReadFile(filepath.Join(f.checkout, ".git", "FETCH_HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(f.checkout, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.log, nil, 0600); err != nil {
		t.Fatal(err)
	}
	lease, err := f.manager.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), f.request, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("native fsmonitor ran during PR preflight")
	}
	if gitTest(t, f.checkout, "show-ref") != refs {
		t.Fatal("preflight changed refs")
	}
	for path, expected := range map[string]string{filepath.Join(f.checkout, ".git", "FETCH_HEAD"): string(fetchHead), filepath.Join(f.checkout, ".git", "index"): string(index)} {
		raw, err := os.ReadFile(path)
		if err != nil || string(raw) != expected {
			t.Fatal("read-only preflight changed original Git metadata", filepath.Base(path), err)
		}
	}
	log, err := os.ReadFile(f.log)
	if err != nil || strings.Count(string(log), "network:no\n") != 2 || strings.Contains(string(log), "network:yes") {
		t.Fatal("preflight fetched or skipped current remote operands", string(log), err)
	}
}

func TestPRFirstExecutionRejectsBranchChangeDuringRemoteRead(t *testing.T) {
	f := newPRPreparationFixture(t)
	manifest, err := f.manager.Prepare(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(f.manager.Git.Executable)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(filepath.Dir(f.marker), "switched")
	change := "  if [ ! -f " + fixtureShellQuote(marker) + " ]; then\n    " + fixtureShellQuote(git) + " -C " + fixtureShellQuote(manifest.PrimaryPath) + " switch -c late-branch || exit $?\n    printf switched > " + fixtureShellQuote(marker) + "\n  fi\n  for arg do\n"
	if err = os.WriteFile(f.manager.Git.Executable, []byte(strings.Replace(string(script), "  for arg do\n", change, 1)), 0700); err != nil {
		t.Fatal(err)
	}
	job := domain.NewID()
	if _, err = f.manager.ClaimFirstExecution(context.Background(), job, domain.NewID(), f.request, manifest); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("branch changed during remote read without blocking first execution", err)
	}
	if _, err = os.Stat(f.manager.executionClaimPath(f.request.SessionID)); !os.IsNotExist(err) {
		t.Fatal("late branch change published claim")
	}
	if gitTest(t, manifest.PrimaryPath, "branch", "--show-current") != "late-branch" {
		t.Fatal("preflight replaced changed branch")
	}
}
