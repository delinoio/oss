//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishedSnapshotRecoveryPreservesLaterFIFO(t *testing.T) {
	for _, action := range []StorageAction{StorageCreate, StorageCleanup} {
		t.Run(string(action), func(t *testing.T) {
			m, r := snapshotPublicationFixture(t, action)
			m.storageScratchCleanupFault = func(string) error { return os.ErrPermission }
			if _, err := m.Storage(context.Background(), r); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal(err)
			}
			path := filepath.Join(r.Manifest.PrimaryPath, "later-pipe")
			if err := unix.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
			m.storageScratchCleanupFault = nil
			result := storageDo(t, m, recoveryRequest(r))
			expected := domain.JobFailed
			if action == StorageCreate {
				expected = domain.JobSucceeded
			}
			if result.RecoveredJobState != expected || !result.CleanupVerified || result.WorkspaceState != domain.WorkspacePresent {
				t.Fatal(result)
			}
			if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
				t.Fatal("FIFO changed", err)
			}
		})
	}
}
