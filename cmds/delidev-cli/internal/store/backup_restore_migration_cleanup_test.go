// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestBackupRestorePreservesChangedMigrationImages(t *testing.T) {
	for _, checkpoint := range []string{"prepared", "recorded"} {
		for _, change := range []string{"modified", "replaced", "unexpected", "legacy-unfingerprinted"} {
			t.Run(checkpoint+"/"+change, func(t *testing.T) {
				s, root, ctx, input, _ := restoreFixture(t)
				source := filepath.Join(root, "backups", string(input.Backup.ID)+".sqlite")
				image, err := openRestoreDatabase(source)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := historicalSchema(image, "023-titles"); err != nil {
					t.Fatal(err)
				}
				if err := image.Close(); err != nil {
					t.Fatal(err)
				}
				inspection, err := s.InspectBackup(ctx, input.Backup.ID, input.ServerID)
				if err != nil {
					t.Fatal(err)
				}
				input.Backup, input.SHA256 = inspection.Backup, inspection.SHA256
				request := domain.NewID()
				injected := errors.New("fixture interrupts restore before startup cleanup")
				_, _, err = s.restoreBackup(ctx, request, input, func(stage string) error {
					if stage == checkpoint {
						return injected
					}
					return nil
				})
				if !errors.Is(err, injected) {
					t.Fatal("restore did not reach selected checkpoint", err)
				}
				migration := filepath.Join(restoreDirectory(root, request), "backups")
				entries, err := os.ReadDir(migration)
				if err != nil || len(entries) != 1 {
					t.Fatal("fixture has no original migration image", entries, err)
				}
				target := filepath.Join(migration, entries[0].Name())
				switch change {
				case "modified":
					image, err := openRestoreDatabase(target)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := image.Exec("INSERT INTO metadata(key,value) VALUES('fixture.changed','preserve this evidence')"); err != nil {
						t.Fatal(err)
					}
					if err := image.Close(); err != nil {
						t.Fatal(err)
					}
				case "replaced", "unexpected":
					// A valid database with this server's identity is still another image.
					replacement, err := os.ReadFile(filepath.Join(root, "state.sqlite"))
					if err != nil {
						t.Fatal(err)
					}
					if change == "unexpected" {
						target = filepath.Join(migration, string(domain.NewID())+".sqlite")
					}
					if err := security.WriteAtomic(target, replacement); err != nil {
						t.Fatal(err)
					}
				case "legacy-unfingerprinted":
					// A pre-fingerprint v1 receipt cannot grant ownership of existing copies.
					for _, path := range []string{restoreJournal(root, request), filepath.Join(restoreRoot(root), "active.json")} {
						raw, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						var journal map[string]json.RawMessage
						if err := json.Unmarshal(raw, &journal); err != nil {
							t.Fatal(err)
						}
						delete(journal, "migration_images")
						raw, err = json.Marshal(journal)
						if err != nil {
							t.Fatal(err)
						}
						if err := security.WriteAtomic(path, raw); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err := validateBackup(ctx, target, &input.ServerID); err != nil {
					t.Fatal("replacement must remain valid for the same server", err)
				}
				retained, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := Open(ctx, root)
				if err == nil {
					reopened.Close()
					t.Fatal("startup removed changed or unowned migration evidence")
				}
				if domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("unexpected recovery result", err)
				}
				after, err := os.ReadFile(target)
				if err != nil || !bytes.Equal(retained, after) {
					t.Fatal("startup changed retained migration evidence", err)
				}
				// Cleanup refusal and restart retries cannot adopt the changed image.
				reopened, err = Open(ctx, root)
				if err == nil {
					reopened.Close()
					t.Fatal("retry adopted changed migration evidence")
				}
				after, err = os.ReadFile(target)
				if err != nil || !bytes.Equal(retained, after) {
					t.Fatal("retry changed retained migration evidence", err)
				}
			})
		}
	}
}

func TestBackupRestoreMigrationCleanupResumesInterruptedUnlink(t *testing.T) {
	for _, checkpoint := range []string{"prepared", "recorded"} {
		t.Run(checkpoint, func(t *testing.T) {
			s, root, ctx, input, _ := restoreFixture(t)
			source := filepath.Join(root, "backups", string(input.Backup.ID)+".sqlite")
			image, err := openRestoreDatabase(source)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := historicalSchema(image, "023-titles"); err != nil {
				t.Fatal(err)
			}
			if err := image.Close(); err != nil {
				t.Fatal(err)
			}
			inspection, err := s.InspectBackup(ctx, input.Backup.ID, input.ServerID)
			if err != nil {
				t.Fatal(err)
			}
			input.Backup, input.SHA256 = inspection.Backup, inspection.SHA256
			request := domain.NewID()
			injected := errors.New("fixture stops before migration cleanup")
			_, _, err = s.restoreBackup(ctx, request, input, func(stage string) error {
				if stage == checkpoint {
					return injected
				}
				return nil
			})
			if !errors.Is(err, injected) {
				t.Fatal(err)
			}
			journal, err := readRestoreReceipt(root, request)
			if err != nil || journal.MigrationImages == nil || len(journal.MigrationImages.Images) != 1 {
				t.Fatal("original copy has no pinned ownership", err)
			}
			claim := journal.MigrationImages.Images[0]
			path := filepath.Join(restoreDirectory(root, request), "backups", string(claim.Backup.ID)+".sqlite")
			actual, err := fingerprintBackupPublication(ctx, path, claim.Backup.ID)
			if err != nil || actual != claim {
				t.Fatal("journal does not pin the original migration image", err)
			}
			// Emulate a crash after one authorized unlink and before directory fsync.
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(ctx, root)
			if err != nil {
				t.Fatal("interrupted cleanup did not finish", err)
			}
			defer reopened.Close()
			if err := reopened.checkRestoreImagesRetired(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
