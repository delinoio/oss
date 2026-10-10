// SPDX-License-Identifier: Apache-2.0
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const codexAppsPrefix = "codex-apps-v1:"
const codexAppsOperationPrefix = "codex-apps-operation-v1:"
const codexAppsMetadataLimit = 4 << 20

// CodexAppsSnapshot is metadata in the existing transaction/receipt domain.
// Native policy claims and uncertain obligations cannot be replaced by discovery.
type CodexAppsSnapshot struct {
	AccountGeneration domain.ID                     `json:"account_generation"`
	ConnectionID      domain.ID                     `json:"connection_id"`
	Version           int                           `json:"version"`
	Revision          uint64                        `json:"revision,string"`
	SessionID         domain.ID                     `json:"session_id"`
	Configuration     *domain.CodexAppConfiguration `json:"configuration,omitempty"`
	Operation         *domain.CodexAppsOperation    `json:"operation,omitempty"`
	Inventory         *domain.CodexAppsInventory    `json:"inventory,omitempty"`
}

func codexAppsConflict() error {
	return domain.Fail(domain.RecoveryRequired, "Original Codex app metadata is unavailable.", "Preserve its original session, account, generation and pending native obligation.")
}
func (v CodexAppsSnapshot) validate(session domain.ID) error {
	if v.Version != 1 || v.Revision == 0 || v.Revision >= 1<<63-1 || v.SessionID != session || session.Validate() != nil {
		return codexAppsConflict()
	}
	if v.Configuration != nil && (v.Configuration.Validate() != nil || v.Configuration.SessionID != session || v.AccountGeneration.Validate() != nil || v.ConnectionID.Validate() != nil) {
		return codexAppsConflict()
	}
	if v.Operation != nil && (v.Operation.Validate() != nil || v.Operation.Original.SessionID != session || v.Configuration == nil) {
		return codexAppsConflict()
	}
	if v.Inventory != nil && (v.Inventory.Validate() != nil || v.Inventory.SessionID != session || v.Configuration == nil || v.Inventory.AccountID != v.Configuration.AccountID || v.Inventory.ConfigurationGeneration != v.Configuration.Generation) {
		return codexAppsConflict()
	}
	return nil
}
func (t *Tx) CodexAppsSnapshot(session domain.ID) (*CodexAppsSnapshot, error) {
	if err := session.Validate(); err != nil {
		return nil, err
	}
	var raw string
	err := t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", codexAppsPrefix+string(session)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, storageError(err)
	}
	var value CodexAppsSnapshot
	if len(raw) > codexAppsMetadataLimit || domain.DecodeBounded([]byte(raw), &value, codexAppsMetadataLimit) != nil || value.validate(session) != nil {
		return nil, codexAppsConflict()
	}
	return &value, nil
}
func (t *Tx) PutCodexAppsSnapshot(session domain.ID, expected uint64, value CodexAppsSnapshot) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	original, err := t.CodexAppsSnapshot(session)
	if err != nil {
		return err
	}
	revision := uint64(0)
	if original != nil {
		revision = original.Revision
	}
	if revision != expected || expected >= 1<<63-2 {
		return codexAppsConflict()
	}
	// Claimed/uncertain native work is retained, including across other actors.
	if original != nil && original.Operation != nil && (original.Operation.State == domain.CodexAppsClaimed || original.Operation.State == domain.CodexAppsUncertain || original.Operation.State == domain.CodexAppsQueued) && (value.Operation == nil || value.Operation.ID != original.Operation.ID) {
		return codexAppsConflict()
	}
	if original != nil && original.Operation != nil && slices.Contains([]domain.CodexAppsState{domain.CodexAppsQueued, domain.CodexAppsClaimed, domain.CodexAppsUncertain}, original.Operation.State) {
		if original.AccountGeneration != value.AccountGeneration || original.ConnectionID != value.ConnectionID {
			return codexAppsConflict()
		}
		if !reflect.DeepEqual(original.Configuration, value.Configuration) && !(original.Operation.State == domain.CodexAppsClaimed && value.Operation != nil && value.Operation.State == domain.CodexAppsSucceeded && value.Operation.Action == domain.CodexAppsRevoke && value.Operation.Next != nil && reflect.DeepEqual(value.Configuration, value.Operation.Next)) {
			return codexAppsConflict()
		}
	}
	value.Version, value.SessionID, value.Revision = 1, session, expected+1
	if err := value.validate(session); err != nil {
		return err
	}
	if err := t.putCodexAppsHistory(value); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return storageError(err)
	}
	if len(raw) > codexAppsMetadataLimit {
		return domain.Fail(domain.ResourceExhausted, "Codex app metadata exceeds its bound.", "Retain the original state and use a smaller complete catalog.")
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", codexAppsPrefix+string(session), string(raw))
	if err == nil {
		t.touched[session] = true
	}
	return storageError(err)
}
func (t *Tx) CodexAppsSelection(session domain.ID) (*domain.CodexAppConfiguration, error) {
	value, err := t.CodexAppsSnapshot(session)
	if err != nil || value == nil {
		return nil, err
	}
	if value.Operation != nil && slices.Contains([]domain.CodexAppsState{domain.CodexAppsQueued, domain.CodexAppsClaimed, domain.CodexAppsUncertain}, value.Operation.State) {
		return nil, codexAppsConflict()
	}
	if value.Configuration == nil {
		return nil, nil
	}
	result := value.Configuration.Clone()
	return &result, nil
}

// Assignment admission supplies the exact protected account generation. Neither
// an unchanged account UUID nor a replaced connection can adopt old app policy.
func (t *Tx) CodexAppsAssignmentSelection(session, account, accountGeneration, connection domain.ID) (*domain.CodexAppConfiguration, error) {
	value, err := t.CodexAppsSnapshot(session)
	if err != nil || value == nil {
		return nil, err
	}
	if value.Configuration == nil {
		return nil, nil
	}
	if value.Configuration.AccountID != account || value.AccountGeneration != accountGeneration || value.ConnectionID != connection || accountGeneration.Validate() != nil || connection.Validate() != nil {
		return nil, codexAppsConflict()
	}
	return t.CodexAppsSelection(session)
}
func (t *Tx) CodexAppsCurrentSelection(session domain.ID, original domain.CodexAppConfiguration) (*domain.CodexAppConfiguration, error) {
	if original.Validate() != nil || original.SessionID != session {
		return nil, codexAppsConflict()
	}
	value, err := t.CodexAppsSnapshot(session)
	if err != nil {
		return nil, err
	}
	if value == nil || value.Configuration == nil {
		return nil, codexAppsConflict()
	}
	current := value.Configuration.Clone()
	if current.SessionID != original.SessionID || current.AccountID != original.AccountID || !(current.Generation == original.Generation && slices.Equal(current.AppIDs, original.AppIDs) || original.RemovalOnly(current)) {
		return nil, codexAppsConflict()
	}
	// Pending removal cannot silently restore its frozen positive selection.
	if value.Operation != nil && value.Operation.State != domain.CodexAppsSucceeded && value.Operation.State != domain.CodexAppsFailed && value.Operation.Action == domain.CodexAppsRevoke {
		return nil, codexAppsConflict()
	}
	return &current, nil
}

type codexAppsOperationHistory struct {
	Version           int                       `json:"version"`
	AccountGeneration domain.ID                 `json:"account_generation"`
	ConnectionID      domain.ID                 `json:"connection_id"`
	Operation         domain.CodexAppsOperation `json:"operation"`
}

func (t *Tx) codexAppsHistory(id domain.ID) (*codexAppsOperationHistory, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	var raw string
	err := t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", codexAppsOperationPrefix+string(id)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, storageError(err)
	}
	var value codexAppsOperationHistory
	if domain.DecodeBounded([]byte(raw), &value, codexAppsMetadataLimit) != nil || value.Version != 1 || value.AccountGeneration.Validate() != nil || value.ConnectionID.Validate() != nil || value.Operation.ID != id || value.Operation.Validate() != nil {
		return nil, codexAppsConflict()
	}
	return &value, nil
}
func (t *Tx) CodexAppsOperation(id domain.ID) (*domain.CodexAppsOperation, error) {
	value, err := t.codexAppsHistory(id)
	if err != nil || value == nil {
		return nil, err
	}
	operation := value.Operation
	return &operation, nil
}
func codexAppsImmutableOperation(a, b domain.CodexAppsOperation) bool {
	a.State, b.State = domain.CodexAppsQueued, domain.CodexAppsQueued
	a.Revision, b.Revision = 1, 1
	a.ClaimID, b.ClaimID = "", ""
	a.PositiveNoNativeSend, b.PositiveNoNativeSend = false, false
	a.Inventory, b.Inventory = nil, nil
	a.Problem, b.Problem = nil, nil
	return reflect.DeepEqual(a, b)
}
func (t *Tx) putCodexAppsHistory(value CodexAppsSnapshot) error {
	operation := value.Operation
	if operation == nil {
		return nil
	}
	original, err := t.codexAppsHistory(operation.ID)
	if err != nil {
		return err
	}
	if original != nil {
		if reflect.DeepEqual(original.Operation, *operation) {
			return nil
		}
		old := original.Operation
		if !codexAppsImmutableOperation(old, *operation) || original.AccountGeneration != value.AccountGeneration || original.ConnectionID != value.ConnectionID || operation.Revision != old.Revision+1 {
			return codexAppsConflict()
		}
		allowed := old.State == domain.CodexAppsQueued && (operation.State == domain.CodexAppsClaimed || operation.State == domain.CodexAppsCanceled) || old.State == domain.CodexAppsClaimed && (operation.State == domain.CodexAppsSucceeded || operation.State == domain.CodexAppsFailed || operation.State == domain.CodexAppsUncertain) && old.ClaimID == operation.ClaimID
		if !allowed {
			return codexAppsConflict()
		}
	} else {
		if operation.State != domain.CodexAppsQueued || operation.Revision != 1 {
			return codexAppsConflict()
		}
		var count int
		err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM metadata WHERE key LIKE ? AND json_extract(value,'$.operation.execution_job_id')=?", codexAppsOperationPrefix+"%", operation.ExecutionJobID).Scan(&count)
		if err != nil {
			return storageError(err)
		}
		if count >= domain.MaxExecutionInteractions {
			return domain.Fail(domain.ResourceExhausted, "The original execution reached its Codex app control bound.", "Retain its full journal without evicting controls.")
		}
	}
	history := codexAppsOperationHistory{Version: 1, AccountGeneration: value.AccountGeneration, ConnectionID: value.ConnectionID, Operation: *operation}
	raw, err := json.Marshal(history)
	if err != nil {
		return storageError(err)
	}
	if len(raw) > codexAppsMetadataLimit {
		return codexAppsConflict()
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", codexAppsOperationPrefix+string(operation.ID), string(raw))
	return storageError(err)
}

// Original terminal cleanup can settle only a still-unclaimed control. Claimed
// native effects and uncertainty retain their separate durable obligations.
func (t *Tx) CancelUnclaimedCodexAppsControl(jobID domain.ID) error {
	record, err := t.Get(domain.JobKind, jobID)
	if err != nil {
		return err
	}
	job, err := Decode[domain.Job](record)
	if err != nil {
		return err
	}
	if job.Type != domain.ExecuteSessionJob || !slices.Contains([]domain.JobState{domain.JobSucceeded, domain.JobFailed, domain.JobCanceled}, job.State) {
		return codexAppsConflict()
	}
	var input domain.ExecutionJobInput
	var complete domain.ExecutionCompletion
	if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || domain.Decode(job.Output, &complete) != nil || complete.ValidateForHarness(input.Configuration.Harness) != nil || !complete.CleanupVerified || complete.ExecutionID != input.ExecutionID || complete.InputID != input.InputID {
		return codexAppsConflict()
	}
	value, err := t.CodexAppsSnapshot(record.SessionID)
	if err != nil || value == nil || value.Operation == nil {
		return err
	}
	operation := value.Operation
	if operation.ExecutionJobID != jobID || operation.State != domain.CodexAppsQueued {
		return nil
	}
	if operation.ExecutionID != complete.ExecutionID || operation.NativeThreadID != complete.NativeThreadID || operation.ClaimID != "" {
		return codexAppsConflict()
	}
	operation.State, operation.PositiveNoNativeSend, operation.Revision = domain.CodexAppsCanceled, true, operation.Revision+1
	return t.PutCodexAppsSnapshot(value.SessionID, value.Revision, *value)
}

// Original queued controls are bounded as a complete page; overflow fails closed.
func (t *Tx) CodexAppsQueued(machine, instance domain.ID) ([]domain.CodexAppsOperation, error) {
	for _, id := range []domain.ID{machine, instance} {
		if err := id.Validate(); err != nil {
			return nil, err
		}
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT value FROM metadata WHERE key LIKE ? AND json_extract(value,'$.operation.state')='queued' AND json_extract(value,'$.operation.machine_id')=? AND json_extract(value,'$.operation.instance_id')=? ORDER BY key LIMIT 1001", codexAppsPrefix+"%", machine, instance)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	values := []domain.CodexAppsOperation{}
	for rows.Next() {
		var raw string
		var value CodexAppsSnapshot
		if rows.Scan(&raw) != nil || len(raw) > codexAppsMetadataLimit || domain.DecodeBounded([]byte(raw), &value, codexAppsMetadataLimit) != nil || value.validate(value.SessionID) != nil || value.Operation == nil {
			return nil, codexAppsConflict()
		}
		values = append(values, *value.Operation)
		if len(values) > 1000 {
			return nil, domain.Fail(domain.ResourceExhausted, "Too many pending Codex app controls.", "Resolve original controls without truncating their ownership inventory.")
		}
	}
	return values, storageError(rows.Err())
}
