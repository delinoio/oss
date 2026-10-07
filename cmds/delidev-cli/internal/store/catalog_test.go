package store

import (
	"context"

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
