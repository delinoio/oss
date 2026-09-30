// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func restoreReservedStore(t *testing.T, rollback bool) (*Store, context.Context, BackupRestoreInput, domain.ID) {
	t.Helper()
	s, root, ctx, input, _ := restoreFixture(t)
	request := domain.NewID()
	if rollback {
		_, _, err := s.restoreBackup(ctx, request, input, func(boundary string) error {
			if boundary == "prepared" {
				return errors.New("fixture interrupted before publication")
			}
			return nil
		})
		if err == nil {
			t.Fatal("fixture did not interrupt publication")
		}
	} else if err := security.PrivateDir(restoreDirectory(root, request)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if !s.restoreReservations[request] {
		t.Fatal("startup lost the external UUID reservation")
	}
	if rollback {
		receipt, err := s.GetBackupRestore(ctx, request)
		if err != nil || receipt.State != RestoreRolledBack {
			t.Fatal("startup did not reconcile the original database", err)
		}
	}
	return s, ctx, input, request
}

func assertNoReservedReceipt(t *testing.T, s *Store, request domain.ID) {
	t.Helper()
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM receipts WHERE id=?", request).Scan(&count); err != nil || count != 0 {
		t.Fatal("another operation acquired the restore UUID", count, err)
	}
}

func TestBackupRestoreReservationBlocksSessionDeletionAndIntentRecovery(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "unjournaled"
		if rollback {
			name = "rolled-back"
		}
		t.Run(name, func(t *testing.T) {
			s, ctx, input, reserved := restoreReservedStore(t, rollback)
			row, _, _ := deletionSession(t, s, "preserved")
			if _, _, err := s.DeleteSession(ctx, reserved, row.ID, input.ServerID, row.Revision); domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("reserved request accepted destructive deletion", err)
			}
			current, err := s.Get(ctx, row.Kind, row.ID)
			if err != nil || current.Revision != row.Revision || !bytes.Equal(current.Data, row.Data) || s.SessionDeletionRecoveryRequired() {
				t.Fatal("rejected request changed session ownership", err)
			}
			if _, err := s.readSessionDeletion(row.ID); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected request persisted deletion intent", err)
			}
			assertNoReservedReceipt(t, s, reserved)
			// Simulate a conflicting retained deletion intent without its SQL
			// acknowledgement. Recovery must not claim the reserved UUID either.
			intent, _, err := s.DeleteSession(ctx, domain.NewID(), row.ID, input.ServerID, row.Revision)
			if err != nil {
				t.Fatal(err)
			}
			intent.RequestID = reserved
			if err := s.writeSessionDeletion(intent); err != nil {
				t.Fatal(err)
			}
			before, _ := s.Get(ctx, row.Kind, row.ID)
			if err := s.RestoreSessionDeletionIntents(ctx, input.ServerID); domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("intent recovery acquired the reserved UUID", err)
			}
			after, err := s.Get(ctx, row.Kind, row.ID)
			if err != nil || before.Revision != after.Revision || !bytes.Equal(before.Data, after.Data) {
				t.Fatal("failed recovery changed the original session", err)
			}
			assertNoReservedReceipt(t, s, reserved)
		})
	}
}

func TestBackupRestoreReservationBlocksWorkerDeletionAcknowledgmentAndRecovery(t *testing.T) {
	s, owner, worker, instance, intent := sessionDeletionWorkerFixture(t)
	root, reserved := s.Root(), domain.NewID()
	if err := security.PrivateDir(restoreDirectory(root, reserved)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(owner, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Mutate(owner, domain.NewID(), "fixture.reconnect-deletion-worker", nil, func(tx *Tx) (any, error) {
		return nil, tx.SetWorkerInstance(intent.Workers[0].Work.MachineID, instance, time.Now().UTC())
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.sessionDeletionPath(intent.SessionID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcknowledgeSessionDeletion(worker, intent.SessionID, intent.ID, reserved, instance, intent.Workers[0].Work.Digest()); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("Worker acknowledgement acquired restore UUID", err)
	}
	after, err := os.ReadFile(s.sessionDeletionPath(intent.SessionID))
	if err != nil || !bytes.Equal(before, after) || s.SessionDeletionRecoveryRequired() {
		t.Fatal("rejected acknowledgement changed the retained journal", err)
	}
	assertNoReservedReceipt(t, s, reserved)
	intent.Workers[0].Acknowledged, intent.Workers[0].RequestID = true, reserved
	if err := s.writeSessionDeletion(intent); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreSessionDeletionIntents(owner, intent.ServerID); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("Worker receipt recovery acquired restore UUID", err)
	}
	assertNoReservedReceipt(t, s, reserved)
}
