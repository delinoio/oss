package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestMigrationBacksUpOriginalAndRollsBackOnFailure(t *testing.T) {
	for _, conflicting := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "conflict"}[conflicting], func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "state")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "state.sqlite")
			db, err := sql.Open("sqlite", databaseURI(path, false))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(schema); err != nil {
				t.Fatal(err)
			}
			if conflicting {
				if _, err := db.Exec("CREATE TABLE jobs(id TEXT PRIMARY KEY)"); err != nil {
					t.Fatal(err)
				}
			}
			db.Close()
			if err := os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			s, err := Open(context.Background(), root)
			if conflicting {
				if err == nil {
					s.Close()
					t.Fatal("conflicting migration succeeded")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				s.Close()
			}
			backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
			if err != nil || len(backups) != 1 {
				t.Fatalf("missing pre-migration backup: %v %v", backups, err)
			}
			for _, item := range []struct {
				Path    string
				Version int
			}{{backups[0], 1}, {path, map[bool]int{false: 2, true: 1}[conflicting]}} {
				db, err := sql.Open("sqlite", databaseURI(item.Path, true))
				if err != nil {
					t.Fatal(err)
				}
				var version int
				err = db.QueryRow("PRAGMA user_version").Scan(&version)
				db.Close()
				if err != nil || version != item.Version {
					t.Fatalf("version %d want %d: %v", version, item.Version, err)
				}
			}
		})
	}
}
func TestRevokedPrincipalCannotCommitOrReplayReceipt(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	id := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "device", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.DeviceKind, id, 0, "", "", domain.Device{Name: "client", Type: domain.ClientDevice, PairedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := domain.WithPrincipal(ctx, domain.Principal{Type: domain.ClientDevice, DeviceID: id})
	request := domain.NewID()
	calls := 0
	action := func(tx *Tx) (any, error) {
		calls++
		return tx.Put(domain.ProjectKind, domain.NewID(), 0, "", "", map[string]string{"name": "accepted"})
	}
	if _, err := s.Mutate(actor, request, "create", nil, action); err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "revoke", nil, func(tx *Tx) (any, error) {
		r, err := tx.Get(domain.DeviceKind, id)
		if err != nil {
			return nil, err
		}
		v, err := Decode[domain.Device](r)
		if err != nil {
			return nil, err
		}
		v.Revoked = true
		return tx.Put(domain.DeviceKind, id, r.Revision, "", "", v)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []domain.ID{request, domain.NewID()} {
		_, err := s.Mutate(actor, req, "create", nil, action)
		assertCode(t, err, domain.Unauthenticated)
	}
	if calls != 1 {
		t.Fatal("revoked in-flight principal mutated state")
	}
}
