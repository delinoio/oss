// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
		entries, err := restoreMigrationInventory(migration)
		if err != nil {
			return err
		}
		expected := map[domain.ID]backupPublication{}
		if v.MigrationImages != nil {
			for _, image := range v.MigrationImages.Images {
				expected[image.Backup.ID] = image
			}
		}
		for _, entry := range entries {
			id := domain.ID(strings.TrimSuffix(entry.Name(), ".sqlite"))
			claim, owned := expected[id]
			// A legacy journal has no independent ownership proof for retained
			// migration images. Never adopt its current bytes during recovery.
			if !owned {
				return backupUnavailable()
			}
			path := filepath.Join(migration, entry.Name())
			actual, err := fingerprintBackupPublication(ctx, path, id)
			if err != nil || !reflect.DeepEqual(actual, claim) {
				return backupUnavailable()
			}
			if err := validateBackup(ctx, path, &v.Input.ServerID); err != nil {
				return err
			}
		}
		// Missing pinned files are allowed only as interrupted cleanup: every
		// remaining file must still match its original synchronized receipt.
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

// Record migration-copy ownership before synchronizing the prepared journal.
// Same-server validity alone cannot identify the image this invocation created.
func fingerprintRestoreMigrationImages(ctx context.Context, root string, server domain.ID) (*restoreMigrationImages, error) {
	path := filepath.Join(root, "backups")
	entries, err := restoreMigrationInventory(path)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	images := &restoreMigrationImages{}
	for _, entry := range entries {
		id := domain.ID(strings.TrimSuffix(entry.Name(), ".sqlite"))
		file := filepath.Join(path, entry.Name())
		if err := validateBackup(ctx, file, &server); err != nil {
			return nil, err
		}
		claim, err := fingerprintBackupPublication(ctx, file, id)
		if err != nil {
			return nil, err
		}
		images.Images = append(images.Images, claim)
	}
	return images, nil
}

func restoreMigrationInventory(path string) ([]os.DirEntry, error) {
	if err := security.CheckPrivateDir(path); err != nil {
		return nil, storageError(err)
	}
	directory, err := os.Open(path)
	if err != nil {
		return nil, storageError(err)
	}
	entries, err := directory.ReadDir(SchemaVersion + 1)
	closeErr := directory.Close()
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > SchemaVersion {
		return nil, backupUnavailable()
	}
	if closeErr != nil {
		return nil, storageError(closeErr)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sqlite") || domain.ID(strings.TrimSuffix(entry.Name(), ".sqlite")).Validate() != nil {
			return nil, backupUnavailable()
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}
