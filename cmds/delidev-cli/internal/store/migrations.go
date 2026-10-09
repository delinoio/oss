// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"strings"
	"time"
)

type migrationDefinition struct {
	version int
	apply   func(context.Context, *sql.Tx, int) error
}

// Only implemented definitions belong here. Reservations are data, not migrations.
var migrations = []migrationDefinition{}

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

// No historical migration is reachable from current initialization or reopen.
func applyMigrations(context.Context, *sql.Tx, int) error {
	return domain.Fail(domain.Unsupported, "Historical upgrades are retired.", "Preserve the original database and use its matching version.")
}
func migrate(ctx context.Context, db *sql.DB, root string) error {
	var v int
	if e := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); e != nil {
		return storageError(e)
	}
	if v != SchemaVersion {
		return domain.Fail(domain.RecoveryRequired, "Earlier databases are unsupported.", "Preserve the original database and sidecars; explicitly reset only after handling native and credential ownership.")
	}
	return nil
}
func seedCurrentProviders(ctx context.Context, tx *sql.Tx) error {
	t := &Tx{tx: tx, ctx: ctx, now: time.Now().UTC().Truncate(time.Millisecond), touched: map[domain.ID]bool{}}
	for _, preset := range providers.Presets() {
		if !strings.HasPrefix(preset.Provider.Endpoint, "https://") {
			continue
		}
		p := providers.WithAPIFormats(preset.Provider)
		p.SetEnabled(true)
		if _, e := t.Put(domain.ProviderKind, domain.NewID(), 0, "", "", p); e != nil {
			return e
		}
	}
	return nil
}
