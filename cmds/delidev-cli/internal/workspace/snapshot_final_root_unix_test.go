// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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

type faultyClaimTemp struct {
	*os.File
	chmodErr error
	writeErr error
	short    bool
}

func (f *faultyClaimTemp) Chmod(mode os.FileMode) error {
	if f.chmodErr != nil {
		return f.chmodErr
	}
	return f.File.Chmod(mode)
}

func (f *faultyClaimTemp) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	if f.short {
		return f.File.Write(p[:len(p)/2])
	}
	return f.File.Write(p)
}

func finalRootClaimFixture(t *testing.T) (*Manager, storageFinalRootClaim, string) {
	t.Helper()
	m, r := finalRootFixture(t, StorageCleanup)
	claim := storageFinalRootClaim{RootName: domain.NewID(), Version: 1, Reference: removalReference(r)}
	path := m.finalRemovalClaimPath(r.OperationID)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	return m, claim, path
}

func assertNoClaimPublished(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("claim must not be published, lstat error: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pending-"+filepath.Base(path)) {
			t.Fatalf("temporary claim file leaked: %s", entry.Name())
		}
	}
}

func TestPublishFinalRemovalClaimAbortsOnTempWriteFailure(t *testing.T) {
	chmodErr := errors.New("fixture chmod failure")
	writeErr := errors.New("fixture write failure")
	cases := []struct {
		name  string
		fault faultyClaimTemp
		want  error
	}{
		{name: "chmod", fault: faultyClaimTemp{chmodErr: chmodErr}, want: chmodErr},
		{name: "write", fault: faultyClaimTemp{writeErr: writeErr}, want: writeErr},
		{name: "short-write", fault: faultyClaimTemp{short: true}, want: io.ErrShortWrite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, claim, path := finalRootClaimFixture(t)
			m.storageClaimTempFile = func(f *os.File) claimTempFile {
				fault := tc.fault
				fault.File = f
				return &fault
			}
			err := m.publishFinalRemovalClaim(context.Background(), claim, storageFinalRootPrepared)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			assertNoClaimPublished(t, path)
		})
	}
}

func TestPublishFinalRemovalClaimPublishesExactPrivateBytesOnce(t *testing.T) {
	m, claim, path := finalRootClaimFixture(t)
	if err := m.publishFinalRemovalClaim(context.Background(), claim, storageFinalRootPrepared); err != nil {
		t.Fatal(err)
	}
	claim.State = storageFinalRootPrepared
	want, err := json.Marshal(claim)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("claim bytes differ: got %q want %q", got, want)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("claim mode = %v, want 0600", info.Mode().Perm())
	}
	if err := m.publishFinalRemovalClaim(context.Background(), claim, storageFinalRootPrepared); err == nil {
		t.Fatal("second publish must fail the no-replace rename")
	}
	again, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(again, want) {
		t.Fatalf("existing claim must be preserved: %v", err)
	}
}
