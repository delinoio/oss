package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func migrateRestoreImage(ctx context.Context, path, root string) error {
	if err := security.PrivateDir(filepath.Join(root, "backups")); err != nil {
		return storageError(err)
	}
	db, err := sql.Open("sqlite", databaseURI(path, false))
	if err != nil {
		return storageError(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL"); err != nil {
		return storageError(err)
	}
	return migrate(ctx, db, root)
}

// Only the private candidate is writable. The synchronized current snapshot is
// attached immutable/read-only; it supplies revocations and deletion obligations
// even when the selected image predates them. Every transformation is one SQL
// transaction. Historical source bytes and current live state remain untouched.
func prepareRestoreImage(ctx context.Context, path, safety string, receipt BackupRestore) error {
	db, err := sql.Open("sqlite", databaseURI(path, false))
	if err != nil {
		return storageError(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL; PRAGMA secure_delete=ON"); err != nil {
		return storageError(err)
	}
	if _, err := db.ExecContext(ctx, "ATTACH DATABASE ? AS current_state", databaseURI(safety, true)+"&immutable=1"); err != nil {
		return storageError(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	defer tx.Rollback()
	var conflicting bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM receipts a JOIN current_state.receipts b ON a.id=b.id WHERE a.digest<>b.digest)").Scan(&conflicting); err != nil {
		return storageError(err)
	}
	if conflicting {
		return backupUnavailable()
	}
	queries := []string{
		"INSERT OR REPLACE INTO tombstones SELECT * FROM current_state.tombstones",
		// Old grants and pairing codes never acquire fresh authority.
		"DELETE FROM credential_verifiers",
		"DELETE FROM pairing_verifiers",
		"DELETE FROM worker_instances",
		"DELETE FROM execution_grants",
		"DELETE FROM execution_references",
		// Claims in a database snapshot are historical evidence, never a
		// current Worker journal comparison or continuation grant.
		"DELETE FROM job_assignments",
		"DELETE FROM entities WHERE kind IN ('device','pairing')",
		"INSERT INTO entities SELECT * FROM current_state.entities WHERE kind='device'",
		"INSERT INTO credential_verifiers SELECT c.* FROM current_state.credential_verifiers c JOIN current_state.entities e ON e.id=c.device_id WHERE json_extract(e.body,'$.type')='client' AND json_extract(e.body,'$.revoked')=0",
		// Worker files are not restored. Their database association must be
		// explicitly paired again after startup, never implicitly reattached.
		"UPDATE entities SET body=json_set(body,'$.revoked',json('true')) WHERE kind='device' AND json_extract(body,'$.type')='worker'",
		"DELETE FROM entities WHERE id IN (SELECT id FROM tombstones) OR session_id IN (SELECT id FROM tombstones WHERE kind='session')",
		// Preserve original backup-removal jobs exactly for the independent
		// external obligation controller. These are the sole resumable jobs.
		"DELETE FROM backup_deletions",
		"DELETE FROM entities WHERE kind='job' AND json_extract(body,'$.type')='delete-backup'",
		"INSERT INTO entities SELECT e.* FROM current_state.entities e JOIN current_state.backup_deletions b ON b.job_id=e.id",
		"INSERT INTO jobs SELECT j.* FROM current_state.jobs j JOIN current_state.backup_deletions b ON b.job_id=j.id",
		"INSERT INTO backup_deletions SELECT * FROM current_state.backup_deletions",
		"INSERT OR REPLACE INTO deleted_project_policies SELECT * FROM current_state.deleted_project_policies",
		"INSERT OR IGNORE INTO model_suppressions SELECT s.* FROM current_state.model_suppressions s JOIN entities p ON p.id=s.provider_id",
		// Receipts keep their immutable digests so reconnect cannot accept an
		// old operation as new. Their historical content/grants are quarantined.
		"INSERT OR IGNORE INTO receipts SELECT * FROM current_state.receipts",
		"DELETE FROM receipt_entities",
		"UPDATE receipts SET result='" + quarantinedReceipt + "'",
		"UPDATE receipts SET result=(SELECT r.result FROM current_state.receipts r WHERE r.id=receipts.id) WHERE id IN (SELECT request_id FROM current_state.backup_deletions)",
		"INSERT OR REPLACE INTO metadata SELECT * FROM current_state.metadata WHERE key<>'event_floor'",
	}
	for _, query := range queries {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return storageError(err)
		}
	}
	// Retain closed domain models and historical claims. Changing execution
	// bookkeeping alone never proves native cleanup or allows Resume/recovery.
	rows, err := tx.QueryContext(ctx, "SELECT id,kind,body FROM entities WHERE kind IN ('session','job','schedule','account','integration','forward') ORDER BY id")
	if err != nil {
		return storageError(err)
	}
	type change struct {
		id   domain.ID
		body []byte
	}
	changes := []change{}
	var total int64
	for rows.Next() {
		var id domain.ID
		var kind domain.Kind
		var raw []byte
		if err := rows.Scan(&id, &kind, &raw); err != nil {
			rows.Close()
			return storageError(err)
		}
		// Bound aggregate retained documents as well as each domain document.
		total += int64(len(raw))
		if len(changes) >= 100000 || total > 256<<20 {
			rows.Close()
			return domain.Fail(domain.ResourceExhausted, "Restore transformation exceeds its document bound.", "Preserve both databases and arrange offline maintenance.")
		}
		var value any
		switch kind {
		case domain.SessionKind:
			var v domain.Session
			if err := domain.Decode(raw, &v); err != nil {
				rows.Close()
				return err
			}
			v.Dispatch, v.Recovery = domain.DispatchPaused, domain.NeedsRecovery
			v.Problem = restoreQuarantined().(*domain.Error)
			value = v
		case domain.JobKind:
			var v domain.Job
			if err := domain.Decode(raw, &v); err != nil {
				rows.Close()
				return err
			}
			if v.Type == domain.DeleteBackupJob {
				continue
			}
			if !v.State.Terminal() {
				v.State, v.Problem, v.FinishedAt = domain.JobCanceled, restoreQuarantined().(*domain.Error), &receipt.CreatedAt
			}
			value = v
		case domain.ScheduleKind:
			var v domain.Schedule
			if err := domain.Decode(raw, &v); err != nil {
				rows.Close()
				return err
			}
			if v.ConfigurationRevision >= 1<<63-1 {
				rows.Close()
				return restoreConflict()
			}
			v.Definition.Enabled, v.NextRunAt = false, nil
			v.ConfigurationRevision++
			v.Problem = restoreQuarantined().(*domain.Error)
			value = v
		case domain.AccountKind:
			var v domain.Account
			if err := domain.Decode(raw, &v); err != nil {
				rows.Close()
				return err
			}
			v.Health = domain.AccountDisconnected
			v.Connection, v.Removal, v.Validation, v.Catalog = nil, nil, nil, nil
			v.Quota, v.ConfirmedExhausted = nil, false
			value = v
		case domain.IntegrationKind:
			var v domain.Integration
			if err := domain.Decode(raw, &v); err != nil {
				rows.Close()
				return err
			}
			v.Connection, v.Pending = nil, nil
			value = v
		case domain.ForwardKind:
			var v domain.Forward
			if err := domain.Decode(raw, &v); err != nil {
				rows.Close()
				return err
			}
			// Historical claims never reopen sockets. Stop retains unknown
			// original cleanup rather than inventing proof from replacement.
			v.Stop()
			value = v
		}
		body, err := json.Marshal(value)
		if err != nil {
			rows.Close()
			return storageError(err)
		}
		changes = append(changes, change{id, body})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return storageError(err)
	}
	rows.Close()
	for _, v := range changes {
		if _, err := tx.ExecContext(ctx, "UPDATE entities SET body=? WHERE id=?", v.body, v.id); err != nil {
			return storageError(err)
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE jobs SET state=(SELECT json_extract(body,'$.state') FROM entities WHERE entities.id=jobs.id)"); err != nil {
		return storageError(err)
	}
	// A pre-restore resource revision cannot target replacement content. Keep
	// events beyond both timelines and expire old cursors to force resnapshot.
	var exhausted bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM entities WHERE revision>=9223372036854775806) OR EXISTS(SELECT 1 FROM current_state.entities WHERE revision>=9223372036854775806)").Scan(&exhausted); err != nil {
		return storageError(err)
	}
	if exhausted {
		return restoreConflict()
	}
	if _, err := tx.ExecContext(ctx, "UPDATE entities SET revision=MAX(revision,COALESCE((SELECT revision FROM current_state.entities c WHERE c.id=entities.id),0))+1,updated_at=? WHERE id NOT IN (SELECT job_id FROM backup_deletions)", receipt.CreatedAt.UnixMilli()); err != nil {
		return storageError(err)
	}
	var highwater uint64
	if err := tx.QueryRowContext(ctx, "SELECT MAX(COALESCE((SELECT MAX(sequence) FROM events),0),COALESCE((SELECT MAX(sequence) FROM current_state.events),0))+1").Scan(&highwater); err != nil {
		return storageError(err)
	}
	if highwater >= 1<<63-1 {
		return restoreConflict()
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM events"); err != nil {
		return storageError(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO events(sequence,id,entity_id,kind,session_id,revision,action,created_at) VALUES(?,?,?,'snapshot','',1,'updated',?)", highwater, domain.NewID(), receipt.RequestID, receipt.CreatedAt.UnixMilli()); err != nil {
		return storageError(err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE metadata SET value=? WHERE key='event_floor'", highwater); err != nil {
		return storageError(err)
	}
	digest, err := mutationDigest(receipt.RequestID, "backup.restore", receipt.Input)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO receipts(id,digest,result,created_at) VALUES(?,?,?,?)", receipt.RequestID, digest, quarantinedReceipt, receipt.CreatedAt.UnixMilli()); err != nil {
		return storageError(err)
	}
	return storageError(tx.Commit())
}
