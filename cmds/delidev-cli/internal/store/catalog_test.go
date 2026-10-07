package store

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
)

func TestProviderAvailabilityReadsRequireExplicitEnablement(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	var activeProvider, missingProvider, activeModel, activeAccount domain.ID
	_, err := s.Mutate(ctx, domain.NewID(), "provider-availability.fixture", nil, func(tx *Tx) (any, error) {
		for _, enabled := range []bool{true, false} {
			provider, model, account := domain.NewID(), domain.NewID(), domain.NewID()
			if enabled {
				activeProvider, activeModel, activeAccount = provider, model, account
			} else {
				missingProvider = provider
			}
			if _, err := tx.Put(domain.ProviderKind, provider, 0, "", "", domain.Provider{Name: "Availability fixture", Endpoint: "https://api.example.test/v1", Protocol: domain.OpenAIResponses, Authentication: domain.BearerAuth, Discovery: true, Enabled: &enabled}); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.ModelKind, model, 0, "", "", domain.Model{Name: "Availability fixture", ProviderID: provider, NativeID: "fixture-model"}); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.AccountKind, account, 0, "", "", domain.Account{Alias: "Availability fixture", ProviderID: provider, Type: domain.APIAccount, Enabled: true, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.BearerAuth, ConnectedAt: now}}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Inject a historical document shape without going through current validation.
	// Read-only availability queries must neither activate it nor repair its bytes.
	if _, err := s.db.Exec("UPDATE entities SET body=json_remove(body,'$.enabled') WHERE id=?", missingProvider); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := s.db.QueryRow("SELECT body FROM entities WHERE id=?", missingProvider).Scan(&before); err != nil {
		t.Fatal(err)
	}
	models, _, _, err := s.SearchModels(ctx, ModelSearch{EnabledProvidersOnly: true, Limit: MaxPage})
	if err != nil || len(models) != 1 || models[0].ID != activeModel {
		t.Fatalf("model availability inferred missing enablement: %+v %v", models, err)
	}
	items, _, _, err := s.ProviderInventoryPage(ctx, providers.Presets(), ProviderInventorySearch{EnabledOnly: true, Query: "Availability fixture", Limit: MaxPage})
	if err != nil || len(items) != 1 || items[0].ProviderID != activeProvider || !items[0].Enabled {
		t.Fatalf("provider inventory inferred missing enablement: %+v %v", items, err)
	}
	accounts, err := s.CatalogCandidates(ctx, "", MaxPage, now, time.Minute)
	if err != nil || len(accounts) != 1 || accounts[0].ID != activeAccount {
		t.Fatalf("discovery inferred missing enablement: %+v %v", accounts, err)
	}
	if err := s.db.QueryRow("SELECT body FROM entities WHERE id=?", missingProvider).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("availability queries changed the unsupported document")
	}
}

func TestFreshSchemaInstallsProviderActivationIndex(t *testing.T) {
	s, _ := openTest(t)
	var version, indexes int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='provider_preset_unique'").Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion || indexes != 1 {
		t.Fatalf("fresh database lacks provider activation schema: user_version=%d preset_index=%d", version, indexes)
	}
}

func TestCatalogCandidatesPersistDueTimeAndRetryDelay(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	provider := domain.NewID()
	var records []Record
	_, err := s.Mutate(ctx, domain.NewID(), "catalog.fixture", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.ProviderKind, provider, 0, "", "", domain.Provider{Discovery: true, Enabled: new(true)}); err != nil {
			return nil, err
		}
		for _, state := range []string{"new", "recent", "due", "retry", "disabled", "disconnected", "removing"} {
			connection := domain.NewID()
			a := domain.Account{ProviderID: provider, Type: domain.APIAccount, Enabled: true, Connection: &domain.AccountConnection{ID: connection}}
			if state != "new" {
				a.Catalog = &domain.CatalogObservation{ConnectionID: connection, ObservedAt: now.Add(-20 * time.Minute)}
			}
			switch state {
			case "recent":
				a.Catalog.ObservedAt = now.Add(-time.Minute)
			case "retry":
				delay := uint32(3600)
				a.Catalog.RetryAfterSeconds = &delay
			case "disabled":
				a.Enabled = false
			case "disconnected":
				a.Connection = nil
			case "removing":
				a.Removal = &domain.AccountRemoval{}
			}
			r, err := tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", a)
			if err != nil {
				return nil, err
			}
			records = append(records, r)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	due, err := s.CatalogCandidates(ctx, "", MaxPage, now, 15*time.Minute)
	if err != nil || len(due) != 2 || due[0].ID != records[0].ID || due[1].ID != records[2].ID {
		t.Fatalf("wrong due accounts: %+v %v", due, err)
	}
	later, err := s.CatalogCandidates(ctx, records[2].ID, MaxPage, now.Add(time.Hour), 15*time.Minute)
	if err != nil || len(later) != 1 || later[0].ID != records[3].ID {
		t.Fatalf("retry delay/page cursor: %+v %v", later, err)
	}
}
