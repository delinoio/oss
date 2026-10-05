// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
)

// A real 29 predecessor retains its implemented 26/27/28/29 tables. Only the
// successor's marker and explicit new default rows are removed for this fixture.
func predecessor29(t *testing.T, s *Store, retain domain.ID) {
	t.Helper()
	for _, preset := range additionalPresetIDs() {
		if _, err := s.db.Exec("DELETE FROM entities WHERE kind='provider' AND json_extract(body,'$.preset_id')=? AND id<>?", preset, retain); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec("DELETE FROM metadata WHERE key='provider_presets_layout'; PRAGMA user_version=29"); err != nil {
		t.Fatal(err)
	}
}
func additionalPresetIDs() []domain.ProviderPresetID {
	var ids []domain.ProviderPresetID
	for _, preset := range providers.Presets()[6:32] {
		ids = append(ids, preset.ID)
	}
	return ids
}

func TestAddedProvidersUpgrade29PreservesExistingIdentitiesAndDeletion(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	initial := checkPresetDefaults(t, s)
	gemini, openAI := initial[domain.PresetGemini].ProviderID, initial[domain.PresetOpenAI].ProviderID
	custom, account := domain.NewID(), domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.provider-additions", nil, func(tx *Tx) (any, error) {
		r, err := tx.Get(domain.ProviderKind, gemini)
		if err != nil {
			return nil, err
		}
		p, err := Decode[domain.Provider](r)
		if err != nil {
			return nil, err
		}
		p.SetEnabled(false)
		if _, err := tx.Put(domain.ProviderKind, gemini, r.Revision, "", "", p); err != nil {
			return nil, err
		}
		clone := p
		clone.PresetID = nil
		if _, err := tx.Put(domain.ProviderKind, custom, 0, "", "", clone); err != nil {
			return nil, err
		}
		return tx.Put(domain.AccountKind, account, 0, "", "", domain.Account{Alias: "fixture", ProviderID: gemini, Type: domain.APIAccount, Health: domain.AccountDisconnected})
	})
	if err != nil {
		t.Fatal(err)
	}
	var originalBody []byte
	if err := s.db.QueryRow("SELECT body FROM entities WHERE id=?", account).Scan(&originalBody); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DELETE FROM entities WHERE id=?", openAI); err != nil {
		t.Fatal(err)
	}
	predecessor29(t, s, gemini)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	page, more, _, err := s.ProviderInventoryPage(ctx, providers.Presets(), ProviderInventorySearch{Limit: 50})
	if err != nil || more || len(page) != 36 {
		t.Fatal("upgrade inventory mismatch", len(page), err)
	}
	var groq domain.ID
	for _, item := range page {
		if item.PresetID == nil {
			if item.ProviderID != custom {
				t.Fatal("custom identity merged")
			}
			continue
		}
		switch *item.PresetID {
		case domain.PresetGemini:
			if item.ProviderID != gemini || item.Enabled || item.Provider.Revision != 2 || item.TotalAccounts != 1 {
				t.Fatal("saved identity/account/Off changed")
			}
		case domain.PresetOpenAI:
			if item.ProviderID != "" || item.Enabled {
				t.Fatal("deleted old preset reseeded")
			}
		case domain.PresetGroq:
			groq = item.ProviderID
		}
	}
	var body []byte
	if err := s.db.QueryRow("SELECT body FROM entities WHERE id=?", account).Scan(&body); err != nil || string(body) != string(originalBody) {
		t.Fatal("historical account rewritten", err)
	}
	if groq == "" {
		t.Fatal("added default absent")
	}
	if _, err := s.db.Exec("DELETE FROM entities WHERE id=?", groq); err != nil {
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
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM entities WHERE kind='provider' AND json_extract(body,'$.preset_id')='groq'").Scan(&count); err != nil || count != 0 {
		t.Fatal("restart reseeded deleted added provider", err)
	}
}

func TestAddedProvidersUpgrade29RollbackIsAtomic(t *testing.T) {
	s, root := openTest(t)
	predecessor29(t, s, "")
	if _, err := s.db.Exec(`CREATE TRIGGER reject_added_provider BEFORE INSERT ON entities WHEN NEW.kind='provider' AND json_extract(NEW.body,'$.preset_id')='alibaba-model-studio-hong-kong' BEGIN SELECT RAISE(ABORT,'fixture rejection'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if failed, err := Open(context.Background(), root); err == nil {
		failed.Close()
		t.Fatal("migration ignored rejected final preset")
	}
	backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
	if err != nil || len(backups) != 1 {
		t.Fatal("original backup missing", err)
	}
	db, err := sql.Open("sqlite", databaseURI(filepath.Join(root, "state.sqlite"), false))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, count, marker int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 29 {
		t.Fatal("schema advanced after rollback", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM metadata WHERE key='provider_presets_layout'").Scan(&marker); err != nil || marker != 0 {
		t.Fatal("partial marker published", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM entities WHERE kind='provider'").Scan(&count); err != nil || count != 6 {
		t.Fatal("partial added defaults published", err)
	}
	if _, err := db.Exec("DROP TRIGGER reject_added_provider"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkPresetDefaults(t, s)
}
