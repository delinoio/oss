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
	config := Config{Root: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
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

func TestSessionDeletionLegacyExactStagesAndInterruptedPairs(t *testing.T) {
	for _, stage := range []string{"untouched", "started", "complete", "equal-pair", "changed-pair", "foreign-digest", "foreign-receipt", "missing-fence"} {
		t.Run(stage, func(t *testing.T) {
			config, works := failedDeletionObligations(t)
			work := works[0]
			if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
				t.Fatal(err)
			}
			legacy := sessionDeletionProof{Version: 1, Digest: work.Digest(), ReportID: domain.NewID(), RemovalStarted: stage == "started" || stage == "complete", Complete: stage == "complete"}
			expected := legacy
			paths, _ := workspace.SessionDeletionProofPaths(config.Root, work)
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
