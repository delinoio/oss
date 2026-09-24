package store

import (
	"bytes"
	"database/sql"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const executionSchema = `
CREATE TABLE execution_grants (
 job_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 digest BLOB NOT NULL UNIQUE CHECK(length(digest)=32),
 execution_id TEXT NOT NULL, machine_id TEXT NOT NULL,
 instance_id TEXT NOT NULL, device_id TEXT NOT NULL, server_epoch TEXT NOT NULL
);
CREATE TABLE execution_references (
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 account_id TEXT NOT NULL, connection_id TEXT NOT NULL, model_id TEXT NOT NULL,
 reference_kind TEXT NOT NULL, native_id TEXT NOT NULL,
 PRIMARY KEY(session_id,account_id,connection_id,model_id,reference_kind,native_id)
);
PRAGMA user_version=7;
`

type ExecutionGrant struct {
	JobID       domain.ID
	Digest      []byte
	ExecutionID domain.ID
	MachineID   domain.ID
	InstanceID  domain.ID
	DeviceID    domain.ID
	ServerEpoch domain.ID
}

func (t *Tx) PutExecutionGrant(grant ExecutionGrant) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	for _, id := range []domain.ID{grant.JobID, grant.ExecutionID, grant.MachineID, grant.InstanceID, grant.DeviceID, grant.ServerEpoch} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if len(grant.Digest) != 32 {
		return domain.Fail(domain.InvalidArgument, "Invalid execution credential digest.", "Register only a SHA-256 digest of a private random execution token.")
	}
	var old ExecutionGrant
	err := t.tx.QueryRowContext(t.ctx, "SELECT digest,execution_id,machine_id,instance_id,device_id,server_epoch FROM execution_grants WHERE job_id=?", grant.JobID).Scan(&old.Digest, &old.ExecutionID, &old.MachineID, &old.InstanceID, &old.DeviceID, &old.ServerEpoch)
	if err == nil {
		if !bytes.Equal(old.Digest, grant.Digest) || old.ExecutionID != grant.ExecutionID || old.MachineID != grant.MachineID || old.InstanceID != grant.InstanceID || old.DeviceID != grant.DeviceID || old.ServerEpoch != grant.ServerEpoch {
			return domain.Fail(domain.Conflict, "This execution already has another credential binding.", "Reconcile its original native attempt; do not replace the credential or replay input.")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return storageError(err)
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO execution_grants(job_id,digest,execution_id,machine_id,instance_id,device_id,server_epoch) VALUES(?,?,?,?,?,?,?)", grant.JobID, grant.Digest, grant.ExecutionID, grant.MachineID, grant.InstanceID, grant.DeviceID, grant.ServerEpoch)
	return storageError(err)
}

func (t *Tx) ExecutionGrant(digest []byte) (ExecutionGrant, error) {
	var result ExecutionGrant
	if len(digest) != 32 {
		return result, domain.Fail(domain.Unauthenticated, "The execution credential is invalid.", "Use the original private execution authority.")
	}
	err := t.tx.QueryRowContext(t.ctx, "SELECT job_id,execution_id,machine_id,instance_id,device_id,server_epoch FROM execution_grants WHERE digest=?", digest).Scan(&result.JobID, &result.ExecutionID, &result.MachineID, &result.InstanceID, &result.DeviceID, &result.ServerEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return result, domain.Fail(domain.Unauthenticated, "The execution credential is unavailable.", "Reconcile the accepted native execution.")
	}
	return result, storageError(err)
}

type ExecutionReference struct {
	SessionID, AccountID, ConnectionID, ModelID domain.ID
	Kind                                        domain.NativeReferenceKind
	NativeID                                    string
}

func (r ExecutionReference) validate() error {
	for _, id := range []domain.ID{r.SessionID, r.AccountID, r.ConnectionID, r.ModelID} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if r.Kind != domain.NativeResponseReference && r.Kind != domain.NativeConversationReference {
		return domain.Fail(domain.Unsupported, "Unknown native reference kind.", "Use a supported execution-owned native reference.")
	}
	return domain.Text(r.NativeID, "native reference", 256, true)
}

func (t *Tx) HasExecutionReference(ref ExecutionReference) (bool, error) {
	if err := ref.validate(); err != nil {
		return false, err
	}
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM execution_references WHERE session_id=? AND account_id=? AND connection_id=? AND model_id=? AND reference_kind=? AND native_id=?)", ref.SessionID, ref.AccountID, ref.ConnectionID, ref.ModelID, ref.Kind, ref.NativeID).Scan(&exists)
	return exists, storageError(err)
}

func (t *Tx) ObserveExecutionReference(ref ExecutionReference) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if err := ref.validate(); err != nil {
		return err
	}
	exists, err := t.HasExecutionReference(ref)
	if err != nil || exists {
		return err
	}
	var count int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM execution_references WHERE session_id=?", ref.SessionID).Scan(&count); err != nil {
		return storageError(err)
	}
	if count >= 10000 {
		return domain.Fail(domain.ResourceExhausted, "The session's native reference bound is reached.", "Retain the original execution for explicit recovery; no reference was substituted.")
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO execution_references(session_id,account_id,connection_id,model_id,reference_kind,native_id) VALUES(?,?,?,?,?,?)", ref.SessionID, ref.AccountID, ref.ConnectionID, ref.ModelID, ref.Kind, ref.NativeID)
	return storageError(err)
}
