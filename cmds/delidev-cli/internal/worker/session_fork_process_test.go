// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestRejectedForkRetiresOriginalChildHEADReadOwners(t *testing.T) {
	for _, scenario := range []string{"symlink", "bytes", "head-conflict"} {
		t.Run(scenario, func(t *testing.T) {
			c, _, source, _ := deletionWorkerFixture(t, domain.Local)
			manager := workspace.Manager{Root: c.Root, Logger: c.Logger}
			original, _ := os.ReadFile(filepath.Join(source.PrimaryPath, "tracked.txt"))
			for range 2 {
				child := domain.NewID()
				prep, err := manager.ForkPreparation(context.Background(), source, child, domain.Worktree)
				if err != nil {
					t.Fatal(err)
				}
				ownerPath := filepath.Join(c.Root, "processes", string(child))
				identity, err := os.Lstat(ownerPath)
				if err != nil {
					t.Fatal(err)
				}
				bad := filepath.Join(source.PrimaryPath, "unsupported")
				switch scenario {
				case "symlink":
					if err := os.Symlink("tracked.txt", bad); err != nil {
						t.Skip(err)
					}
				case "bytes":
					f, err := os.OpenFile(bad, os.O_CREATE|os.O_WRONLY, 0600)
					if err != nil {
						t.Fatal(err)
					}
					err = f.Truncate(257 << 20)
					f.Close()
					if err != nil {
						t.Fatal(err)
					}
				case "head-conflict":
					prep.Repositories[0].Base.Name = "0000000000000000000000000000000000000000"
				}
				_, err = manager.InspectForkSnapshot(context.Background(), source, prep)
				if err == nil || domain.SafeError(err).Code == domain.RecoveryRequired {
					t.Fatal("expected definite snapshot rejection", err)
				}
				expected := domain.SafeError(err).Code
				err = finishForkChildProcessFailure(c.Root, child, identity, forkRuntimeUnused, err)
				if domain.SafeError(err).Code != expected {
					t.Fatal("lost definite result", err)
				}
				for _, p := range []string{ownerPath, ownerPath + ".recovery.lock", filepath.Join(c.Root, "workspaces", string(child))} {
					if _, err := os.Lstat(p); !os.IsNotExist(err) {
						t.Fatal("retained unpublished owner", p, err)
					}
				}
				if scenario != "head-conflict" {
					if err := os.Remove(bad); err != nil {
						t.Fatal(err)
					}
				}
			}
			after, _ := os.ReadFile(filepath.Join(source.PrimaryPath, "tracked.txt"))
			if string(original) != string(after) {
				t.Fatal("source changed")
			}
		})
	}
}

func TestForkChildRetirementPreservesReplacedOrUncertainOwners(t *testing.T) {
	for _, scenario := range []string{"replacement", "malformed", "busy", "native-possible", "recovery"} {
		t.Run(scenario, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "worker")
			owner := domain.NewID()
			path := filepath.Join(root, "processes", string(owner))
			if err := security.PrivateDir(path); err != nil {
				t.Fatal(err)
			}
			original, _ := os.Lstat(path)
			phase := forkRuntimeUnused
			problem := error(domain.Fail(domain.Unsupported, "Rejected.", "Preserve the source."))
			switch scenario {
			case "replacement":
				if err := os.Rename(path, path+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(filepath.Join(path, "foreign"), []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
			case "busy":
				lock, err := security.TryLock(path + ".recovery.lock")
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			case "native-possible":
				phase = forkChildNativePossible
			case "recovery":
				problem = executionCheckpointUncertain()
			}
			err := finishForkChildProcessFailure(root, owner, original, phase, problem)
			if scenario != "native-possible" && domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("uncertainty lost", err)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatal("uncertain owner removed", err)
			}
		})
	}
}

func TestDeletionRetiresFailedForkChildOwnerAndRejectsReappearance(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	child := domain.NewID()
	job := domain.NewID()
	runtimeID := domain.NewID()
	index := filepath.Join(c.Root, "processes", string(child))
	executable, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Run(context.Background(), process.Config{Directory: filepath.Join(c.Root, "processes"), OwnerID: child, Executable: executable, Args: []string{"--version"}, Env: os.Environ(), Cwd: c.Root, Logger: c.Logger}); err != nil {
		t.Fatal(err)
	}
	copy := domain.SessionDeletionCopy{JobID: job, Type: domain.ForkSessionJob, Revision: 2, InstanceID: w.Copies[0].InstanceID, Digest: w.Copies[0].Digest, ExecutionID: runtimeID, UnpublishedChildProcessID: child}
	if err := writeJSON(filepath.Join(c.Root, "jobs", string(job)+".json"), journal{Version: 1, JobID: job, InstanceID: copy.InstanceID, Revision: copy.Revision, Digest: copy.Digest, State: journalReported, ReportID: domain.NewID(), Problem: domain.Fail(domain.Unsupported, "Rejected.", "Preserve the source.")}); err != nil {
		t.Fatal(err)
	}
	w.Copies = append(w.Copies, copy)
	proof, err := deleteSessionCopies(context.Background(), c, w)
	if err != nil || !proof.Complete {
		t.Fatal(proof, err)
	}
	for _, path := range []string{index, index + ".recovery.lock"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("child owner retained", err)
		}
	}
	again, err := deleteSessionCopies(context.Background(), c, w)
	if err != nil || again.ReportID != proof.ReportID {
		t.Fatal("replay failed", err)
	}
	if err := os.Mkdir(index, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteSessionCopies(context.Background(), c, w); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("restored owner adopted", err)
	}
	if _, err := os.Stat(index); err != nil {
		t.Fatal("restored owner removed", err)
	}
}

func TestLegacyForkDeletionProofCannotRebuildRemovedEvidence(t *testing.T) {
	for _, scenario := range []string{"untouched", "started", "complete", "published"} {
		t.Run(scenario, func(t *testing.T) {
			c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
			child, job := domain.NewID(), domain.NewID()
			copy := domain.SessionDeletionCopy{JobID: job, Type: domain.ForkSessionJob, Revision: 2, InstanceID: w.Copies[0].InstanceID, Digest: w.Copies[0].Digest, ExecutionID: domain.NewID()}
			w.Copies = append(w.Copies, copy)
			originalProof := sessionDeletionProof{Version: 1, Digest: w.Digest(), ReportID: domain.NewID(), RemovalStarted: scenario == "started" || scenario == "complete", Complete: scenario == "complete"}
			if err := security.PrivateDir(filepath.Join(c.Root, "session-deletions")); err != nil {
				t.Fatal(err)
			}
			if err := writeJSON(sessionDeletionPath(c.Root, w.SessionID), originalProof); err != nil {
				t.Fatal(err)
			}
			executable, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			if err := process.Run(context.Background(), process.Config{Directory: filepath.Join(c.Root, "processes"), OwnerID: child, Executable: executable, Args: []string{"--version"}, Env: os.Environ(), Cwd: c.Root, Logger: c.Logger}); err != nil {
				t.Fatal(err)
			}
			j := journal{Version: 1, JobID: job, InstanceID: copy.InstanceID, Revision: 2, Digest: copy.Digest, State: journalReported, ReportID: domain.NewID(), Problem: domain.Fail(domain.Unsupported, "Rejected.", "Preserve the source.")}
			if scenario == "published" {
				j.Problem = nil
				j.Output = []byte(`{"published":true}`)
			}
			if err := writeJSON(filepath.Join(c.Root, "jobs", string(job)+".json"), j); err != nil {
				t.Fatal(err)
			}
			w.Copies[1].UnpublishedChildProcessID = child
			proof, err := deleteSessionCopies(context.Background(), c, w)
			if scenario == "untouched" {
				if err != nil || !proof.Complete || proof.ReportID != originalProof.ReportID {
					t.Fatal("original proof did not rebind", err)
				}
				return
			}
			if domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("missing original proof accepted", err)
			}
			if _, err := os.Lstat(filepath.Join(c.Root, "processes", string(child))); err != nil {
				t.Fatal("unproved child removed", err)
			}
		})
	}
}

func TestLegacyFailedForkWithoutOriginalChildProofStaysPending(t *testing.T) {
	c, w, manifest, _ := deletionWorkerFixture(t, domain.GeneralChat)
	w.Copies = append(w.Copies, domain.SessionDeletionCopy{JobID: domain.NewID(), Type: domain.ForkSessionJob, Revision: 2, Digest: w.Copies[0].Digest, InstanceID: w.Copies[0].InstanceID, ExecutionID: domain.NewID()})
	if _, err := deleteSessionCopies(context.Background(), c, w); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("unproved legacy Fork accepted", err)
	}
	if _, err := os.Stat(manifest.PrimaryPath); err != nil {
		t.Fatal("source removed before owner proof", err)
	}
}
