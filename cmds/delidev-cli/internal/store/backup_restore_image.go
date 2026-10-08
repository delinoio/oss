// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"time"

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
	return migrate(context.WithValue(ctx, historicalSubscriptionRetirement{}, true), db, root)
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
	// Only a current, still-connected original OAuth result retains vault
	// authority across restore. Neither an older account image nor a disconnected
	// current descriptor can recover an old credential generation.
	const connectedOAuthAccounts = `SELECT e.id FROM current_state.entities e JOIN current_state.account_oauth_attempts a ON e.id=json_extract(a.body,'$.account_id') WHERE e.kind='account' AND a.state='connected' AND json_type(e.body,'$.connection')='object' AND COALESCE(json_extract(e.body,'$.connection.credential_id'),json_extract(e.body,'$.connection.id'))=json_extract(a.body,'$.connect_request_id')`
	queries := []string{
		// Current image ownership and completed removals cannot roll back.
		// Historical image bytes are never part of a database backup.
		"DELETE FROM entities WHERE kind='job' AND json_extract(body,'$.type')='image-attachment'",
		"INSERT INTO entities SELECT * FROM current_state.entities WHERE kind='job' AND json_extract(body,'$.type')='image-attachment'",
		"DELETE FROM jobs WHERE id NOT IN (SELECT id FROM entities WHERE kind='job')",
		"INSERT OR REPLACE INTO jobs SELECT j.* FROM current_state.jobs j JOIN current_state.entities e ON e.id=j.id WHERE json_extract(e.body,'$.type')='image-attachment'",
		// A historical image cannot replace current once-only OAuth dispatch or
		// cleanup evidence. Eligibility already excludes every unresolved attempt.
		// Installation state is current once-only authority, never historical configuration.
		"DELETE FROM entities WHERE kind IN ('ssh_setup','update')",
		"INSERT INTO entities SELECT * FROM current_state.entities WHERE kind IN ('ssh_setup','update')",
		"DELETE FROM account_oauth_credentials",
		"INSERT INTO account_oauth_credentials SELECT * FROM current_state.account_oauth_credentials",
		"DELETE FROM account_oauth_attempts",
		"INSERT INTO account_oauth_attempts SELECT * FROM current_state.account_oauth_attempts",
		// Preserve the coupled account/provider from the current safety image;
		// it was explicitly connected after the historical backup was taken.
		"DELETE FROM entities WHERE id IN (" + connectedOAuthAccounts + ")",
		"INSERT INTO entities SELECT * FROM current_state.entities WHERE id IN (" + connectedOAuthAccounts + ")",
		"DELETE FROM entities WHERE id IN (SELECT json_extract(body,'$.provider_id') FROM current_state.entities WHERE id IN (" + connectedOAuthAccounts + "))",
		"INSERT INTO entities SELECT * FROM current_state.entities WHERE kind='provider' AND id IN (SELECT json_extract(body,'$.provider_id') FROM current_state.entities WHERE id IN (" + connectedOAuthAccounts + "))",
		"INSERT OR REPLACE INTO tombstones SELECT * FROM current_state.tombstones",
		"INSERT OR REPLACE INTO retired_configurations SELECT * FROM current_state.retired_configurations",
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
		// Vault generations and immutable routing are current explicit authority,
		// not historical configuration that a database rollback may reactivate.
		"DELETE FROM entities WHERE kind IN ('network_profile','network_route')",
		"INSERT INTO entities SELECT * FROM current_state.entities WHERE kind IN ('network_profile','network_route')",
		// Current Worker routes require their current machine descriptors for
		// owner administration. Metadata grants no Worker credential/lease;
		// those remain cleared and the copied devices are revoked below.
		"DELETE FROM entities WHERE kind='machine' AND id IN (SELECT json_extract(CAST(body AS TEXT),'$.machine_id') FROM current_state.entities WHERE kind='network_route')",
		"INSERT INTO entities SELECT * FROM current_state.entities WHERE kind='machine' AND id IN (SELECT json_extract(CAST(body AS TEXT),'$.machine_id') FROM current_state.entities WHERE kind='network_route')",
		// Worker files are not restored. Their database association must be
		// explicitly paired again after startup, never implicitly reattached.
		"UPDATE entities SET body=json_set(body,'$.revoked',json('true')) WHERE kind='device' AND json_extract(body,'$.type')='worker'",
		"DELETE FROM entities WHERE id IN (SELECT id FROM tombstones) OR session_id IN (SELECT id FROM tombstones WHERE kind='session')",
		"DELETE FROM entities WHERE kind='project_prompt_history' AND project_id NOT IN (SELECT id FROM entities WHERE kind='project')",
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
		if query == "DELETE FROM entities WHERE id IN (SELECT id FROM tombstones) OR session_id IN (SELECT id FROM tombstones WHERE kind='session')" {
			if err := redactRestoredDeletedSessions(ctx, tx, receipt.CreatedAt); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return storageError(err)
		}
	}
	retainedOAuth := map[domain.ID]bool{}
	oauthRows, err := tx.QueryContext(ctx, connectedOAuthAccounts)
	if err != nil {
		return storageError(err)
	}
	for oauthRows.Next() {
		var id domain.ID
		if err := oauthRows.Scan(&id); err != nil {
			oauthRows.Close()
			return storageError(err)
		}
		retainedOAuth[id] = true
		if len(retainedOAuth) > 100000 {
			oauthRows.Close()
			return backupUnavailable()
		}
	}
	if err := oauthRows.Err(); err != nil {
		oauthRows.Close()
		return storageError(err)
	}
	oauthRows.Close()
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
			if v.Type == domain.ImageAttachmentJob {
				var upload domain.ImageUpload
				if domain.Decode(v.Input, &upload) != nil || validateImageUpload(upload) != nil {
					rows.Close()
					return domain.InvalidImageInput()
				}
				upload.Quarantined = true
				v.Input, err = json.Marshal(upload)
				if err != nil {
					rows.Close()
					return storageError(err)
				}
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
			if retainedOAuth[id] {
				continue
			}
			var v domain.Account
			if err := domain.Decode(raw, &v); err != nil {
				rows.Close()
				return err
			}
			v.Health = domain.AccountDisconnected
			v.Connection, v.Removal, v.Validation, v.Catalog = nil, nil, nil, nil
			v.RetainedConnections = nil
			v.Quota, v.ConfirmedExhausted = nil, false
			if v.Subscription != nil {
				state := v.Subscription
				if state.ServerQuotaActive() || state.NativeProfileID != "" || state.OwnerMachineID != "" || state.Generation != "" || state.IdentityCommitment != "" || state.Pending != nil || state.Lease != nil || state.RecoveryRequired {
					// The vault is outside this image. Retain historical references
					// without authorizing an older bundle or native claim.
					state.RecoveryRequired = true
				} else {
					// Settled logout and failed login can leave an empty object.
					// It owns no external reference requiring recovery.
					v.Subscription = nil
				}
			}
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

// Shared remediation records can retain session operands without row-level
// session ownership. Apply the permanent deletion redactor before deleting
// tombstoned rows, while its original source/index graph is still available.
func redactRestoredDeletedSessions(ctx context.Context, tx *sql.Tx, now time.Time) error {
	rows, err := tx.QueryContext(ctx, "SELECT id FROM tombstones WHERE kind='session' ORDER BY id LIMIT 100001")
	if err != nil {
		return storageError(err)
	}
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return storageError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return storageError(err)
	}
	if len(ids) > 100000 {
		return domain.SessionDeletionPending()
	}
	t := &Tx{tx: tx, ctx: ctx, now: now, touched: map[domain.ID]bool{}}
	for _, id := range ids {
		if err := t.redactSessionRemediation(id); err != nil {
			return err
		}
	}
	return nil
}
