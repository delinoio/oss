// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration022(ctx context.Context, tx *sql.Tx, original int) error {
	// Previous unmerged backup layouts used versions 21 and 22. Detect their
	// retained deletion table before adding schema 23. Only the backup layout
	// of version 22 still needs the hosted-provider defaults; main's version 22
	// already applied them and must preserve subsequent explicit deletions.
	var existingDeletionTable bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='backup_deletions')").Scan(&existingDeletionTable); err != nil {
		return storageError(err)
	}
	if original == 21 && existingDeletionTable {
		var providerIndex bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='index' AND name='provider_preset_unique')").Scan(&providerIndex); err != nil {
			return storageError(err)
		}
		if !providerIndex {
			if _, err := tx.ExecContext(ctx, providerActivationSchema); err != nil {
				return storageError(err)
			}
		}
	}
	if original < 22 || (original == 22 && existingDeletionTable) {
		if err := seedHostedProviders(ctx, tx); err != nil {
			return err
		}
	}
	return nil
}
