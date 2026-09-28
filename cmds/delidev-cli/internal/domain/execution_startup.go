package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

type ExecutionStartupResultType string

const PRStartupRejectedResult ExecutionStartupResultType = "pr-startup-rejected"

// PRStartupRejectionProof carries only the original synchronized workspace
// phase and cleanup comparison facts. It is not a native completion or a grant
// to replay the input. The workspace boundary validates its original digests.
type PRStartupRejectionProof struct {
	JobID             ID        `json:"job_id"`
	ExecutionID       ID        `json:"execution_id"`
	SessionID         ID        `json:"session_id"`
	PreparationDigest string    `json:"preparation_digest"`
	ManifestDigest    string    `json:"manifest_digest"`
	TargetDigest      string    `json:"target_digest"`
	JournalDigest     string    `json:"journal_digest"`
	Reason            Code      `json:"reason"`
	StartedAt         time.Time `json:"started_at"`
	FinishedAt        time.Time `json:"finished_at"`
}

func PRStartupRejectionReason(code Code) bool {
	switch code {
	case Conflict, MissingInput, Unavailable, Canceled, ResourceExhausted, InvalidArgument:
		return true
	default:
		return false
	}
}

func StartupRejectionUncertain() *Error {
	return Fail(RecoveryRequired, "The original pre-native rejection requires reconciliation.", "Preserve the original assignment and startup journals; do not resend the input.")
}

func (p PRStartupRejectionProof) Validate() error {
	if UniqueIDs([]ID{p.JobID, p.ExecutionID, p.SessionID}) != nil || !PRStartupRejectionReason(p.Reason) || p.StartedAt.IsZero() || p.FinishedAt.IsZero() || p.FinishedAt.Before(p.StartedAt) {
		return StartupRejectionUncertain()
	}
	for _, value := range []string{p.PreparationDigest, p.ManifestDigest, p.TargetDigest, p.JournalDigest} {
		if !lowerDigest(value) {
			return StartupRejectionUncertain()
		}
	}
	return nil
}

type ExecutionStartupRejection struct {
	Version               uint32                     `json:"version"`
	Type                  ExecutionStartupResultType `json:"type"`
	ServerID              ID                         `json:"server_id"`
	DeviceID              ID                         `json:"device_id"`
	InstanceID            ID                         `json:"instance_id"`
	MachineID             ID                         `json:"machine_id"`
	InputID               ID                         `json:"input_id"`
	AccountID             ID                         `json:"account_id"`
	ConnectionID          ID                         `json:"connection_id"`
	AssignmentRevision    uint64                     `json:"assignment_revision,string"`
	AssignmentDigest      string                     `json:"assignment_digest"`
	AssignmentInputDigest string                     `json:"assignment_input_digest"`
	ConfigurationDigest   string                     `json:"configuration_digest"`
	Workspace             PRStartupRejectionProof    `json:"workspace"`
}

func (r ExecutionStartupRejection) Validate() error {
	if r.Version != 1 || r.Type != PRStartupRejectedResult || r.AssignmentRevision == 0 || r.Workspace.Validate() != nil {
		return StartupRejectionUncertain()
	}
	for _, id := range []ID{r.ServerID, r.DeviceID, r.InstanceID, r.MachineID, r.InputID, r.AccountID, r.ConnectionID} {
		if id.Validate() != nil {
			return StartupRejectionUncertain()
		}
	}
	for _, value := range []string{r.AssignmentDigest, r.AssignmentInputDigest, r.ConfigurationDigest} {
		if !lowerDigest(value) {
			return StartupRejectionUncertain()
		}
	}
	return nil
}

// ValidateAssignment compares the exact retained claimed bytes. A changed job
// result, configuration or current account cannot recreate original ownership.
func (r ExecutionStartupRejection) ValidateAssignment(jobID ID, revision uint64, raw json.RawMessage) error {
	var job Job
	var input ExecutionJobInput
	if r.Validate() != nil || r.Workspace.JobID != jobID || r.AssignmentRevision != revision || domainStartupDigest(raw) != r.AssignmentDigest || Decode(raw, &job) != nil || job.Validate() != nil || job.Type != ExecuteSessionJob || job.State != JobClaimed || job.InstanceID != r.InstanceID || job.AssignedDeviceID != "" && job.AssignedDeviceID != r.DeviceID || job.MachineID != r.MachineID || Decode(job.Input, &input) != nil || input.Validate() != nil || input.Continuation != nil {
		return StartupRejectionUncertain()
	}
	if input.ExecutionID != r.Workspace.ExecutionID || input.SessionID != r.Workspace.SessionID || input.MachineID != r.MachineID || input.InputID != r.InputID || input.AccountID != r.AccountID || input.ConnectionID != r.ConnectionID || input.ConfigurationDigest != r.ConfigurationDigest || domainStartupDigest(job.Input) != r.AssignmentInputDigest {
		return StartupRejectionUncertain()
	}
	return nil
}

func domainStartupDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
