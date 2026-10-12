// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

// Historical failed preparations have distinct retained jobs on one machine,
// with positive no-workspace outcomes. No duplicate live storage is fabricated.
func failedDeletionObligations(t *testing.T) (Config, []domain.SessionDeletionWork) {
	t.Helper()
	config := Config{Root: filepath.Join(t.TempDir(), "worker"), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := security.PrivateDir(config.Root); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(config.Root, "jobs")); err != nil {
		t.Fatal(err)
	}
	first := domain.SessionDeletionWork{Version: 1, ServerID: domain.NewID(), SessionID: domain.NewID(), DeletionID: domain.NewID(), MachineID: domain.NewID()}
	works := []domain.SessionDeletionWork{}
	for range 2 {
		work := first
		work.DeviceID = domain.NewID()
		copy := domain.SessionDeletionCopy{JobID: domain.NewID(), Type: domain.PrepareWorkspaceJob, Revision: 1, InstanceID: domain.NewID(), Digest: strings.Repeat("a", 64)}
		work.Copies = []domain.SessionDeletionCopy{copy}
		if work.Validate() != nil {
			t.Fatal("invalid original obligation")
		}
		j := journal{Version: 1, JobID: copy.JobID, InstanceID: copy.InstanceID, Revision: copy.Revision, Digest: copy.Digest, State: journalReported, ReportID: domain.NewID(), Problem: domain.Fail(domain.Unsupported, "Preparation rejected.", "No workspace was created.")}
		if err := writeJSON(filepath.Join(config.Root, "jobs", string(copy.JobID)+".json"), j); err != nil {
			t.Fatal(err)
		}
		works = append(works, work)
	}
	owner := works[0].DeviceID
	if string(works[1].DeviceID) < string(owner) {
		owner = works[1].DeviceID
	}
	for i := range works {
		works[i].SharedCleanupOwnerDeviceID = owner
	}
	return config, works
}

func TestSessionDeletionIndependentOriginalDevicesRetainDistinctReceipts(t *testing.T) {
	config, works := failedDeletionObligations(t)
	sentinel := filepath.Join(config.Root, "unrelated")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	receipts := []domain.ID{}
	for _, work := range works {
		proof, err := deleteSessionCopies(context.Background(), config, work)
		if err != nil || !proof.Complete || !proof.WorkspaceRetired {
			t.Fatal("independent cleanup failed", err)
		}
		receipts = append(receipts, proof.ReportID)
		paths, _ := workspace.SessionDeletionProofPaths(config.Root, work)
		if _, err := os.Lstat(paths.Worker); err != nil {
			t.Fatal("original progress missing", err)
		}
		if _, err := os.Lstat(paths.Workspace); !os.IsNotExist(err) {
			t.Fatal("workspace proof survived retirement", err)
		}
	}
	if receipts[0] == receipts[1] {
		t.Fatal("devices shared a report receipt")
	}
	for i, work := range works {
		proof, err := deleteSessionCopies(context.Background(), config, work)
		if err != nil || proof.ReportID != receipts[i] {
			t.Fatal("replay changed original receipt", err)
		}
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "preserve" {
		t.Fatal("unrelated data changed", err)
	}
	first, _ := workspace.SessionDeletionProofPaths(config.Root, works[0])
	second, _ := workspace.SessionDeletionProofPaths(config.Root, works[1])
	raw, err := security.ReadPrivate(first.Worker, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(second.Worker, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteSessionCopies(context.Background(), config, works[1]); err == nil {
		t.Fatal("foreign device proof was acknowledged")
	}
}

func TestSessionDeletionSharedCopiesHaveOneDeterministicOwner(t *testing.T) {
	config, works := failedDeletionObligations(t)
	owner := works[0].DeviceID
	if string(works[1].DeviceID) < string(owner) {
		owner = works[1].DeviceID
	}
	for i := range works {
		works[i].SharedCleanupOwnerDeviceID = owner
	}
	shared := filepath.Join(config.Root, "execution-history", string(works[0].SessionID))
	if err := os.MkdirAll(shared, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(shared, "original")
	if err := os.WriteFile(marker, []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ownerIndex := 0
	if works[1].DeviceID == owner {
		ownerIndex = 1
	}
	otherIndex := 1 - ownerIndex

	if _, err := deleteSessionCopies(context.Background(), config, works[ownerIndex]); err != nil {
		t.Fatal("shared-path owner could not finish", err)
	}
	if _, err := os.Lstat(shared); !os.IsNotExist(err) {
		t.Fatal("shared-path owner did not remove the original path", err)
	}
	if err := os.MkdirAll(shared, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("restored"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteSessionCopies(context.Background(), config, works[otherIndex]); err != nil {
		t.Fatal("non-owner cleanup failed", err)
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "restored" {
		t.Fatal("non-owner removed a restored shared path", err)
	}
}

func TestLegacySessionDeletionWithoutSharedOwnerStaysPending(t *testing.T) {
	config, works := failedDeletionObligations(t)
	shared := filepath.Join(config.Root, "execution-history", string(works[0].SessionID))
	if err := os.MkdirAll(shared, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(shared, "restored")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := range works {
		// An omitted field models a durable plan written before explicit shared
		// cleanup ownership was added.
		works[i].SharedCleanupOwnerDeviceID = ""
		if _, err := deleteSessionCopies(context.Background(), config, works[i]); err == nil {
			t.Fatal("legacy plan without a shared owner was admitted")
		}
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "preserve" {
		t.Fatal("legacy cleanup removed a restored shared path", err)
	}
}

func TestSessionDeletionLegacyExactStagesAndInterruptedPairs(t *testing.T) {
	for _, stage := range []string{"untouched", "started", "complete", "missing-started-workspace", "missing-complete-workspace", "equal-pair", "changed-pair", "foreign-digest", "foreign-receipt", "missing-fence"} {
		t.Run(stage, func(t *testing.T) {
			config, works := failedDeletionObligations(t)
			work := works[0]
			if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
				t.Fatal(err)
			}
			legacy := sessionDeletionProof{Version: 1, Digest: work.Digest(), ReportID: domain.NewID(), RemovalStarted: stage == "started" || stage == "complete" || stage == "missing-started-workspace" || stage == "missing-complete-workspace", Complete: stage == "complete" || stage == "missing-complete-workspace"}
			expected := legacy
			paths, _ := workspace.SessionDeletionProofPaths(config.Root, work)
			if stage == "started" || stage == "complete" || stage == "equal-pair" {
				legacyWorkspacePath := filepath.Join(config.Root, "session-deletions", string(work.SessionID)+"-workspace.json")
				if err := writeJSON(legacyWorkspacePath, map[string]any{"version": 1, "digest": work.Digest(), "removed": stage == "started" || stage == "complete"}); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "foreign-digest" {
				legacy.Digest = works[1].Digest()
			}
			if err := writeJSON(sessionDeletionPath(config.Root, work.SessionID), legacy); err != nil {
				t.Fatal(err)
			}
			if stage == "equal-pair" || stage == "changed-pair" || stage == "foreign-receipt" || stage == "missing-fence" {
				keyed := expected
				keyed.Version = 2
				keyed.Identity = work.Identity()
				if stage == "changed-pair" {
					keyed.RemovalStarted = true
				}
				if stage == "foreign-receipt" {
					keyed.ReportID = domain.NewID()
				}
				if err := writeJSON(paths.Worker, keyed); err != nil {
					t.Fatal(err)
				}
				if stage == "missing-fence" {
					if err := os.Remove(sessionDeletionPath(config.Root, work.SessionID)); err != nil {
						t.Fatal(err)
					}
				}
			}
			proof, _, lock, err := loadSessionDeletionProof(config.Root, work)
			accepted := stage == "untouched" || stage == "started" || stage == "complete" || stage == "equal-pair"
			if lock != nil {
				lock.Close()
			}
			if (err == nil) != accepted {
				t.Fatalf("legacy admission %s: %v", stage, err)
			}
			if accepted && (proof.ReportID != expected.ReportID || proof.RemovalStarted != expected.RemovalStarted || proof.Complete != expected.Complete || proof.Identity != work.Identity()) {
				t.Fatal("legacy identity/stages changed")
			}
		})
	}
}

func TestSessionDeletionCompletedProofRejectsRestoredWorkspaceProof(t *testing.T) {
	config, works := failedDeletionObligations(t)
	work := works[0]
	proof, err := deleteSessionCopies(context.Background(), config, work)
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := workspace.SessionDeletionProofPaths(config.Root, work)
	restored := map[string]any{"version": 2, "identity": work.Identity(), "digest": work.Digest(), "removed": true}
	if err := writeJSON(paths.Workspace, restored); err != nil {
		t.Fatal(err)
	}
	raw, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	again, err := deleteSessionCopies(context.Background(), config, work)
	if err == nil || again.ReportID != proof.ReportID {
		t.Fatal("restored proof acknowledged or changed receipt", err)
	}
	after, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err != nil || string(after) != string(raw) {
		t.Fatal("restored proof was removed", err)
	}
}

func TestSessionDeletionOriginalLocksAndChangedWorkCannotBorrowProof(t *testing.T) {
	config, works := failedDeletionObligations(t)
	work := works[0]
	proof, path, lock, err := loadSessionDeletionProof(config.Root, work)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, other, err := loadSessionDeletionProof(config.Root, work); err == nil || other != nil {
		t.Fatal("active original obligation lock was bypassed")
	}
	lock.Close()
	changed := work
	changed.Copies = append([]domain.SessionDeletionCopy(nil), work.Copies...)
	changed.Copies[0].InstanceID = domain.NewID()
	before, err := security.ReadPrivate(path, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, other, err := loadSessionDeletionProof(config.Root, changed); err == nil || other != nil {
		t.Fatal("changed immutable work borrowed progress")
	}
	after, err := security.ReadPrivate(path, 4096)
	if err != nil || string(before) != string(after) {
		t.Fatal("original proof changed", err)
	}
	again, _, other, err := loadSessionDeletionProof(config.Root, work)
	if err != nil || again.ReportID != proof.ReportID {
		t.Fatal("released original obligation failed", err)
	}
	if other != nil {
		other.Close()
	}
	fence, err := security.TryLock(filepath.Join(config.Root, "session-deletions", string(work.SessionID)+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, other, err := loadSessionDeletionProof(config.Root, work); err == nil || other != nil {
		t.Fatal("original legacy fence lock bypassed")
	}
	fence.Close()
}

func TestSessionDeletionLegacyCompletedReceiptAndInterruptedRetirement(t *testing.T) {
	for _, stage := range []string{"legacy-complete", "retirement-present", "retirement-absent"} {
		t.Run(stage, func(t *testing.T) {
			config, works := failedDeletionObligations(t)
			work := works[0]
			original, err := deleteSessionCopies(context.Background(), config, work)
			if err != nil {
				t.Fatal(err)
			}
			paths, _ := workspace.SessionDeletionProofPaths(config.Root, work)
			if stage == "legacy-complete" {
				legacyWorkspacePath := filepath.Join(config.Root, "session-deletions", string(work.SessionID)+"-workspace.json")
				if err := writeJSON(legacyWorkspacePath, map[string]any{"version": 1, "digest": work.Digest(), "removed": true}); err != nil {
					t.Fatal(err)
				}
				legacy := original
				legacy.Version = 1
				legacy.Identity = domain.SessionDeletionIdentity{}
				legacy.AdmissionDigest = ""
				legacy.WorkspaceRetired = false
				if err := writeJSON(sessionDeletionPath(config.Root, work.SessionID), legacy); err != nil {
					t.Fatal(err)
				}
				legacyWorkspacePath := filepath.Join(config.Root, "session-deletions", string(work.SessionID)+"-workspace.json")
				if err := writeJSON(legacyWorkspacePath, map[string]any{"version": 1, "digest": work.Digest(), "removed": true}); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(paths.Worker); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(sessionRemovalAdmissionPath(paths.Worker)); err != nil {
					t.Fatal(err)
				}
			} else {
				pending := original
				pending.WorkspaceRetired = false
				if err := writeJSON(paths.Worker, pending); err != nil {
					t.Fatal(err)
				}
				if stage == "retirement-present" {
					if err := writeJSON(paths.Workspace, map[string]any{"version": 2, "identity": work.Identity(), "digest": work.Digest(), "removed": true}); err != nil {
						t.Fatal(err)
					}
				}
			}
			proof, err := deleteSessionCopies(context.Background(), config, work)
			if err != nil || !proof.Complete || !proof.WorkspaceRetired || proof.ReportID != original.ReportID {
				t.Fatal("original receipt/retirement recovery failed", err)
			}
			if _, err := os.Lstat(paths.Workspace); !os.IsNotExist(err) {
				t.Fatal("workspace proof not retired", err)
			}
		})
	}
}

// Restore legacy evidence after admission has released the session fence, while
// the original obligation lock still belongs to the pending cleanup attempt.
func TestSessionDeletionCurrentFenceRejectsLegacyProofAtWorkspaceCleanup(t *testing.T) {
	for _, stage := range []string{"untouched", "removed", "equal-pair"} {
		t.Run(stage, func(t *testing.T) {
			config, works := failedDeletionObligations(t)
			work := works[0]
			proof, path, lock, err := loadSessionDeletionProof(config.Root, work)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			before, err := security.ReadPrivate(path, 4096)
			if err != nil {
				t.Fatal(err)
			}
			paths, err := workspace.SessionDeletionProofPaths(config.Root, work)
			if err != nil {
				t.Fatal(err)
			}
			removed := stage != "untouched"
			legacyPath := filepath.Join(config.Root, "session-deletions", string(work.SessionID)+"-workspace.json")
			legacy := map[string]any{"version": 1, "digest": work.Digest(), "removed": removed}
			if err := writeJSON(legacyPath, legacy); err != nil {
				t.Fatal(err)
			}
			legacyBefore, err := security.ReadPrivate(legacyPath, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			var scopedBefore []byte
			if stage == "equal-pair" {
				// Even a byte-equal interrupted import pair is no longer admissible
				// once the current fence has committed.
				keyed := struct {
					Version  uint32                         `json:"version"`
					Identity domain.SessionDeletionIdentity `json:"identity,omitempty"`
					Digest   string                         `json:"digest"`
					Removed  bool                           `json:"removed"`
				}{2, work.Identity(), work.Digest(), removed}
				if err := writeJSON(paths.Workspace, keyed); err != nil {
					t.Fatal(err)
				}
				scopedBefore, err = security.ReadPrivate(paths.Workspace, 1<<20)
				if err != nil {
					t.Fatal(err)
				}
			}
			manager := workspace.Manager{Root: config.Root, Logger: config.Logger}
			invoked := false
			err = manager.DeleteOwnedWorkspace(context.Background(), work, true, func() error { invoked = true; return nil })
			if domain.SafeError(err).Code != domain.RecoveryRequired || invoked {
				t.Fatal("current cleanup admitted restored legacy proof", err)
			}
			legacyAfter, err := security.ReadPrivate(legacyPath, 1<<20)
			if err != nil || string(legacyAfter) != string(legacyBefore) {
				t.Fatal("restored legacy proof changed", err)
			}
			scopedAfter, scopedErr := security.ReadPrivate(paths.Workspace, 1<<20)
			if stage == "equal-pair" {
				if scopedErr != nil || string(scopedAfter) != string(scopedBefore) {
					t.Fatal("restored keyed proof changed", scopedErr)
				}
			} else if !os.IsNotExist(scopedErr) {
				t.Fatal("restored legacy proof acquired keyed authority", scopedErr)
			}
			after, err := security.ReadPrivate(path, 4096)
			if err != nil || string(after) != string(before) || proof.ReportID.Validate() != nil {
				t.Fatal("pending progress or original receipt changed", err)
			}
		})
	}
}
