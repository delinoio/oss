// SPDX-License-Identifier: Apache-2.0
package domain

import "encoding/json"

// ForkOrigin is a retained boundary, never an executable copy of source input.
// Snapshot remains immutable even before the child's first explicit input.
type ForkOrigin struct {
	SourceSessionID   ID               `json:"source_session_id"`
	SourceRevision    uint64           `json:"source_revision"`
	SourceExecutionID ID               `json:"source_execution_id"`
	SourceTurnID      NativeIdentity   `json:"source_turn_id"`
	JobID             ID               `json:"job_id"`
	RuntimeID         ID               `json:"runtime_id"`
	NativeThreadID    NativeIdentity   `json:"native_thread_id"`
	CheckpointDigest  string           `json:"checkpoint_digest"`
	Snapshot          InitialExecution `json:"snapshot"`
	WorkerDeviceID    ID               `json:"worker_device_id"`
	JobInputDigest    string           `json:"job_input_digest"`
}

type ForkJobInput struct {
	Version          uint32              `json:"version"`
	SourceSessionID  ID                  `json:"source_session_id"`
	SourceRevision   uint64              `json:"source_revision"`
	ChildSessionID   ID                  `json:"child_session_id"`
	RuntimeID        ID                  `json:"runtime_id"`
	NativeRequestID  ID                  `json:"native_request_id"`
	Name             string              `json:"name"`
	Workspace        WorkspaceType       `json:"workspace"`
	LocalOrigin      *LocalOrigin        `json:"local_origin,omitempty"`
	CreatedBy        ID                  `json:"created_by"`
	Actor            Principal           `json:"actor"`
	SourceJobID      ID                  `json:"source_job_id"`
	SourceAssignment ExecutionJobInput   `json:"source_assignment"`
	Completion       ExecutionCompletion `json:"completion"`
	Progress         ExecutionProgress   `json:"progress"`
	Snapshot         InitialExecution    `json:"snapshot"`
}

func (i ForkJobInput) Validate() error {
	digest, digestErr := i.Snapshot.Configuration.Digest()
	if i.Version != 1 || UniqueIDs([]ID{i.SourceSessionID, i.ChildSessionID, i.RuntimeID, i.NativeRequestID, i.SourceJobID}) != nil || i.SourceRevision == 0 || Text(i.Name, "fork name", 256, true) != nil || (i.CreatedBy != "" && i.CreatedBy.Validate() != nil) || i.CreatedBy != i.Actor.DeviceID || i.SourceAssignment.Validate() != nil || i.SourceAssignment.SessionID != i.SourceSessionID || i.SourceAssignment.Configuration.Harness != Codex || i.Completion.Version != 2 || i.Completion.Validate() != nil || i.Completion.Outcome != ExecutionSucceeded || i.Completion.ExecutionID != i.SourceAssignment.ExecutionID || i.Completion.InputID != i.SourceAssignment.InputID || !i.Progress.CleanupVerified || i.Progress.Waiting != (NativeWaiting{}) || i.Progress.UnconfirmedResponses != 0 || i.Progress.ExecutionID != i.Completion.ExecutionID || i.Progress.Outcome != ExecutionSucceeded || i.Progress.LastSequence != i.Completion.LastSequence || i.Progress.JobID != i.SourceJobID || i.Progress.NativeTurnID != string(i.Completion.NativeTurnID) || i.Progress.NativeThreadID != string(i.Completion.NativeThreadID) || digestErr != nil || digest != i.SourceAssignment.ConfigurationDigest || i.Snapshot.ConfigurationDigest != i.SourceAssignment.ConfigurationDigest || i.Snapshot.InitialAccountID != i.SourceAssignment.AccountID || i.Snapshot.ConnectionID != i.SourceAssignment.ConnectionID {
		return Fail(RecoveryRequired, "The fork does not identify one verified completed source boundary.", "Preserve the original session, assignment, checkpoint and cleanup evidence.")
	}
	if i.Workspace != Worktree && i.Workspace != GeneralChat && i.Workspace != Local {
		return Fail(InvalidArgument, "Unknown fork workspace.", "Select an independent Worktree, General Chat or explicit Local sharing.")
	}
	if (i.Workspace == Local) != (i.LocalOrigin != nil) || i.LocalOrigin != nil && i.LocalOrigin.MachineID != i.SourceAssignment.MachineID {
		return Fail(PermissionDenied, "Local fork lacks same-machine authority.", "Authenticate the original machine's private Worker scope.")
	}
	return nil
}

type ForkJobResult struct {
	Version          uint32          `json:"version"`
	ChildSessionID   ID              `json:"child_session_id"`
	RuntimeID        ID              `json:"runtime_id"`
	NativeThreadID   NativeIdentity  `json:"native_thread_id"`
	NativeTurnID     NativeIdentity  `json:"native_turn_id"`
	CheckpointDigest string          `json:"checkpoint_digest"`
	Preparation      json.RawMessage `json:"preparation"`
	Manifest         json.RawMessage `json:"manifest"`
	CleanupVerified  bool            `json:"cleanup_verified"`
}

type ForkExecution struct {
	JobID            ID             `json:"job_id"`
	RuntimeID        ID             `json:"runtime_id"`
	NativeThreadID   NativeIdentity `json:"native_thread_id"`
	NativeTurnID     NativeIdentity `json:"native_turn_id"`
	CheckpointDigest string         `json:"checkpoint_digest"`
	HistoryRequestID ID             `json:"history_request_id"`
}

func (f ForkExecution) Validate(i ExecutionJobInput) error {
	if i.Configuration.Harness != Codex || UniqueIDs([]ID{f.JobID, f.RuntimeID, f.HistoryRequestID, i.SessionID, i.ExecutionID, i.InputID, i.ThreadRequestID, i.TurnRequestID}) != nil || f.NativeThreadID.Validate(Codex, NativeThreadIdentity) != nil || f.NativeTurnID.Validate(Codex, NativeTurnIdentity) != nil || !canonicalDigest(f.CheckpointDigest) {
		return Fail(RecoveryRequired, "The first fork continuation lacks its original boundary.", "Retain the accepted fork job and Worker-private checkpoint.")
	}
	return nil
}

func canonicalDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (r ForkJobResult) ValidateIdentity(input ForkJobInput) error {
	if r.Version != 1 || !r.CleanupVerified || r.ChildSessionID != input.ChildSessionID || r.RuntimeID != input.RuntimeID || r.NativeTurnID != input.Completion.NativeTurnID || r.NativeThreadID == input.Completion.NativeThreadID || r.NativeThreadID.Validate(Codex, NativeThreadIdentity) != nil || !canonicalDigest(r.CheckpointDigest) {
		return Fail(RecoveryRequired, "Fork completion lacks its exact verified child boundary.", "Retain the original Worker operation without repeating native Fork.")
	}
	return nil
}

// Validate checks the child-owned publication seed without reopening its parent.
func (f ForkOrigin) Validate() error {
	digest, err := f.Snapshot.Configuration.Digest()
	if err != nil || digest != f.Snapshot.ConfigurationDigest || f.Snapshot.Configuration.Harness != Codex || f.SourceRevision == 0 || f.SourceSessionID.Validate() != nil || f.SourceExecutionID.Validate() != nil || f.JobID.Validate() != nil || f.RuntimeID.Validate() != nil || f.WorkerDeviceID.Validate() != nil || f.NativeThreadID.Validate(Codex, NativeThreadIdentity) != nil || f.SourceTurnID.Validate(Codex, NativeTurnIdentity) != nil || !canonicalDigest(f.CheckpointDigest) || !canonicalDigest(f.JobInputDigest) || f.Snapshot.InitialAccountID.Validate() != nil || f.Snapshot.ConnectionID.Validate() != nil {
		return Fail(RecoveryRequired, "The child lost its immutable fork boundary.", "Preserve the child-owned seed and original Worker checkpoint.")
	}
	return nil
}
