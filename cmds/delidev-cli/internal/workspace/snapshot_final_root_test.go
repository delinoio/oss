// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func finalRootFixture(t *testing.T, action StorageAction) (*Manager, StorageRequest) {
	t.Helper()
	m, prepare, manifest := chatExecutionFixture(t)
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	r := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, r)
	r.Action, r.OperationID, r.SnapshotID, r.PreviewDigest = action, domain.NewID(), domain.NewID(), preview.PreviewDigest
	if action == StorageDelete {
		r.Action = StorageCreate
		created := storageDo(t, m, r)
		r.Action, r.OperationID, r.SnapshotDigest, r.SnapshotMetadata = StorageDelete, domain.NewID(), created.Snapshot.SHA256, created.Snapshot
	}
	return m, r
}

func TestFinalRootRemovalPreservesReplacementAfterValidation(t *testing.T) {
	for _, action := range []StorageAction{StorageCleanup, StorageDelete} {
		for _, link := range []bool{false, true} {
			name := "directory"
			if link {
				name = "symlink"
			}
			t.Run(string(action)+"/"+name, func(t *testing.T) {
				m, r := finalRootFixture(t, action)
				removal := filepath.Join(m.Root, "workspace-removals", string(r.OperationID))
				moved := filepath.Join(m.Root, "moved-original")
				target := t.TempDir()
				if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
				// Check symlink privileges before causing any storage effects.
				if link {
					probe := filepath.Join(m.Root, "link-probe")
					if err := os.Symlink(target, probe); err != nil {
						t.Skip("native symlink creation unavailable")
					}
					os.Remove(probe)
				}
				m.storageFinalRootFault = func(stage storageFinalRootStage) error {
					if stage != storageFinalRootBeforeClaim {
						return nil
					}
					if err := os.Rename(removal, moved); err != nil {
						t.Fatal(err)
					}
					var err error
					if link {
						err = os.Symlink(target, removal)
					} else {
						err = os.Mkdir(removal, 0700)
					}
					if err != nil {
						t.Fatal(err)
					}
					return nil
				}
				result, err := m.Storage(context.Background(), r)
				if domain.SafeError(err).Code != domain.RecoveryRequired || result.CleanupVerified || result.RemovedSourceBytes != 0 {
					t.Fatal("replacement granted completion", result, err)
				}
				if _, err := os.Lstat(removal); err != nil {
					t.Fatal("foreign replacement unlinked", err)
				}
				if _, err := os.Lstat(moved); err != nil {
					t.Fatal("original root removed", err)
				}
				if raw, err := os.ReadFile(filepath.Join(target, "sentinel")); err != nil || string(raw) != "foreign" {
					t.Fatal("symlink target changed", err)
				}
				restarted := &Manager{Root: m.Root, Logger: m.Logger}
				if _, err := restarted.Storage(context.Background(), recoveryRequest(r)); domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("recovery adopted replacement", err)
				}
				// Return only the original native root to its claimed name. Its
				// durable child receipts permit recovery without source/native replay.
				if err := os.Remove(removal); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(moved, removal); err != nil {
					t.Fatal(err)
				}
				recovered := storageDo(t, restarted, recoveryRequest(r))
				if !recovered.CleanupVerified || recovered.RecoveredJobState != domain.JobSucceeded {
					t.Fatal("original claim did not recover", recovered)
				}
			})
		}
	}
}

func TestFinalRootClaimPublicationDoesNotReplaceConcurrentClaim(t *testing.T) {
	m, r := finalRootFixture(t, StorageCleanup)
	claimPath := m.finalRemovalClaimPath(r.OperationID)
	m.storageFinalRootFault = func(stage storageFinalRootStage) error {
		if stage != storageFinalRootClaimChecked {
			return nil
		}
		if err := os.WriteFile(claimPath, []byte(`{"foreign":true}`), 0600); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	result, err := m.Storage(context.Background(), r)
	if domain.SafeError(err).Code != domain.RecoveryRequired || result.CleanupVerified {
		t.Fatal("concurrent claim replacement settled", result, err)
	}
	if raw, err := os.ReadFile(claimPath); err != nil || string(raw) != `{"foreign":true}` {
		t.Fatalf("foreign final-root claim was replaced: %q, %v", raw, err)
	}
}

func TestFinalRootRemovalRecoveryRequiresOriginalTransition(t *testing.T) {
	stages := []storageFinalRootStage{storageFinalRootPrepared, storageFinalRootRenamed, storageFinalRootClaimed, storageFinalRootVerified, storageFinalRootNativeGone, storageFinalRootUnlinked, storageFinalRootSynced}
	for _, action := range []StorageAction{StorageCleanup, StorageDelete} {
		for _, stage := range stages {
			t.Run(string(action)+"/"+string(stage), func(t *testing.T) {
				m, r := finalRootFixture(t, action)
				injected := false
				m.storageFinalRootFault = func(current storageFinalRootStage) error {
					if current == stage && !injected {
						injected = true
						return errors.New("fixture interruption")
					}
					return nil
				}
				result, err := m.Storage(context.Background(), r)
				if !injected || domain.SafeError(err).Code != domain.RecoveryRequired || result.CleanupVerified || result.RemovedSourceBytes != 0 {
					t.Fatal("interruption settled", result, err)
				}
				if action == StorageCleanup {
					if _, _, err := m.inspectSnapshot(context.Background(), r.SnapshotID); err != nil {
						t.Fatal("recoverable snapshot invalidated", err)
					}
				}
				restarted := &Manager{Root: m.Root, Logger: m.Logger}
				recovered, err := restarted.Storage(context.Background(), recoveryRequest(r))
				if stage == storageFinalRootNativeGone {
					if domain.SafeError(err).Code != domain.RecoveryRequired || recovered.CleanupVerified {
						t.Fatal("missing unlink receipt inferred completion", recovered, err)
					}
					return
				}
				if err != nil || !recovered.CleanupVerified || recovered.RecoveredJobState != domain.JobSucceeded || restarted.finalRemovalAbsent(r.OperationID) != nil {
					t.Fatal("original transition failed recovery", recovered, err)
				}
				if action == StorageCleanup && (recovered.RemovedSourceBytes != recovered.SourceBytes || recovered.PreviewDigest != r.PreviewDigest) || action == StorageDelete && (recovered.RemovedSourceBytes != 0 || recovered.SourceBytes == 0 || !recovered.Snapshot.Deleted) {
					t.Fatal("accounting changed", recovered)
				}
				if err := restarted.RetireStorageRemoval(context.Background(), removalReference(r)); err != nil {
					t.Fatal(err)
				}
				for _, path := range []string{m.finalRemovalClaimPath(r.OperationID), m.removalClaimPath(r.OperationID), m.removalClaimJournalPath(r.OperationID), m.removalIntentPath(r.OperationID)} {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatal("proof not retired", err)
					}
				}
			})
		}
	}
}

func TestFinalRootRemovalPersistsReceiptAfterCancellation(t *testing.T) {
	m, r := finalRootFixture(t, StorageCleanup)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.storageFinalRootFault = func(stage storageFinalRootStage) error {
		if stage == storageFinalRootNativeGone {
			cancel()
		}
		return nil
	}

	if _, err := m.Storage(ctx, r); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("cancellation did not retain recovery ownership", err)
	}
	raw, err := os.ReadFile(m.finalRemovalClaimPath(r.OperationID))
	if err != nil {
		t.Fatal("unlink receipt was not retained", err)
	}
	var claim storageFinalRootClaim
	if err := domain.Decode(raw, &claim); err != nil || claim.State != storageFinalRootUnlinked {
		t.Fatalf("unlink receipt state = %q, decode error = %v", claim.State, err)
	}
}

func TestRetireStorageRemovalAllowsMissingLegacyFinalRootNamespace(t *testing.T) {
	m, r := finalRootFixture(t, StorageCleanup)
	storageDo(t, m, r)
	if err := os.RemoveAll(filepath.Join(m.Root, "storage-removal-root-claims")); err != nil {
		t.Fatal(err)
	}
	if err := m.RetireStorageRemoval(context.Background(), removalReference(r)); err != nil {
		t.Fatal("legacy retirement was blocked by the absent final-root namespace", err)
	}
	for _, path := range []string{m.removalIntentPath(r.OperationID), m.removalClaimPath(r.OperationID), m.removalClaimJournalPath(r.OperationID)} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("retirement evidence survived", path, err)
		}
	}
}

func TestFinalRootRemovalRejectsReappearingOldName(t *testing.T) {
	m, r := finalRootFixture(t, StorageCleanup)
	removal := filepath.Join(m.Root, "workspace-removals", string(r.OperationID))
	m.storageFinalRootFault = func(stage storageFinalRootStage) error {
		if stage == storageFinalRootVerified {
			return os.Mkdir(removal, 0700)
		}
		return nil
	}
	result, err := m.Storage(context.Background(), r)
	if domain.SafeError(err).Code != domain.RecoveryRequired || result.CleanupVerified {
		t.Fatal("old replacement granted completion", err)
	}
	if _, err := os.Lstat(removal); err != nil {
		t.Fatal("old replacement removed", err)
	}
	if _, err := os.Lstat(finalRootFixturePath(t, m, r.OperationID)); err != nil {
		t.Fatal("original private root not retained", err)
	}
	if err := m.RetireStorageRemoval(context.Background(), removalReference(r)); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("unsettled proof retired", err)
	}
}

func TestFinalRootRemovalMissingProofOrReplacedPrivateRootStaysUncertain(t *testing.T) {
	for _, mutation := range []string{"missing-proof", "missing-root", "replaced-root", "root-mode", "replaced-proof"} {
		t.Run(mutation, func(t *testing.T) {
			m, r := finalRootFixture(t, StorageDelete)
			m.storageFinalRootFault = func(stage storageFinalRootStage) error {
				if stage == storageFinalRootClaimed {
					return errors.New("fixture interruption")
				}
				return nil
			}
			if _, err := m.Storage(context.Background(), r); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal(err)
			}
			private := finalRootFixturePath(t, m, r.OperationID)
			switch mutation {
			case "missing-proof":
				if err := os.Remove(m.finalRemovalClaimPath(r.OperationID)); err != nil {
					t.Fatal(err)
				}
			case "replaced-proof":
				if err := os.WriteFile(m.finalRemovalClaimPath(r.OperationID), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-root", "replaced-root":
				if err := os.Rename(private, filepath.Join(m.Root, "moved-private")); err != nil {
					t.Fatal(err)
				}
				if mutation == "replaced-root" {
					if err := os.Mkdir(private, 0700); err != nil {
						t.Fatal(err)
					}
				}
			case "root-mode":
				if err := os.Chmod(private, 0500); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(private, 0700)
			}
			restarted := &Manager{Root: m.Root, Logger: m.Logger}
			result, err := restarted.Storage(context.Background(), recoveryRequest(r))
			if domain.SafeError(err).Code != domain.RecoveryRequired || result.CleanupVerified {
				t.Fatal("replacement or missing proof settled", result, err)
			}
			if mutation != "missing-root" {
				if _, err := os.Lstat(private); err != nil {
					t.Fatal("protected root removed", err)
				}
			}
		})
	}
}

func TestPermanentDeletionReconcilesOnlyOriginalFinalRoot(t *testing.T) {
	for _, mutation := range []string{"original", "foreign", "missing-proof"} {
		t.Run(mutation, func(t *testing.T) {
			m, r := finalRootFixture(t, StorageCleanup)
			m.storageFinalRootFault = func(stage storageFinalRootStage) error {
				if stage == storageFinalRootClaimed {
					return errors.New("fixture interruption")
				}
				return nil
			}
			if _, err := m.Storage(context.Background(), r); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal(err)
			}
			private := finalRootFixturePath(t, m, r.OperationID)
			if mutation == "foreign" {
				if err := os.Rename(private, filepath.Join(m.Root, "original-final-root")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(private, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if mutation == "missing-proof" {
				if err := os.Remove(m.finalRemovalClaimPath(r.OperationID)); err != nil {
					t.Fatal(err)
				}
			}
			work := domain.SessionDeletionWork{SessionID: r.Preparation.SessionID, MachineID: r.Preparation.MachineID, PreparationDigests: []string{r.Manifest.InputDigest}, Copies: []domain.SessionDeletionCopy{{JobID: r.OperationID, SnapshotID: r.SnapshotID, Type: domain.WorkspaceStorageJob}}}
			restarted := &Manager{Root: m.Root, Logger: m.Logger}
			err := restarted.cleanupDeletionFinalRoots(context.Background(), work)
			if mutation == "original" {
				if err != nil || restarted.finalRemovalAbsent(r.OperationID) != nil {
					t.Fatal("original deletion claim blocked", err)
				}
			} else {
				if err == nil {
					t.Fatal("foreign root granted deletion authority")
				}
				if _, err := os.Lstat(private); err != nil {
					t.Fatal("protected root removed", err)
				}
			}
			paths := SessionStorageCopyPaths(m.Root, work)
			remnants, err := SessionStorageRemnantPaths(context.Background(), m.Root, work)
			if err != nil {
				t.Fatal(err)
			}
			paths = append(paths, remnants...)
			requiredPaths := []string{m.finalRemovalClaimPath(r.OperationID)}
			if mutation != "original" {
				requiredPaths = append(requiredPaths, private)
			}
			for _, required := range requiredPaths {
				found := false
				for _, path := range paths {
					found = found || path == required
				}
				if !found {
					t.Fatal("final-root deletion inventory omitted", required)
				}
			}
		})
	}
}

func finalRootFixturePath(t *testing.T, m *Manager, id domain.ID) string {
	t.Helper()
	raw, err := os.ReadFile(m.finalRemovalClaimPath(id))
	var claim storageFinalRootClaim
	if err != nil || domain.Decode(raw, &claim) != nil || claim.RootName.Validate() != nil {
		t.Fatal("fixture final-root proof unavailable", err)
	}
	return m.finalRemovalRoot(claim)
}
