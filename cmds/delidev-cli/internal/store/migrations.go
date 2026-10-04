// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type migrationDefinition struct {
	version int
	apply   func(context.Context, *sql.Tx, int) error
}

// Only implemented definitions belong here. Reservations are data, not migrations.
var migrations = []migrationDefinition{
	{2, migration002},
	{3, migration003},
	{4, migration004},
	{5, migration005},
	{6, migration006},
	{7, migration007},
	{8, migration008},
	{9, migration009},
	{10, migration010},
	{11, migration011},
	{12, migration012},
	{13, migration013},
	{14, migration014},
	{15, migration015},
	{16, migration016},
	{17, migration017},
	{18, migration018},
	{19, migration019},
	{20, migration020},
	{21, migration021},
	{22, migration022},
	{23, migration023},
	{24, migration024},
	{25, migration025},
	{26, migration026},
	{27, migration027},
}

func validateMigrations(definitions []migrationDefinition) error {
	if len(definitions) != SchemaVersion-1 {
		return fmt.Errorf("migration sequence must reach schema %d", SchemaVersion)
	}
	for i, definition := range definitions {
		if definition.version != i+2 || definition.apply == nil {
			return fmt.Errorf("invalid migration at position %d", i)
		}
	}
	return nil
}
func applyMigrations(ctx context.Context, tx *sql.Tx, original int) error {
	if err := validateMigrations(migrations); err != nil {
		return err
	}
	for _, definition := range migrations {
		// Known pre-main variants require idempotent layout reconciliation even when
		// their original version equals 22 or 23. Remove only if those inputs cease
		// to be supported; intermediate user_version writes cannot identify them.
		reconcile := (definition.version == 22 && original == 22) || (definition.version == 23 && original == 23)
		if definition.version <= original && !reconcile {
			continue
		}
		if err := definition.apply(ctx, tx, original); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", definition.version)); err != nil {
			return storageError(err)
		}
	}
	return nil
}
func migrate(ctx context.Context, db *sql.DB, root string) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return storageError(err)
	}
	if version == SchemaVersion {
		return nil
	}
	if version < 1 || version >= SchemaVersion {
		return domain.Fail(domain.RecoveryRequired, "No supported migration exists for this database.", "Preserve the original and use a matching server version.")
	}
	if err := migrationBackup(ctx, db, root); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	defer tx.Rollback()
	if err := applyMigrations(ctx, tx, version); err != nil {
		return err
	}
	return storageError(tx.Commit())
}
