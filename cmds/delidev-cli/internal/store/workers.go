package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (t *Tx) writeAllowed() error {
	if t.readOnly {
		return domain.Fail(domain.PermissionDenied, "Read transactions cannot mutate state.", "Use a product mutation.")
	}
	return nil
}
func (t *Tx) Authorize() error {
	actor, ok := domain.PrincipalFrom(t.ctx)
	if !ok || actor.Type == domain.OwnerDevice {
		return nil
	}
	record, err := t.Get(domain.DeviceKind, actor.DeviceID)
	if err != nil {
		return domain.Fail(domain.Unauthenticated, "The device is no longer authorized.", "Pair this device again with a new code.")
	}
	device, err := Decode[domain.Device](record)
	if err != nil {
		return err
	}
	if device.Revoked || device.Type != actor.Type || device.MachineID != actor.MachineID {
		return domain.Fail(domain.Unauthenticated, "The device authorization was revoked.", "Pair this device again with a new code.")
	}
	return nil
}
func (t *Tx) PutPairingVerifier(id domain.ID, digest []byte) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if len(digest) != 32 {
		return domain.Fail(domain.InvalidArgument, "Invalid pairing verifier.", "Use a SHA-256 verifier of a fresh random code.")
	}
	_, err := t.tx.ExecContext(t.ctx, "INSERT INTO pairing_verifiers(pairing_id,digest) VALUES(?,?)", id, digest)
	return storageError(err)
}
func (t *Tx) PairingVerifier(id domain.ID) ([]byte, error) {
	var digest []byte
	err := t.tx.QueryRowContext(t.ctx, "SELECT digest FROM pairing_verifiers WHERE pairing_id=?", id).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.Fail(domain.Unauthenticated, "The pairing grant is unavailable or already used.", "Request a fresh pairing code.")
	}
	return digest, storageError(err)
}
func (t *Tx) ConsumePairing(id domain.ID) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx, "DELETE FROM pairing_verifiers WHERE pairing_id=?", id)
	return storageError(err)
}
func (t *Tx) PutCredential(id domain.ID, digest []byte) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if len(digest) != 32 {
		return domain.Fail(domain.InvalidArgument, "Invalid device credential verifier.", "Use a SHA-256 verifier of a fresh device credential.")
	}
	_, err := t.tx.ExecContext(t.ctx, "INSERT INTO credential_verifiers(device_id,digest) VALUES(?,?)", id, digest)
	return storageError(err)
}
func (t *Tx) RevokeCredential(id domain.ID) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx, "DELETE FROM credential_verifiers WHERE device_id=?", id)
	return storageError(err)
}
func (s *Store) Authenticate(ctx context.Context, digest []byte) (domain.Principal, error) {
	var actor domain.Principal
	err := s.Read(ctx, func(tx *Tx) error {
		var id domain.ID
		if err := tx.tx.QueryRowContext(ctx, "SELECT device_id FROM credential_verifiers WHERE digest=?", digest).Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.Fail(domain.Unauthenticated, "The device credential is invalid or revoked.", "Pair this device again with a new code.")
			}
			return storageError(err)
		}
		record, err := tx.Get(domain.DeviceKind, id)
		if err != nil {
			return err
		}
		device, err := Decode[domain.Device](record)
		if err != nil {
			return err
		}
		if device.Revoked {
			return domain.Fail(domain.Unauthenticated, "The device credential was revoked.", "Pair this device again with a new code.")
		}
		actor = domain.Principal{Type: device.Type, DeviceID: id, MachineID: device.MachineID}
		return nil
	})
	return actor, err
}
func (t *Tx) WorkerInstance(machine domain.ID) (domain.ID, time.Time, error) {
	var instance domain.ID
	var seen int64
	err := t.tx.QueryRowContext(t.ctx, "SELECT instance_id,last_seen FROM worker_instances WHERE machine_id=?", machine).Scan(&instance, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, nil
	}
	return instance, time.UnixMilli(seen).UTC(), storageError(err)
}
func (t *Tx) SetWorkerInstance(machine, instance domain.ID, seen time.Time) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if err := machine.Validate(); err != nil {
		return err
	}
	if err := instance.Validate(); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx, "INSERT INTO worker_instances(machine_id,instance_id,last_seen) VALUES(?,?,?) ON CONFLICT(machine_id) DO UPDATE SET instance_id=excluded.instance_id,last_seen=excluded.last_seen", machine, instance, seen.UnixMilli())
	return storageError(err)
}
func (s *Store) Heartbeat(ctx context.Context, machine, instance domain.ID) error {
	// Connection liveness is metadata, not an accepted product mutation. Keep it
	// out of the event/receipt stream while still checking device revocation.
	s.gate.RLock()
	defer s.gate.RUnlock()
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	defer sqlTx.Rollback()
	tx := &Tx{tx: sqlTx, ctx: ctx}
	if err := tx.Authorize(); err != nil {
		return err
	}
	result, err := sqlTx.ExecContext(ctx, "UPDATE worker_instances SET last_seen=? WHERE machine_id=? AND instance_id=?", time.Now().UTC().UnixMilli(), machine, instance)
	if err != nil {
		return storageError(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return storageError(err)
	}
	if count != 1 {
		return domain.Fail(domain.Conflict, "The Worker instance no longer owns this connection.", "Reattach with the current process identity.")
	}
	return storageError(sqlTx.Commit())
}
func (t *Tx) PutJob(id domain.ID, expected uint64, session, project domain.ID, job domain.Job) (Record, error) {
	if err := job.Validate(); err != nil {
		return Record{}, err
	}
	if expected > 0 {
		existing, err := t.Get(domain.JobKind, id)
		if err != nil {
			return Record{}, err
		}
		original, err := Decode[domain.Job](existing)
		if err != nil {
			return Record{}, err
		}
		if original.Type != job.Type || original.MachineID != job.MachineID || original.ParentID != job.ParentID || !original.AcceptedAt.Equal(job.AcceptedAt) {
			return Record{}, domain.Fail(domain.InvalidArgument, "Accepted job routing is immutable.", "Create a new explicit operation instead of reassigning accepted work.")
		}
	}
	r, err := t.Put(domain.JobKind, id, expected, session, project, job)
	if err != nil {
		return Record{}, err
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO jobs(id,machine_id,parent_id,state,accepted_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state", id, job.MachineID, job.ParentID, job.State, job.AcceptedAt.UnixMilli())
	if err != nil {
		return Record{}, storageError(err)
	}
	if job.State == domain.JobClaimed {
		if err := t.rememberAssignment(r); err != nil {
			return Record{}, err
		}
	}
	return r, nil
}
func (t *Tx) Jobs(machine, parent domain.ID, state domain.JobState, after domain.ID, limit int) ([]Record, error) {
	if limit < 1 || limit > MaxPage {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid job page bound.", "Use a bounded page.")
	}
	query := "SELECT e.id,e.kind,e.revision,e.session_id,e.project_id,e.body,e.created_at,e.updated_at FROM jobs j JOIN entities e ON e.id=j.id WHERE j.id>?"
	args := []any{after}
	if machine != "" {
		query += " AND j.machine_id=?"
		args = append(args, machine)
	}
	if parent != "" {
		query += " AND j.parent_id=?"
		args = append(args, parent)
	}
	if state != "" {
		query += " AND j.state=?"
		args = append(args, state)
	}
	// UUID-v7 IDs carry acceptance order and remain stable across reconnects.
	query += " ORDER BY j.id LIMIT ?"
	args = append(args, limit)
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	records := []Record{}
	for rows.Next() {
		record, err := scan(rows)
		if err != nil {
			return nil, storageError(err)
		}
		records = append(records, record)
	}
	return records, storageError(rows.Err())
}
