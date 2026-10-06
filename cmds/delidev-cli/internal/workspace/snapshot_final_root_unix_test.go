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

func TestFinalRootRemovalRejectsPrivateReplacementAfterLastIdentityCheck(t *testing.T) {
	m, r := finalRootFixture(t, StorageCleanup)
	m.storageFinalRootFault = func(stage storageFinalRootStage) error {
		if stage == storageFinalRootClaimed {
			return errors.New("fixture interruption")
		}
		return nil
	}
	if _, err := m.Storage(context.Background(), r); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("fixture did not retain final-root transition", err)
	}
	private := finalRootFixturePath(t, m, r.OperationID)
	moved := filepath.Join(m.Root, "moved-after-final-check")
	m.storageFinalRootFault = func(stage storageFinalRootStage) error {
		if stage != storageFinalRootAfterVerification {
			return nil
		}
		// macOS requires the directory itself to remain writable for a rename;
		// Linux permits a retained parent handle to move it after the final check.
		if err := os.Chmod(private, 0700); err != nil {
			return err
		}
		if err := os.Rename(private, moved); err != nil {
			return err
		}
		return os.Mkdir(private, 0700)
	}

	intent, err := os.ReadFile(m.removalIntentPath(r.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	claim, _, _, err := m.readRemovalClaimState(r, intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.finishFinalRootRemoval(context.Background(), r, claim); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("replacement after the last identity check granted completion", err)
	}
	if _, err := os.Lstat(private); err != nil {
		t.Fatal("private replacement was removed", err)
	}
	if _, err := os.Lstat(moved); err != nil {
		t.Fatal("original private root was removed", err)
	}
}

func TestFinalRootRemovalRestoresModeAfterFailedUnlink(t *testing.T) {
	m, r := finalRootFixture(t, StorageCleanup)
	injected := false
	var private string
	m.storageFinalRootFault = func(stage storageFinalRootStage) error {
		if stage != storageFinalRootBeforeUnlink || injected {
			return nil
		}
		injected = true
		private = finalRootFixturePath(t, m, r.OperationID)
		return os.WriteFile(filepath.Join(private, "retained"), []byte("writer data"), 0600)
	}

	result, err := m.Storage(context.Background(), r)
	if !injected || domain.SafeError(err).Code != domain.RecoveryRequired || result.CleanupVerified {
		t.Fatal("retained writer did not keep removal uncertain", result, err)
	}
	info, err := os.Stat(private)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("failed unlink left private root mode %04o", info.Mode().Perm())
	}
	if err := os.Remove(filepath.Join(private, "retained")); err != nil {
		t.Fatal(err)
	}

	restarted := &Manager{Root: m.Root, Logger: m.Logger}
	recovered, err := restarted.Storage(context.Background(), recoveryRequest(r))
	if err != nil || !recovered.CleanupVerified || recovered.RecoveredJobState != domain.JobSucceeded {
		t.Fatal("original final-root transition did not recover", recovered, err)
	}
}
