// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func snapshotDatabaseFiles(t *testing.T, path string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		raw, err := os.ReadFile(path + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		files[suffix] = raw
	}
	return files
}

func requireDatabaseFilesUnchanged(t *testing.T, path string, before map[string][]byte) {
	t.Helper()
	after := snapshotDatabaseFiles(t, path)
	if len(after) != len(before) {
		t.Fatal("database sidecar inventory changed")
	}
	for suffix, raw := range before {
		if !bytes.Equal(raw, after[suffix]) {
			t.Fatalf("database artifact %q changed", suffix)
		}
	}
}

func TestUnsupportedDatabaseSchemasPreserveOriginalBytes(t *testing.T) {
	versions := []int{0, 33, 999}
	for version := 1; version < SchemaVersion; version++ {
		versions = append(versions, version)
	}
	for _, version := range versions {
		for _, wal := range []bool{false, true} {
			t.Run(fmt.Sprintf("schema-%d/wal-%t", version, wal), func(t *testing.T) {
				s, root := openTest(t)
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, "state.sqlite")
				db, err := sql.Open("sqlite", databaseURI(path, false))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if wal {
					if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0"); err != nil {
						t.Fatal(err)
					}
				}
				if _, err = db.Exec(fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
					t.Fatal(err)
				}
				if !wal {
					if err = db.Close(); err != nil {
						t.Fatal(err)
					}
				}
				before := snapshotDatabaseFiles(t, path)
				reopened, err := Open(context.Background(), root)
				if reopened != nil {
					reopened.Close()
					t.Fatal("unsupported database opened")
				}
				assertCode(t, err, domain.RecoveryRequired)
				requireDatabaseFilesUnchanged(t, path, before)
			})
		}
	}
}

func TestUnsupportedBackupSchemaPreservesImageAndSidecars(t *testing.T) {
	for _, version := range []int{1, 24, 28, 31, 33} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			s, root, ctx, input, _ := restoreFixture(t)
			path := filepath.Join(root, "backups", string(input.Backup.ID)+".sqlite")
			db, err := sql.Open("sqlite", databaseURI(path, false))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			before := snapshotDatabaseFiles(t, path)
			_, err = s.InspectBackup(ctx, input.Backup.ID, input.ServerID)
			assertCode(t, err, domain.RecoveryRequired)
			_, _, err = s.RestoreBackup(ctx, domain.NewID(), input)
			if err == nil {
				t.Fatal("unsupported backup restored")
			}
			requireDatabaseFilesUnchanged(t, path, before)
			for _, suffix := range []string{"-wal", "-shm", "-journal"} {
				if err := os.WriteFile(path+suffix, []byte("preserve unsupported backup evidence"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before = snapshotDatabaseFiles(t, path)
			_, err = s.InspectBackup(ctx, input.Backup.ID, input.ServerID)
			assertCode(t, err, domain.RecoveryRequired)
			requireDatabaseFilesUnchanged(t, path, before)
		})
	}
}
