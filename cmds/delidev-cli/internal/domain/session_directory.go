// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"path"
	"strings"
)

const SessionDirectoryV1 WorkerCapability = "session-directory-v1"

// A directory generation is metadata authority, not a fresh execution or input.
type SessionDirectoryRef struct {
	GenerationID         ID     `json:"generation_id"`
	JobID                ID     `json:"job_id"`
	RequestID            ID     `json:"request_id"`
	ExecutionID          ID     `json:"execution_id"`
	RepositoryID         ID     `json:"repository_id"`
	RelativePath         string `json:"relative_path"`
	PreviousGenerationID ID     `json:"previous_generation_id,omitempty"`
	CheckpointDigest     string `json:"checkpoint_digest"`
}

func DirectoryUncertain() *Error {
	return Fail(RecoveryRequired, "The original directory transition is not verified.", "Retain the original request and source checkpoint; inspect its operation without repeating native work.")
}

func ValidateSessionDirectoryPath(value string) error {
	if Text(value, "relative directory", 4096, true) != nil || value == "" || strings.ContainsAny(value, "\\\x00:") || path.IsAbs(value) || path.Clean(value) != value || value == ".." || strings.HasPrefix(value, "../") {
		return Fail(InvalidArgument, "The directory must be a canonical relative path within an original prepared root.", "Select an existing directory under the original repository; use a dot for its root.")
	}
	return nil
}

func (r SessionDirectoryRef) Validate() error {
	if UniqueIDs([]ID{r.GenerationID, r.JobID, r.RequestID, r.ExecutionID}) != nil || r.RepositoryID != "" && r.RepositoryID.Validate() != nil || ValidateSessionDirectoryPath(r.RelativePath) != nil || !validCompactionDigest(r.CheckpointDigest) || r.PreviousGenerationID != "" && (r.PreviousGenerationID.Validate() != nil || r.PreviousGenerationID == r.GenerationID) {
		return DirectoryUncertain()
	}
	return nil
}

type SessionDirectoryInput struct {
	Version            uint32               `json:"version"`
	RequestID          ID                   `json:"request_id"`
	GenerationID       ID                   `json:"generation_id"`
	SourceJobID        ID                   `json:"source_job_id"`
	HistoryExecutionID ID                   `json:"history_execution_id"`
	Assignment         ExecutionJobInput    `json:"assignment"`
	Completion         ExecutionCompletion  `json:"completion"`
	PreviousExecution  ExecutionProgress    `json:"previous_execution"`
	RepositoryID       ID                   `json:"repository_id"`
	RelativePath       string               `json:"relative_path"`
	Previous           *SessionDirectoryRef `json:"previous,omitempty"`
}

func (i SessionDirectoryInput) Validate() error {
	a, done, p := i.Assignment, i.Completion, i.PreviousExecution
	if i.Version != 1 || UniqueIDs([]ID{i.RequestID, i.GenerationID, i.SourceJobID, a.SessionID, a.ExecutionID, a.InputID}) != nil || i.HistoryExecutionID.Validate() != nil || !sessionDirectoryRepository(i.Assignment, i.RepositoryID) || ValidateSessionDirectoryPath(i.RelativePath) != nil || a.Validate() != nil || a.Configuration.Harness != Codex || a.Fork != nil || a.SidechatRetry != nil || a.Configuration.SidechatPolicy != "" || done.Version != 2 || done.Validate() != nil || done.Outcome != ExecutionSucceeded || done.ExecutionID != a.ExecutionID || done.InputID != a.InputID || p.JobID != i.SourceJobID || p.ExecutionID != done.ExecutionID || p.InputID != done.InputID || p.NativeThreadID != string(done.NativeThreadID) || p.NativeTurnID != string(done.NativeTurnID) || p.LastSequence != done.LastSequence || p.Outcome != done.Outcome || !p.CleanupVerified || p.ContextRevision != a.ContextRevision || p.Waiting != (NativeWaiting{}) || p.UnconfirmedResponses != 0 || len(p.Subagents) != 0 || !p.NativeCompactions.Closed() || !p.AutoReviews.Closed() || p.TurnTiming != nil || p.Observed.ValidateForInput(a.Configuration, a.Input.Mode) != nil {
		return DirectoryUncertain()
	}
	if _, err := CheckedExecutionInputs(a.InputID, BindSessionInput(a.InputID, a.Input).PromptDigest, p.AcceptedInputs); err != nil {
		return DirectoryUncertain()
	}
	if i.Previous != nil && (i.Previous.Validate() != nil || i.Previous.GenerationID == i.GenerationID || i.Previous.RequestID == i.RequestID) {
		return DirectoryUncertain()
	}
	return nil
}

type SessionDirectoryResult struct {
	Version         uint32              `json:"version"`
	RequestID       ID                  `json:"request_id"`
	GenerationID    ID                  `json:"generation_id"`
	ExecutionID     ID                  `json:"execution_id"`
	Checkpoint      SessionDirectoryRef `json:"checkpoint"`
	CleanupVerified bool                `json:"cleanup_verified"`
}

func (r SessionDirectoryResult) Validate() error {
	if r.Version != 1 || !r.CleanupVerified || r.Checkpoint.Validate() != nil || r.RequestID != r.Checkpoint.RequestID || r.GenerationID != r.Checkpoint.GenerationID || r.ExecutionID != r.Checkpoint.ExecutionID {
		return DirectoryUncertain()
	}
	return nil
}

// Inspect only root-selection metadata; the Worker validates the complete
// original manifest and canonical filesystem identities independently.
func sessionDirectoryRepository(a ExecutionJobInput, id ID) bool {
	var manifest struct {
		SessionID    ID            `json:"session_id"`
		MachineID    ID            `json:"machine_id"`
		Type         WorkspaceType `json:"type"`
		State        string        `json:"state"`
		Repositories []struct {
			RepositoryID ID `json:"id"`
		} `json:"repositories"`
	}
	if json.Unmarshal(a.Manifest, &manifest) != nil || manifest.SessionID != a.SessionID || manifest.MachineID != a.MachineID || manifest.State != "ready" {
		return false
	}
	if manifest.Type == GeneralChat {
		return id == "" && len(manifest.Repositories) == 0
	}
	if id.Validate() != nil {
		return false
	}
	for _, repository := range manifest.Repositories {
		if repository.RepositoryID == id {
			return true
		}
	}
	return false
}
