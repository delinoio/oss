package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
)

func hostedPreset(id domain.ProviderPresetID) bool {
	switch id {
	case domain.PresetOpenAI, domain.PresetAnthropic, domain.PresetOpenRouter, domain.PresetVercel, domain.PresetXAI, domain.PresetDeepSeek:
		return true
	default:
		return false
	}
}

func checkPresetDefaults(t *testing.T, s *Store) map[domain.ProviderPresetID]ProviderInventoryItem {
	t.Helper()
	page, more, _, err := s.ProviderInventoryPage(context.Background(), providers.Presets(), ProviderInventorySearch{Limit: 50})
	if err != nil || more || len(page) != 9 {
		t.Fatalf("preset inventory: %d %t %v", len(page), more, err)
	}
	result := make(map[domain.ProviderPresetID]ProviderInventoryItem, 9)
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

func TestHostedProviderDefaultsMigrationPreservesSavedOffAndCustom(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	initial := checkPresetDefaults(t, s)
	openAI := initial[domain.PresetOpenAI].ProviderID
	custom := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.provider-defaults", nil, func(tx *Tx) (any, error) {
		record, err := tx.Get(domain.ProviderKind, openAI)
		if err != nil {
			return nil, err
		}
		provider, err := Decode[domain.Provider](record)
		if err != nil {
			return nil, err
		}
		provider.SetEnabled(false)
		if _, err := tx.Put(domain.ProviderKind, openAI, record.Revision, "", "", provider); err != nil {
			return nil, err
		}
		return tx.Put(domain.ProviderKind, custom, 0, "", "", domain.Provider{Name: "OpenAI", Endpoint: provider.Endpoint, Protocol: provider.Protocol, Authentication: provider.Authentication, Discovery: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	for id, item := range initial {
		if !hostedPreset(id) || id == domain.PresetOpenAI {
			continue
		}
		if _, err := s.db.ExecContext(ctx, "DELETE FROM entities WHERE id=?", item.ProviderID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, dropNativeAccountingFixtureSchema+"PRAGMA user_version=21"); err != nil {
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
	var hostedCount, customCount int
	for _, entry := range page {
		if entry.PresetID == nil {
			customCount++
			if entry.ProviderID != custom {
				t.Fatalf("custom provider changed identity: %+v", entry)
			}
			continue
		}
		if *entry.PresetID == domain.PresetOpenAI {
			if entry.ProviderID != openAI || entry.Enabled || entry.Provider.Revision != 2 {
				t.Fatalf("saved Off provider changed: %+v", entry)
			}
		} else if hostedPreset(*entry.PresetID) {
			hostedCount++
			if !entry.Enabled || entry.ProviderID == "" {
				t.Fatalf("missing hosted default: %+v", entry)
			}
		} else if entry.Enabled || entry.ProviderID != "" {
			t.Fatalf("local preset activated: %+v", entry)
		}
	}
	if hostedCount != 5 || customCount != 1 {
		t.Fatalf("unexpected migrated providers: hosted=%d custom=%d", hostedCount, customCount)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM entities WHERE kind='provider'").Scan(&count); err != nil || count != 7 {
		t.Fatalf("migration created duplicate providers: %d %v", count, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.db.QueryRow("SELECT COUNT(*) FROM entities WHERE kind='provider'").Scan(&count); err != nil || count != 7 {
		t.Fatalf("restart repeated defaults: %d %v", count, err)
	}
}

func TestHostedProviderDefaultsMigrationRollsBackTogether(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	for _, entry := range checkPresetDefaults(t, s) {
		if _, err := s.db.ExecContext(ctx, "DELETE FROM entities WHERE id=?", entry.ProviderID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, dropNativeAccountingFixtureSchema+`CREATE TRIGGER reject_fixture_provider BEFORE INSERT ON entities WHEN NEW.kind='provider' AND json_extract(NEW.body,'$.preset_id')='anthropic' BEGIN SELECT RAISE(ABORT,'fixture rejection'); END; PRAGMA user_version=21;`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if failed, err := Open(ctx, root); err == nil {
		failed.Close()
		t.Fatal("migration unexpectedly succeeded")
	}
	backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("failed migration did not preserve its original backup: %v %v", backups, err)
	}
	db, err := sql.Open("sqlite", databaseURI(filepath.Join(root, "state.sqlite"), false))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, count int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 21 {
		t.Fatalf("failed migration changed version: %d %v", version, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM entities WHERE kind='provider'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration partially seeded providers: %d %v", count, err)
	}
	if _, err := db.Exec("DROP TRIGGER reject_fixture_provider"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkPresetDefaults(t, s)
}
