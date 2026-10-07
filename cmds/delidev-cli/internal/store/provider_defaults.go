// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
)

// Seed only at first initialization. Restart cannot undo a user's deletion or
// activation choice; local endpoints remain explicit user configurations.
func seedHostedProviders(ctx context.Context, sqlTx *sql.Tx) error {
	transaction := &Tx{tx: sqlTx, ctx: ctx, now: time.Now().UTC().Truncate(time.Millisecond), touched: map[domain.ID]bool{}}
	for _, preset := range providers.Presets() {
		if preset.Provider.Authentication == domain.KeylessAuth {
			continue
		}
		provider := preset.Provider
		provider.SetEnabled(true)
		if err := provider.Validate(); err != nil {
			return err
		}
		if _, err := transaction.Put(domain.ProviderKind, domain.NewID(), 0, "", "", provider); err != nil {
			return err
		}
	}
	return nil
}
