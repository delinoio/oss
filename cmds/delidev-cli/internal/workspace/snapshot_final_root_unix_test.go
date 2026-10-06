// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestFinalRootRemovalPreservesPrivateReplacementAfterIdentityCheck(t *testing.T) {
	m, r := finalRootFixture(t, StorageCleanup)
	m.storageFinalRootFault = func(stage storageFinalRootStage) error {
		if stage == storageFinalRootVerified {
			return errors.New("fixture interruption")
		}
		return nil
	}
	if _, err := m.Storage(context.Background(), r); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("fixture did not retain final-root transition", err)
	}
	private := finalRootFixturePath(t, m, r.OperationID)
	moved := filepath.Join(m.Root, "moved-private-final-root")
	m.storageFinalRootFault = func(stage storageFinalRootStage) error {
		if stage != storageFinalRootBeforeUnlink {
			return nil
		}
		if err := os.Rename(private, moved); err != nil {
			return err
		}
		return os.Mkdir(private, 0700)
	}

	result, err := m.Storage(context.Background(), recoveryRequest(r))
	if domain.SafeError(err).Code != domain.RecoveryRequired || result.CleanupVerified || result.RemovedSourceBytes != 0 {
		t.Fatal("private replacement granted completion", result, err)
	}
	if _, err := os.Lstat(private); err != nil {
		t.Fatal("private replacement was removed", err)
	}
	if _, err := os.Lstat(moved); err != nil {
		t.Fatal("original private root was removed", err)
	}
}
