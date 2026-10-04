// SPDX-License-Identifier: Apache-2.0
package workspace

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
)

func snapshotPublicationFixture(t *testing.T, action StorageAction) (*Manager, StorageRequest) {
	t.Helper()
	m, preparation, manifest := chatExecutionFixture(t)
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	r := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: preparation, Manifest: manifest}
	preview := storageDo(t, m, r)
	r.OperationID, r.SnapshotID, r.Action, r.PreviewDigest = domain.NewID(), domain.NewID(), action, preview.PreviewDigest
	return m, r
}

func TestSnapshotCaptureRecoveryRejectsMatchingForeignPublication(t *testing.T) {
	for _, action := range []StorageAction{StorageCreate, StorageCleanup} {
		t.Run(string(action), func(t *testing.T) {
			m, r := snapshotPublicationFixture(t, action)
			m.storageBeforeSnapshotPublish = func(staging string) {
				// A same-user writer copies the exact manifest and payload, including
				// the reserved operation ID, before the original no-replace rename.
				if _, err := walkSnapshot(context.Background(), staging, m.snapshotPath(r.SnapshotID), nil); err != nil {
					t.Fatal(err)
				}
			}
			m.storageScratchCleanupFault = func(string) error { return os.ErrPermission }
			if _, err := m.Storage(context.Background(), r); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("foreign publication did not retain uncertainty", err)
			}
			m.storageScratchCleanupFault = nil
			if _, err := m.Storage(context.Background(), recoveryRequest(r)); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("matching foreign snapshot was adopted", err)
			}
			for _, root := range []string{m.snapshotPath(r.SnapshotID), m.stagingPath(r.OperationID)} {
				if raw, err := os.ReadFile(filepath.Join(root, "workspace", "chat", "keep")); err != nil || string(raw) != "original" {
					t.Fatal("foreign snapshot or original staging was changed", err)
				}
			}
			if raw, err := os.ReadFile(filepath.Join(r.Manifest.PrimaryPath, "keep")); err != nil || string(raw) != "original" {
				t.Fatal("original workspace was removed", err)
			}
			work := domain.SessionDeletionWork{SessionID: r.Preparation.SessionID, MachineID: r.Preparation.MachineID, PreparationDigests: []string{r.Manifest.InputDigest}, Copies: []domain.SessionDeletionCopy{{JobID: r.OperationID, SnapshotID: r.SnapshotID, Type: domain.WorkspaceStorageJob}}}
			if _, err := m.deletionSnapshotManifest(context.Background(), work); err == nil {
				t.Fatal("foreign snapshot granted permanent deletion authority")
			}
		})
	}
}

func TestSnapshotCaptureRecoveryRequiresOriginalPublication(t *testing.T) {
	for _, mutation := range []string{"missing", "unpublished", "digest", "request", "replacement"} {
		t.Run(mutation, func(t *testing.T) {
			m, r := snapshotPublicationFixture(t, StorageCreate)
			created := storageDo(t, m, r)
			claim, err := m.readStagingClaim(r.OperationID)
			if err != nil || claim.PublishedSnapshotDigest != created.Snapshot.SHA256 {
				t.Fatal("creation omitted publication proof", err)
			}
			switch mutation {
			case "missing":
				if err := os.Remove(m.stagingClaimPath(r.OperationID)); err != nil {
					t.Fatal(err)
				}
			case "unpublished":
				claim.PublishedSnapshotDigest = ""
			case "digest":
				claim.PublishedSnapshotDigest = strings.Repeat("0", 64)
			case "request":
				claim.RequestDigest = strings.Repeat("0", 64)
			case "replacement":
				root := m.snapshotPath(r.SnapshotID)
				if err := os.Rename(root, root+"-original"); err != nil {
					t.Fatal(err)
				}
				if _, err := walkSnapshot(context.Background(), root+"-original", root, nil); err != nil {
					t.Fatal(err)
				}
			}
			if mutation != "missing" {
				raw, _ := json.Marshal(claim)
				if err := security.WriteAtomic(m.stagingClaimPath(r.OperationID), raw); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.Storage(context.Background(), recoveryRequest(r)); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("missing or mismatched publication authorized recovery", err)
			}
			if mutation != "request" {
				inspect := r
				inspect.OperationID, inspect.Action, inspect.SnapshotDigest = domain.NewID(), StorageDelete, created.Snapshot.SHA256
				if _, err := m.Storage(context.Background(), inspect); domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("missing or mismatched publication authorized deletion", err)
				}
			}
			if _, err := os.Stat(filepath.Join(m.snapshotPath(r.SnapshotID), "workspace", "chat", "keep")); err != nil {
				t.Fatal("unproved snapshot was removed", err)
			}
		})
	}
}

func TestSnapshotCaptureRecoveryRetainsPublishedOriginal(t *testing.T) {
	m, r := snapshotPublicationFixture(t, StorageCreate)
	m.storageScratchCleanupFault = func(string) error { return os.ErrPermission }
	if _, err := m.Storage(context.Background(), r); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("lost completion did not retain uncertainty", err)
	}
	m.storageScratchCleanupFault = nil
	m.storageBeforeSnapshotPublish = func(string) { t.Fatal("recovery repeated snapshot publication") }
	recovered := storageDo(t, m, recoveryRequest(r))
	if recovered.RecoveredJobState != domain.JobSucceeded || recovered.Snapshot == nil || recovered.Snapshot.ID != r.SnapshotID || recovered.WorkspaceState != domain.WorkspacePresent || !recovered.CleanupVerified {
		t.Fatal("original published snapshot was not recovered", recovered)
	}
}

func TestPublishedSnapshotRecoveryIgnoresLaterCopyEligibility(t *testing.T) {
	for _, action := range []StorageAction{StorageCreate, StorageCleanup} {
		t.Run(string(action), func(t *testing.T) {
			m, r := snapshotPublicationFixture(t, action)
			m.storageScratchCleanupFault = func(string) error { return os.ErrPermission }
			if _, err := m.Storage(context.Background(), r); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal(err)
			}
			snapshot, metadata, err := m.inspectSnapshot(context.Background(), r.SnapshotID)
			if err != nil {
				t.Fatal(err)
			}
			// Ordinary later writes can exceed the original admission inventory.
			for n := 0; n <= MaxSnapshotEntries; n++ {
				if err := os.WriteFile(filepath.Join(r.Manifest.PrimaryPath, fmt.Sprintf("later-%d", n)), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			m.storageScratchCleanupFault = nil
			m.storageBeforeSnapshotPublish = func(string) { t.Fatal("recovery repeated publication") }
			recovered := storageDo(t, m, recoveryRequest(r))
			expected := domain.JobFailed
			if action == StorageCreate {
				expected = domain.JobSucceeded
			}
			if recovered.RecoveredJobState != expected || recovered.WorkspaceState != domain.WorkspacePresent || recovered.SourceBytes != snapshot.SourceBytes || recovered.PreviewDigest != snapshot.SourceDigest || recovered.Snapshot == nil || *recovered.Snapshot != metadata || !recovered.CleanupVerified {
				t.Fatal("original publication not settled", recovered)
			}
			if _, err := os.Stat(filepath.Join(r.Manifest.PrimaryPath, "later-0")); err != nil {
				t.Fatal("later source removed", err)
			}
		})
	}
}

func TestPublishedSnapshotRecoveryRejectsReplacedSourceDirectory(t *testing.T) {
	m, r := snapshotPublicationFixture(t, StorageCreate)
	m.storageScratchCleanupFault = func(string) error { return os.ErrPermission }
	if _, err := m.Storage(context.Background(), r); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal(err)
	}
	old := r.Manifest.PrimaryPath + "-original"
	if err := os.Rename(r.Manifest.PrimaryPath, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(r.Manifest.PrimaryPath, 0700); err != nil {
		t.Fatal(err)
	}
	m.storageScratchCleanupFault = nil
	if _, err := m.Storage(context.Background(), recoveryRequest(r)); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("replacement adopted", err)
	}
	if _, err := os.Stat(filepath.Join(old, "keep")); err != nil {
		t.Fatal(err)
	}
}

func TestPublishedSnapshotRecoveryIgnoresUnavailableExternalGit(t *testing.T) {
	for _, action := range []StorageAction{StorageCreate, StorageCleanup} {
		t.Run(string(action), func(t *testing.T) {
			m := manager(t)
			r, sources := snapshotRequest(t, m, false)
			preview := storageDo(t, m, r)
			r.OperationID, r.SnapshotID, r.Action, r.PreviewDigest = domain.NewID(), domain.NewID(), action, preview.PreviewDigest
			m.storageScratchCleanupFault = func(string) error { return os.ErrPermission }
			if _, err := m.Storage(context.Background(), r); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal(err)
			}
			if err := os.Rename(sources[0], sources[0]+"-offline"); err != nil {
				t.Fatal(err)
			}
			m.storageScratchCleanupFault = nil
			result := storageDo(t, m, recoveryRequest(r))
			expected := domain.JobFailed
			if action == StorageCreate {
				expected = domain.JobSucceeded
			}
			if result.RecoveredJobState != expected || result.WorkspaceState != domain.WorkspacePresent || !result.CleanupVerified {
				t.Fatal(result)
			}
		})
	}
}
