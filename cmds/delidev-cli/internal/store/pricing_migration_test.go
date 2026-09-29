package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestPricingMigrationNeverInventsHistoricalBasis(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "migrate", true: "rollback"}[conflict], func(t *testing.T) {
			s, root := openTest(t)
			record := responseRecord(seedSearch(t, s, "source", domain.Archived))
			id := domain.NewID()
			if _, _, err := writeResponse(s, id, record); err != nil {
				t.Fatal(err)
			}
			original, err := s.ResponseUsage(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(dropPricingFixtureSchema + "DROP TABLE IF EXISTS native_accounting; PRAGMA user_version=14;"); err != nil {
				t.Fatal(err)
			}
			if conflict {
				if _, err = s.db.Exec("CREATE TABLE pricing_versions(conflict TEXT)"); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			migrated, err := Open(context.Background(), root)
			if conflict {
				if err == nil {
					migrated.Close()
					t.Fatal("adopted conflicting price schema")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer migrated.Close()
				value, basis := readEstimate(t, migrated, id)
				if value.KnownAmount != "" || basis != nil || value.Coverage != domain.EstimateUnavailable {
					t.Fatal("migration invented historical price")
				}
				retained, err := migrated.ResponseUsage(context.Background(), id)
				if err != nil || retained.Record.Sequence != original.Record.Sequence || retained.Record.Usage.ResponseDigest != original.Record.Usage.ResponseDigest || !retained.CreatedAt.Equal(original.CreatedAt) {
					t.Fatal("migration changed original usage", err)
				}
			}
			backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
			if err != nil || len(backups) != 1 {
				t.Fatal("missing original backup", err)
			}
			for _, path := range []string{backups[0], filepath.Join(root, "state.sqlite")} {
				db, err := sql.Open("sqlite", databaseURI(path, true))
				if err != nil {
					t.Fatal(err)
				}
				var version int
				err = db.QueryRow("PRAGMA user_version").Scan(&version)
				db.Close()
				want := 14
				if path != backups[0] && !conflict {
					want = SchemaVersion
				}
				if err != nil || version != want {
					t.Fatal("migration lost original version or rollback", version, err)
				}
			}
		})
	}
}
