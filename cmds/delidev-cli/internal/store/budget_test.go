package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func readBudgetEstimate(t *testing.T, s *Store, session domain.ID, currency domain.Currency) domain.BudgetEvidence {
	t.Helper()
	var value domain.BudgetEvidence
	if err := s.Read(context.Background(), func(tx *Tx) error { var err error; value, err = tx.SessionEstimate(session, currency); return err }); err != nil {
		t.Fatal(err)
	}
	return value
}
func TestSessionBudgetRollupDeduplicatesAndPreservesLifetimeEvidence(t *testing.T) {
	s, root := openTest(t)
	record := responseRecord(seedSearch(t, s, "source", domain.Archived))
	price := preparePrice(t, s, record)
	id := domain.NewID()
	if _, _, err := writeResponse(s, id, record); err != nil {
		t.Fatal(err)
	}
	record.Sequence++
	if _, replay, err := writeResponse(s, domain.NewID(), record); err != nil || !replay {
		t.Fatal("duplicate", err)
	}
	total := readBudgetEstimate(t, s, record.SessionID, "USD")
	if total.KnownAmount != "0.000065" || total.CompleteResponses != 1 {
		t.Fatal("duplicate rollup", total)
	}
	gate := domain.EstimatedCostBudget{Currency: "USD", Threshold: "0.000065"}
	err := s.Read(context.Background(), func(tx *Tx) error { return tx.RequireSessionBudget(record.SessionID, &gate) })
	assertCode(t, err, domain.BudgetReached)
	gate.Threshold = "0.000065000000001"
	if err = s.Read(context.Background(), func(tx *Tx) error { return tx.RequireSessionBudget(record.SessionID, &gate) }); err != nil {
		t.Fatal("rounded budget", err)
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.budget-rollback", nil, func(tx *Tx) (any, error) {
		copy := record
		copy.Usage.ResponseDigest = strings.Repeat("f", 64)
		if _, _, err := tx.PutResponseUsage(domain.NewID(), copy); err != nil {
			return nil, err
		}
		return nil, domain.Fail(domain.Conflict, "rollback", "retry")
	})
	assertCode(t, err, domain.Conflict)
	if after := readBudgetEstimate(t, s, record.SessionID, "USD"); after != total {
		t.Fatal("rollback changed total")
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.budget-price-change", nil, func(tx *Tx) (any, error) {
		basis := pricingFixture()
		basis.Currency = "EUR"
		basis.InputPerMillion = nil
		return tx.PutPricing(record.ModelID, price.Revision, domain.NewID(), basis)
	})
	if err != nil {
		t.Fatal(err)
	}
	record.Usage.ResponseDigest = strings.Repeat("b", 64)
	if _, _, err = writeResponse(s, domain.NewID(), record); err != nil {
		t.Fatal(err)
	}
	if value := readBudgetEstimate(t, s, record.SessionID, "EUR"); value.KnownAmount != "0.00004" || value.PartialResponses != 1 {
		t.Fatal("currency/basis lost", value)
	}
	record.ModelID = domain.NewID()
	record.Usage.ResponseDigest = strings.Repeat("c", 64)
	if _, _, err = writeResponse(s, domain.NewID(), record); err != nil {
		t.Fatal(err)
	}
	if value := readBudgetEstimate(t, s, record.SessionID, ""); value.KnownAmount != "" || value.UnavailableResponses != 1 {
		t.Fatal("unpriced invented zero", value)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if value := readBudgetEstimate(t, s, record.SessionID, "USD"); value != total {
		t.Fatal("restart changed lifetime cost")
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.delete-budget-session", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.SessionKind, record.SessionID, 1) })
	if err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM session_estimate_totals").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("session deletion retained estimate", err)
	}
}
func TestBudgetMigrationPreservesHistoricalPricesAndBackup(t *testing.T) {
	for _, corrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "migrate", true: "reject-corruption"}[corrupted], func(t *testing.T) {
			s, root := openTest(t)
			record := responseRecord(seedSearch(t, s, "source", domain.NotArchived))
			price := preparePrice(t, s, record)
			id := domain.NewID()
			if _, _, err := writeResponse(s, id, record); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(dropNotificationFixtureSchema + "DROP TABLE session_estimate_totals; DROP TABLE IF EXISTS native_accounting; PRAGMA user_version=15"); err != nil {
				t.Fatal(err)
			}
			if corrupted {
				if _, err := s.db.Exec("UPDATE response_estimates SET body=json_set(body,'$.known_amount','999')"); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(context.Background(), root)
			if corrupted {
				if err == nil {
					reopened.Close()
					t.Fatal("accepted corrupt original estimate")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				if value := readBudgetEstimate(t, reopened, record.SessionID, "USD"); value.KnownAmount != "0.000065" || value.CompleteResponses != 1 {
					t.Fatal("migration changed basis", value)
				}
				_, retained := readEstimate(t, reopened, id)
				if retained.ID != price.ID {
					t.Fatal("migration selected current price")
				}
			}
			backups, _ := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
			if len(backups) != 1 {
				t.Fatal("missing backup")
			}
			for _, path := range []string{backups[0], filepath.Join(root, "state.sqlite")} {
				db, err := sql.Open("sqlite", databaseURI(path, true))
				if err != nil {
					t.Fatal(err)
				}
				var version int
				err = db.QueryRow("PRAGMA user_version").Scan(&version)
				db.Close()
				want := 15
				if path != backups[0] && !corrupted {
					want = SchemaVersion
				}
				if err != nil || version != want {
					t.Fatal("migration rollback", version, err)
				}
			}
		})
	}
}
