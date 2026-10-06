// SPDX-License-Identifier: Apache-2.0
package store

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Only non-secret original transfer pins survive in SQL. This metadata grants
// no credentials and is independently checked against the current route.
type WorkerNetworkTransfer struct {
	MachineID  domain.ID `json:"machine_id"`
	DeviceID   domain.ID `json:"device_id"`
	KeyID      domain.ID `json:"key_id"`
	RouteID    domain.ID `json:"route_id"`
	Generation uint64    `json:"generation,string"`
	Digest     string    `json:"digest"`
}

func (v WorkerNetworkTransfer) validate() error {
	for _, id := range []domain.ID{v.MachineID, v.DeviceID, v.KeyID, v.RouteID} {
		if id.Validate() != nil {
			return domain.Fail(domain.InvalidArgument, "Invalid Worker transfer identity.", "Use the original encrypted transfer pins.")
		}
	}
	digest, err := hex.DecodeString(v.Digest)
	if v.Generation == 0 || v.Generation >= 1<<63 || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != v.Digest {
		return domain.Fail(domain.InvalidArgument, "Invalid Worker transfer pin.", "Retain the original ciphertext digest and exact generation.")
	}
	return nil
}

func (t *Tx) PutWorkerNetworkTransfer(v WorkerNetworkTransfer) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if err := v.validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return storageError(err)
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "worker-network-transfer:"+string(v.MachineID), string(raw))
	return storageError(err)
}
func (t *Tx) WorkerNetworkTransfer(machine domain.ID) (WorkerNetworkTransfer, error) {
	var v WorkerNetworkTransfer
	var raw string
	err := t.tx.QueryRowContext(t.ctx, `SELECT value FROM metadata WHERE key=?`, "worker-network-transfer:"+string(machine)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v, domain.Fail(domain.NotFound, "The original Worker transfer has not been issued.", "Request a fresh authenticated encrypted transfer.")
	}
	if err != nil {
		return v, storageError(err)
	}
	if domain.Decode([]byte(raw), &v) != nil || v.validate() != nil || v.MachineID != machine {
		return v, domain.Fail(domain.RecoveryRequired, "The Worker transfer pin is inconsistent.", "Reconcile the current route without native dispatch.")
	}
	return v, nil
}

// A pending single-use grant can bind only one original prepared recipient,
// even before its Machine/Device entities exist. Pairing must check this pin
// independently of a caller-selected machine's current route.
type WorkerNetworkPairing struct {
	MachineID domain.ID                   `json:"machine_id"`
	Binding   domain.WorkerNetworkBinding `json:"binding"`
}

func (t *Tx) WorkerNetworkPairing(pairing domain.ID) (WorkerNetworkPairing, error) {
	var value WorkerNetworkPairing
	var raw string
	err := t.tx.QueryRowContext(t.ctx, `SELECT value FROM metadata WHERE key=?`, "worker-network-pairing:"+string(pairing)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return value, domain.Fail(domain.NotFound, "No original encrypted pairing binding exists.", "Prepare the original recipient before exporting its configuration.")
	}
	if err != nil {
		return value, storageError(err)
	}
	if domain.Decode([]byte(raw), &value) != nil || value.MachineID.Validate() != nil || value.Binding.Validate() != nil || value.Binding.PairingID != pairing {
		return value, domain.Fail(domain.RecoveryRequired, "The original encrypted pairing pin is inconsistent.", "Retain the original grant without native dispatch.")
	}
	return value, nil
}

func (t *Tx) PutWorkerNetworkPairing(value WorkerNetworkPairing) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if value.MachineID.Validate() != nil || value.Binding.Validate() != nil {
		return domain.Fail(domain.InvalidArgument, "Invalid original encrypted pairing pin.", "Retain the original prepared machine, device and recipient.")
	}
	original, err := t.WorkerNetworkPairing(value.Binding.PairingID)
	if err == nil {
		if original != value {
			return domain.Fail(domain.Conflict, "The pairing grant belongs to another original recipient.", "Use its original prepared Worker or issue a separate pairing grant.")
		}
		return nil
	}
	if domain.SafeError(err).Code != domain.NotFound {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return storageError(err)
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO metadata(key,value) VALUES(?,?)`, "worker-network-pairing:"+string(value.Binding.PairingID), string(raw))
	return storageError(err)
}

type WorkerNativeRoute struct {
	JobID       domain.ID                     `json:"job_id"`
	ExecutionID domain.ID                     `json:"execution_id"`
	MachineID   domain.ID                     `json:"machine_id"`
	InstanceID  domain.ID                     `json:"instance_id"`
	DeviceID    domain.ID                     `json:"device_id"`
	RouteID     domain.ID                     `json:"route_id"`
	Generation  uint64                        `json:"generation,string"`
	JobRevision uint64                        `json:"job_revision,string"`
	State       domain.WorkerNativeRouteState `json:"state"`
	ObservedAt  time.Time                     `json:"observed_at"`
}

func (v WorkerNativeRoute) validate() error {
	for _, id := range []domain.ID{v.JobID, v.ExecutionID, v.MachineID, v.InstanceID, v.DeviceID, v.RouteID} {
		if id.Validate() != nil {
			return domain.Fail(domain.InvalidArgument, "Invalid original native route identity.", "Use the exact registered API job and reconciled route.")
		}
	}
	if v.Generation == 0 || v.Generation >= 1<<63 || v.JobRevision == 0 || v.JobRevision >= 1<<63 || !v.State.Valid() || v.ObservedAt.IsZero() {
		return domain.Fail(domain.InvalidArgument, "Invalid original native route observation.", "Retain the exact applied generation and bounded native state.")
	}
	return nil
}

func (t *Tx) WorkerNativeRoute(job domain.ID) (WorkerNativeRoute, error) {
	var value WorkerNativeRoute
	var raw string
	err := t.tx.QueryRowContext(t.ctx, `SELECT value FROM metadata WHERE key=?`, "worker-native-route:"+string(job)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return value, domain.Fail(domain.NotFound, "No original native route has been bound.", "Bind the original generation before native startup.")
	}
	if err != nil {
		return value, storageError(err)
	}
	if domain.Decode([]byte(raw), &value) != nil || value.validate() != nil || value.JobID != job {
		return value, domain.Fail(domain.RecoveryRequired, "The original native route pin is inconsistent.", "Preserve original ownership without another native launch.")
	}
	return value, nil
}

func (t *Tx) PutWorkerNativeRoute(value WorkerNativeRoute) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if err := value.validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return storageError(err)
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "worker-native-route:"+string(value.JobID), string(raw))
	return storageError(err)
}

func (t *Tx) deleteWorkerNativeRoute(job domain.ID) error {
	_, err := t.tx.ExecContext(t.ctx, `DELETE FROM metadata WHERE key=?`, "worker-native-route:"+string(job))
	return storageError(err)
}
