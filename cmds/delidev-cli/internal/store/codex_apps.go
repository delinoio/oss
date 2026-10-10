// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"

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
	if record.SessionID.Validate() != nil {
		return nil
	}
	value, err := t.CodexAppsSnapshot(record.SessionID)
	if err != nil || value == nil || value.Operation == nil {
		return err
	}
	operation := value.Operation
	if operation.ExecutionJobID != jobID || operation.State != domain.CodexAppsQueued {
		return nil
	}
	job, err := Decode[domain.Job](record)
	if err != nil || job.Type != domain.ExecuteSessionJob || !slices.Contains([]domain.JobState{domain.JobSucceeded, domain.JobFailed, domain.JobCanceled}, job.State) {
		return nil
	}
	var input domain.ExecutionJobInput
	var complete domain.ExecutionCompletion
	// Missing or uncertain cleanup is not a failed original completion. Keep the
	// queued obligation unchanged until its original no-send proof is available.
	if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || domain.Decode(job.Output, &complete) != nil || complete.ValidateForHarness(input.Configuration.Harness) != nil || !complete.CleanupVerified || record.SessionID != input.SessionID || job.MachineID != input.MachineID || complete.ExecutionID != input.ExecutionID || complete.InputID != input.InputID {
		return nil
	}
	if operation.ExecutionID != complete.ExecutionID || operation.MachineID != job.MachineID || operation.InstanceID != job.InstanceID || input.CodexApps == nil || input.CodexApps.AccountID != operation.Original.AccountID || !(input.CodexApps.Generation == operation.Original.Generation && slices.Equal(input.CodexApps.AppIDs, operation.Original.AppIDs) || input.CodexApps.RemovalOnly(operation.Original)) || operation.NativeThreadID != complete.NativeThreadID || operation.ClaimID != "" {
		return nil
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
	rows, err := t.tx.QueryContext(t.ctx, "SELECT value FROM metadata WHERE key LIKE ? AND json_extract(value,'$.operation.state')='queued' AND json_extract(value,'$.operation.machine_id')=? AND json_extract(value,'$.operation.instance_id')=? ORDER BY key LIMIT ?", codexAppsPrefix+"%", machine, instance, domain.MaxExecutionInteractions+1)
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
		if len(values) > domain.MaxExecutionInteractions {
			return nil, domain.Fail(domain.ResourceExhausted, "Too many pending Codex app controls.", "Resolve original controls without truncating their ownership inventory.")
		}
	}
	return values, storageError(rows.Err())
}

// RequireSettledCodexApps protects original native obligations before session
// deletion admission and final erasure. Terminal metadata remains immutable.
func (t *Tx) RequireSettledCodexApps(session domain.ID) error {
	if session.Validate() != nil {
		return codexAppsConflict()
	}
	value, err := t.CodexAppsSnapshot(session)
	if err != nil {
		return err
	}
	if value != nil && value.Operation != nil && codexAppsProtected(value.Operation.State) {
		return domain.SessionDeletionPending()
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT key,value FROM metadata WHERE key LIKE ? AND json_extract(value,'$.operation.original.session_id')=? ORDER BY key LIMIT ?", codexAppsOperationPrefix+"%", session, 100001)
	if err != nil {
		return storageError(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		var key, raw string
		if rows.Scan(&key, &raw) != nil || count > 100000 {
			return codexAppsConflict()
		}
		operation, err := decodeCodexAppsMetadata(key, raw)
		if err != nil {
			return err
		}
		if operation != nil && operation.Original.SessionID == session && codexAppsProtected(operation.State) {
			return domain.SessionDeletionPending()
		}
	}
	return storageError(rows.Err())
}

func codexAppsProtected(state domain.CodexAppsState) bool {
	return slices.Contains([]domain.CodexAppsState{domain.CodexAppsQueued, domain.CodexAppsClaimed, domain.CodexAppsUncertain}, state)
}

// The closed metadata decoder also verifies key ownership. Malformed or future
// records block destructive maintenance rather than silently losing obligations.
func decodeCodexAppsMetadata(key, raw string) (*domain.CodexAppsOperation, error) {
	if len(raw) > codexAppsMetadataLimit {
		return nil, codexAppsConflict()
	}
	if strings.HasPrefix(key, codexAppsPrefix) {
		var value CodexAppsSnapshot
		if domain.DecodeBounded([]byte(raw), &value, codexAppsMetadataLimit) != nil || value.validate(domain.ID(strings.TrimPrefix(key, codexAppsPrefix))) != nil {
			return nil, codexAppsConflict()
		}
		return value.Operation, nil
	}
	var value codexAppsOperationHistory
	if !strings.HasPrefix(key, codexAppsOperationPrefix) || domain.DecodeBounded([]byte(raw), &value, codexAppsMetadataLimit) != nil || value.Version != 1 || value.AccountGeneration.Validate() != nil || value.ConnectionID.Validate() != nil || value.Operation.Validate() != nil || string(value.Operation.ID) != strings.TrimPrefix(key, codexAppsOperationPrefix) {
		return nil, codexAppsConflict()
	}
	return &value.Operation, nil
}

// Run before the general current-state metadata overlay in prepareRestoreImage.
// Only the current explicit account/generation/connection selection is retained;
// historical image-only selections cannot acquire fresh authority. Unresolved
// image-only obligations reject restore instead of being discarded or requeued.
func restoreCodexAppsMetadata(ctx context.Context, tx *sql.Tx) error {
	for _, schema := range []string{"main", "current_state"} {
		rows, err := tx.QueryContext(ctx, "SELECT key,value FROM "+schema+".metadata WHERE key LIKE ? OR key LIKE ? ORDER BY key LIMIT 100001", codexAppsPrefix+"%", codexAppsOperationPrefix+"%")
		if err != nil {
			return storageError(err)
		}
		count := 0
		for rows.Next() {
			count++
			var key, raw string
			if rows.Scan(&key, &raw) != nil || count > 100000 {
				rows.Close()
				return codexAppsConflict()
			}
			if _, err := decodeCodexAppsMetadata(key, raw); err != nil {
				rows.Close()
				return err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return storageError(err)
		}
	}
	// Every latest pointer must retain its original complete operation history.
	// Missing history is not proof that the original obligation was settled.
	for _, schema := range []string{"main", "current_state"} {
		rows, err := tx.QueryContext(ctx, "SELECT m.key,m.value,h.value FROM "+schema+".metadata m LEFT JOIN "+schema+".metadata h ON h.key=?||json_extract(m.value,'$.operation.id') WHERE m.key LIKE ? AND json_extract(m.value,'$.operation.id') IS NOT NULL ORDER BY m.key LIMIT 100001", codexAppsOperationPrefix, codexAppsPrefix+"%")
		if err != nil {
			return storageError(err)
		}
		for rows.Next() {
			var key, raw string
			var historyRaw sql.NullString
			if rows.Scan(&key, &raw, &historyRaw) != nil || !historyRaw.Valid {
				rows.Close()
				return codexAppsConflict()
			}
			operation, err := decodeCodexAppsMetadata(key, raw)
			if err != nil || operation == nil {
				rows.Close()
				return codexAppsConflict()
			}
			history, err := decodeCodexAppsMetadata(codexAppsOperationPrefix+string(operation.ID), historyRaw.String)
			if err != nil || history == nil || !reflect.DeepEqual(*operation, *history) {
				rows.Close()
				return codexAppsConflict()
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return storageError(err)
		}
	}
	// A same-ID current receipt may supersede old state only for the identical
	// original source. Revision rollback and foreign-ID collisions fail closed.
	joined, err := tx.QueryContext(ctx, "SELECT m.key,m.value,c.value FROM metadata m JOIN current_state.metadata c ON c.key=m.key WHERE m.key LIKE ? ORDER BY m.key LIMIT 100001", codexAppsOperationPrefix+"%")
	if err != nil {
		return storageError(err)
	}
	for joined.Next() {
		var key, oldRaw, currentRaw string
		if joined.Scan(&key, &oldRaw, &currentRaw) != nil {
			joined.Close()
			return codexAppsConflict()
		}
		var old, current codexAppsOperationHistory
		if domain.DecodeBounded([]byte(oldRaw), &old, codexAppsMetadataLimit) != nil || domain.DecodeBounded([]byte(currentRaw), &current, codexAppsMetadataLimit) != nil || old.AccountGeneration != current.AccountGeneration || old.ConnectionID != current.ConnectionID || !codexAppsImmutableOperation(old.Operation, current.Operation) || current.Operation.Revision < old.Operation.Revision || old.Operation.ClaimID != "" && old.Operation.ClaimID != current.Operation.ClaimID || current.Operation.Revision == old.Operation.Revision && !reflect.DeepEqual(old, current) {
			joined.Close()
			return codexAppsConflict()
		}
	}
	err = joined.Err()
	joined.Close()
	if err != nil {
		return storageError(err)
	}
	rows, err := tx.QueryContext(ctx, "SELECT m.key,m.value FROM metadata m WHERE (m.key LIKE ? OR m.key LIKE ?) AND NOT EXISTS(SELECT 1 FROM current_state.metadata c WHERE c.key=m.key) ORDER BY m.key LIMIT 100001", codexAppsPrefix+"%", codexAppsOperationPrefix+"%")
	if err != nil {
		return storageError(err)
	}
	for rows.Next() {
		var key, raw string
		if rows.Scan(&key, &raw) != nil {
			rows.Close()
			return codexAppsConflict()
		}
		operation, err := decodeCodexAppsMetadata(key, raw)
		if err != nil || operation != nil && codexAppsProtected(operation.State) {
			rows.Close()
			return codexAppsConflict()
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return storageError(err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM metadata WHERE key LIKE ? OR key LIKE ?", codexAppsPrefix+"%", codexAppsOperationPrefix+"%"); err != nil {
		return storageError(err)
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO metadata SELECT * FROM current_state.metadata WHERE key LIKE ? OR key LIKE ?", codexAppsPrefix+"%", codexAppsOperationPrefix+"%")
	return storageError(err)
}

// PurgeSessionCodexApps is called only inside the fully joined session purge.
// The existing deletion tombstone and reference-only mutation receipts retain
// identity; app names, catalogs and selected native IDs do not survive erasure.
func (t *Tx) PurgeSessionCodexApps(session domain.ID) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if err := t.RequireSettledCodexApps(session); err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(t.ctx, "DELETE FROM metadata WHERE key LIKE ? AND json_extract(value,'$.operation.original.session_id')=?", codexAppsOperationPrefix+"%", session); err != nil {
		return storageError(err)
	}
	_, err := t.tx.ExecContext(t.ctx, "DELETE FROM metadata WHERE key=?", codexAppsPrefix+string(session))
	return storageError(err)
}
