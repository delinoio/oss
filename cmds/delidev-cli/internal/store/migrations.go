// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
)

func checkCurrentSchemaVersion(ctx context.Context, db *sql.DB) error {
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
