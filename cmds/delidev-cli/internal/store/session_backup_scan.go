// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const maxSessionBackupScanBytes = 3 << 20
const sessionBackupScanSuffix = ".backup-scan.json"

// This private operation metadata is separate from the full-capacity immutable
// Worker obligation. Database restore cannot roll back its completed progress.
// It grants neither backup deletion nor session completion authority.
type sessionBackupClassification struct {
	Backup         Backup      `json:"backup"`
	NativeIdentity string      `json:"native_identity"`
	Mode           os.FileMode `json:"mode"`
	SHA256         string      `json:"sha256"`
	SchemaVersion  uint32      `json:"schema_version"`
}
type sessionBackupScan struct {
	Version      uint32                        `json:"version"`
	DeletionID   domain.ID                     `json:"deletion_id"`
	SessionID    domain.ID                     `json:"session_id"`
	ServerID     domain.ID                     `json:"server_id"`
	IntentDigest string                        `json:"intent_digest"`
	Clean        []sessionBackupClassification `json:"clean"`
}

func (s *Store) sessionBackupScanPath(id domain.ID) string {
	return filepath.Join(s.root, "session-deletions", string(id)+sessionBackupScanSuffix)
}
func (v sessionBackupScan) validate(deletion SessionDeletion) error {
	digest, err := sessionDeletionDigest(deletion)
	if err != nil || v.Version != 1 || v.DeletionID != deletion.ID || v.SessionID != deletion.SessionID || v.ServerID != deletion.ServerID || v.IntentDigest != digest || len(v.Clean) > MaxBackupInventory {
		return domain.SessionDeletionPending()
	}
	seen := map[domain.ID]bool{}
	for _, c := range v.Clean {
		hash, err := hex.DecodeString(c.SHA256)
		if c.Backup.ID.Validate() != nil || c.Backup.Bytes > uint64(MaxBackupInspectionBytes) || c.Backup.ModifiedAt.IsZero() || c.SchemaVersion == 0 || !c.Mode.IsRegular() || c.Mode&^os.FileMode(0777) != 0 || domain.Text(c.NativeIdentity, "backup classification identity", 256, true) != nil || err != nil || len(hash) != 32 || hex.EncodeToString(hash) != c.SHA256 || seen[c.Backup.ID] {
			return domain.SessionDeletionPending()
		}
		seen[c.Backup.ID] = true
	}
	return nil
}
func (s *Store) readSessionBackupScan(v SessionDeletion) (sessionBackupScan, error) {
	digest, err := sessionDeletionDigest(v)
	if err != nil {
		return sessionBackupScan{}, err
	}
	scan := sessionBackupScan{Version: 1, DeletionID: v.ID, SessionID: v.SessionID, ServerID: v.ServerID, IntentDigest: digest, Clean: []sessionBackupClassification{}}
	raw, err := security.ReadPrivate(s.sessionBackupScanPath(v.SessionID), maxSessionBackupScanBytes)
	if errors.Is(err, os.ErrNotExist) {
		return scan, nil
	}
	if err != nil {
		return scan, storageError(err)
	}
	if domain.DecodeWithLimit(raw, &scan, maxSessionBackupScanBytes) != nil || scan.validate(v) != nil {
		return scan, domain.SessionDeletionPending()
	}
	return scan, nil
}
func (s *Store) writeSessionBackupScan(ctx context.Context, v SessionDeletion, scan sessionBackupScan) error {
	if ctx.Err() != nil {
		return domain.SafeError(ctx.Err())
	}
	// Join the original private-intent writer. A stale caller cannot publish
	// progress under a replaced operation or borrow a restore image's authority.
	s.gate.Lock()
	defer s.gate.Unlock()
	current, err := s.readSessionDeletion(v.SessionID)
	if err != nil || current.ID != v.ID || scan.validate(current) != nil {
		return domain.SessionDeletionPending()
	}
	raw, err := json.Marshal(scan)
	if err != nil || len(raw) > maxSessionBackupScanBytes {
		return domain.SessionDeletionPending()
	}
	if ctx.Err() != nil {
		return domain.SafeError(ctx.Err())
	}
	return storageError(security.WriteAtomic(s.sessionBackupScanPath(v.SessionID), raw))
}
func (s *Store) SessionBackupScanProgress(v SessionDeletion) (int, error) {
	scan, err := s.readSessionBackupScan(v)
	if err != nil {
		return 0, err
	}
	return len(scan.Clean), nil
}
func (v *sessionBackupScan) retainInventory(items []Backup) {
	present := map[domain.ID]bool{}
	for _, item := range items {
		present[item.ID] = true
	}
	v.Clean = slices.DeleteFunc(v.Clean, func(c sessionBackupClassification) bool { return !present[c.Backup.ID] })
}
func (v *sessionBackupScan) remove(id domain.ID) {
	v.Clean = slices.DeleteFunc(v.Clean, func(c sessionBackupClassification) bool { return c.Backup.ID == id })
}
func (v *sessionBackupScan) find(id domain.ID) (sessionBackupClassification, bool) {
	for _, c := range v.Clean {
		if c.Backup.ID == id {
			return c, true
		}
	}
	return sessionBackupClassification{}, false
}

// Caller holds backupGate. Open and compare the original native handle before
// reusing a digest-bound clean fact. Sidecars, replacements and writes invalidate
// that one fact; neither SQLite nor media/content readers open the source.
func sessionBackupClassificationInfo(path string) (os.FileInfo, string, error) {
	before, err := backupInfo(path)
	if err != nil {
		return nil, "", err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return nil, "", backupUnavailable()
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", storageError(err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameBackup(before, opened) {
		return nil, "", backupUnavailable()
	}
	identity, err := sessionBackupFileIdentity(file)
	if err != nil {
		return nil, "", storageError(err)
	}
	after, err := backupInfo(path)
	if err != nil || !sameBackup(before, after) {
		return nil, "", backupUnavailable()
	}
	last, err := file.Stat()
	if err != nil || !sameBackup(opened, last) {
		return nil, "", backupUnavailable()
	}
	lastIdentity, err := sessionBackupFileIdentity(file)
	if err != nil || identity != lastIdentity {
		return nil, "", backupUnavailable()
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return nil, "", backupUnavailable()
		}
	}
	return after, identity, nil
}
func (s *Store) reuseSessionBackupClassification(ctx context.Context, v SessionDeletion, c sessionBackupClassification) (BackupInspection, bool, error) {
	if err := lockBackupContext(ctx, &s.backupGate); err != nil {
		return BackupInspection{}, false, err
	}
	defer s.backupGate.Unlock()
	return s.inspectSessionBackupClassification(v, c)
}

func (s *Store) inspectSessionBackupClassification(v SessionDeletion, c sessionBackupClassification) (BackupInspection, bool, error) {
	info, identity, err := sessionBackupClassificationInfo(filepath.Join(s.root, "backups", string(c.Backup.ID)+".sqlite"))
	if err != nil {
		return BackupInspection{}, false, err
	}
	matches := identity == c.NativeIdentity && info.Mode() == c.Mode && uint64(info.Size()) == c.Backup.Bytes && info.ModTime().Equal(c.Backup.ModifiedAt)
	return BackupInspection{Backup: c.Backup, SHA256: c.SHA256, ServerID: v.ServerID, SchemaVersion: c.SchemaVersion, sourceInfo: info, sourceIdentity: identity}, matches, nil
}

func sessionBackupScanSession(name string) (domain.ID, bool) {
	if !strings.HasSuffix(name, sessionBackupScanSuffix) {
		return "", false
	}
	id := domain.ID(strings.TrimSuffix(name, sessionBackupScanSuffix))
	return id, id.Validate() == nil
}

// Completion rechecks every remaining image under the same publication gate.
// A new/replaced image after a successful pass cannot borrow its clean map.
func (s *Store) confirmSessionBackupScanLocked(ctx context.Context, v SessionDeletion) error {
	scan, err := s.readSessionBackupScan(v)
	if err != nil {
		return err
	}
	classified := map[domain.ID]BackupInspection{}
	for _, clean := range scan.Clean {
		if ctx.Err() != nil {
			return domain.SafeError(ctx.Err())
		}
		checked, match, err := s.inspectSessionBackupClassification(v, clean)
		if err != nil && domain.SafeError(err).Code == domain.NotFound {
			continue
		}
		if err != nil {
			return err
		}
		if match {
			classified[clean.Backup.ID] = checked
		}
	}
	return s.finishSessionBackupScanLocked(ctx, classified)
}
