// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Classification uses the same private, identity-checked immutable SQLite copy
// as user inspection. No original image is opened directly by SQLite.
func (s *Store) RemoveSessionBackups(ctx context.Context, v SessionDeletion) error {
	items, e := s.BackupInventory(ctx)
	if e != nil {
		return e
	}
	classified := map[domain.ID]BackupInspection{}
	for _, item := range items {
		if e := ctx.Err(); e != nil {
			return domain.SafeError(e)
		}
		var deletion domain.ID
		e = s.Read(ctx, func(t *Tx) error {
			e := t.tx.QueryRowContext(ctx, "SELECT job_id FROM backup_deletions WHERE backup_id=?", item.ID).Scan(&deletion)
			if errors.Is(e, sql.ErrNoRows) {
				return nil
			}
			return e
		})
		if e != nil {
			return storageError(e)
		}
		if deletion != "" {
			if _, e = s.RunBackupDeletion(ctx, deletion, v.ServerID); e != nil {
				return e
			}
			continue
		}
		contains := false
		checked, e := s.inspectBackupContent(ctx, item.ID, v.ServerID, nil, func(db *sql.DB) error {
			return db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM entities WHERE id=? OR session_id=? OR (kind='job' AND json_extract(body,'$.type')='image-attachment' AND EXISTS(SELECT 1 FROM json_each(entities.body,'$.input.owners') WHERE value=?)) OR (kind='problem' AND json_extract(body,'$.type')='pull-request-remediation-attempt' AND json_extract(body,'$.session_id')=?))", v.SessionID, v.SessionID, v.SessionID, v.SessionID).Scan(&contains)
		})
		if e != nil {
			return e
		}
		if !contains {
			classified[item.ID] = checked
			continue
		}
		row, _, e := s.DeleteBackup(ctx, domain.NewID(), BackupDeletionInput{Actor: domain.Principal{Type: domain.OwnerDevice}, ServerID: v.ServerID, Backup: checked.Backup, ExpectedRevision: 1, SHA256: checked.SHA256})
		if e != nil {
			return e
		}
		if _, e = s.RunBackupDeletion(ctx, row.ID, v.ServerID); e != nil {
			return e
		}
	}
	return s.finishSessionBackupScan(ctx, classified)
}

// Publication and final validation share the backup gate. Every remaining image
// must still be the exact image classified without session content; a new or
// replaced image stays pending until the next full content-classification pass.
func (s *Store) finishSessionBackupScan(ctx context.Context, classified map[domain.ID]BackupInspection) error {
	// A killed copy/inspection can retain session bytes in an unpublished image.
	// Unknown scratch ownership cannot be silently skipped or unlinked. Keep the
	// deletion pending until that original image is independently recovered.
	if e := lockBackupContext(ctx, &s.backupGate); e != nil {
		return e
	}
	defer s.backupGate.Unlock()
	f, e := os.Open(filepath.Join(s.root, "backups"))
	if e != nil {
		return storageError(e)
	}
	defer f.Close()
	entries, e := f.ReadDir(MaxBackupInventory + 1)
	if e != nil && !errors.Is(e, io.EOF) || len(entries) > MaxBackupInventory {
		return domain.SessionDeletionPending()
	}
	for _, entry := range entries {
		if e := ctx.Err(); e != nil {
			return domain.SafeError(e)
		}
		if !strings.HasSuffix(entry.Name(), ".sqlite") {
			return domain.SessionDeletionPending()
		}
		id := domain.ID(strings.TrimSuffix(entry.Name(), ".sqlite"))
		checked, ok := classified[id]
		if id.Validate() != nil || !ok {
			return domain.SessionDeletionPending()
		}
		current, e := backupInfo(filepath.Join(s.root, "backups", entry.Name()))
		if e != nil || !sameBackup(checked.sourceInfo, current) {
			return domain.SessionDeletionPending()
		}
	}
	claims, e := os.Open(filepath.Join(s.root, "backup-removals"))
	if e != nil {
		return storageError(e)
	}
	defer claims.Close()
	retained, e := claims.ReadDir(1)
	if e != nil && !errors.Is(e, io.EOF) {
		return storageError(e)
	}
	if len(retained) != 0 {
		return domain.SessionDeletionPending()
	}
	return s.checkRestoreImagesRetired(ctx)
}
