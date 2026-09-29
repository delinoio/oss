package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type backupPublication struct {
	Version uint32 `json:"version"`
	Backup  Backup `json:"backup"`
	SHA256  string `json:"sha256"`
}

// Publication claims live in the original database, independently of the image
// they attest. The exclusive store gate covers the copy, claim and publication.
// Restoring a database without the claim must fail closed, not adopt a filename.
func (s *Store) recordBackupPublication(ctx context.Context, path string, id domain.ID) error {
	claim, err := fingerprintBackupPublication(ctx, path, id)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(claim)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "backup_publication:"+string(id), string(raw))
	return storageError(err)
}

func (s *Store) verifyBackupPublication(ctx context.Context, path string, id domain.ID) error {
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key=?", "backup_publication:"+string(id)).Scan(&raw); err != nil {
		return backupUnavailable()
	}
	var expected backupPublication
	if domain.Decode([]byte(raw), &expected) != nil || expected.Version != 1 || expected.Backup.ID != id {
		return backupUnavailable()
	}
	actual, err := fingerprintBackupPublication(ctx, path, id)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, expected) {
		return backupUnavailable()
	}
	return nil
}

func fingerprintBackupPublication(ctx context.Context, path string, id domain.ID) (backupPublication, error) {
	var result backupPublication
	if err := backupSidecarsAbsent(path); err != nil {
		return result, err
	}
	before, err := backupInfo(path)
	if err != nil {
		return result, err
	}
	f, err := os.Open(path)
	if err != nil {
		return result, storageError(err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !sameBackup(before, opened) {
		return result, backupUnavailable()
	}
	hash := sha256.New()
	buffer := make([]byte, 128<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return result, domain.SafeError(err)
		}
		n, readErr := f.Read(buffer)
		total += int64(n)
		if total > before.Size() {
			return result, backupUnavailable()
		}
		_, _ = hash.Write(buffer[:n])
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return result, storageError(readErr)
		}
	}
	after, err := f.Stat()
	if err != nil || total != before.Size() || !sameBackup(before, after) {
		return result, backupUnavailable()
	}
	current, err := backupInfo(path)
	if err != nil || !sameBackup(before, current) {
		return result, backupUnavailable()
	}
	if err := backupSidecarsAbsent(path); err != nil {
		return result, err
	}
	return backupPublication{Version: 1, Backup: backupMetadata(id, before), SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}
