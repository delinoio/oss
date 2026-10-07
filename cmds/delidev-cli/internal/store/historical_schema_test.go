// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"embed"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Frozen historical layouts are rejection fixtures, never an upgrade source.
//
//go:embed testdata/schema/*.sql
var historicalSchemas embed.FS

func TestHistoricalLayoutsAreRejectedWithoutChanges(t *testing.T) {
	entries, err := historicalSchemas.ReadDir("testdata/schema")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "state")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "state.sqlite")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", databaseURI(path, false))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := historicalSchemas.ReadFile("testdata/schema/" + entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(string(raw)); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			before := snapshotDatabaseFiles(t, path)
			s, err := Open(context.Background(), root)
			if s != nil {
				s.Close()
				t.Fatal("historical layout opened")
			}
			assertCode(t, err, domain.RecoveryRequired)
			requireDatabaseFilesUnchanged(t, path, before)
		})
	}
}
