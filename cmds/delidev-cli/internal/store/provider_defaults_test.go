package store

import (
	"context"

	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
)

func hostedPreset(id domain.ProviderPresetID) bool {
	return id.Valid() && id != domain.PresetOllama && id != domain.PresetLMStudio && id != domain.PresetVLLM
}

func checkPresetDefaults(t *testing.T, s *Store) map[domain.ProviderPresetID]ProviderInventoryItem {
	t.Helper()
	page, more, _, err := s.ProviderInventoryPage(context.Background(), providers.Presets(), ProviderInventorySearch{Limit: 50})
	if err != nil || more || len(page) != 35 {
		t.Fatalf("preset inventory: %d %t %v", len(page), more, err)
	}
	result := make(map[domain.ProviderPresetID]ProviderInventoryItem, 35)
	for _, entry := range page {
		if entry.PresetID == nil {
			continue
		}
		if entry.Enabled != hostedPreset(*entry.PresetID) || (entry.ProviderID != "") != hostedPreset(*entry.PresetID) || entry.TotalAccounts != 0 || entry.ConnectedAccounts != 0 || !entry.AccountCountsAvailable {
			t.Fatalf("unexpected default preset: %+v", entry)
		}
		result[*entry.PresetID] = entry
	}
	return result
}

func TestHostedProviderDefaultsOnFreshDatabase(t *testing.T) {
	s, _ := openTest(t)
	checkPresetDefaults(t, s)
	var version, accounts, models int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("fresh schema version: %d %v", version, err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM entities WHERE kind='account'").Scan(&accounts); err != nil || accounts != 0 {
		t.Fatalf("defaults created accounts: %d %v", accounts, err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM entities WHERE kind='model'").Scan(&models); err != nil || models != 0 {
		t.Fatalf("defaults created models: %d %v", models, err)
	}
}

func TestHostedProviderDeletionDoesNotSeedAgainOnRestart(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	openAI := checkPresetDefaults(t, s)[domain.PresetOpenAI].ProviderID
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.delete-hosted-provider", nil, func(tx *Tx) (any, error) {
		return nil, tx.Delete(domain.ProviderKind, openAI, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	page, _, _, err := s.ProviderInventoryPage(ctx, providers.Presets(), ProviderInventorySearch{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page {
		if entry.PresetID != nil && *entry.PresetID == domain.PresetOpenAI {
			if entry.Enabled || entry.ProviderID != "" {
				t.Fatalf("restart recreated a deleted hosted provider: %+v", entry)
			}
			return
		}
	}
	t.Fatal("deleted hosted preset is missing from inventory")
}
