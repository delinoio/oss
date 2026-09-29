package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
)

// seedHostedProviders runs only while creating a database or migrating it to
// version 22. A stored provider, including one explicitly turned off, keeps
// its identity and availability. The version boundary prevents a later user
// deletion from being undone on every server restart.
func seedHostedProviders(ctx context.Context, sqlTx *sql.Tx) error {
	transaction := &Tx{tx: sqlTx, ctx: ctx, now: time.Now().UTC().Truncate(time.Millisecond), touched: map[domain.ID]bool{}}
	for _, preset := range providers.Presets() {
		switch preset.ID {
		case domain.PresetOpenAI, domain.PresetAnthropic, domain.PresetOpenRouter, domain.PresetVercel, domain.PresetXAI, domain.PresetDeepSeek:
		default:
			continue
		}
		var existing domain.ID
		err := sqlTx.QueryRowContext(ctx, "SELECT id FROM entities WHERE kind='provider' AND json_extract(body,'$.preset_id')=?", preset.ID).Scan(&existing)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return storageError(err)
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
