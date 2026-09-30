package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// migrationBackup publishes only a validated, synchronized pre-migration image.
// A killed writer can leave a .pending file, never a published .sqlite backup.
func migrationBackup(ctx context.Context, db *sql.DB, root string) error {
	// Back up even this additive migration. Destructive future migrations must
	// retain the same pre-migration backup boundary and never reset on failure.
	backup := filepath.Join(root, "backups", string(domain.NewID())+".sqlite")
	pending := backup + ".pending"
	defer os.Remove(pending)
	private, err := os.OpenFile(pending, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return storageError(err)
	}
	if err := private.Close(); err != nil {
		return storageError(err)
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", pending); err != nil {
		return storageError(err)
	}
	if err := ValidateBackup(ctx, pending); err != nil {
		return err
	}
	file, err := os.OpenFile(pending, os.O_RDWR, 0)
	if err != nil {
		return storageError(err)
	}
	err = file.Sync()
	closeErr := file.Close()
	if err != nil {
		return storageError(err)
	}
	if closeErr != nil {
		return storageError(closeErr)
	}
	if err := os.Rename(pending, backup); err != nil {
		return storageError(err)
	}
	if err := security.SyncParent(backup); err != nil {
		return storageError(err)
	}
	return nil
}
