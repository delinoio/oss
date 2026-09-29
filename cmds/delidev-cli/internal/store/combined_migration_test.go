package store

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestMigrationPreservesBothVersion23Layouts(t *testing.T) {
	for _, backupLayout := range []bool{true, false} {
		name := "titles"
		if backupLayout {
			name = "backups"
		}
		t.Run(name, func(t *testing.T) {
			s, root, ctx, deletion := deletionFixture(t)
			f := seedSearch(t, s, "retained usage", domain.Archived)
			usage := responseRecord(f)
			if !backupLayout {
				usage.Purpose = domain.SessionTitleUsage
			}
			usageID := domain.NewID()
			if _, _, err := writeResponse(s, usageID, usage); err != nil {
				t.Fatal(err)
			}
			var backupJob Record
			titleJob := domain.NewID()
			if backupLayout {
				var err error
				backupJob, _, err = s.DeleteBackup(ctx, domain.NewID(), deletion)
				if err != nil {
					t.Fatal(err)
				}
				// Reconstruct the backup-only schema 23 before automatic titles existed.
				if _, err := s.db.Exec(`DROP TABLE session_title_send_claims; DROP TABLE session_title_http_claims; DROP INDEX response_usage_purpose_time; ALTER TABLE response_usage DROP COLUMN purpose;`); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.Mutate(ctx, domain.NewID(), "fixture.title-claim", nil, func(tx *Tx) (any, error) {
					if _, err := tx.Put(domain.JobKind, titleJob, 0, f.project, f.session, domain.Job{Type: domain.GenerateSessionTitleJob, State: domain.JobClaimed, MachineID: domain.NewID(), ParentID: f.session, Input: json.RawMessage(`{}`), AcceptedAt: tx.now}); err != nil {
						return nil, err
					}
					if _, err := tx.ClaimTitleInference(titleJob); err != nil {
						return nil, err
					}
					return tx.ClaimTitleHTTPRequest(titleJob)
				}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec("DROP TABLE backup_deletions"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec("DELETE FROM entities WHERE kind='provider' AND json_extract(body,'$.preset_id')='anthropic'; DROP TABLE IF EXISTS native_accounting; PRAGMA user_version=23;"); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			migrated, err := Open(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			defer migrated.Close()
			var version, tableCount, providers int
			if err := migrated.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
				t.Fatal(version, err)
			}
			if err := migrated.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('backup_deletions','session_title_send_claims','session_title_http_claims')").Scan(&tableCount); err != nil || tableCount != 3 {
				t.Fatal(tableCount, err)
			}
			retained, err := migrated.ResponseUsage(ctx, usageID)
			if err != nil || retained.Record.Usage.ResponseDigest != usage.Usage.ResponseDigest || retained.Record.Purpose != usage.Purpose {
				t.Fatal("migration changed usage attribution", retained, err)
			}
			if err := migrated.db.QueryRow("SELECT count(*) FROM entities WHERE kind='provider' AND json_extract(body,'$.preset_id')='anthropic'").Scan(&providers); err != nil || providers != 0 {
				t.Fatal("migration recreated a deleted provider", providers, err)
			}
			if backupLayout {
				row, err := migrated.RunBackupDeletion(ctx, backupJob.ID, deletion.ServerID)
				job, decodeErr := Decode[domain.Job](row)
				if err != nil || decodeErr != nil || job.State != domain.JobSucceeded {
					t.Fatal("migration lost deletion job", job, err, decodeErr)
				}
			} else {
				if _, err := migrated.Mutate(ctx, domain.NewID(), "fixture.title-reclaim", nil, func(tx *Tx) (any, error) {
					fresh, err := tx.ClaimTitleInference(titleJob)
					if err != nil || fresh {
						t.Fatal("migration lost inference claim", fresh, err)
					}
					fresh, err = tx.ClaimTitleHTTPRequest(titleJob)
					if err != nil || fresh {
						t.Fatal("migration lost HTTP claim", fresh, err)
					}
					return nil, nil
				}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
