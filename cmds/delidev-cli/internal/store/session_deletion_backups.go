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
			return db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM entities WHERE id=? OR session_id=? OR (kind='problem' AND json_extract(body,'$.type')='pull-request-remediation-attempt' AND json_extract(body,'$.session_id')=?))", v.SessionID, v.SessionID, v.SessionID).Scan(&contains)
		})
		if e != nil {
			return e
		}
		if !contains {
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
		if !strings.HasSuffix(entry.Name(), ".sqlite") {
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
	return nil
}
