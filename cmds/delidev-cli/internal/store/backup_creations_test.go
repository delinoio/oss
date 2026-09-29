package store

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func creationFixture(t *testing.T) (*Store, string, context.Context, domain.ID) {
	t.Helper()
	s, root := openTest(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	owner := domain.NewID()
	if err := s.BindIdentity(ctx, owner); err != nil {
		t.Fatal(err)
	}
	return s, root, ctx, owner
}
func TestBackupCreationConcurrentReceiptRestartAndLostPublication(t *testing.T) {
	s, root, ctx, owner := creationFixture(t)
	request := domain.NewID()
	var wg sync.WaitGroup
	rows := make(chan Record, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, _, err := s.RequestBackup(ctx, request, owner)
			if err != nil {
				t.Error(err)
				return
			}
			rows <- row
		}()
	}
	wg.Wait()
	close(rows)
	var original Record
	for row := range rows {
		if original.ID != "" && original.ID != row.ID {
			t.Fatal("request created another job")
		}
		original = row
	}
	_, intent, err := DecodeBackupCreation(original)
	if err != nil {
		t.Fatal(err)
	}
	if items, err := s.BackupInventory(ctx); err != nil || len(items) != 0 {
		t.Fatal("admission copied an image", items, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.RunBackupCreation(canceled, original.ID, owner); err == nil {
		t.Fatal("canceled copy succeeded")
	}
	// Simulate a synchronized original image followed by process loss before the
	// completion receipt. The same job must publish that image after reopening.
	if _, err := s.BackupID(ctx, intent.BackupID); err != nil {
		t.Fatal(err)
	}
	before, err := s.InspectBackup(ctx, intent.BackupID, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	current, err := reopened.RunBackupCreation(ctx, original.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := DecodeBackupCreation(current)
	if err != nil || job.State != domain.JobSucceeded || job.FinishedAt == nil {
		t.Fatal(job, err)
	}
	repeated, replayed, err := reopened.RequestBackup(ctx, request, owner)
	if err != nil || !replayed || repeated.ID != original.ID || repeated.Revision != current.Revision {
		t.Fatal(repeated, replayed, err)
	}
	after, err := reopened.InspectBackup(ctx, intent.BackupID, owner)
	if err != nil || before.SHA256 != after.SHA256 || !before.ModifiedAt.Equal(after.ModifiedAt) {
		t.Fatal("original image was replaced", err)
	}
}
func TestBackupCreationCannotReviveDeletedImage(t *testing.T) {
	s, _, ctx, owner := creationFixture(t)
	row, _, err := s.RequestBackup(ctx, domain.NewID(), owner)
	if err != nil {
		t.Fatal(err)
	}
	_, intent, _ := DecodeBackupCreation(row)
	if _, err := s.BackupID(ctx, intent.BackupID); err != nil {
		t.Fatal(err)
	}
	checked, err := s.InspectBackup(ctx, intent.BackupID, owner)
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	deletion, _, err := s.DeleteBackup(ctx, domain.NewID(), BackupDeletionInput{Actor: actor, ServerID: owner, Backup: checked.Backup, ExpectedRevision: 1, SHA256: checked.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunBackupDeletion(ctx, deletion.ID, owner); err != nil {
		t.Fatal(err)
	}
	current, err := s.RunBackupCreation(ctx, row.ID, owner)
	if err == nil {
		t.Fatal("deleted image recreation succeeded")
	}
	job, _, _ := DecodeBackupCreation(current)
	if job.State != domain.JobFailed || job.Problem.Code != domain.RecoveryRequired {
		t.Fatal(job)
	}
	if items, err := s.BackupInventory(ctx); err != nil || len(items) != 0 {
		t.Fatal("deleted backup revived", items, err)
	}
}
func TestBackupCreationBoundsQueueAndRetriesPrivateStorageFailure(t *testing.T) {
	s, root, ctx, owner := creationFixture(t)
	var first Record
	for range MaxPendingBackupCreations {
		row, _, err := s.RequestBackup(ctx, domain.NewID(), owner)
		if err != nil {
			t.Fatal(err)
		}
		if first.ID == "" {
			first = row
		}
	}
	if _, _, err := s.RequestBackup(ctx, domain.NewID(), owner); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal(err)
	}
	_, intent, _ := DecodeBackupCreation(first)
	// A nonempty directory at the private unpublished name simulates a cleanup
	// failure without changing global permissions or unrelated user files.
	pending := filepath.Join(root, "backups", string(intent.BackupID)+".sqlite.pending")
	if err := os.Mkdir(pending, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending, "held"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	failed, err := s.RunBackupCreation(ctx, first.ID, owner)
	if err == nil {
		t.Fatal("blocked publication succeeded")
	}
	job, _, _ := DecodeBackupCreation(failed)
	if job.State != domain.JobUncertain {
		t.Fatal(job)
	}
	var receiptsBefore, receiptsAfter int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM receipts").Scan(&receiptsBefore); err != nil {
		t.Fatal(err)
	}
	repeated, err := s.RunBackupCreation(ctx, first.ID, owner)
	if err == nil || repeated.Revision != failed.Revision {
		t.Fatal("unchanged failure rewrote status", repeated, err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM receipts").Scan(&receiptsAfter); err != nil || receiptsBefore != receiptsAfter {
		t.Fatal("unchanged retry grew receipts", receiptsBefore, receiptsAfter, err)
	}
	if err := os.RemoveAll(pending); err != nil {
		t.Fatal(err)
	}
	current, err := s.RunBackupCreation(ctx, first.ID, owner)
	job, _, _ = DecodeBackupCreation(current)
	if err != nil || job.State != domain.JobSucceeded {
		t.Fatal(job, err)
	}
	if _, _, err := s.RequestBackup(ctx, domain.NewID(), owner); err != nil {
		t.Fatal("completed job held queue slot", err)
	}
}
func TestBackupCreationRejectsRevokedOriginalActor(t *testing.T) {
	s, _, ctx, owner := creationFixture(t)
	device := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.device", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Fixture", Type: domain.ClientDevice})
	})
	if err != nil {
		t.Fatal(err)
	}
	client := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: device})
	request := domain.NewID()
	row, _, err := s.RequestBackup(client, request, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RequestBackup(ctx, request, owner); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("actor borrowed receipt", err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.revoke", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.DeviceKind, device, 1, "", "", domain.Device{Name: "Fixture", Type: domain.ClientDevice, Revoked: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.RunBackupCreation(ctx, row.ID, owner)
	job, _, _ := DecodeBackupCreation(current)
	if err == nil || job.State != domain.JobFailed || job.Problem.Code != domain.Unauthenticated {
		t.Fatal(job, err)
	}
	if items, err := s.BackupInventory(ctx); err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
}

func TestBackupCreationRevocationWinsBeforeImageWriteGate(t *testing.T) {
	s, root, ownerCtx, owner := creationFixture(t)
	device := domain.NewID()
	if _, err := s.Mutate(ownerCtx, domain.NewID(), "fixture.device", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Fixture", Type: domain.ClientDevice})
	}); err != nil {
		t.Fatal(err)
	}
	actor := domain.Principal{Type: domain.ClientDevice, DeviceID: device}
	clientCtx := domain.WithPrincipal(context.Background(), actor)
	row, _, err := s.RequestBackup(clientCtx, domain.NewID(), owner)
	if err != nil {
		t.Fatal(err)
	}
	_, intent, _ := DecodeBackupCreation(row)
	copyReached := false
	current, err := s.runBackupCreation(ownerCtx, row.ID, owner, func(work context.Context, id domain.ID) (domain.ID, error) {
		copyReached = true
		if observed, ok := domain.PrincipalFrom(work); !ok || observed != actor {
			t.Fatal("maintenance substituted its own backup authority")
		}
		// A successful preliminary read is insufficient. Commit revocation at
		// the real copy boundary, after acceptance and before the write gate.
		if err := s.Read(work, func(tx *Tx) error { return tx.Authorize() }); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Mutate(ownerCtx, domain.NewID(), "fixture.revoke", nil, func(tx *Tx) (any, error) {
			return tx.Put(domain.DeviceKind, device, 1, "", "", domain.Device{Name: "Fixture", Type: domain.ClientDevice, Revoked: true})
		}); err != nil {
			t.Fatal(err)
		}
		return s.BackupID(work, id)
	})
	job, _, decodeErr := DecodeBackupCreation(current)
	if !copyReached || err == nil || decodeErr != nil || job.State != domain.JobFailed || job.Problem.Code != domain.Unauthenticated {
		t.Fatal("revocation lost to stale backup authority", job, err, decodeErr)
	}
	for _, suffix := range []string{".sqlite", ".sqlite.pending"} {
		if _, err := os.Lstat(filepath.Join(root, "backups", string(intent.BackupID)+suffix)); !os.IsNotExist(err) {
			t.Fatal("revoked client caused a filesystem side effect", suffix, err)
		}
	}
}

func TestBackupCreationWaitersHonorCancellationDuringOtherImageOwnership(t *testing.T) {
	s, _, ctx, owner := creationFixture(t)
	row, _, err := s.RequestBackup(ctx, domain.NewID(), owner)
	if err != nil {
		t.Fatal(err)
	}
	s.backupGate.Lock()
	defer s.backupGate.Unlock()
	wait, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := s.RunBackupCreation(wait, row.ID, owner); done <- err }()
	cancel()
	select {
	case err := <-done:
		if domain.SafeError(err).Code != domain.Canceled {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled creation waited for unrelated image lock")
	}
}

func TestBackupCreationRecoveryRejectsForeignImageAndAdjacentWAL(t *testing.T) {
	for _, scenario := range []string{"foreign", "sidecar"} {
		t.Run(scenario, func(t *testing.T) {
			s, root, ctx, owner := creationFixture(t)
			row, _, err := s.RequestBackup(ctx, domain.NewID(), owner)
			if err != nil {
				t.Fatal(err)
			}
			_, intent, _ := DecodeBackupCreation(row)
			target := filepath.Join(root, "backups", string(intent.BackupID)+".sqlite")
			if scenario == "foreign" {
				other, otherRoot, otherCtx, _ := creationFixture(t)
				id, err := other.Backup(otherCtx)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(filepath.Join(otherRoot, "backups", string(id)+".sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, raw, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.BackupID(ctx, intent.BackupID); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target+"-wal", []byte("untrusted fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.RunBackupCreation(ctx, row.ID, owner)
			job, _, decodeErr := DecodeBackupCreation(current)
			if err == nil || decodeErr != nil || job.State != domain.JobFailed || job.Problem.Code != domain.RecoveryRequired {
				t.Fatal(job, err, decodeErr)
			}
			after, err := os.ReadFile(target)
			if err != nil || string(before) != string(after) {
				t.Fatal("unsafe image changed", err)
			}
		})
	}
}
