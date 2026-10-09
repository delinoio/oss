// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Historical cleanup claims can survive the current-only reset boundary. Build
// one original retained claim explicitly; current restores never create these
// historical copies or derive cleanup authority from whatever bytes exist now.
func retainedRestoreCopyFixture(t *testing.T) (string, domain.ID, string) {
	t.Helper()
	s, root, ctx, input, _ := restoreFixture(t)
	request := domain.NewID()
	if _, _, err := s.RestoreBackup(ctx, request, input); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	receipt, err := readRestoreReceipt(root, request)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(restoreDirectory(root, request), "backups")
	if err := security.PrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	target := filepath.Join(dir, string(id)+".sqlite")
	source := filepath.Join(root, "backups", string(input.Backup.ID)+".sqlite")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(target, raw); err != nil {
		t.Fatal(err)
	}
	claim, err := fingerprintBackupPublication(ctx, target, id)
	if err != nil {
		t.Fatal(err)
	}
	receipt.MigrationImages = &restoreMigrationImages{Images: []backupPublication{claim}}
	if err := writeRestore(restoreJournal(root, request), receipt); err != nil {
		t.Fatal(err)
	}
	return root, request, target
}

func TestBackupRestorePreservesChangedRetainedImages(t *testing.T) {
	for _, change := range []string{"modified", "replaced", "unexpected", "legacy-unfingerprinted"} {
		t.Run(change, func(t *testing.T) {
			root, request, target := retainedRestoreCopyFixture(t)
			switch change {
			case "modified":
				db, err := openRestoreDatabase(target)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec("INSERT INTO metadata VALUES('fixture.changed','preserve original evidence')"); err != nil {
					t.Fatal(err)
				}
				if err = db.Close(); err != nil {
					t.Fatal(err)
				}
			case "replaced", "unexpected":
				raw, err := os.ReadFile(filepath.Join(root, "state.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				if change == "unexpected" {
					target = filepath.Join(filepath.Dir(target), string(domain.NewID())+".sqlite")
				}
				if err := security.WriteAtomic(target, raw); err != nil {
					t.Fatal(err)
				}
			case "legacy-unfingerprinted":
				receipt, err := readRestoreReceipt(root, request)
				if err != nil {
					t.Fatal(err)
				}
				receipt.MigrationImages = nil
				if err := writeRestore(restoreJournal(root, request), receipt); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := cleanupSettledRestoreImages(context.Background(), root, map[domain.ID]bool{request: true}); err == nil {
					t.Fatal("changed or unowned copy retired")
				}
				after, err := os.ReadFile(target)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("recovery evidence changed", err)
				}
			}
		})
	}
}

func TestBackupRestoreRetainedCleanupResumesInterruptedUnlink(t *testing.T) {
	root, request, target := retainedRestoreCopyFixture(t)
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := cleanupSettledRestoreImages(context.Background(), root, map[domain.ID]bool{request: true}); err != nil {
			t.Fatal("original cleanup did not resume", err)
		}
	}
	if _, err := os.Stat(filepath.Dir(target)); !os.IsNotExist(err) {
		t.Fatal("retained image directory survived", err)
	}
}
