package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func deletionFixture(t *testing.T) (*Store, string, context.Context, BackupDeletionInput) {
	t.Helper()
	s, root := openTest(t)
	actor := domain.Principal{Type: domain.OwnerDevice}
	ctx := domain.WithPrincipal(context.Background(), actor)
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	id, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := s.InspectBackup(ctx, id, server)
	if err != nil {
		t.Fatal(err)
	}
	return s, root, ctx, BackupDeletionInput{Actor: actor, ServerID: server, Backup: checked.Backup, ExpectedRevision: 1, SHA256: checked.SHA256}
}
func TestBackupDeletionDurableJobReplayAndConcurrentClaims(t *testing.T) {
	s, root, ctx, in := deletionFixture(t)
	request := domain.NewID()
	var rows [8]Record
	var errs [8]error
	var wg sync.WaitGroup
	for i := range rows {
		wg.Add(1)
		go func() { defer wg.Done(); rows[i], _, errs[i] = s.DeleteBackup(ctx, request, in) }()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil || rows[i].ID != rows[0].ID {
			t.Fatal(i, err, rows)
		}
	}
	if _, _, err := s.DeleteBackup(ctx, domain.NewID(), in); err == nil {
		t.Fatal("duplicate independent deletion accepted")
	}
	if _, err := s.BackupID(ctx, in.Backup.ID); err == nil {
		t.Fatal("creation retry bypassed accepted job")
	}
	if _, err := s.InspectBackup(ctx, in.Backup.ID, in.ServerID); err == nil {
		t.Fatal("pending deletion still inspectable")
	}
	row, err := s.RunBackupDeletion(ctx, rows[0].ID, in.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := Decode[domain.Job](row)
	if err != nil || job.State != domain.JobSucceeded {
		t.Fatal(job, err)
	}
	var output BackupDeletionOutput
	if domain.Decode(job.Output, &output) != nil || !output.RemovalObserved || output.ImageBytes != in.Backup.Bytes {
		t.Fatal(string(job.Output))
	}
	if _, err := os.Lstat(filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := s.readDeletionIntent(in.Backup.ID); err != nil {
		t.Fatal(err)
	}
	repeated, replayed, err := s.DeleteBackup(ctx, request, in)
	if err != nil || !replayed || repeated.Revision != row.Revision {
		t.Fatal(repeated, replayed, err)
	}
	again, err := s.RunBackupDeletion(ctx, row.ID, in.ServerID)
	if err != nil || again.Revision != row.Revision {
		t.Fatal("completed work was republished", again, err)
	}
	changed := in
	changed.ExpectedRevision = 2
	if _, _, err := s.DeleteBackup(ctx, request, changed); err == nil {
		t.Fatal("stale revision accepted")
	}
	changed = in
	changed.SHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, _, err := s.DeleteBackup(ctx, request, changed); err == nil {
		t.Fatal("same request changed input")
	}
}
func TestBackupDeletionRetriesIntentFailureAndHashMismatchWithoutRemovingSource(t *testing.T) {
	s, root, ctx, in := deletionFixture(t)
	row, _, err := s.DeleteBackup(ctx, domain.NewID(), in)
	if err != nil {
		t.Fatal(err)
	}
	intentPath := s.deletionPath(in.Backup.ID)
	// Replace the already synchronized intent with an invalid entry to emulate
	// inaccessible metadata during later maintenance; the original image stays.
	retained, err := os.ReadFile(intentPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(intentPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(intentPath, 0700); err != nil {
		t.Fatal(err)
	}
	row, err = s.RunBackupDeletion(ctx, row.ID, in.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := Decode[domain.Job](row)
	if job.State != domain.JobUncertain {
		t.Fatal(job)
	}
	image := filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite")
	original, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(intentPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(intentPath, retained, 0600); err != nil {
		t.Fatal(err)
	}
	changed := append([]byte{}, original...)
	changed[len(changed)-1] ^= 1
	if err := os.WriteFile(image, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(image, in.Backup.ModifiedAt, in.Backup.ModifiedAt); err != nil {
		t.Fatal(err)
	}
	row, err = s.RunBackupDeletion(ctx, row.ID, in.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	job, _ = Decode[domain.Job](row)
	if job.State != domain.JobUncertain {
		t.Fatal("changed image deleted", job)
	}
	if err := os.WriteFile(image, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(image, in.Backup.ModifiedAt, in.Backup.ModifiedAt); err != nil {
		t.Fatal(err)
	}
	row, err = s.RunBackupDeletion(ctx, row.ID, in.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	job, _ = Decode[domain.Job](row)
	if job.State != domain.JobSucceeded {
		t.Fatal(job)
	}
}
func TestBackupDeletionSurvivesDatabaseRollbackAndMissingUnlinkReceipt(t *testing.T) {
	s, root, ctx, in := deletionFixture(t)
	baseline, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "backups", string(baseline)+".sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	request := domain.NewID()
	row, _, err := s.DeleteBackup(ctx, request, in)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := Decode[domain.Job](row)
	var intent backupDeletionIntent
	if err := domain.Decode(job.Input, &intent); err != nil {
		t.Fatal(err)
	}
	if err := s.persistDeletionIntent(intent); err != nil {
		t.Fatal(err)
	}
	// Simulate interruption after the physical unlink but before its DB receipt.
	if err := os.Remove(filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state.sqlite"), before, 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.RestoreBackupDeletionIntents(ctx, in.ServerID); err != nil {
		t.Fatal(err)
	}
	repeated, replayed, err := reopened.DeleteBackup(ctx, request, in)
	if err != nil || !replayed || repeated.ID != row.ID {
		t.Fatal(repeated, replayed, err)
	}
	completed, err := reopened.RunBackupDeletion(ctx, row.ID, in.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	job, _ = Decode[domain.Job](completed)
	var output BackupDeletionOutput
	if job.State != domain.JobSucceeded || domain.Decode(job.Output, &output) != nil || output.RemovalObserved || output.ImageBytes != 0 {
		t.Fatal(job)
	}
	if _, err := reopened.BackupID(ctx, in.Backup.ID); err == nil {
		t.Fatal("database rollback revived deleted backup")
	}
}
func TestBackupDeletionRejectsCanceledAndStaleAdmission(t *testing.T) {
	s, _, ctx, in := deletionFixture(t)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := s.DeleteBackup(canceled, domain.NewID(), in); err == nil {
		t.Fatal("canceled request accepted")
	}
	changed := in
	changed.Backup.Bytes++
	if _, _, err := s.DeleteBackup(ctx, domain.NewID(), changed); err == nil {
		t.Fatal("stale metadata accepted")
	}
	rows, err := s.BackupDeletionJobs(ctx, "", 10)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

func TestBackupDeletionMigrationFrom20PreservesExistingTablesAndBackup(t *testing.T) {
	s, root, ctx, in := deletionFixture(t)
	if _, err := s.db.ExecContext(ctx, "DROP TABLE backup_deletions; PRAGMA user_version=20;"); err != nil {
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
	var version int
	if err := reopened.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != 21 {
		t.Fatal(version, err)
	}
	images, err := reopened.BackupInventory(ctx)
	if err != nil || len(images) != 2 {
		t.Fatal(images, err)
	}
	for _, image := range images {
		inspected, err := reopened.InspectBackup(ctx, image.ID, in.ServerID)
		if err != nil {
			t.Fatal(err)
		}
		if image.ID != in.Backup.ID && inspected.SchemaVersion != 20 {
			t.Fatal("migration did not preserve original schema", inspected)
		}
	}
	if _, _, err := reopened.DeleteBackup(ctx, domain.NewID(), in); err != nil {
		t.Fatal(err)
	}
}

func TestBackupDeletionDoesNotAcknowledgeMissingExternalIntent(t *testing.T) {
	s, root, ctx, in := deletionFixture(t)
	journal := filepath.Join(root, "backup-deletions")
	if err := os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	request := domain.NewID()
	row, _, err := s.DeleteBackup(ctx, request, in)
	if err == nil || domain.SafeError(err).Code != domain.Unavailable || row.ID == "" {
		t.Fatal(row, err)
	}
	if _, err := os.Stat(filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(journal, 0700); err != nil {
		t.Fatal(err)
	}
	repeated, replayed, err := s.DeleteBackup(ctx, request, in)
	if err != nil || !replayed || repeated.ID != row.ID {
		t.Fatal(repeated, replayed, err)
	}
	if _, err := s.readDeletionIntent(in.Backup.ID); err != nil {
		t.Fatal(err)
	}
}

func TestBackupDeletionCompletedScansAvoidSyncAndStillRemoveReappearingImages(t *testing.T) {
	s, root, ctx, in := deletionFixture(t)
	path := filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite")
	image, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	row, _, err := s.DeleteBackup(ctx, domain.NewID(), in)
	if err != nil {
		t.Fatal(err)
	}
	syncs := 0
	syncParent := func(path string) error { syncs++; return security.SyncParent(path) }
	row, err = s.runBackupDeletion(ctx, row.ID, in.ServerID, syncParent)
	if err != nil || syncs != 2 {
		t.Fatal(row, err, syncs)
	}
	for range 3 {
		syncs = 0
		next, err := s.runBackupDeletion(ctx, row.ID, in.ServerID, syncParent)
		if err != nil || next.Revision != row.Revision || syncs != 0 {
			t.Fatal(next, err, syncs)
		}
	}
	if err := os.WriteFile(path, image, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, in.Backup.ModifiedAt, in.Backup.ModifiedAt); err != nil {
		t.Fatal(err)
	}
	syncs = 0
	next, err := s.runBackupDeletion(ctx, row.ID, in.ServerID, syncParent)
	if err != nil || next.Revision != row.Revision || syncs != 1 {
		t.Fatal(next, err, syncs)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestBackupDeletionMissingUnlinkAcknowledgmentStillRequiresSync(t *testing.T) {
	s, root, ctx, in := deletionFixture(t)
	row, _, err := s.DeleteBackup(ctx, domain.NewID(), in)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	syncs := 0
	failSync := func(name string) error {
		syncs++
		if name == path {
			return errors.New("directory sync failed")
		}
		return security.SyncParent(name)
	}
	row, err = s.runBackupDeletion(ctx, row.ID, in.ServerID, failSync)
	job, decodeErr := Decode[domain.Job](row)
	if err != nil || decodeErr != nil || job.State != domain.JobUncertain || syncs != 2 {
		t.Fatal(job, err, decodeErr, syncs)
	}
	syncs = 0
	row, err = s.runBackupDeletion(ctx, row.ID, in.ServerID, func(name string) error { syncs++; return security.SyncParent(name) })
	job, decodeErr = Decode[domain.Job](row)
	if err != nil || decodeErr != nil || job.State != domain.JobSucceeded || syncs != 2 {
		t.Fatal(job, err, decodeErr, syncs)
	}
}
