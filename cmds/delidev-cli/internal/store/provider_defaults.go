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
// the hosted-defaults boundary (22 on main, 23 from the backup branch). A stored provider, including one explicitly turned off, keeps
// its identity and availability. The version boundary prevents a later user
// deletion from being undone on every server restart.
func seedHostedProviders(ctx context.Context, sqlTx *sql.Tx) error {
	// Historical migrations retain exactly their original six identities even
	// as the product registry grows. Never seed newly added presets here.
	return seedProviderSet(ctx, sqlTx, []domain.ProviderPresetID{domain.PresetOpenAI, domain.PresetAnthropic, domain.PresetOpenRouter, domain.PresetVercel, domain.PresetXAI, domain.PresetDeepSeek})
}

func seedProviderSet(ctx context.Context, sqlTx *sql.Tx, ids []domain.ProviderPresetID) error {
	transaction := &Tx{tx: sqlTx, ctx: ctx, now: time.Now().UTC().Truncate(time.Millisecond), touched: map[domain.ID]bool{}}
	selected := make(map[domain.ProviderPresetID]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	for _, preset := range providers.Presets() {
		if !selected[preset.ID] {
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
		// Historical seed migrations retain their original document shape.
		// Current inventory projects profiles from the canonical registry.
		provider.APIFormats = nil
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
