// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestLegacyV1WorkspaceProofMigrationIsRecoverable(t *testing.T) {
	t.Run("migration publishes the original receipt and phase", func(t *testing.T) {
		config, works := failedDeletionObligations(t)
		work := works[0]
		if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
			t.Fatal(err)
		}
		reportID := domain.NewID()
		legacy := sessionDeletionProof{Version: 1, Digest: work.Digest(), ReportID: reportID, RemovalStarted: true}
		if err := writeJSON(sessionDeletionPath(config.Root, work.SessionID), legacy); err != nil {
			t.Fatal(err)
		}
		legacyWorkspacePath := filepath.Join(config.Root, "session-deletions", string(work.SessionID)+"-workspace.json")
		if err := writeJSON(legacyWorkspacePath, map[string]any{"version": 1, "digest": work.Digest(), "removed": true}); err != nil {
			t.Fatal(err)
		}

		proof, _, lock, err := loadSessionDeletionProof(config.Root, work)
		if err != nil {
			t.Fatal("legacy receipt migration failed", err)
		}
		lock.Close()
		if proof.ReportID != reportID || !proof.RemovalStarted || proof.Identity != work.Identity() {
			t.Fatal("legacy Worker identity or progress changed", proof)
		}
		paths, err := workspace.SessionDeletionProofPaths(config.Root, work)
		if err != nil {
			t.Fatal(err)
		}
		keyedWorkspace, err := security.ReadPrivate(paths.Workspace, 4096)
		var imported struct {
			Version  uint32                         `json:"version"`
			Identity domain.SessionDeletionIdentity `json:"identity"`
			Digest   string                         `json:"digest"`
			Removed  bool                           `json:"removed"`
		}
		if err != nil || json.Unmarshal(keyedWorkspace, &imported) != nil || imported.Version != 2 || imported.Identity != work.Identity() || imported.Digest != work.Digest() || !imported.Removed {
			t.Fatal("exact historical workspace proof was not imported", err)
		}
		if _, err := os.Lstat(legacyWorkspacePath); !os.IsNotExist(err) {
			t.Fatal("legacy workspace proof was not retired after import", err)
		}
		markerRaw, err := security.ReadPrivate(sessionDeletionPath(config.Root, work.SessionID), domain.MaxSessionDeletionBytes)
		var marker sessionDeletionMarker
		if err != nil || domain.Decode(markerRaw, &marker) != nil || !validDeletionMarker(marker, work) {
			t.Fatal("version-3 admission marker was not published", err)
		}
		obligation, ok := markerObligation(&marker, work)
		if !ok || obligation.ReportID != reportID || obligation.Phase != sessionDeletionMarkerPublished {
			t.Fatal("legacy workspace import did not reach its published phase")
		}
	})

	t.Run("retired legacy name recovers from the keyed pair", func(t *testing.T) {
		config, works := failedDeletionObligations(t)
		work := works[0]
		if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
			t.Fatal(err)
		}
		paths, err := workspace.SessionDeletionProofPaths(config.Root, work)
		if err != nil {
			t.Fatal(err)
		}
		reportID := domain.NewID()
		started := sessionDeletionProof{Version: 2, Identity: work.Identity(), Digest: work.Digest(), ReportID: reportID, RemovalStarted: true}
		if err := writeJSON(paths.Worker, started); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(paths.Workspace, map[string]any{"version": 2, "identity": work.Identity(), "digest": work.Digest(), "removed": true}); err != nil {
			t.Fatal(err)
		}
		marker := deletionMarker(work)
		marker.Obligations = []sessionDeletionMarkerObligation{{Identity: work.Identity(), Digest: work.Digest(), ReportID: reportID, Phase: sessionDeletionMarkerLegacyWorkspaceImport}}
		if err := writeDeletionMarker(sessionDeletionPath(config.Root, work.SessionID), marker); err != nil {
			t.Fatal(err)
		}

		proof, _, lock, err := loadSessionDeletionProof(config.Root, work)
		if err != nil {
			t.Fatal("interrupted legacy workspace import was not recovered", err)
		}
		lock.Close()
		if proof.ReportID != reportID || !proof.RemovalStarted {
			t.Fatal("recovery changed original legacy progress", proof)
		}
		markerRaw, err := security.ReadPrivate(sessionDeletionPath(config.Root, work.SessionID), domain.MaxSessionDeletionBytes)
		if err != nil || domain.Decode(markerRaw, &marker) != nil {
			t.Fatal("recovered marker could not be read", err)
		}
		obligation, ok := markerObligation(&marker, work)
		if !ok || obligation.Phase != sessionDeletionMarkerPublished {
			t.Fatal("recovered import did not publish its marker phase")
		}
	})

	t.Run("missing Worker proof is not reconstructed", func(t *testing.T) {
		config, works := failedDeletionObligations(t)
		work := works[0]
		if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
			t.Fatal(err)
		}
		paths, err := workspace.SessionDeletionProofPaths(config.Root, work)
		if err != nil {
			t.Fatal(err)
		}
		reportID := domain.NewID()
		marker := deletionMarker(work)
		marker.Obligations = []sessionDeletionMarkerObligation{{Identity: work.Identity(), Digest: work.Digest(), ReportID: reportID, Phase: sessionDeletionMarkerLegacyWorkspaceImport}}
		if err := writeDeletionMarker(sessionDeletionPath(config.Root, work.SessionID), marker); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(paths.Workspace, map[string]any{"version": 2, "identity": work.Identity(), "digest": work.Digest(), "removed": true}); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := loadSessionDeletionProof(config.Root, work); domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatal("missing legacy Worker progress was recreated", err)
		}
		if _, err := os.Lstat(paths.Worker); !os.IsNotExist(err) {
			t.Fatal("missing Worker receipt was rewritten", err)
		}
	})
}

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

func TestLegacySharedOwnerDigestMigrationPreservesFencedProgress(t *testing.T) {
	config, works := failedDeletionObligations(t)
	legacy := works[0]
	legacy.SharedCleanupOwnerDeviceID = ""
	legacy.PreviousDigest = ""
	previousDigest := legacy.Digest()
	work := legacy
	work.SharedCleanupOwnerDeviceID = work.DeviceID
	work.PreviousDigest = previousDigest
	if err := work.Validate(); err != nil {
		t.Fatal("derived-owner work was invalid", err)
	}
	if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	paths, err := workspace.SessionDeletionProofPaths(config.Root, work)
	if err != nil {
		t.Fatal(err)
	}
	reportID := domain.NewID()
	workerProof := sessionDeletionProof{Version: 2, Identity: work.Identity(), Digest: previousDigest, ReportID: reportID, RemovalStarted: true}
	workspaceProof := map[string]any{"version": 2, "identity": work.Identity(), "digest": previousDigest, "removed": true}
	if err := writeJSON(paths.Worker, workerProof); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(paths.Workspace, workspaceProof); err != nil {
		t.Fatal(err)
	}
	marker := sessionDeletionMarker{Version: 3, ServerID: work.ServerID, SessionID: work.SessionID, DeletionID: work.DeletionID, MachineID: work.MachineID, Obligations: []sessionDeletionMarkerObligation{{Identity: work.Identity(), Digest: previousDigest, ReportID: reportID, Phase: sessionDeletionMarkerPublished}}}
	markerPath := sessionDeletionPath(config.Root, work.SessionID)
	if err := writeDeletionMarker(markerPath, marker); err != nil {
		t.Fatal(err)
	}

	proof, _, lock, err := loadSessionDeletionProof(config.Root, work)
	if err != nil {
		t.Fatal("fenced digest migration failed", err)
	}
	lock.Close()
	if proof.ReportID != reportID || proof.Digest != work.Digest() || !proof.RemovalStarted {
		t.Fatal("Worker progress changed during digest migration", proof)
	}
	var migratedWorker sessionDeletionProof
	workerRaw, err := security.ReadPrivate(paths.Worker, 4096)
	if err != nil || domain.Decode(workerRaw, &migratedWorker) != nil || migratedWorker.Digest != work.Digest() || migratedWorker.ReportID != reportID || !migratedWorker.RemovalStarted {
		t.Fatal("keyed Worker proof did not preserve the original stage", err)
	}
	var migratedWorkspace map[string]any
	workspaceRaw, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err != nil || json.Unmarshal(workspaceRaw, &migratedWorkspace) != nil || migratedWorkspace["digest"] != work.Digest() || migratedWorkspace["removed"] != true {
		t.Fatal("keyed workspace proof did not preserve its removed stage", err)
	}
	var migratedMarker sessionDeletionMarker
	markerRaw, err := security.ReadPrivate(markerPath, domain.MaxSessionDeletionBytes)
	if err != nil || domain.Decode(markerRaw, &migratedMarker) != nil || migratedMarker.Version != 4 || len(migratedMarker.Obligations) != 1 || migratedMarker.Obligations[0].Digest != work.Digest() || migratedMarker.Obligations[0].PreviousDigest != "" || migratedMarker.Obligations[0].ReportID != reportID || migratedMarker.Obligations[0].Phase != sessionDeletionMarkerPublished {
		t.Fatal("admission marker did not finish the digest migration", err)
	}

	proof, _, lock, err = loadSessionDeletionProof(config.Root, work)
	if err != nil {
		t.Fatal("migrated proof did not replay", err)
	}
	lock.Close()
	if proof.ReportID != reportID || !proof.RemovalStarted {
		t.Fatal("replay changed the migrated receipt", proof)
	}
}

func TestLegacySharedOwnerDigestMigrationDoesNotRecreatePublishedProgress(t *testing.T) {
	config, works := failedDeletionObligations(t)
	legacy := works[0]
	legacy.SharedCleanupOwnerDeviceID = ""
	legacy.PreviousDigest = ""
	previousDigest := legacy.Digest()
	work := legacy
	work.SharedCleanupOwnerDeviceID = work.DeviceID
	work.PreviousDigest = previousDigest
	if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	paths, err := workspace.SessionDeletionProofPaths(config.Root, work)
	if err != nil {
		t.Fatal(err)
	}
	marker := sessionDeletionMarker{Version: 3, ServerID: work.ServerID, SessionID: work.SessionID, DeletionID: work.DeletionID, MachineID: work.MachineID, Obligations: []sessionDeletionMarkerObligation{{Identity: work.Identity(), Digest: previousDigest, ReportID: domain.NewID(), Phase: sessionDeletionMarkerPublished}}}
	markerPath := sessionDeletionPath(config.Root, work.SessionID)
	if err := writeDeletionMarker(markerPath, marker); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(paths.Workspace, map[string]any{"version": 2, "identity": work.Identity(), "digest": previousDigest, "removed": true}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadSessionDeletionProof(config.Root, work); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("published fence recreated its lost Worker proof", err)
	}
	if _, err := os.Lstat(paths.Worker); !os.IsNotExist(err) {
		t.Fatal("lost Worker proof was recreated", err)
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

func TestSessionDeletionInitialFenceRejectsOrphanedWorkspaceProof(t *testing.T) {
	config, works := failedDeletionObligations(t)
	work := works[0]
	if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	paths, err := workspace.SessionDeletionProofPaths(config.Root, work)
	if err != nil {
		t.Fatal(err)
	}
	marker := deletionMarker(work)
	reportID := domain.NewID()
	marker.Obligations = []sessionDeletionMarkerObligation{{Identity: work.Identity(), Digest: work.Digest(), ReportID: reportID, Phase: sessionDeletionMarkerInitial}}
	markerPath := sessionDeletionPath(config.Root, work.SessionID)
	if err := writeDeletionMarker(markerPath, marker); err != nil {
		t.Fatal(err)
	}
	workerProof := sessionDeletionProof{Version: 2, Identity: work.Identity(), Digest: work.Digest(), ReportID: reportID}
	if err := writeJSON(paths.Worker, workerProof); err != nil {
		t.Fatal(err)
	}
	orphan := map[string]any{"version": 2, "identity": work.Identity(), "digest": work.Digest(), "removed": false}
	if err := writeJSON(paths.Workspace, orphan); err != nil {
		t.Fatal(err)
	}
	markerBefore, err := security.ReadPrivate(markerPath, domain.MaxSessionDeletionBytes)
	if err != nil {
		t.Fatal(err)
	}
	workerBefore, err := security.ReadPrivate(paths.Worker, 4096)
	if err != nil {
		t.Fatal(err)
	}
	workspaceBefore, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadSessionDeletionProof(config.Root, work); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("initial-fence recovery adopted an orphaned workspace proof", err)
	}
	for path, before := range map[string][]byte{markerPath: markerBefore, paths.Worker: workerBefore, paths.Workspace: workspaceBefore} {
		after, err := security.ReadPrivate(path, 1<<20)
		if err != nil || string(after) != string(before) {
			t.Fatal("initial-fence recovery changed orphan evidence", path, err)
		}
	}
}

func TestSessionDeletionSharedFencePersistsNewObligationBeforeWorkerProof(t *testing.T) {
	config, works := failedDeletionObligations(t)
	first, second := works[0], works[1]
	if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	marker := deletionMarker(first)
	marker.Obligations = []sessionDeletionMarkerObligation{{Identity: first.Identity(), Digest: first.Digest(), ReportID: domain.NewID(), Phase: sessionDeletionMarkerPublished}}
	if err := writeDeletionMarker(sessionDeletionPath(config.Root, first.SessionID), marker); err != nil {
		t.Fatal(err)
	}
	proof, _, lock, err := loadSessionDeletionProof(config.Root, second)
	if err != nil {
		t.Fatal("new device obligation was not admitted", err)
	}
	defer lock.Close()
	var published sessionDeletionMarker
	raw, err := security.ReadPrivate(sessionDeletionPath(config.Root, second.SessionID), domain.MaxSessionDeletionBytes)
	if err != nil || domain.Decode(raw, &published) != nil || len(published.Obligations) != 2 {
		t.Fatal("shared fence did not retain both original device obligations", err)
	}
	var found bool
	for _, obligation := range published.Obligations {
		if obligation.Identity == second.Identity() {
			found = obligation.Phase == sessionDeletionMarkerPublished && obligation.ReportID == proof.ReportID
		}
	}
	if !found {
		t.Fatal("new Worker proof was not bound to its durable marker entry")
	}
}

func TestSessionDeletionSharedFenceRejectsOrphanedProofForNewObligation(t *testing.T) {
	config, works := failedDeletionObligations(t)
	first, second := works[0], works[1]
	if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	marker := deletionMarker(first)
	marker.Obligations = []sessionDeletionMarkerObligation{{Identity: first.Identity(), Digest: first.Digest(), ReportID: domain.NewID(), Phase: sessionDeletionMarkerPublished}}
	markerPath := sessionDeletionPath(config.Root, first.SessionID)
	if err := writeDeletionMarker(markerPath, marker); err != nil {
		t.Fatal(err)
	}
	markerBefore, err := security.ReadPrivate(markerPath, domain.MaxSessionDeletionBytes)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := workspace.SessionDeletionProofPaths(config.Root, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(paths.Workspace, map[string]any{"version": 2, "identity": second.Identity(), "digest": second.Digest(), "removed": false}); err != nil {
		t.Fatal(err)
	}
	orphanBefore, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadSessionDeletionProof(config.Root, second); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("new obligation adopted an orphaned keyed workspace proof", err)
	}
	if _, err := os.Lstat(paths.Worker); !os.IsNotExist(err) {
		t.Fatal("orphaned workspace proof created Worker progress", err)
	}
	markerAfter, err := security.ReadPrivate(markerPath, domain.MaxSessionDeletionBytes)
	if err != nil || string(markerAfter) != string(markerBefore) {
		t.Fatal("orphan rejection changed the shared fence", err)
	}
	orphanAfter, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err != nil || string(orphanAfter) != string(orphanBefore) {
		t.Fatal("orphan rejection changed the keyed workspace proof", err)
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

func TestSessionDeletionFreshAdmissionFencesLegacyWorkspaceImport(t *testing.T) {
	config, works := failedDeletionObligations(t)
	work := works[0]
	if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(config.Root, "session-deletions", string(work.SessionID)+"-workspace.json")
	legacy := map[string]any{"version": 1, "digest": work.Digest(), "removed": true}
	if err := writeJSON(legacyPath, legacy); err != nil {
		t.Fatal(err)
	}
	proof, path, lock, err := loadSessionDeletionProof(config.Root, work)
	if err != nil {
		t.Fatal("fresh admission did not complete the fenced import", err)
	}
	defer lock.Close()
	if proof.ReportID.Validate() != nil {
		t.Fatal("fresh import did not retain its original report ID")
	}
	paths, err := workspace.SessionDeletionProofPaths(config.Root, work)
	if err != nil {
		t.Fatal(err)
	}
	var imported struct {
		Version  uint32                         `json:"version"`
		Identity domain.SessionDeletionIdentity `json:"identity"`
		Digest   string                         `json:"digest"`
		Removed  bool                           `json:"removed"`
	}
	raw, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err != nil || domain.Decode(raw, &imported) != nil || imported.Version != 2 || imported.Identity != work.Identity() || imported.Digest != work.Digest() || !imported.Removed {
		t.Fatal("fenced import did not retain exact keyed workspace evidence", err)
	}
	if _, err := os.Lstat(legacyPath); !os.IsNotExist(err) {
		t.Fatal("successful import did not retire the legacy proof", err)
	}
	if got, err := security.ReadPrivate(path, 4096); err != nil || domain.Decode(got, &proof) != nil {
		t.Fatal("fenced Worker receipt was not published", err)
	}
	var marker sessionDeletionMarker
	markerRaw, err := security.ReadPrivate(sessionDeletionPath(config.Root, work.SessionID), domain.MaxSessionDeletionBytes)
	if err != nil || domain.Decode(markerRaw, &marker) != nil || len(marker.Obligations) != 1 || marker.Obligations[0].Phase != sessionDeletionMarkerPublished || marker.Obligations[0].ReportID != proof.ReportID {
		t.Fatal("legacy import did not finish its original marker phase", err)
	}
}

func TestSessionDeletionInitialLegacyImportRecoversAfterRetirement(t *testing.T) {
	config, works := failedDeletionObligations(t)
	work := works[0]
	if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	paths, err := workspace.SessionDeletionProofPaths(config.Root, work)
	if err != nil {
		t.Fatal(err)
	}
	reportID := domain.NewID()
	marker := deletionMarker(work)
	marker.Obligations = []sessionDeletionMarkerObligation{{Identity: work.Identity(), Digest: work.Digest(), ReportID: reportID, Phase: sessionDeletionMarkerInitialWorkspaceImport}}
	if err := writeDeletionMarker(sessionDeletionPath(config.Root, work.SessionID), marker); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(paths.Worker, sessionDeletionProof{Version: 2, Identity: work.Identity(), Digest: work.Digest(), ReportID: reportID}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(paths.Workspace, map[string]any{"version": 2, "identity": work.Identity(), "digest": work.Digest(), "removed": true}); err != nil {
		t.Fatal(err)
	}
	proof, _, lock, err := loadSessionDeletionProof(config.Root, work)
	if err != nil {
		t.Fatal("exact interrupted legacy import did not recover", err)
	}
	defer lock.Close()
	if proof.ReportID != reportID {
		t.Fatal("interrupted import changed its original report ID")
	}
	var published sessionDeletionMarker
	raw, err := security.ReadPrivate(sessionDeletionPath(config.Root, work.SessionID), domain.MaxSessionDeletionBytes)
	if err != nil || domain.Decode(raw, &published) != nil || len(published.Obligations) != 1 || published.Obligations[0].Phase != sessionDeletionMarkerPublished || published.Obligations[0].ReportID != reportID {
		t.Fatal("recovered import did not publish its original marker", err)
	}
}

func TestSessionDeletionStartedWorkerProofRequiresKeyedWorkspaceProof(t *testing.T) {
	config, works := failedDeletionObligations(t)
	work := works[0]
	if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	paths, err := workspace.SessionDeletionProofPaths(config.Root, work)
	if err != nil {
		t.Fatal(err)
	}
	marker := deletionMarker(work)
	reportID := domain.NewID()
	marker.Obligations = []sessionDeletionMarkerObligation{{Identity: work.Identity(), Digest: work.Digest(), ReportID: reportID, Phase: sessionDeletionMarkerPublished}}
	if err := writeDeletionMarker(sessionDeletionPath(config.Root, work.SessionID), marker); err != nil {
		t.Fatal(err)
	}
	started := sessionDeletionProof{Version: 2, Identity: work.Identity(), Digest: work.Digest(), ReportID: reportID, RemovalStarted: true}
	if err := writeJSON(paths.Worker, started); err != nil {
		t.Fatal(err)
	}
	workerBefore, err := security.ReadPrivate(paths.Worker, 4096)
	if err != nil {
		t.Fatal(err)
	}
	markerBefore, err := security.ReadPrivate(sessionDeletionPath(config.Root, work.SessionID), domain.MaxSessionDeletionBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadSessionDeletionProof(config.Root, work); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("started Worker removal recreated missing workspace proof", err)
	}
	if _, err := os.Lstat(paths.Workspace); !os.IsNotExist(err) {
		t.Fatal("missing keyed workspace proof was recreated", err)
	}
	workerAfter, err := security.ReadPrivate(paths.Worker, 4096)
	if err != nil || string(workerAfter) != string(workerBefore) {
		t.Fatal("started Worker progress changed", err)
	}
	markerAfter, err := security.ReadPrivate(sessionDeletionPath(config.Root, work.SessionID), domain.MaxSessionDeletionBytes)
	if err != nil || string(markerAfter) != string(markerBefore) {
		t.Fatal("published admission fence changed", err)
	}
}

func TestWorkspaceFreeDeletionRejectsUnexpectedKeyedWorkspaceProof(t *testing.T) {
	config, works := failedDeletionObligations(t)
	work := works[0]
	work.Copies = nil
	work.Images = []domain.ImageAttachment{{ID: domain.NewID(), MachineID: work.MachineID, MediaType: domain.ImagePNG, ByteLength: 1, SHA256: strings.Repeat("a", 64)}}
	if work.Validate() != nil {
		t.Fatal("workspace-free deletion fixture is invalid")
	}
	if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	reportID := domain.NewID()
	marker := deletionMarker(work)
	marker.Obligations = []sessionDeletionMarkerObligation{{Identity: work.Identity(), Digest: work.Digest(), ReportID: reportID, Phase: sessionDeletionMarkerPublished}}
	if err := writeDeletionMarker(sessionDeletionPath(config.Root, work.SessionID), marker); err != nil {
		t.Fatal(err)
	}
	paths, err := workspace.SessionDeletionProofPaths(config.Root, work)
	if err != nil {
		t.Fatal(err)
	}
	started := sessionDeletionProof{Version: 2, Identity: work.Identity(), Digest: work.Digest(), ReportID: reportID, RemovalStarted: true}
	if err := writeJSON(paths.Worker, started); err != nil {
		t.Fatal(err)
	}
	keyedWorkspace := map[string]any{"version": 2, "identity": work.Identity(), "digest": work.Digest(), "removed": true}
	if err := writeJSON(paths.Workspace, keyedWorkspace); err != nil {
		t.Fatal(err)
	}
	workspaceBefore, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadSessionDeletionProof(config.Root, work); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("workspace-free cleanup accepted unexpected keyed workspace proof", err)
	}
	workspaceAfter, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err != nil || string(workspaceAfter) != string(workspaceBefore) {
		t.Fatal("unexpected keyed workspace proof changed", err)
	}
}

func TestWorkspaceFreeStartedDeletionDoesNotRequireWorkspaceProof(t *testing.T) {
	for _, version := range []uint32{1, 3} {
		t.Run(fmt.Sprintf("version-%d", version), func(t *testing.T) {
			config, works := failedDeletionObligations(t)
			work := works[0]
			work.Copies = nil
			work.Images = []domain.ImageAttachment{{ID: domain.NewID(), MachineID: work.MachineID, MediaType: domain.ImagePNG, ByteLength: 1, SHA256: strings.Repeat("a", 64)}}
			if work.Validate() != nil {
				t.Fatal("workspace-free deletion fixture is invalid")
			}
			if err := security.PrivateDir(filepath.Join(config.Root, "session-deletions")); err != nil {
				t.Fatal(err)
			}
			reportID := domain.NewID()
			started := sessionDeletionProof{Version: 2, Identity: work.Identity(), Digest: work.Digest(), ReportID: reportID, RemovalStarted: true}
			if version == 1 {
				started.Version = 1
				started.Identity = domain.SessionDeletionIdentity{}
				if err := writeJSON(sessionDeletionPath(config.Root, work.SessionID), started); err != nil {
					t.Fatal(err)
				}
			} else {
				marker := deletionMarker(work)
				marker.Obligations = []sessionDeletionMarkerObligation{{Identity: work.Identity(), Digest: work.Digest(), ReportID: reportID, Phase: sessionDeletionMarkerPublished}}
				if err := writeDeletionMarker(sessionDeletionPath(config.Root, work.SessionID), marker); err != nil {
					t.Fatal(err)
				}
				paths, err := workspace.SessionDeletionProofPaths(config.Root, work)
				if err != nil {
					t.Fatal(err)
				}
				if err := writeJSON(paths.Worker, started); err != nil {
					t.Fatal(err)
				}
			}
			proof, _, lock, err := loadSessionDeletionProof(config.Root, work)
			if err != nil {
				t.Fatal("workspace-free started deletion required workspace proof", err)
			}
			defer lock.Close()
			if proof.ReportID != reportID || !proof.RemovalStarted {
				t.Fatal("workspace-free started deletion changed its original receipt")
			}
		})
	}
}
