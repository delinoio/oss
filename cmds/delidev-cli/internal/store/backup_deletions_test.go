package store

import (
	"context"
	"errors"
	"fmt"
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

func TestBackupDeletionInventorySeparatesObligationsFromPendingRemnants(t *testing.T) {
	root := t.TempDir()
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte("retained fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for range MaxBackupInventory {
		write(string(domain.NewID()) + ".json")
	}
	write(".pending-interrupted")
	ids, err := backupDeletionIntentIDs(root)
	if err != nil || len(ids) != MaxBackupInventory {
		t.Fatal("an atomic-write remnant displaced a recoverable obligation", len(ids), err)
	}
	extra := string(domain.NewID()) + ".json"
	write(extra)
	if _, err := backupDeletionIntentIDs(root); err == nil {
		t.Fatal("obligation limit was relaxed")
	}
	if err := os.Remove(filepath.Join(root, extra)); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < maxBackupDeletionEntries-MaxBackupInventory; i++ {
		write(fmt.Sprintf(".pending-%d", i))
	}
	if ids, err := backupDeletionIntentIDs(root); err != nil || len(ids) != MaxBackupInventory {
		t.Fatal("bounded crash remnants prevented inventory", len(ids), err)
	}
	write(".pending-overflow")
	if _, err := backupDeletionIntentIDs(root); err == nil {
		t.Fatal("independent directory inventory limit was ignored")
	}
	if _, err := os.Stat(filepath.Join(root, ".pending-interrupted")); err != nil {
		t.Fatal("recovery discarded private pending evidence", err)
	}
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
	if err == nil {
		t.Fatal("intent persistence failure was hidden from maintenance")
	}
	job, _ := Decode[domain.Job](row)
	if job.State != domain.JobUncertain {
		t.Fatal(job)
	}
	repeated, repeatErr := s.RunBackupDeletion(ctx, row.ID, in.ServerID)
	if repeatErr == nil || repeated.Revision != row.Revision || domain.SafeError(repeatErr).Code != domain.SafeError(err).Code {
		t.Fatal("unchanged failure was hidden or republished", repeated, repeatErr)
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
	if err == nil || domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("image mismatch was hidden from maintenance", err)
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
	if _, err := s.db.ExecContext(ctx, "DROP INDEX provider_preset_unique; DROP TABLE backup_deletions; DROP TABLE IF EXISTS native_accounting; PRAGMA user_version=20;"); err != nil {
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
	if err := reopened.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
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
	if err != nil || syncs != 4 {
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
	if err != nil || next.Revision != row.Revision || syncs != 3 {
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
	if err == nil || decodeErr != nil || job.State != domain.JobUncertain || syncs != 2 {
		t.Fatal(job, err, decodeErr, syncs)
	}
	syncs = 0
	row, err = s.runBackupDeletion(ctx, row.ID, in.ServerID, func(name string) error { syncs++; return security.SyncParent(name) })
	job, decodeErr = Decode[domain.Job](row)
	if err != nil || decodeErr != nil || job.State != domain.JobSucceeded || syncs != 3 {
		t.Fatal(job, err, decodeErr, syncs)
	}
}

func TestBackupDeletionMigrationFromBothVersion21Layouts(t *testing.T) {
	for _, legacyBackupBranch := range []bool{false, true} {
		t.Run(fmt.Sprint(legacyBackupBranch), func(t *testing.T) {
			s, root, ctx, in := deletionFixture(t)
			var original Record
			if legacyBackupBranch {
				var err error
				original, _, err = s.DeleteBackup(ctx, domain.NewID(), in)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.ExecContext(ctx, "DROP INDEX provider_preset_unique; DROP TABLE IF EXISTS native_accounting; PRAGMA user_version=21;"); err != nil {
					t.Fatal(err)
				}
			} else if _, err := s.db.ExecContext(ctx, "DROP TABLE backup_deletions; DROP TABLE IF EXISTS native_accounting; PRAGMA user_version=21;"); err != nil {
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
			var version, index int
			if err := reopened.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
				t.Fatal(version, err)
			}
			if err := reopened.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='provider_preset_unique' AND type='index'").Scan(&index); err != nil || index != 1 {
				t.Fatal(index, err)
			}
			if legacyBackupBranch {
				row, err := reopened.RunBackupDeletion(ctx, original.ID, in.ServerID)
				job, decodeErr := Decode[domain.Job](row)
				if err != nil || decodeErr != nil || job.State != domain.JobSucceeded {
					t.Fatal(job, err, decodeErr)
				}
			} else if _, _, err := reopened.DeleteBackup(ctx, domain.NewID(), in); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBackupDeletionClaimPreservesReplacementAndNeverOverwritesRecovery(t *testing.T) {
	s, root, ctx, in := deletionFixture(t)
	path := filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite")
	claimed := filepath.Join(root, "backup-removals", string(in.Backup.ID)+".sqlite")
	foreign := []byte("foreign replacement must survive")
	_, err := s.removeBackupImageClaim(ctx, in, domain.JobQueued, security.SyncParent, func(from, to string) error {
		if err := os.WriteFile(from, foreign, 0600); err != nil {
			return err
		}
		return claimBackupImage(from, to)
	})
	if err == nil {
		t.Fatal("replacement was accepted")
	}
	retained, err := os.ReadFile(claimed)
	if err != nil || string(retained) != string(foreign) {
		t.Fatal("replacement lost", err)
	}
	if err := os.WriteFile(path, []byte("second image"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := claimBackupImage(path, claimed); err == nil {
		t.Fatal("existing claim overwritten")
	}
	retained, err = os.ReadFile(claimed)
	if err != nil || string(retained) != string(foreign) {
		t.Fatal("claim changed", err)
	}
	retained, err = os.ReadFile(path)
	if err != nil || string(retained) != "second image" {
		t.Fatal("original name removed", err)
	}
}

func TestBackupDeletionClaimResumesAfterSyncFailureAndRejectsLateSidecars(t *testing.T) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal", ".pending"} {
		t.Run(suffix, func(t *testing.T) {
			s, root, ctx, in := deletionFixture(t)
			path := filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite")
			claimed := filepath.Join(root, "backup-removals", string(in.Backup.ID)+".sqlite")
			row, _, err := s.DeleteBackup(ctx, domain.NewID(), in)
			if err != nil {
				t.Fatal(err)
			}
			row, err = s.runBackupDeletion(ctx, row.ID, in.ServerID, func(name string) error {
				if name == claimed {
					if suffix == "" {
						return errors.New("interrupted claim sync")
					}
					if err := os.WriteFile(path+suffix, []byte("retained sidecar"), 0600); err != nil {
						return err
					}
				}
				return security.SyncParent(name)
			})
			job, _ := Decode[domain.Job](row)
			if err == nil || job.State != domain.JobUncertain {
				t.Fatal("unsafe completion", err, job)
			}
			if _, err := os.Stat(claimed); err != nil {
				t.Fatal("claim discarded", err)
			}
			if suffix != "" {
				if err := os.Remove(path + suffix); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			row, err = reopened.RunBackupDeletion(ctx, row.ID, in.ServerID)
			job, _ = Decode[domain.Job](row)
			if err != nil || job.State != domain.JobSucceeded {
				t.Fatal("claim not recovered", err, job)
			}
			if _, err := os.Stat(claimed); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("claim survived completion", err)
			}
		})
	}
}

func TestBackupDeletionClaimPreservesReopenedOriginal(t *testing.T) {
	s, root, ctx, in := deletionFixture(t)
	path := filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite")
	claimed := filepath.Join(root, "backup-removals", string(in.Backup.ID)+".sqlite")
	_, err := s.removeBackupImageClaim(ctx, in, domain.JobQueued, security.SyncParent, func(from, to string) error {
		if err := claimBackupImage(from, to); err != nil {
			return err
		}
		return os.WriteFile(from, []byte("reopened image"), 0600)
	})
	if err == nil {
		t.Fatal("reopened original was reported deleted")
	}
	retained, err := os.ReadFile(path)
	if err != nil || string(retained) != "reopened image" {
		t.Fatal("reopened original removed", err)
	}
	if err := verifyDeletionImage(ctx, claimed, in); err != nil {
		t.Fatal("claimed original lost", err)
	}
}

func TestBackupDeletionMigrationFromBothVersion22Layouts(t *testing.T) {
	for _, backupLayout := range []bool{false, true} {
		t.Run(fmt.Sprint(backupLayout), func(t *testing.T) {
			s, root, ctx, in := deletionFixture(t)
			var openAI, anthropic domain.ID
			for preset, target := range map[string]*domain.ID{"openai": &openAI, "anthropic": &anthropic} {
				if err := s.db.QueryRow("SELECT id FROM entities WHERE kind='provider' AND json_extract(body,'$.preset_id')=?", preset).Scan(target); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Mutate(ctx, domain.NewID(), "fixture.saved-off", nil, func(tx *Tx) (any, error) {
				row, err := tx.Get(domain.ProviderKind, openAI)
				if err != nil {
					return nil, err
				}
				provider, err := Decode[domain.Provider](row)
				if err != nil {
					return nil, err
				}
				provider.SetEnabled(false)
				return tx.Put(domain.ProviderKind, openAI, row.Revision, "", "", provider)
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec("DELETE FROM entities WHERE id=?", anthropic); err != nil {
				t.Fatal(err)
			}
			var job Record
			if backupLayout {
				var err error
				job, _, err = s.DeleteBackup(ctx, domain.NewID(), in)
				if err != nil {
					t.Fatal(err)
				}
			} else if _, err := s.db.Exec("DROP TABLE backup_deletions"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec("DROP TABLE IF EXISTS native_accounting; PRAGMA user_version=22"); err != nil {
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
			row, err := reopened.Get(ctx, domain.ProviderKind, openAI)
			provider, decodeErr := Decode[domain.Provider](row)
			if err != nil || decodeErr != nil || row.Revision != 2 || provider.EnabledValue() {
				t.Fatal("saved Off identity changed", row, err, decodeErr)
			}
			var version, hosted, table int
			if err := reopened.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
				t.Fatal(version, err)
			}
			if err := reopened.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='backup_deletions'").Scan(&table); err != nil || table != 1 {
				t.Fatal(table, err)
			}
			if err := reopened.db.QueryRow("SELECT count(*) FROM entities WHERE kind='provider' AND json_extract(body,'$.preset_id')='anthropic'").Scan(&hosted); err != nil {
				t.Fatal(err)
			}
			want := 0
			if backupLayout {
				want = 1
			}
			if hosted != want {
				t.Fatal("provider defaults crossed the wrong migration boundary", hosted, want)
			}
			if backupLayout {
				row, err := reopened.RunBackupDeletion(ctx, job.ID, in.ServerID)
				value, decodeErr := Decode[domain.Job](row)
				if err != nil || decodeErr != nil || value.State != domain.JobSucceeded {
					t.Fatal(value, err, decodeErr)
				}
			}
		})
	}
}
