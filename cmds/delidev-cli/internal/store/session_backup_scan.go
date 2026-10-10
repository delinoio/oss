// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const maxSessionBackupCheckpointBytes = 1024

// Private, non-content progress in existing metadata; the independently durable
// external deletion intent remains sole operation authority. No public revision
// or worker-obligation envelope grows when a clean image is checkpointed.
type sessionBackupCheckpoint struct {
	Version          uint32    `json:"version"`
	DeletionID       domain.ID `json:"deletion_id"`
	SessionID        domain.ID `json:"session_id"`
	ServerID         domain.ID `json:"server_id"`
	RequestID        domain.ID `json:"request_id"`
	ExpectedRevision uint64    `json:"expected_revision,string"`
	Backup           Backup    `json:"backup"`
	Mode             uint32    `json:"mode"`
	FileIdentity     string    `json:"file_identity"`
	SHA256           string    `json:"sha256"`
}

func sessionBackupScanPrefix(v SessionDeletion) string {
	return "session-deletion-backup-scan:" + string(v.ID) + ":"
}
func sessionBackupScanUpper(v SessionDeletion) string {
	return strings.TrimSuffix(sessionBackupScanPrefix(v), ":") + ";"
}

func (p sessionBackupCheckpoint) validate(v SessionDeletion) error {
	digest, err := hex.DecodeString(p.SHA256)
	if p.Version != 1 || p.DeletionID != v.ID || p.SessionID != v.SessionID || p.ServerID != v.ServerID || p.RequestID != v.RequestID || p.ExpectedRevision != v.ExpectedRevision || p.Backup.ID.Validate() != nil || p.Backup.ModifiedAt.IsZero() || p.Backup.Bytes > uint64(MaxBackupInspectionBytes) || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != p.SHA256 || len(p.FileIdentity) == 0 || len(p.FileIdentity) > 64 || strings.Trim(p.FileIdentity, "0123456789abcdef:") != "" || !os.FileMode(p.Mode).IsRegular() {
		return domain.SessionDeletionPending()
	}
	return nil
}
func (s *Store) checkSessionBackupIntent(v SessionDeletion) error {
	current, err := s.readSessionDeletion(v.SessionID)
	if err != nil {
		return err
	}
	if current.ID != v.ID || current.ServerID != v.ServerID || current.RequestID != v.RequestID || current.ExpectedRevision != v.ExpectedRevision || current.Actor != v.Actor || !current.AcceptedAt.Equal(v.AcceptedAt) {
		return domain.SessionDeletionPending()
	}
	return nil
}
func (s *Store) loadSessionBackupCheckpoints(ctx context.Context, v SessionDeletion) (map[domain.ID]sessionBackupCheckpoint, error) {
	found := map[domain.ID]sessionBackupCheckpoint{}
	err := s.Read(ctx, func(t *Tx) error {
		if err := s.checkSessionBackupIntent(v); err != nil {
			return err
		}
		rows, err := t.tx.QueryContext(ctx, "SELECT substr(key,1,256),substr(value,1,?) FROM metadata WHERE key>=? AND key<? ORDER BY key LIMIT ?", maxSessionBackupCheckpointBytes+1, sessionBackupScanPrefix(v), sessionBackupScanUpper(v), MaxBackupInventory+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(found) == MaxBackupInventory {
				return domain.SessionDeletionPending()
			}
			var key, raw string
			if err := rows.Scan(&key, &raw); err != nil {
				return err
			}
			var p sessionBackupCheckpoint
			if domain.DecodeWithLimit([]byte(raw), &p, maxSessionBackupCheckpointBytes) != nil || p.validate(v) != nil || key != sessionBackupScanPrefix(v)+string(p.Backup.ID) {
				return domain.SessionDeletionPending()
			}
			found[p.Backup.ID] = p
		}
		return rows.Err()
	})
	return found, storageError(err)
}

// The handle identity and fresh FileInfo are retained together for the original
// final sameBackup gate. This fast path never trusts IDs/size/mtime alone and
// never opens the source in SQLite or replays content/native work.
func (s *Store) resumeSessionBackupCheckpoint(ctx context.Context, p sessionBackupCheckpoint) (BackupInspection, bool, error) {
	if err := lockBackupContext(ctx, &s.backupGate); err != nil {
		return BackupInspection{}, false, err
	}
	defer s.backupGate.Unlock()
	path := filepath.Join(s.root, "backups", string(p.Backup.ID)+".sqlite")
	before, err := backupInfo(path)
	if err != nil {
		return BackupInspection{}, false, err
	}
	file, err := os.Open(path)
	if err != nil {
		return BackupInspection{}, false, storageError(err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameBackup(before, opened) {
		return BackupInspection{}, false, backupUnavailable()
	}
	identity, err := sessionBackupFileIdentity(file, opened)
	if err != nil {
		return BackupInspection{}, false, err
	}
	if identity != p.FileIdentity || uint32(opened.Mode()) != p.Mode || uint64(opened.Size()) != p.Backup.Bytes || !opened.ModTime().Equal(p.Backup.ModifiedAt) {
		return BackupInspection{}, false, nil
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !os.IsNotExist(err) {
			return BackupInspection{}, false, backupUnavailable()
		}
	}
	current, err := backupInfo(path)
	if err != nil || !sameBackup(opened, current) {
		return BackupInspection{}, false, backupUnavailable()
	}
	if err := ctx.Err(); err != nil {
		return BackupInspection{}, false, domain.SafeError(err)
	}
	return BackupInspection{Backup: p.Backup, SHA256: p.SHA256, ServerID: p.ServerID, sourceInfo: opened, sourceIdentity: identity}, true, nil
}
func checkpointForSessionBackup(v SessionDeletion, checked BackupInspection) sessionBackupCheckpoint {
	return sessionBackupCheckpoint{Version: 1, DeletionID: v.ID, SessionID: v.SessionID, ServerID: v.ServerID, RequestID: v.RequestID, ExpectedRevision: v.ExpectedRevision, Backup: checked.Backup, Mode: uint32(checked.sourceInfo.Mode()), FileIdentity: checked.sourceIdentity, SHA256: checked.SHA256}
}

// A context-canceled or partially copied image cannot call this publication.
// Each completed classification commits independently before the next image.
func (s *Store) saveSessionBackupCheckpoint(ctx context.Context, v SessionDeletion, p sessionBackupCheckpoint) error {
	if err := p.validate(v); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > maxSessionBackupCheckpointBytes {
		return domain.SessionDeletionPending()
	}
	if err := lockRestoreContext(ctx, &s.gate); err != nil {
		return err
	}
	defer s.gate.Unlock()
	if err := s.checkSessionBackupIntent(v); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM metadata WHERE key>=? AND key<?", sessionBackupScanPrefix(v), sessionBackupScanUpper(v)).Scan(&count); err != nil {
		return storageError(err)
	}
	if count >= MaxBackupInventory {
		var present int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM metadata WHERE key=?", sessionBackupScanPrefix(v)+string(p.Backup.ID)).Scan(&present); err != nil {
			return storageError(err)
		}
		if present != 1 {
			return domain.SessionDeletionPending()
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", sessionBackupScanPrefix(v)+string(p.Backup.ID), string(raw)); err != nil {
		return storageError(err)
	}
	return storageError(tx.Commit())
}
func (s *Store) discardSessionBackupCheckpoint(ctx context.Context, v SessionDeletion, id domain.ID) error {
	if err := lockRestoreContext(ctx, &s.gate); err != nil {
		return err
	}
	defer s.gate.Unlock()
	if err := s.checkSessionBackupIntent(v); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM metadata WHERE key=?", sessionBackupScanPrefix(v)+string(id))
	return storageError(err)
}
