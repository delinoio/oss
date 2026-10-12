// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestSessionDeletionPublishedFenceDoesNotRecreateMissingWorkerProof(t *testing.T) {
	config, works := failedDeletionObligations(t)
	work := works[0]
	proof, err := deleteSessionCopies(context.Background(), config, work)
	if err != nil || !proof.Complete {
		t.Fatal("initial cleanup failed", err)
	}
	paths, err := workspace.SessionDeletionProofPaths(config.Root, work)
	if err != nil {
		t.Fatal(err)
	}
	markerPath := sessionDeletionPath(config.Root, work.SessionID)
	markerBefore, err := security.ReadPrivate(markerPath, domain.MaxSessionDeletionBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.Worker); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteSessionCopies(context.Background(), config, work); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("missing proof behind a published fence was regenerated", err)
	}
	if _, err := os.Lstat(paths.Worker); !os.IsNotExist(err) {
		t.Fatal("missing Worker proof was recreated", err)
	}
	markerAfter, err := security.ReadPrivate(markerPath, domain.MaxSessionDeletionBytes)
	if err != nil || string(markerAfter) != string(markerBefore) {
		t.Fatal("published admission fence changed", err)
	}
}

func TestSessionDeletionInitialFenceRecoversExactReportID(t *testing.T) {
	config, works := failedDeletionObligations(t)
	work := works[0]
	if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	marker := deletionMarker(work)
	reportID := domain.NewID()
	marker.Obligations = []sessionDeletionMarkerObligation{{Identity: work.Identity(), Digest: work.Digest(), ReportID: reportID, Phase: sessionDeletionMarkerInitial}}
	if err := writeDeletionMarker(sessionDeletionPath(config.Root, work.SessionID), marker); err != nil {
		t.Fatal(err)
	}
	proof, _, lock, err := loadSessionDeletionProof(config.Root, work)
	if err != nil {
		t.Fatal("initial publication did not recover", err)
	}
	defer lock.Close()
	if proof.ReportID != reportID {
		t.Fatal("interrupted publication changed the original report ID")
	}
	var published sessionDeletionMarker
	raw, err := security.ReadPrivate(sessionDeletionPath(config.Root, work.SessionID), domain.MaxSessionDeletionBytes)
	if err != nil || domain.Decode(raw, &published) != nil || len(published.Obligations) != 1 || published.Obligations[0].Phase != sessionDeletionMarkerPublished || published.Obligations[0].ReportID != reportID {
		t.Fatal("recovered publication did not close its fence", err)
	}
}

func TestSessionDeletionFreshAdmissionRejectsOrphanedWorkspaceProof(t *testing.T) {
	config, works := failedDeletionObligations(t)
	work := works[0]
	if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	paths, err := workspace.SessionDeletionProofPaths(config.Root, work)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(paths.Workspace, map[string]any{
		"version":  2,
		"identity": work.Identity(),
		"digest":   work.Digest(),
		"removed":  false,
	}); err != nil {
		t.Fatal(err)
	}
	orphanBefore, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadSessionDeletionProof(config.Root, work); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("fresh admission adopted orphaned workspace proof", err)
	}
	if _, err := os.Lstat(paths.Worker); !os.IsNotExist(err) {
		t.Fatal("fresh admission created Worker progress", err)
	}
	if _, err := os.Lstat(sessionDeletionPath(config.Root, work.SessionID)); !os.IsNotExist(err) {
		t.Fatal("fresh admission created the session fence", err)
	}
	orphanAfter, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err != nil || string(orphanAfter) != string(orphanBefore) {
		t.Fatal("fresh admission changed orphaned workspace proof", err)
	}
}
