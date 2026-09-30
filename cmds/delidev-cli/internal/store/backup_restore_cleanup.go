// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// server.lock excludes all publication while terminal receipts retire only
// their private staging/safety images. Journals remain forever; database copies
// must not outlive recovery and bypass later permanent session backup erasure.
// Unknown/unaccepted staging is preserved and blocks deletion completion.
func cleanupSettledRestoreImages(ctx context.Context, root string, reserved map[domain.ID]bool) error {
	for id := range reserved {
		v, err := readRestoreReceipt(root, id)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return storageError(err)
		}
		if v.State != RestoreCompleted && v.State != RestoreRolledBack {
			return backupUnavailable()
		}
		dir := restoreDirectory(root, id)
		for _, item := range []struct{ name, digest string }{{"safety.sqlite", v.SafetySHA256}, {"candidate.sqlite", v.CandidateSHA256}} {
			path := filepath.Join(dir, item.name)
			if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
				// Retry a crash between unlink and directory synchronization.
				if err := security.SyncParent(path); err != nil {
					return storageError(err)
				}
				continue
			} else if err != nil {
				return storageError(err)
			}
			claim, err := fingerprintBackupPublication(ctx, path, id)
			if err != nil || claim.SHA256 != item.digest {
				return backupUnavailable()
			}
			if err := removeRestoreArtifact(ctx, root, path); err != nil {
				return err
			}
		}
		// Supported staging migrations retain their mandatory pre-migration copy
		// only until recovery settles. The selected published source stays intact.
		migration := filepath.Join(dir, "backups")
		if _, err := os.Lstat(migration); errors.Is(err, os.ErrNotExist) {
			if err := security.SyncParent(migration); err != nil {
				return storageError(err)
			}
			continue
		}
		if err := security.CheckPrivateDir(migration); err != nil {
			return storageError(err)
		}
		directory, err := os.Open(migration)
		if err != nil {
			return storageError(err)
		}
		entries, err := directory.ReadDir(SchemaVersion + 1)
		directory.Close()
		if err != nil && !errors.Is(err, io.EOF) || len(entries) > SchemaVersion {
			return backupUnavailable()
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".sqlite") || domain.ID(strings.TrimSuffix(entry.Name(), ".sqlite")).Validate() != nil {
				return backupUnavailable()
			}
			if err := validateBackup(ctx, filepath.Join(migration, entry.Name()), &v.Input.ServerID); err != nil {
				return err
			}
		}
		if err := removeRestoreArtifact(ctx, root, migration); err != nil {
			return err
		}
	}
	return nil
}

// Called under backupGate at final session backup acknowledgement. A crash
// before the accepted restore barrier may leave unjournaled private copies;
// neither a deletion nor a later restore may silently discard that evidence.
func (s *Store) checkRestoreImagesRetired(ctx context.Context) error {
	for id := range s.restoreReservations {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		entries, err := os.ReadDir(restoreDirectory(s.root, id))
		if err != nil {
			return storageError(err)
		}
		for _, entry := range entries {
			if entry.Name() != "receipt.json" && !strings.HasPrefix(entry.Name(), ".pending-") {
				return domain.SessionDeletionPending()
			}
		}
	}
	return nil
}

// macOS temporary roots may be reached through /var -> /private/var. Resolve
// that established root alias before the removal helper's strict parent checks;
// descendants still cannot redirect cleanup outside the original private root.
func removeRestoreArtifact(ctx context.Context, root, path string) error {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return storageError(err)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
		return backupUnavailable()
	}
	return security.RemoveOwnedTree(ctx, canonical, filepath.Join(canonical, relative))
}
