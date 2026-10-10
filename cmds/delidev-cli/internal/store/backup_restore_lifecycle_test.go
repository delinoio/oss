// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
	"testing/synctest"
	"time"
)

func TestRestoreAdmissionReadIsCancellableBehindOriginalRestore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _, owner, _, _ := restoreFixture(t)
		s.gate.Lock()
		defer s.gate.Unlock()
		ctx, cancel := context.WithTimeout(owner, 30*time.Second)
		defer cancel()
		started := time.Now()
		if err := s.AuthorizeBackupRestore(ctx); err == nil {
			t.Fatal("blocked authorization lost its deadline")
		}
		if time.Since(started) != 30*time.Second {
			t.Fatal("authorization wait changed original bound", time.Since(started))
		}
		worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice})
		if err := s.AuthorizeBackupRestore(worker); domain.SafeError(err).Code != domain.PermissionDenied {
			t.Fatal("unauthorized actor waited behind restore", err)
		}
	})
}

func TestRestoreServerCancellationPreservesOriginalBarrierRecovery(t *testing.T) {
	for _, boundary := range []string{"staged", "prepared", "renamed"} {
		t.Run(boundary, func(t *testing.T) {
			s, root, owner, in, _ := restoreFixture(t)
			ctx, cancel := context.WithCancel(owner)
			defer cancel()
			request := domain.NewID()
			_, _, err := s.restoreBackup(ctx, request, in, func(phase string) error {
				if phase == boundary {
					cancel()
					return ctx.Err()
				}
				return nil
			})
			if err == nil {
				t.Fatal("original cancellation was ignored")
			}
			s.Close()
			reopened, err := Open(owner, root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			receipt, err := reopened.GetBackupRestore(owner, request)
			switch boundary {
			case "staged":
				if domain.SafeError(err).Code != domain.NotFound {
					t.Fatal("unprepared cancellation fabricated receipt", receipt, err)
				}
			case "prepared":
				if err != nil || receipt.State != RestoreRolledBack {
					t.Fatal("prepared cancellation lost original rollback", receipt, err)
				}
			case "renamed":
				if err != nil || receipt.State != RestoreCompleted {
					t.Fatal("published cancellation lost original completion", receipt, err)
				}
			}
			inspection, err := reopened.InspectBackup(owner, in.Backup.ID, in.ServerID)
			if err != nil || inspection.SHA256 != in.SHA256 {
				t.Fatal("cancellation changed source image", err)
			}
		})
	}
}

func TestRestoreControlledCandidatePreparationBeyondThirtySeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, root, owner, in, _ := restoreFixture(t)
		request := domain.NewID()
		started := time.Now()
		result, replayed, err := s.restoreBackup(owner, request, in, func(boundary string) error {
			if boundary == "staged" {
				time.Sleep(31 * time.Second)
			}
			return nil
		})
		if err != nil || replayed || result.RequestID != request || result.State != RestorePublished || time.Since(started) < 31*time.Second {
			t.Fatal("controlled preparation lost original publication", result, err)
		}
		s.Close()
		reopened, err := Open(owner, root)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		receipt, err := reopened.GetBackupRestore(owner, request)
		if err != nil || receipt.State != RestoreCompleted || receipt.Input != in {
			t.Fatal("original completed receipt changed", receipt, err)
		}
	})
}
