// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSnapshotRecoveryEstablishesOnlyIntactRenamedOriginalClaim(t *testing.T) {
	for _, mode := range []string{"intact", "changed", "absent", "malformed-claim"} {
		t.Run(mode, func(t *testing.T) {
			m := manager(t)
			prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
			manifest, err := m.Prepare(context.Background(), prepare)
			if err != nil {
				t.Fatal(err)
			}
			keep := filepath.Join(manifest.PrimaryPath, "keep")
			if err := os.WriteFile(keep, []byte("original captured bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			input := StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StoragePreview, Preparation: prepare, Manifest: manifest}
			preview := storageDo(t, m, input)
			input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
			ctx, cancel := context.WithCancel(context.Background())
			m.storageBeforeRemovalClaim = cancel
			if _, err := m.Storage(ctx, input); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal(err)
			}
			cancel()
			// Reproduce the abrupt-stop window, with the durable original intent and
			// published snapshot intact but no claim and no individual unlink yet.
			removal := filepath.Join(m.Root, "workspace-removals", string(input.OperationID))
			source := filepath.Join(m.Root, "workspaces", string(prepare.SessionID))
			if err := os.Rename(source, removal); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "changed":
				if err := os.WriteFile(filepath.Join(removal, "chat", "keep"), []byte("foreign replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			case "absent":
				if err := os.Rename(removal, removal+"-preserved"); err != nil {
					t.Fatal(err)
				}
			case "malformed-claim":
				if err := os.WriteFile(m.removalClaimPath(input.OperationID), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			m = &Manager{Root: m.Root, Logger: m.Logger}
			result, err := m.Storage(context.Background(), recoveryRequest(input))
			if mode == "intact" {
				if err != nil || !result.CleanupVerified || result.WorkspaceState != domain.WorkspaceStored || result.RecoveredJobState != domain.JobSucceeded {
					t.Fatal("intact original rename stayed stranded", result, err)
				}
				if _, err := os.Lstat(removal); !os.IsNotExist(err) {
					t.Fatal("recovered removal remained", err)
				}
			} else {
				if domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("unknown removal acquired authority", err)
				}
				if mode == "changed" {
					if raw, err := os.ReadFile(filepath.Join(removal, "chat", "keep")); err != nil || string(raw) != "foreign replacement" {
						t.Fatal("foreign bytes changed", err)
					}
				}
			}
		})
	}
}
