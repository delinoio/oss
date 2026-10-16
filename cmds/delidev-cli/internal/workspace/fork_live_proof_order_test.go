// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// recordingGit returns a Git whose every invocation is appended to a log as
// "<cwd> <args>", so tests can prove which paths a phase touched.
func recordingGit(t *testing.T, git Git) (Git, func() []string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "git.log")
	script := "#!/bin/sh\nprintf '%s %s\\n' \"$(pwd)\" \"$*\" >> " + fixtureShellQuote(logPath) + "\nexec " + fixtureShellQuote(real) + " \"$@\"\n"
	git.Executable = filepath.Join(t.TempDir(), "recording-git")
	if err = os.WriteFile(git.Executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return git, func() []string {
		raw, err := os.ReadFile(logPath)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
}

func requireNoGitTouch(t *testing.T, calls []string, paths ...string) {
	t.Helper()
	for _, call := range calls {
		for _, path := range paths {
			if path != "" && strings.Contains(call, path) {
				t.Fatalf("over-budget verification ran Git against %s: %s", path, call)
			}
		}
	}
}

func TestForkLiveConfigProofBytesAdmitsAtTheVerificationBoundary(t *testing.T) {
	snapshot := []forkCopy{presentProofCopy("/a", 1000, true, true)}
	prepared := []forkCopy{presentProofCopy("/a", 1000, true, true)}
	total := uint64(1000) + 2*forkConfigSliceHeaderBytes
	for _, tc := range []struct {
		name  string
		limit uint64
		code  domain.Code
	}{
		{"one byte below", 4096 + total - 1, domain.Conflict},
		{"exact", 4096 + total, ""},
		{"one byte above", 4096 + total + 1, ""},
		// Omitting the reusing holder's slice headers would have admitted this.
		{"headers omitted would have fit", 4096 + 1000, domain.Conflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setForkVerifyByteLimit(t, tc.limit)
			got, err := forkLiveConfigProofBytes(snapshot, prepared)
			if tc.code == "" {
				if err != nil || got != total {
					t.Fatalf("fitting total was rejected: %d, %v", got, err)
				}
				return
			}
			if domain.SafeError(err).Code != tc.code {
				t.Fatalf("over-budget total was not a snapshot conflict: %d, %v", got, err)
			}
		})
	}
}

func TestIndependentForkVerifyAdmitsLiveProofsBeforeAnyGitCommand(t *testing.T) {
	m, source, child, proof, _ := forkInventoryCopyFixtureWith(t, false, retainedConfigSetup)
	git := m.Git
	git.OwnerID = child.SessionID
	git, calls := recordingGit(t, git)
	own := proof.gitInventory.configProof.retainedBytes
	var overBudget uint64 = maxForkBytes - 4095
	if err := proof.gitInventory.verify(context.Background(), git, source, proof.target, overBudget); domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("over-budget verification was not a snapshot conflict: %v", err)
	}
	requireNoGitTouch(t, calls(), source, proof.target)
	// The same copy verifies when the live total fits, and then does run Git.
	if err := proof.gitInventory.verify(context.Background(), git, source, proof.target, own); err != nil {
		t.Fatalf("fitting verification was rejected: %v", err)
	}
	if len(calls()) == 0 {
		t.Fatal("recording Git saw no command, so the ordering assertion is vacuous")
	}
}

func TestIndependentForkVerifyReportsConflictForCorruptChildWhenOverBudget(t *testing.T) {
	m, source, child, proof, _ := forkInventoryCopyFixtureWith(t, false, retainedConfigSetup)
	git := m.Git
	git.OwnerID = child.SessionID
	// Git cannot parse this config, so child access would fail with a Git error.
	if err := os.WriteFile(filepath.Join(proof.target, ".git", "config"), []byte("[broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	own := proof.gitInventory.configProof.retainedBytes
	if err := proof.gitInventory.verify(context.Background(), git, source, proof.target, own); err == nil {
		t.Fatal("corrupt child config was accepted")
	}
	if err := proof.gitInventory.verify(context.Background(), git, source, proof.target, maxForkBytes); domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("over-budget verification surfaced a child config error: %v", err)
	}
}

func TestIndependentForkVerifyMapsChildInventoryOverflowToConflict(t *testing.T) {
	m, source, child, proof, _ := forkInventoryCopyFixtureWith(t, false, retainedConfigSetup)
	git := m.Git
	git.OwnerID = child.SessionID
	own := proof.gitInventory.configProof.retainedBytes
	probe, err := newForkVerifyBudget(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = walkSnapshotBudget(context.Background(), filepath.Join(proof.target, ".git"), "", nil, maxForkEntries, probe); err != nil {
		t.Fatal(err)
	}
	used := maxForkBytes - 4096 - probe.bytes
	for _, tc := range []struct {
		name  string
		limit uint64
	}{{"inventory one byte beyond the remaining allowance", 4096 + own + used - 1}, {"inventory beyond the live proofs alone", 4096 + own}} {
		t.Run(tc.name, func(t *testing.T) {
			setForkVerifyByteLimit(t, tc.limit)
			if err := proof.gitInventory.verify(context.Background(), git, source, proof.target, own); domain.SafeError(err).Code != domain.Conflict {
				t.Fatalf("child inventory beyond the allowance was not snapshot drift: %v", err)
			}
		})
	}
}

func TestForkCopyVerifyAdmitsLiveProofsBeforeChildAccess(t *testing.T) {
	m, source, child, proof, _ := forkInventoryCopyFixtureWith(t, false, retainedConfigSetup)
	git := m.Git
	git.OwnerID = child.SessionID
	git, calls := recordingGit(t, git)
	if err := proof.verify(context.Background(), git, maxForkBytes); domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("copy verification ignored an over-budget live total: %v", err)
	}
	requireNoGitTouch(t, calls(), source, proof.target)
}

func TestForkSnapshotVerifyAdmitsLiveProofsBeforeAnyGitCommand(t *testing.T) {
	m, source, child, proof, _ := forkInventoryCopyFixtureWith(t, false, retainedConfigSetup)
	git := m.Git
	git.OwnerID = child.SessionID
	git, calls := recordingGit(t, git)
	snapshot := &ForkSnapshot{git: git, copies: []forkCopy{proof}}
	snapshot.copies[0].source = source
	setForkVerifyByteLimit(t, 4096+proof.gitInventory.configProof.retainedBytes-1)
	if err := snapshot.Verify(context.Background(), child); domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("snapshot verification ignored an over-budget live total: %v", err)
	}
	requireNoGitTouch(t, calls(), source, proof.target)
}

func TestPrepareForkGitInventoryAdmitsLiveProofsBeforeCreatingChild(t *testing.T) {
	input := forkInventoryFixtureInputWith(t, false, retainedConfigSetup)
	git, calls := recordingGit(t, input.git)
	child := input.manifest.Repositories[0].Path
	prior := []forkCopy{syntheticProofCopy("/earlier-a", maxForkBytes/2), syntheticProofCopy("/earlier-b", maxForkBytes/2)}
	_, _, err := input.m.prepareForkGitInventory(context.Background(), git, input.spec, &input.manifest, func() error { return nil }, newForkCopyBudget(1), nil, prior)
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("earlier prepared proofs were not deducted: %v", err)
	}
	requireNoGitTouch(t, calls(), child)
	if _, statErr := os.Lstat(filepath.Join(child, ".git")); !os.IsNotExist(statErr) {
		t.Fatalf("over-budget preparation created the child Git directory: %v", statErr)
	}
}

func TestReadChildForkConfigRejectsBelowMinimumProofBeforeGit(t *testing.T) {
	m, _, child, proof, _ := forkInventoryCopyFixtureWith(t, false, retainedConfigSetup)
	git := m.Git
	git.OwnerID = child.SessionID
	git, calls := recordingGit(t, git)
	_, _, err := readChildForkConfig(context.Background(), git, proof.target, "config", forkConfigSliceHeaderBytes-1)
	if domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatalf("allowance below one slice header was not exhausted: %v", err)
	}
	if got := calls(); len(got) != 0 {
		t.Fatalf("Git parsed the child config despite an exhausted allowance: %v", got)
	}
}
