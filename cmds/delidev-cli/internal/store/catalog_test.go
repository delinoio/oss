package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

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

func TestCatalogMigrationPreservesV2AndRollsBackIdentityConflicts(t *testing.T) {
	for _, conflict := range []string{"none", "canonical", "alias"} {
		t.Run(conflict, func(t *testing.T) {
			s, root := openTest(t)
			provider, modelID := domain.NewID(), domain.NewID()
			model := domain.Model{ProviderID: provider, NativeID: "fixture-native", Name: "Legacy manual", Alias: "short", MetadataSource: domain.Unknown}
			_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.seed", nil, func(tx *Tx) (any, error) {
				if _, err := tx.Put(domain.ProviderKind, provider, 0, "", "", domain.Provider{Name: "Legacy provider"}); err != nil {
					return nil, err
				}
				return tx.Put(domain.ModelKind, modelID, 0, "", "", model)
			})
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			path := filepath.Join(root, "state.sqlite")
			db, err := sql.Open("sqlite", databaseURI(path, false))
			if err != nil {
				t.Fatal(err)
			}
			// Construct the exact previous schema while retaining real entity,
			// receipt and event data created through the store transaction path.
			_, err = historicalSchema(db, "002")
			if err != nil {
				t.Fatal(err)
			}
			count := 1
			if conflict != "none" {
				if conflict == "alias" {
					model.NativeID = "different-native"
				} else {
					model.Alias = "different-alias"
				}
				raw, _ := json.Marshal(model)
				_, err = db.Exec("INSERT INTO entities(id,kind,revision,body,created_at,updated_at) VALUES(?,'model',1,?,1,1)", domain.NewID(), raw)
				if err != nil {
					t.Fatal(err)
				}
				count++
			}
			db.Close()
			migrated, err := Open(context.Background(), root)
			if conflict == "none" {
				if err != nil {
					t.Fatal(err)
				}
				got, err := migrated.Get(context.Background(), domain.ModelKind, modelID)
				if err != nil || got.Revision != 1 {
					t.Fatalf("migration lost model: %v", err)
				}
				m, err := Decode[domain.Model](got)
				if err != nil || m.Name != "Legacy manual" || m.Discovery != nil {
					t.Fatal("migration invented discovery evidence")
				}
				migrated.Close()
			} else if err == nil {
				migrated.Close()
				t.Fatal("duplicate legacy identity was silently changed")
			}
			backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
			if err != nil || len(backups) != 1 {
				t.Fatalf("missing backup: %v", err)
			}
			for _, candidate := range []string{path, backups[0]} {
				check, err := sql.Open("sqlite", databaseURI(candidate, true))
				if err != nil {
					t.Fatal(err)
				}
				var version, models, indices, receipts int
				for query, out := range map[string]*int{"PRAGMA user_version": &version, "SELECT COUNT(*) FROM entities WHERE kind='model'": &models, "SELECT COUNT(*) FROM sqlite_master WHERE name='model_canonical'": &indices, "SELECT COUNT(*) FROM receipts": &receipts} {
					if err := check.QueryRow(query).Scan(out); err != nil {
						t.Fatal(err)
					}
				}
				check.Close()
				wantVersion, wantIndices := 2, 0
				if candidate == path && conflict == "none" {
					wantVersion, wantIndices = SchemaVersion, 1
				}
				if version != wantVersion || indices != wantIndices || models != count || receipts != 1 {
					t.Fatalf("migration/backup changed original: version=%d indices=%d models=%d receipts=%d", version, indices, models, receipts)
				}
			}
		})
	}
}

func TestCatalogCandidatesPersistDueTimeAndRetryDelay(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	provider := domain.NewID()
	var records []Record
	_, err := s.Mutate(ctx, domain.NewID(), "catalog.fixture", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.ProviderKind, provider, 0, "", "", domain.Provider{Discovery: true}); err != nil {
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

func TestAccountInspectionCandidatesIndependentValidationAndDiscovery(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	for _, test := range []struct {
		name                      string
		discovery                 bool
		validationAge, catalogAge time.Duration
		delay                     uint32
		due                       bool
	}{
		{"both-fresh", true, time.Minute, time.Minute, 0, false},
		{"validation-only", false, 20 * time.Minute, time.Minute, 0, true},
		{"catalog-only", true, time.Minute, 20 * time.Minute, 0, true},
		{"discovery-disabled", false, time.Minute, 20 * time.Minute, 0, false},
		{"validation-retry-after", false, 20 * time.Minute, time.Minute, 3600, false},
		{"validation-first-connection", false, 0, time.Minute, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, id, connection := domain.NewID(), domain.NewID(), domain.NewID()
			_, err := s.Mutate(ctx, domain.NewID(), "inspection.fixture", id, func(tx *Tx) (any, error) {
				if _, err := tx.Put(domain.ProviderKind, p, 0, "", "", domain.Provider{Discovery: test.discovery, Protocol: domain.OpenAIChat}); err != nil {
					return nil, err
				}
				a := domain.Account{ProviderID: p, Type: domain.APIAccount, Enabled: true, Connection: &domain.AccountConnection{ID: connection}, Catalog: &domain.CatalogObservation{ConnectionID: connection, ObservedAt: now.Add(-test.catalogAge)}}
				if test.validationAge > 0 {
					a.Validation = &domain.AccountValidation{ConnectionID: connection, ObservedAt: now.Add(-test.validationAge), RetryAfterSeconds: &test.delay}
				}
				return tx.Put(domain.AccountKind, id, 0, "", "", a)
			})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := s.AccountInspectionCandidates(ctx, "", MaxPage, now, 15*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, r := range rows {
				if r.ID == id {
					found = true
				}
			}
			if found != test.due {
				t.Fatalf("independent due selection: found=%v want=%v", found, test.due)
			}
		})
	}
}
