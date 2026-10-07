// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// The bounded pending projection excludes terminal history, so old completed
// setups cannot starve a newer operation. It carries no protected content.
func (s *Store) PendingSSHInstallations(ctx context.Context) ([]Record, error) {
	var result []Record
	err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		rows, err := tx.tx.QueryContext(ctx, "SELECT id,kind,revision,session_id,project_id,body,created_at,updated_at FROM entities WHERE kind=? AND (json_extract(CAST(body AS TEXT),'$.state') IN ('REQUESTED','RUNNING') OR json_extract(CAST(body AS TEXT),'$.reconcile_requested')=1 OR (json_extract(CAST(body AS TEXT),'$.cancellation_requested')=1 AND json_extract(CAST(body AS TEXT),'$.credential_removed')=0 AND json_extract(CAST(body AS TEXT),'$.state') IN ('CANCELED','SUCCEEDED','FAILED'))) ORDER BY id LIMIT 16", domain.SSHSetupKind)
		if err != nil {
			return storageError(err)
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scan(rows)
			if err != nil {
				return err
			}
			result = append(result, r)
		}
		return storageError(rows.Err())
	})
	return result, err
}

// WorkerUpdateAdmission fences new native ownership while allowing all original
// cleanup/report lanes to finish. It is checked in the same transaction as claim.
func (t *Tx) WorkerUpdateAdmission(machine domain.ID) error {
	var count int
	err := t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM entities WHERE kind=? AND json_extract(CAST(body AS TEXT),'$.machine_id')=? AND json_extract(CAST(body AS TEXT),'$.state') IN ('WAITING_FOR_IDLE','RUNNING','UNCERTAIN')", domain.UpdateKind, machine).Scan(&count)
	if err != nil {
		return storageError(err)
	}
	if count != 0 {
		return domain.Fail(domain.Conflict, "The Worker is draining for its original update.", "Wait for active work and cleanup, or cancel an unclaimed update before starting new work.")
	}
	return nil
}
func (t *Tx) WorkerUpdateIdle(machine domain.ID) (bool, error) {
	var count int
	err := t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM entities WHERE
 (kind='job' AND json_extract(CAST(body AS TEXT),'$.machine_id')=? AND json_extract(CAST(body AS TEXT),'$.state') IN ('claimed','uncertain')) OR
 (kind='terminal' AND json_extract(CAST(body AS TEXT),'$.machine_id')=? AND COALESCE(json_extract(CAST(body AS TEXT),'$.cleanup_verified'),0)<>1) OR
 (kind='forward' AND json_extract(CAST(body AS TEXT),'$.machine_id')=? AND (json_extract(CAST(body AS TEXT),'$.state')<>'stopped' OR COALESCE(json_extract(CAST(body AS TEXT),'$.client_clean'),0)<>1 OR COALESCE(json_extract(CAST(body AS TEXT),'$.worker_clean'),0)<>1)) OR
 (kind='account' AND json_extract(CAST(body AS TEXT),'$.subscription.lease.machine_id')=?)`, machine, machine, machine, machine).Scan(&count)
	if err != nil {
		return false, storageError(err)
	}
	if count != 0 {
		return false, nil
	}
	// Session deletion has an independent external journal. Every original native
	// obligation must finish too, even when its ordinary database job was purged.
	return true, nil
}

// The caller holds the Store transaction gate; deletion acceptance takes its
// exclusive side, so this inventory cannot change across the idle claim.
func (s *Store) WorkerDeletionIdle(tx *Tx, machine domain.ID) (bool, error) {
	plans, err := s.sessionDeletionInventory(tx.ctx)
	if err != nil {
		return false, err
	}
	for _, p := range plans {
		for _, w := range p.Workers {
			if w.Work.MachineID == machine && !w.Acknowledged {
				return false, nil
			}
		}
		for _, child := range p.Dependents {
			for _, w := range child.Workers {
				if w.Work.MachineID == machine && !w.Acknowledged {
					return false, nil
				}
			}
		}
	}
	return true, nil
}

// Installation projections select matching live scope before applying bounds;
// unrelated completed history never hides a pending update or signed admission.
func (t *Tx) InstallationUpdates(machine, device domain.ID, version string, pending bool) ([]Record, error) {
	query := "SELECT id,kind,revision,session_id,project_id,body,created_at,updated_at FROM entities WHERE kind=? AND json_extract(CAST(body AS TEXT),'$.machine_id')=?"
	args := []any{domain.UpdateKind, machine}
	if device != "" {
		query += " AND json_extract(CAST(body AS TEXT),'$.device_id')=?"
		args = append(args, device)
	}
	if pending {
		query += " AND json_extract(CAST(body AS TEXT),'$.state') IN ('WAITING_FOR_IDLE','RUNNING','UNCERTAIN')"
	} else {
		query += " AND json_extract(CAST(body AS TEXT),'$.state') IN ('RUNNING','UNCERTAIN','SUCCEEDED') AND (json_extract(CAST(body AS TEXT),'$.version')=? OR json_extract(CAST(body AS TEXT),'$.current_version')=?)"
		args = append(args, version, version)
	}
	query += " ORDER BY id DESC LIMIT 32"
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	var result []Record
	for rows.Next() {
		r, e := scan(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, r)
	}
	return result, storageError(rows.Err())
}
func (t *Tx) InstallationWorkerDevice(machine domain.ID) (domain.ID, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT id FROM entities WHERE kind=? AND json_extract(CAST(body AS TEXT),'$.type')='worker' AND json_extract(CAST(body AS TEXT),'$.machine_id')=? AND COALESCE(json_extract(CAST(body AS TEXT),'$.revoked'),0)=0 LIMIT 2", domain.DeviceKind, machine)
	if err != nil {
		return "", storageError(err)
	}
	defer rows.Close()
	var id domain.ID
	for rows.Next() {
		if id != "" {
			return "", domain.Fail(domain.Conflict, "Multiple Worker registrations require inspection.", "Retain their original identity and inspect the Runner Device.")
		}
		if err = rows.Scan(&id); err != nil {
			return "", storageError(err)
		}
	}
	if err = rows.Err(); err != nil {
		return "", storageError(err)
	}
	if id == "" {
		return "", domain.Fail(domain.Unavailable, "The original Worker registration is unavailable.", "Verify its existing registration before checking for updates.")
	}
	return id, nil
}

func (t *Tx) WorkerUpdateAttemptExists(machine domain.ID, version string) (bool, error) {
	var count int
	err := t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM entities WHERE kind=? AND json_extract(CAST(body AS TEXT),'$.machine_id')=? AND json_extract(CAST(body AS TEXT),'$.version')=?", domain.UpdateKind, machine, version).Scan(&count)
	return count > 0, storageError(err)
}
