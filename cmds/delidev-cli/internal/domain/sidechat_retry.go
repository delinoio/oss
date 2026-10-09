// SPDX-License-Identifier: Apache-2.0
package domain

import "encoding/json"

// SidechatRetry generations do not amend ForkOrigin. Each generation owns its
// original native Fork and question execution; only a verified successful
// completion may move the presentation pointer.
type SidechatRetry struct {
	WorkerInstanceID  ID                   `json:"worker_instance_id"`
	WorkerDeviceID    ID                   `json:"worker_device_id"`
	ID                ID                   `json:"id"`
	ForkJobID         ID                   `json:"fork_job_id"`
	RuntimeID         ID                   `json:"runtime_id"`
	QuestionID        ID                   `json:"question_id"`
	QuestionRevision  uint64               `json:"question_revision,string"`
	ParentRevision    uint64               `json:"parent_revision,string"`
	ParentExecutionID ID                   `json:"parent_execution_id"`
	ParentTurnID      NativeIdentity       `json:"parent_turn_id"`
	ExecutionID       ID                   `json:"execution_id,omitempty"`
	ExecutionJobID    ID                   `json:"execution_job_id,omitempty"`
	Fork              *SessionDeletionFork `json:"fork,omitempty"`
	Completed         bool                 `json:"completed,omitempty"`
}

type SidechatRetryFork struct {
	WorkerInstanceID ID     `json:"worker_instance_id"`
	WorkerDeviceID   ID     `json:"worker_device_id"`
	GenerationID     ID     `json:"generation_id"`
	ChildRevision    uint64 `json:"child_revision,string"`
	QuestionID       ID     `json:"question_id"`
	QuestionRevision uint64 `json:"question_revision,string"`
	// This is the original completed child workspace owner, not native history.
	PreviousJobID       ID              `json:"previous_job_id"`
	PreviousExecutionID ID              `json:"previous_execution_id"`
	ChildPreparation    json.RawMessage `json:"child_preparation"`
	ChildManifest       json.RawMessage `json:"child_manifest"`
}

type SidechatRetryExecution struct {
	WorkerInstanceID    ID `json:"worker_instance_id"`
	WorkerDeviceID      ID `json:"worker_device_id"`
	GenerationID        ID `json:"generation_id"`
	QuestionID          ID `json:"question_id"`
	PreviousJobID       ID `json:"previous_job_id"`
	PreviousExecutionID ID `json:"previous_execution_id"`
}

func (r SidechatRetryFork) Validate(child ID) error {
	if r.WorkerInstanceID.Validate() != nil || r.WorkerDeviceID.Validate() != nil || UniqueIDs([]ID{r.GenerationID, r.QuestionID, r.PreviousJobID, r.PreviousExecutionID, child}) != nil || r.ChildRevision == 0 || r.QuestionRevision == 0 || len(r.ChildPreparation) == 0 || len(r.ChildManifest) == 0 {
		return SidechatUnavailable()
	}
	return nil
}
func (r SidechatRetryExecution) Validate(i ExecutionJobInput) error {
	if r.WorkerInstanceID.Validate() != nil || r.WorkerDeviceID.Validate() != nil || i.Configuration.SidechatPolicy != CodexReadOnlySidechatV1 || i.Fork == nil || i.Continuation != nil || i.Retry != nil || len(i.Input.Skills) != 0 || len(i.Input.Attachments) != 0 || UniqueIDs([]ID{r.GenerationID, r.QuestionID, r.PreviousJobID, r.PreviousExecutionID, i.ExecutionID}) != nil {
		return SidechatUnavailable()
	}
	return nil
}
