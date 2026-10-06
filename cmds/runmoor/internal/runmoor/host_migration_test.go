package runmoor

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"path/filepath"
	"testing"
)

func TestHostV3MigrationAtomicFailureAndPrivateV4Inspection(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "migration", true: "atomic failure"}[reject], func(t *testing.T) {
			c, store := fixtureStore(t)
			original := store.View()
			original.SchemaVersion = 3
			original.HostDirectories = nil
			original.HostExecutions = nil
			id := newID()
			original.Runners[id] = &Runner{ID: id, Backend: Tart, Phase: Quarantined, Resources: Resources{2, 4096}}
			original.RunnerTartStarts = map[string]string{id: "darwin:10:20"}
			body, _ := json.Marshal(original)
			if _, err := store.db.Exec("UPDATE snapshot SET body=? WHERE id=1", body); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec("PRAGMA user_version=3"); err != nil {
				t.Fatal(err)
			}
			if reject {
				if _, err := store.db.Exec("CREATE TRIGGER reject_migration BEFORE UPDATE ON snapshot BEGIN SELECT RAISE(ABORT, 'fixture'); END"); err != nil {
					t.Fatal(err)
				}
			}
			store.Close()
			read, err := ReadSnapshot(c)
			if err != nil || read.SchemaVersion != 3 {
				t.Fatal("read-only inspection migrated v3", err)
			}
			migrated, err := OpenStore(c)
			if reject {
				requireCode(t, err, ErrState)
				uri := url.URL{Scheme: "file", Path: filepath.Join(c.Storage.State, "state.sqlite"), RawQuery: "mode=ro"}
				db, err := sql.Open("sqlite", uri.String())
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var version int
				var current []byte
				if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
					t.Fatal(err)
				}
				if err = db.QueryRow("SELECT body FROM snapshot WHERE id=1").Scan(&current); err != nil {
					t.Fatal(err)
				}
				if version != 3 || string(current) != string(body) {
					t.Fatal("failed migration changed existing state")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer migrated.Close()
			after := migrated.View()
			if after.SchemaVersion != 4 || after.Installation != original.Installation || after.RunnerTartStarts[id] != original.RunnerTartStarts[id] || fingerprint(after.Runners[id]) != fingerprint(original.Runners[id]) || after.HostDirectories == nil || after.HostExecutions == nil {
				t.Fatal("migration lost ownership or reservations")
			}
			inspected, err := ReadSnapshot(c)
			if err != nil || inspected.SchemaVersion != 4 {
				t.Fatal("read-only v4 inspection failed", err)
			}
		})
	}
}
