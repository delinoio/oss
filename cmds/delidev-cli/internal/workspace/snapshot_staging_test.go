// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestStorageRecoveryPreservesUnownedStaging(t *testing.T) {
	for _, action := range []StorageAction{StoragePreview, StorageCreate, StorageCleanup} {
		t.Run(string(action), func(t *testing.T) {
			m, prepare, manifest := chatExecutionFixture(t)
			original := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
			preview := storageDo(t, m, original)
			original.Action, original.PreviewDigest = action, preview.PreviewDigest
			if action != StoragePreview {
				original.SnapshotID = domain.NewID()
			}
			staging := filepath.Join(m.Root, "snapshot-staging", string(original.OperationID))
			if err := os.Mkdir(staging, 0700); err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(staging, "foreign")
			if err := os.WriteFile(foreign, []byte("unowned private bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Storage(context.Background(), recoveryRequest(original)); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("foreign staging was treated as operation-owned", err)
			}
			if raw, err := os.ReadFile(foreign); err != nil || string(raw) != "unowned private bytes" {
				t.Fatal("foreign staging was removed", err)
			}
		})
	}
}

func TestRestoreScratchReplacementRemainsUncertain(t *testing.T) {
	m, input := storedGeneralChat(t)
	var replaced string
	m.storageRestoreCopyFault = func(staging string) error {
		replaced = staging + "-original"
		if err := os.Rename(staging, replaced); err != nil {
			t.Fatal(err)
		}
		// Matching bytes cannot adopt a different native directory identity.
		if _, err := walkSnapshot(context.Background(), filepath.Join(m.snapshotPath(input.SnapshotID), "workspace"), staging, nil); err != nil {
			t.Fatal(err)
		}
		return os.ErrPermission
	}
	if _, err := m.Storage(context.Background(), input); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("replacement cleanup settled terminal failure", err)
	}
	foreign := filepath.Join(m.Root, "snapshot-staging", string(input.OperationID), "chat", "keep")
	if raw, err := os.ReadFile(foreign); err != nil || string(raw) != "original" {
		t.Fatal("matching foreign scratch was removed", err)
	}
	if _, err := m.Storage(context.Background(), recoveryRequest(input)); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("recovery adopted replacement scratch", err)
	}
	if _, err := os.Stat(replaced); err != nil {
		t.Fatal("original retained scratch was changed", err)
	}
	if raw, err := os.ReadFile(foreign); err != nil || string(raw) != "original" {
		t.Fatal("recovery removed foreign scratch", err)
	}
}

func TestStorageRecoveryRequiresOriginalStagingClaim(t *testing.T) {
	for _, mutation := range []string{"missing", "legacy", "request"} {
		t.Run(mutation, func(t *testing.T) {
			m, prepare, manifest := chatExecutionFixture(t)
			original := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
			storageDo(t, m, original)
			original.Action, original.SnapshotID = StorageCreate, domain.NewID()
			staging, err := m.createStorageStaging(original)
			if err != nil {
				t.Fatal(err)
			}
			keep := filepath.Join(staging, "keep")
			if err := os.WriteFile(keep, []byte("original incomplete copy"), 0600); err != nil {
				t.Fatal(err)
			}
			if mutation == "missing" {
				if err := os.Remove(m.stagingClaimPath(original.OperationID)); err != nil {
					t.Fatal(err)
				}
			} else {
				claim, err := m.readStagingClaim(original.OperationID)
				if err != nil {
					t.Fatal(err)
				}
				if mutation == "legacy" {
					claim.Version = 0
				} else {
					claim.RequestDigest = strings.Repeat("b", 64)
				}
				raw, _ := json.Marshal(claim)
				if err := security.WriteAtomic(m.stagingClaimPath(original.OperationID), raw); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.Storage(context.Background(), recoveryRequest(original)); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("unknown proof granted removal", err)
			}
			if raw, err := os.ReadFile(keep); err != nil || string(raw) != "original incomplete copy" {
				t.Fatal("unproved scratch removed", err)
			}
		})
	}
}
