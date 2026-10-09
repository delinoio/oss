// SPDX-License-Identifier: Apache-2.0
package domain

// Progress is descriptive. No transition here accepts input or proves cleanup.
type StartupWorkspaceOperation int32
type StartupProgressState int32

const (
	StartupWorkspaceSetup     StartupWorkspaceOperation = 1
	StartupWorkspaceInspect   StartupWorkspaceOperation = 2
	StartupWorkspaceClone     StartupWorkspaceOperation = 3
	StartupWorkspaceReference StartupWorkspaceOperation = 4
	StartupWorkspaceCheckout  StartupWorkspaceOperation = 5
	StartupWorkspaceVerify    StartupWorkspaceOperation = 6
	StartupWorkspacePublish   StartupWorkspaceOperation = 7
	StartupProgressRunning    StartupProgressState      = 1
	StartupProgressCompleted  StartupProgressState      = 2
)

type StartupProgressStep struct {
	WorkspaceOperation StartupWorkspaceOperation `json:"workspace_operation,omitempty"`
	NativePhase        ExecutionStartupPhase     `json:"native_phase,omitempty"`
	RepositoryID       ID                        `json:"repository_id,omitempty"`
	RepositoryOrdinal  uint32                    `json:"repository_ordinal,omitempty"`
	RepositoryCount    uint32                    `json:"repository_count,omitempty"`
	State              StartupProgressState      `json:"state,omitempty"`
	Sequence           uint64                    `json:"sequence,omitempty"`
}

func (s StartupProgressStep) SameOperation(other StartupProgressStep) bool {
	return s.WorkspaceOperation == other.WorkspaceOperation && s.NativePhase == other.NativePhase && s.RepositoryID == other.RepositoryID && s.RepositoryOrdinal == other.RepositoryOrdinal && s.RepositoryCount == other.RepositoryCount
}
func (s StartupProgressStep) Validate() error {
	if s.Sequence == 0 || s.Sequence > 806 || (s.State != StartupProgressRunning && s.State != StartupProgressCompleted) || (s.NativePhase != 0 && s.WorkspaceOperation != 0) || (s.NativePhase == 0 && s.WorkspaceOperation == 0) {
		return StartupProgressRejected()
	}
	if s.NativePhase != 0 {
		if s.NativePhase < StartupResolve || s.NativePhase > StartupExecution || s.RepositoryID != "" || s.RepositoryOrdinal != 0 || s.RepositoryCount != 0 {
			return StartupProgressRejected()
		}
		return nil
	}
	if s.WorkspaceOperation < StartupWorkspaceSetup || s.WorkspaceOperation > StartupWorkspacePublish || s.RepositoryCount > 100 {
		return StartupProgressRejected()
	}
	if s.RepositoryID != "" {
		if s.RepositoryID.Validate() != nil || s.RepositoryOrdinal == 0 || s.RepositoryOrdinal > s.RepositoryCount || s.WorkspaceOperation < StartupWorkspaceInspect || s.WorkspaceOperation > StartupWorkspaceCheckout {
			return StartupProgressRejected()
		}
	} else if s.RepositoryOrdinal != 0 || s.RepositoryCount != 0 || (s.WorkspaceOperation != StartupWorkspaceSetup && s.WorkspaceOperation != StartupWorkspaceVerify && s.WorkspaceOperation != StartupWorkspacePublish) {
		return StartupProgressRejected()
	}
	return nil
}

type StartupProgressAttempt struct {
	JobID              ID                    `json:"job_id"`
	ExecutionID        ID                    `json:"execution_id,omitempty"`
	AssignmentRevision uint64                `json:"assignment_revision"`
	MachineID          ID                    `json:"machine_id"`
	InstanceID         ID                    `json:"instance_id"`
	DeviceID           ID                    `json:"device_id"`
	ServerEpoch        ID                    `json:"server_epoch"`
	LastSequence       uint64                `json:"last_sequence"`
	Steps              []StartupProgressStep `json:"steps"`
}
type SessionStartupProgress struct {
	Workspace *StartupProgressAttempt `json:"workspace,omitempty"`
	Native    *StartupProgressAttempt `json:"native,omitempty"`
}

func StartupProgressRejected() *Error {
	return Fail(Conflict, "Startup progress does not match the original active operation.", "Read the original session state; progress grants no execution authority.")
}

// Apply keeps one observation per original applicable operation, including
// pending entries supplied by the server from the immutable assignment.
func (a *StartupProgressAttempt) Apply(s StartupProgressStep) error {
	if s.Validate() != nil || len(a.Steps) > 403 {
		return StartupProgressRejected()
	}
	for i, prior := range a.Steps {
		if !prior.SameOperation(s) {
			continue
		}
		if s.Sequence == prior.Sequence && s == prior {
			return nil
		}
		if s.Sequence <= a.LastSequence || prior.State == StartupProgressCompleted {
			return StartupProgressRejected()
		}
		a.Steps[i] = s
		a.LastSequence = s.Sequence
		return nil
	}
	return StartupProgressRejected()
}
