package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestAccountingMigrationPreservesHistoryWithoutBackfill(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "migrate", true: "conflict"}[conflict], func(t *testing.T) {
			s, root := openTest(t)
			f := seedSearch(t, s, "retained history", domain.Archived)
			id := domain.NewID()
			var original Record
			_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.historical-grok", nil, func(tx *Tx) (any, error) {
				var err error
				original, err = tx.Put(domain.UsageKind, id, 0, f.session, f.project, map[string]any{"historical_native_total": "18446744073709551615"})
				return original, err
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec("DROP TABLE native_accounting; PRAGMA user_version=24"); err != nil {
				t.Fatal(err)
			}
			if conflict {
				if _, err := s.db.Exec("CREATE TABLE native_accounting(unrecognized TEXT)"); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(context.Background(), root)
			if conflict {
				if err == nil {
					reopened.Close()
					t.Fatal("adopted foreign accounting schema")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			retained, err := reopened.Get(context.Background(), domain.UsageKind, id)
			if err != nil || !reflect.DeepEqual(original, retained) {
				t.Fatal("migration rewrote original native history", err)
			}
			var count, version int
			if err := reopened.db.QueryRow("SELECT count(*) FROM native_accounting").Scan(&count); err != nil || count != 0 {
				t.Fatal("migration backfilled unverified history", count, err)
			}
			if err := reopened.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
				t.Fatal(version, err)
			}
			backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
			if err != nil || len(backups) != 1 || ValidateBackup(context.Background(), backups[0]) != nil {
				t.Fatal("migration did not retain a valid backup", err)
			}
		})
	}
}
