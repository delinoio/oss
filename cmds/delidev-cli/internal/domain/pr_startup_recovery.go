package domain

import "encoding/json"

// Startup is an exclusive comparison profile, never a native checkpoint or a
// request to repeat the preflight. Common recovery fields retain the original
// assignment/device even when another process now owns the same Worker.
type PRStartupRecoveryReference struct {
	ExecutionID ID `json:"execution_id"`
	InputID     ID `json:"input_id"`
}

func (r ExecutionRecoveryRequest) validatePRStartupRecovery() error {
	if r.Startup == nil || r.Version != 1 || r.AssignmentRevision == 0 || r.Harness != "" || r.OpenCode != nil || r.Claude != nil || r.Completion != (ExecutionCompletion{}) || r.HistoryExecutionID != "" || r.InputMode != "" || r.PromptDigest != "" || len(r.AcceptedInputs) != 0 {
		return StartupRejectionUncertain()
	}
	for _, id := range []ID{r.ServerID, r.DeviceID, r.InstanceID, r.JobID, r.SessionID, r.MachineID, r.AccountID, r.ConnectionID, r.Startup.ExecutionID, r.Startup.InputID} {
		if id.Validate() != nil {
			return StartupRejectionUncertain()
		}
	}
	if UniqueIDs([]ID{r.JobID, r.SessionID, r.Startup.ExecutionID, r.Startup.InputID}) != nil {
		return StartupRejectionUncertain()
	}
	for _, value := range []string{r.AssignmentDigest, r.AssignmentInputDigest, r.ConfigurationDigest} {
		if !lowerDigest(value) {
			return StartupRejectionUncertain()
		}
	}
	for _, raw := range []json.RawMessage{r.Preparation, r.Manifest} {
		if len(raw) == 0 || len(raw) > 1<<20 || !json.Valid(raw) {
			return StartupRejectionUncertain()
		}
	}
	return nil
}

type PRStartupRecoveryEvidence struct {
	Version   uint32                    `json:"version"`
	JobID     ID                        `json:"job_id"`
	ReportID  ID                        `json:"report_id"`
	Rejection ExecutionStartupRejection `json:"rejection"`
}

func (e PRStartupRecoveryEvidence) Validate(expected ExecutionRecoveryRequest) error {
	r := e.Rejection
	if expected.Startup == nil || expected.Validate() != nil || e.Version != 1 || e.JobID != expected.JobID || e.ReportID.Validate() != nil || r.Validate() != nil {
		return StartupRejectionUncertain()
	}
	if OwnershipBlocks(OwnershipResource, expected.JobID, r.ServerID != expected.ServerID || r.DeviceID != expected.DeviceID || r.InstanceID != expected.InstanceID || r.MachineID != expected.MachineID || r.AccountID != expected.AccountID || r.ConnectionID != expected.ConnectionID || r.Workspace.SessionID != expected.SessionID) || r.InputID != expected.Startup.InputID || r.ConfigurationDigest != expected.ConfigurationDigest || r.AssignmentRevision != expected.AssignmentRevision || r.AssignmentDigest != expected.AssignmentDigest || r.AssignmentInputDigest != expected.AssignmentInputDigest || r.Workspace.JobID != expected.JobID || r.Workspace.ExecutionID != expected.Startup.ExecutionID {
		return StartupRejectionUncertain()
	}
	return nil
}
