package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const MaxBackupInventory = 4096
const MaxBackupInspectionBytes int64 = 8 << 30

type Backup struct {
	ID         domain.ID `json:"id"`
	Bytes      uint64    `json:"bytes,string"`
	ModifiedAt time.Time `json:"modified_at"`
}

type BackupInspection struct {
	Backup
	SHA256        string    `json:"sha256"`
	SchemaVersion uint32    `json:"schema_version"`
	ServerID      domain.ID `json:"server_id"`
}

// BackupInventory reads metadata only. Integrity is a separate explicit operation,
// so listing many backups cannot silently perform thousands of database checks.
func (s *Store) BackupInventory(ctx context.Context) ([]Backup, error) {
	if err := lockBackupContext(ctx, &s.backupGate); err != nil {
		return nil, err
	}
	defer s.backupGate.Unlock()
	s.gate.RLock()
	defer s.gate.RUnlock()
	if s.closed {
		return nil, backupUnavailable()
	}
	root := filepath.Join(s.root, "backups")
	if err := security.CheckPrivateDir(root); err != nil {
		return nil, storageError(err)
	}
	dir, err := os.Open(root)
	if err != nil {
		return nil, storageError(err)
	}
	defer dir.Close()
	items := []Backup{}
	seen := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, domain.SafeError(err)
		}
		names, err := dir.Readdirnames(100)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, storageError(err)
		}
		seen += len(names)
		if seen > MaxBackupInventory {
			return nil, domain.Fail(domain.ResourceExhausted, "The managed backup inventory exceeds its inspection bound.", "Inspect the server's managed backup directory; no partial inventory was returned.")
		}
		for _, name := range names {
			if !strings.HasSuffix(name, ".sqlite") {
				continue
			}
			id := domain.ID(strings.TrimSuffix(name, ".sqlite"))
			if id.Validate() != nil {
				return nil, backupUnavailable()
			}
			path := filepath.Join(root, name)
			info, err := backupInfo(path)
			if err != nil {
				return nil, err
			}
			items = append(items, backupMetadata(id, info))
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID > items[j].ID })
	return items, nil
}

func backupMetadata(id domain.ID, info os.FileInfo) Backup {
	return Backup{ID: id, Bytes: uint64(info.Size()), ModifiedAt: info.ModTime().UTC()}
}

func backupInfo(path string) (os.FileInfo, error) {
	if err := security.RegularPrivate(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, domain.Fail(domain.NotFound, "The managed backup no longer exists.", "Refresh the backup list.")
		}
		return nil, storageError(err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 {
		return nil, backupUnavailable()
	}
	return info, nil
}

func sameBackup(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func backupUnavailable() error {
	return domain.Fail(domain.RecoveryRequired, "Managed backup ownership or contents changed during inspection.", "Preserve the original backup and refresh its metadata before retrying.")
}

// InspectBackup copies an opened, identity-checked image into an exclusive private
// scratch file before SQLite reads it. SQLite never opens the selected backup by
// path and cannot ingest an adjacent WAL or change the source. The hash covers the
// exact inspected bytes, not a later read of a potentially replaced file.
func (s *Store) InspectBackup(ctx context.Context, id, expectedServer domain.ID) (BackupInspection, error) {
	return s.inspectBackup(ctx, id, expectedServer, nil)
}

// afterCopy permits deterministic tests of an external SQLite open while the
// private copy is being validated. Product callers never supply this checkpoint.
func (s *Store) inspectBackup(ctx context.Context, id, expectedServer domain.ID, afterCopy func()) (BackupInspection, error) {
	var result BackupInspection
	if err := lockBackupContext(ctx, &s.backupGate); err != nil {
		return result, err
	}
	defer s.backupGate.Unlock()
	if err := s.backupNotDeleted(ctx, id); err != nil {
		return result, err
	}
	if err := id.Validate(); err != nil {
		return result, err
	}
	if err := expectedServer.Validate(); err != nil {
		return result, err
	}
	s.gate.RLock()
	defer s.gate.RUnlock()
	if s.closed {
		return result, backupUnavailable()
	}
	root := filepath.Join(s.root, "backups")
	if err := security.CheckPrivateDir(root); err != nil {
		return result, storageError(err)
	}
	path := filepath.Join(root, string(id)+".sqlite")
	before, err := backupInfo(path)
	if err != nil {
		return result, err
	}
	if before.Size() > MaxBackupInspectionBytes {
		return result, domain.Fail(domain.ResourceExhausted, "The backup exceeds the 8 GiB online inspection limit.", "Keep the backup and use offline database inspection; it has not been modified.")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return result, backupUnavailable()
		}
	}
	input, err := os.Open(path)
	if err != nil {
		return result, storageError(err)
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !sameBackup(before, opened) {
		return result, backupUnavailable()
	}
	current, err := backupInfo(path)
	if err != nil || !sameBackup(before, current) {
		return result, backupUnavailable()
	}
	scratch, err := os.CreateTemp(root, ".inspection-*.tmp")
	if err != nil {
		return result, storageError(err)
	}
	defer os.Remove(scratch.Name())
	defer scratch.Close()
	hash := sha256.New()
	buffer := make([]byte, 128<<10)
	remaining := before.Size()
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return result, domain.SafeError(err)
		}
		n, err := input.Read(buffer[:min(int64(len(buffer)), remaining)])
		if n > 0 {
			if _, e := scratch.Write(buffer[:n]); e != nil {
				return result, storageError(e)
			}
			_, _ = hash.Write(buffer[:n])
			remaining -= int64(n)
		}
		if err != nil {
			return result, backupUnavailable()
		}
	}
	if n, err := input.Read(buffer[:1]); n != 0 || !errors.Is(err, io.EOF) {
		return result, backupUnavailable()
	}
	if err := scratch.Close(); err != nil {
		return result, storageError(err)
	}
	if afterCopy != nil {
		afterCopy()
	}
	// The private scratch file has no journal sidecars. Immutable also prevents
	// SQLite from creating them while validating a WAL-mode database image.
	db, err := sql.Open("sqlite", databaseURI(scratch.Name(), true)+"&immutable=1")
	if err != nil {
		return result, storageError(err)
	}
	defer db.Close()
	if err := inspect(ctx, db, false); err != nil {
		return result, err
	}
	var version uint32
	var owner domain.ID
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return result, storageError(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='server_id'").Scan(&owner); err != nil || owner != expectedServer {
		return result, domain.Fail(domain.PermissionDenied, "This backup does not belong to the selected server.", "Select a backup with the same original server identity; credentials are not imported.")
	}
	after, err := input.Stat()
	if err != nil || !sameBackup(before, after) {
		return result, backupUnavailable()
	}
	current, err = backupInfo(path)
	if err != nil || !sameBackup(before, current) {
		return result, backupUnavailable()
	}
	// A same-user SQLite connection can create adjacent state without changing
	// the original main-file bytes. Refuse that raced image at publication too.
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return result, backupUnavailable()
		}
	}
	result = BackupInspection{Backup: backupMetadata(id, before), SHA256: hex.EncodeToString(hash.Sum(nil)), SchemaVersion: version, ServerID: owner}
	return result, nil
}
