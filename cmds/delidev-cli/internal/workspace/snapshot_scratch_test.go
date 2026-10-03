// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSnapshotFailedScratchCleanupRetainsExplicitRecovery(t *testing.T) {
	for _, action := range []StorageAction{StorageCreate, StorageCleanup} {
		t.Run(string(action), func(t *testing.T) {
			m := manager(t)
			input, _ := snapshotRequest(t, m, true)
			preview := storageDo(t, m, input)
			input.OperationID, input.Action, input.SnapshotID, input.PreviewDigest = domain.NewID(), action, domain.NewID(), preview.PreviewDigest
			before, err := m.storageObservation(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			m.storageCopyFault = func(string) error { return snapshotDiskFullError() }
			m.storageScratchCleanupFault = func(string) error { return os.ErrPermission }
			if _, err := m.Storage(context.Background(), input); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("unconfirmed scratch settled as terminal", err)
			}
			staging := filepath.Join(m.Root, "snapshot-staging", string(input.OperationID))
			if _, err := os.Stat(filepath.Join(staging, "workspace")); err != nil {
				t.Fatal("original scratch missing", err)
			}
			after, err := m.storageObservation(context.Background(), input)
			if err != nil || after.Digest != before.Digest {
				t.Fatal("source changed on failed capture", err)
			}
			m.storageCopyFault, m.storageScratchCleanupFault = nil, nil
			recovered := storageDo(t, m, recoveryRequest(input))
			if recovered.RecoveredJobState != domain.JobFailed || recovered.WorkspaceState != domain.WorkspacePresent || recovered.Snapshot != nil || !recovered.CleanupVerified {
				t.Fatal("failed capture recovery misreported", recovered)
			}
			if _, err := os.Stat(staging); !os.IsNotExist(err) {
				t.Fatal("recovery leaked scratch", err)
			}
			after, err = m.storageObservation(context.Background(), input)
			if err != nil || after.Digest != before.Digest {
				t.Fatal("recovery changed source", err)
			}
		})
	}
}
