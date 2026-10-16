// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

type failedRemnantReader struct{}

func (failedRemnantReader) Read([]byte) (int, error) { return 0, errors.New("synthetic read failure") }

func TestLegacyRetainedReaderScansCompleteBoundedFile(t *testing.T) {
	benign := bytes.Repeat([]byte("x"), 64<<10)
	for _, tc := range []struct {
		name    string
		reader  io.Reader
		size    int64
		blocked bool
	}{
		{"late-marker", bytes.NewReader(append(bytes.Clone(benign), []byte("refresh_token")...)), int64(len(benign) + len("refresh_token")), true},
		{"late-jwt", bytes.NewReader(append(bytes.Clone(benign), []byte("eyJabcdefgh.ijklmnop.")...)), int64(len(benign) + len("eyJabcdefgh.ijklmnop.")), true},
		{"split-marker", io.MultiReader(bytes.NewReader(append(bytes.Clone(benign), []byte("refresh_")...)), bytes.NewReader([]byte("token"))), int64(len(benign) + len("refresh_token")), true},
		{"split-jwt", io.MultiReader(bytes.NewReader(append(bytes.Clone(benign), []byte("eyJabcdefgh.ijkl")...)), bytes.NewReader([]byte("mnop."))), int64(len(benign) + len("eyJabcdefgh.ijklmnop.")), true},
		{"benign", bytes.NewReader(append(bytes.Clone(benign), 'z')), int64(len(benign) + 1), false},
		{"short-read", bytes.NewReader(benign), int64(len(benign) + 1), true},
		{"grew-during-read", bytes.NewReader(append(bytes.Clone(benign), 'z')), int64(len(benign)), true},
		{"read-error", io.MultiReader(bytes.NewReader(benign), failedRemnantReader{}), int64(len(benign) + 1), true},
		{"oversize", bytes.NewReader(nil), subscription.RuntimeByteLimit + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if blocked := scanRemnantReader(tc.reader, tc.size) != nil; blocked != tc.blocked {
				t.Fatalf("blocked = %v, want %v", blocked, tc.blocked)
			}
		})
	}
}

func TestLegacyQuotaRetainedFileScanControlsSettlement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix helper aliases are not created on Windows")
	}
	benign := bytes.Repeat([]byte("x"), 64<<10)
	for _, tc := range []struct {
		name    string
		content []byte
		blocked bool
	}{
		{"late-marker", append(bytes.Clone(benign), []byte("refresh_token")...), true},
		{"late-jwt", append(bytes.Clone(benign), []byte("eyJabcdefgh.ijklmnop.")...), true},
		{"benign", append(bytes.Clone(benign), 'z'), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, n, op, home, aliases := historicalQuotaFixture(t, true)
			path := filepath.Join(home, "codex", "log.txt")
			if err := os.WriteFile(path, tc.content, 0600); err != nil {
				t.Fatal(err)
			}
			_, before := f.record()
			restartServerQuotas(t, f)
			_, after := f.record()
			quota := after.Subscription.ServerQuota
			receipt, exists, err := f.service.nativeProfilesMust(t).ReadReceipt(context.Background(), nativeprofile.ReceiptLegacyQuota, op)
			if err != nil || n.reads.Load() != 0 {
				t.Fatal("reconciliation failed or replayed the original quota", err)
			}
			if tc.blocked {
				if exists || quota.CleanupConfirmed || quota.Phase != domain.SubscriptionObservationUncertain || !referenceExists(t, f, before.Subscription.Generation) {
					t.Fatalf("unsafe retained file released the quota fence: receipt=%+v quota=%+v", receipt, quota)
				}
			} else if !exists || receipt.Step != nativeprofile.ReceiptSettled || !quota.CleanupConfirmed || quota.Phase != domain.SubscriptionObservationFailed {
				t.Fatalf("benign retained file blocked quota settlement: receipt=%+v quota=%+v", receipt, quota)
			}
			assertLegacyRetainedBytes(t, path, tc.content, aliases)
			restartServerQuotas(t, f)
			_, replay := f.record()
			if !reflect.DeepEqual(replay.Subscription.ServerQuota, quota) {
				t.Fatal("restart changed the original quota outcome")
			}
			assertLegacyRetainedBytes(t, path, tc.content, aliases)
		})
	}
	// The cumulative runtime bound must reject the file before any receipt.
	t.Run("oversize", func(t *testing.T) {
		f, _, op, home, _ := historicalQuotaFixture(t, true)
		path := filepath.Join(home, "codex", "log.txt")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(subscription.RuntimeByteLimit + 1); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		restartServerQuotas(t, f)
		_, a := f.record()
		_, exists, err := f.service.nativeProfilesMust(t).ReadReceipt(context.Background(), nativeprofile.ReceiptLegacyQuota, op)
		if err != nil || exists || a.Subscription.ServerQuota.CleanupConfirmed {
			t.Fatal("oversize retained file released the quota fence", err)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		f, _, op, home, _ := historicalQuotaFixture(t, true)
		path := filepath.Join(home, "codex", "log.txt")
		if err := os.WriteFile(path, benign, 0000); err != nil {
			t.Fatal(err)
		}
		restartServerQuotas(t, f)
		_, a := f.record()
		_, exists, err := f.service.nativeProfilesMust(t).ReadReceipt(context.Background(), nativeprofile.ReceiptLegacyQuota, op)
		if err != nil || exists || a.Subscription.ServerQuota.CleanupConfirmed {
			t.Fatal("unreadable retained file released the quota fence", err)
		}
	})
}

func TestLegacyCreditRetainedFileScanControlsSettlement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix helper aliases are not created on Windows")
	}
	benign := bytes.Repeat([]byte("x"), 64<<10)
	for _, tc := range []struct {
		name    string
		content []byte
		blocked bool
	}{
		{"late-marker", append(bytes.Clone(benign), []byte("access_token")...), true},
		{"late-jwt", append(bytes.Clone(benign), []byte("eyJabcdefgh.ijklmnop.")...), true},
		{"benign", append(bytes.Clone(benign), 'z'), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, n, lease := newAccessCreditFixture(t, accessCreditOptions{details: true, active: true})
			_, before := f.record()
			requestAccessCredit(t, f, requestServerCredit(f))
			rotateExecution(t, f, lease, "second")
			mutateCreditAccount(t, f, func(a *domain.Account) {
				a.Subscription.ServerCredit.Phase = domain.SubscriptionObservationUncertain
				a.Subscription.ServerCredit.AttemptEpoch = domain.NewID()
			})
			_, queued := f.record()
			operation := queued.Subscription.ServerCredit.AttemptID
			runtimeRoot := filepath.Join(f.service.Store.Root(), "subscription-runtime")
			home := filepath.Join(runtimeRoot, "credit", string(operation))
			for _, path := range []string{runtimeRoot, filepath.Join(runtimeRoot, "processes"), filepath.Join(runtimeRoot, "processes", string(operation)), filepath.Join(runtimeRoot, "credit"), home} {
				if err := security.PrivateDir(path); err != nil {
					t.Fatal(err)
				}
			}
			aliases := writeQuotaHelperRuntime(t, home, true)
			path := filepath.Join(home, "codex", "log.txt")
			if err := os.WriteFile(path, tc.content, 0600); err != nil {
				t.Fatal(err)
			}
			if err := f.service.initializeServerQuotas(creditContext()); err != nil {
				t.Fatal(err)
			}
			f.service.runServerCredit(creditContext(), f.input.AccountID)
			_, after := f.record()
			credit := after.Subscription.ServerCredit
			receipt, exists, err := f.service.nativeProfilesMust(t).ReadReceipt(context.Background(), nativeprofile.ReceiptLegacyCredit, operation)
			if err != nil || n.count() != 0 || n.opens != 0 {
				t.Fatal("reconciliation failed or sent the original credit", err)
			}
			if tc.blocked {
				if exists || credit.CleanupConfirmed || credit.Phase != domain.SubscriptionObservationUncertain || !referenceExists(t, f, before.Subscription.Generation) {
					t.Fatalf("unsafe retained file released the credit fence: receipt=%+v credit=%+v", receipt, credit)
				}
			} else if !exists || receipt.Step != nativeprofile.ReceiptSettled || !credit.CleanupConfirmed || credit.Phase != domain.SubscriptionObservationFailed || !referenceExists(t, f, before.Subscription.Generation) || !referenceExists(t, f, after.Subscription.Generation) {
				t.Fatalf("benign retained file blocked credit settlement: receipt=%+v credit=%+v", receipt, credit)
			}
			assertLegacyRetainedBytes(t, path, tc.content, aliases)
		})
	}
}

func assertLegacyRetainedBytes(t *testing.T, path string, want []byte, aliases string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("reconciliation changed retained file bytes", err)
	}
	for _, name := range quotaHelperAliasNames {
		info, err := os.Lstat(filepath.Join(aliases, name))
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatal("reconciliation changed a helper link", name, err)
		}
	}
}
