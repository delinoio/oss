// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func cleanDeletionScanFixture(t *testing.T, count int) (*Store, string, context.Context, SessionDeletion, []domain.ID) {
	t.Helper()
	s, root := openTest(t)
	ctx := notificationOwner()
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	target, _, _ := deletionSession(t, s, "scan target")
	v, _, err := s.DeleteSession(ctx, domain.NewID(), target.ID, server, target.Revision)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.PurgeDeletedSession(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]domain.ID, 0, count)
	for range count {
		id, err := s.Backup(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return s, root, ctx, v, ids
}

// Deterministic pass budget: one complete inspection fits, the next returns the
// same safe deadline classification as the production thirty-second context.
// This exercises durable incremental progress, not wall-clock/platform timing.
func TestSessionBackupScanAdvancesBoundedPassesAndRestart(t *testing.T) {
	s, root, ctx, v, _ := cleanDeletionScanFixture(t, 3)
	completed := map[domain.ID]int{}
	for pass := 1; pass <= 3; pass++ {
		used := 0
		err := s.removeSessionBackups(ctx, v, func(ctx context.Context, id, server domain.ID, observe func(*sql.DB) error) (BackupInspection, error) {
			if used == 1 {
				return BackupInspection{}, domain.SafeError(context.DeadlineExceeded)
			}
			used++
			checked, err := s.inspectSessionBackup(ctx, id, server, observe)
			if err == nil {
				completed[id]++
			}
			return checked, err
		})
		if pass < 3 && err == nil {
			t.Fatal("unseen images completed")
		}
		if pass == 3 && err != nil {
			t.Fatal(err)
		}
		progress, err := s.loadSessionBackupCheckpoints(ctx, v)
		if err != nil || len(progress) != pass {
			t.Fatal("completed checkpoints lost", len(progress), err)
		}
		original, err := s.GetSessionDeletion(ctx, v.SessionID)
		if err != nil || original.Revision != v.Revision || original.BackupsRemoved {
			t.Fatal("private progress changed public intent", original, err)
		}
		if pass == 1 {
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
		}
	}
	for _, count := range completed {
		if count != 1 {
			t.Fatal("clean image reclassified", count)
		}
	}
	if len(completed) != 3 {
		t.Fatal("unseen inventory skipped")
	}
}

func TestSessionBackupScanInvalidatesOnlyChangedImageAndCoversNewImage(t *testing.T) {
	s, root, ctx, v, ids := cleanDeletionScanFixture(t, 2)
	if err := s.RemoveSessionBackups(ctx, v); err != nil {
		t.Fatal(err)
	}
	before, err := s.loadSessionBackupCheckpoints(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "backups", string(ids[0])+".sqlite")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Keep old inode alive, then replace original name with byte-identical SQLite.
	// Mode/size/mtime alone cannot validate this replacement.
	if err := os.Rename(path, path+".retained"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before[ids[0]].Backup.ModifiedAt, before[ids[0]].Backup.ModifiedAt); err != nil {
		t.Fatal(err)
	}
	if _, reusable, err := s.resumeSessionBackupCheckpoint(ctx, before[ids[0]]); err != nil || reusable {
		t.Fatal("replacement reused old identity", reusable, err)
	}
	if err := os.Remove(path + ".retained"); err != nil {
		t.Fatal(err)
	}
	newer, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observed := map[domain.ID]bool{}
	if err := s.removeSessionBackups(ctx, v, func(ctx context.Context, id, server domain.ID, observe func(*sql.DB) error) (BackupInspection, error) {
		observed[id] = true
		return s.inspectSessionBackup(ctx, id, server, observe)
	}); err != nil {
		t.Fatal(err)
	}
	if !observed[ids[0]] || !observed[newer] || observed[ids[1]] || len(observed) != 2 {
		t.Fatal("changed/new invalidation lost independent progress", observed)
	}
	after, err := s.loadSessionBackupCheckpoints(ctx, v)
	if err != nil || after[ids[0]].FileIdentity == before[ids[0]].FileIdentity || after[ids[1]] != before[ids[1]] {
		t.Fatal("classification identity changed incorrectly", err)
	}
	// In-place metadata change independently invalidates only this image.
	changed := before[ids[1]].Backup.ModifiedAt.Add(time.Second)
	if err := os.Chtimes(filepath.Join(root, "backups", string(ids[1])+".sqlite"), changed, changed); err != nil {
		t.Fatal(err)
	}
	if _, reusable, err := s.resumeSessionBackupCheckpoint(ctx, after[ids[1]]); err != nil || reusable {
		t.Fatal("modified image reused old checkpoint", err)
	}
}

func TestSessionBackupScanCanceledCopyHasNoCleanCheckpointAndJoinsScratchCleanup(t *testing.T) {
	s, root, ctx, v, _ := cleanDeletionScanFixture(t, 1)
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	err := s.removeSessionBackups(canceled, v, func(ctx context.Context, id, server domain.ID, observe func(*sql.DB) error) (BackupInspection, error) {
		return s.inspectBackupContent(ctx, id, server, cancel, observe)
	})
	if err == nil {
		t.Fatal("canceled copy completed scan")
	}
	progress, err := s.loadSessionBackupCheckpoints(ctx, v)
	if err != nil || len(progress) != 0 {
		t.Fatal("canceled image marked clean", progress, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".inspection-") {
			t.Fatal("joined copy scratch retained without cleanup")
		}
	}
	original, err := s.GetSessionDeletion(ctx, v.SessionID)
	if err != nil || original.BackupsRemoved || original.FinishedAt != nil {
		t.Fatal("cancellation reported completion", original, err)
	}
	if err := s.RemoveSessionBackups(ctx, v); err != nil {
		t.Fatal("fresh independent pass blocked", err)
	}
}

func TestSessionBackupScanNewTargetImageUsesOriginalDurableDeletion(t *testing.T) {
	s, root := openTest(t)
	ctx := notificationOwner()
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	target, _, _ := deletionSession(t, s, "target image")
	old, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(filepath.Join(root, "backups", string(old)+".sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := s.DeleteSession(ctx, domain.NewID(), target.ID, server, target.Revision)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.PurgeDeletedSession(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	clean, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSessionBackups(ctx, v); err != nil {
		t.Fatal(err)
	}
	restored := domain.NewID()
	path := filepath.Join(root, "backups", string(restored)+".sqlite")
	if err := os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSessionBackups(ctx, v); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("target image survived durable deletion", err)
	}
	var count int
	if err := s.Read(ctx, func(tx *Tx) error {
		return tx.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM backup_deletions WHERE backup_id=?", restored).Scan(&count)
	}); err != nil || count != 1 {
		t.Fatal("original durable deletion bypassed", count, err)
	}
	progress, err := s.loadSessionBackupCheckpoints(ctx, v)
	if err != nil || len(progress) != 1 || progress[clean].Backup.ID != clean {
		t.Fatal("target image checkpointed clean or unrelated image lost", progress, err)
	}
}

func TestSessionBackupScanRetainedScratchStillBlocksCachedCompletion(t *testing.T) {
	s, root, ctx, v, _ := cleanDeletionScanFixture(t, 1)
	if err := s.RemoveSessionBackups(ctx, v); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(root, "backups", ".inspection-unconfirmed.tmp")
	if err := os.WriteFile(scratch, []byte("unconfirmed fixture bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSessionBackups(ctx, v); err == nil {
		t.Fatal("cached scan adopted unknown scratch cleanup")
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Fatal("unknown scratch was removed", err)
	}
	progress, err := s.loadSessionBackupCheckpoints(ctx, v)
	if err != nil || len(progress) != 1 {
		t.Fatal("verified independent clean checkpoint lost", err)
	}
}

func TestSessionBackupCheckpointRejectsForeignIntentDigestAndOversizedMetadata(t *testing.T) {
	s, _, ctx, v, ids := cleanDeletionScanFixture(t, 1)
	checked, err := s.InspectBackup(ctx, ids[0], v.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	valid := checkpointForSessionBackup(v, checked)
	for _, change := range []func(*sessionBackupCheckpoint){func(p *sessionBackupCheckpoint) { p.DeletionID = domain.NewID() }, func(p *sessionBackupCheckpoint) { p.SessionID = domain.NewID() }, func(p *sessionBackupCheckpoint) { p.ServerID = domain.NewID() }, func(p *sessionBackupCheckpoint) { p.RequestID = domain.NewID() }, func(p *sessionBackupCheckpoint) { p.ExpectedRevision++ }, func(p *sessionBackupCheckpoint) { p.SHA256 = "unverified" }, func(p *sessionBackupCheckpoint) { p.FileIdentity = strings.Repeat("1", 65) }} {
		p := valid
		change(&p)
		if err := s.saveSessionBackupCheckpoint(ctx, v, p); err == nil {
			t.Fatal("foreign/unverified checkpoint admitted", p)
		}
	}
	progress, err := s.loadSessionBackupCheckpoints(ctx, v)
	if err != nil || len(progress) != 0 {
		t.Fatal("invalid progress persisted", progress, err)
	}
	s.gate.Lock()
	_, err = s.db.ExecContext(ctx, "INSERT INTO metadata(key,value) VALUES(?,?)", sessionBackupScanPrefix(v)+string(ids[0]), strings.Repeat("x", maxSessionBackupCheckpointBytes+1))
	s.gate.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.loadSessionBackupCheckpoints(ctx, v); err == nil {
		t.Fatal("oversized progress interpreted as verified")
	}
}

func TestSessionBackupScanRestoreDropsHistoricalOnlyCacheAndPreservesCurrentRows(t *testing.T) {
	s, root, ctx, in, _ := restoreFixture(t)
	historical := "session-deletion-backup-scan:" + string(domain.NewID()) + ":" + string(domain.NewID())
	current := "session-deletion-backup-scan:" + string(domain.NewID()) + ":" + string(domain.NewID())
	s.gate.Lock()
	_, err := s.db.ExecContext(ctx, "INSERT INTO metadata(key,value) VALUES(?,?)", historical, "untrusted historical fixture")
	s.gate.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inspected, err := s.InspectBackup(ctx, source, in.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	s.gate.Lock()
	_, err = s.db.ExecContext(ctx, "DELETE FROM metadata WHERE key=?", historical)
	if err == nil {
		_, err = s.db.ExecContext(ctx, "INSERT INTO metadata(key,value) VALUES(?,?)", current, "original current fixture")
	}
	s.gate.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	in.Backup, in.SHA256 = inspected.Backup, inspected.SHA256
	in.ExpectedRevision, err = s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RestoreBackup(ctx, domain.NewID(), in); err != nil {
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
	var value string
	var count int
	if err := reopened.Read(ctx, func(tx *Tx) error {
		if err := tx.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM metadata WHERE key=?", historical).Scan(&count); err != nil {
			return err
		}
		return tx.tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key=?", current).Scan(&value)
	}); err != nil || count != 0 || value != "original current fixture" {
		t.Fatal("restore granted historical cache authority or lost current proof", count, value, err)
	}
}
