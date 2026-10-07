package server

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

// A positive startup rejection is independent of native completion. Invalid or
// contradictory proof falls through to the existing uncertain-input boundary.
func (s *Service) finishPRStartupRejection(tx *store.Tx, actor domain.Principal, record store.Record, job domain.Job, revision uint64, raw json.RawMessage) (store.Record, bool, error) {
	var rejected domain.ExecutionStartupRejection
	if domain.Decode(raw, &rejected) != nil || rejected.Validate() != nil {
		return store.Record{}, false, nil
	}
	return settlePRStartupRejection(tx, record, job, revision, rejected, "")
}

// A recovery ID permits only the independently validated original inspection.
// The original assignment remains immutable; current Worker ownership never
// rewrites that old instance or acquires native publication authority.
func settlePRStartupRejection(tx *store.Tx, record store.Record, job domain.Job, revision uint64, rejected domain.ExecutionStartupRejection, recoveryID domain.ID) (store.Record, bool, error) {
	input, sr, session, err := nativeExecutionScope(tx, record, job)
	if err != nil {
		return store.Record{}, true, err
	}
	if input.Continuation != nil || session.Workspace != domain.Worktree || session.CurrentExecution != nil || session.Execution != nil || session.StartupRejection != nil || session.PendingSteerID != "" {
		return store.Record{}, false, nil
	}
	if recoveryID == "" {
		if job.State != domain.JobClaimed || session.Recovery != domain.NoRecovery || session.Outcome != domain.ExecutionNotStarted || session.ExecutionRecoveryJobID != "" {
			return store.Record{}, false, nil
		}
	} else if job.State != domain.JobUncertain || job.Problem == nil || job.Problem.Code != domain.RecoveryRequired || len(job.Output) != 0 || session.ExecutionRecoveryJobID != recoveryID || session.Recovery != domain.Reconciling || session.Outcome != domain.ExecutionFailed ||
		domain.OwnershipBlocks(domain.OwnershipDevice, "", job.AssignedDeviceID != rejected.DeviceID) {
		return store.Record{}, false, nil
	}
	assignment, err := tx.JobAssignment(record.ID)
	if err != nil {
		return store.Record{}, true, err
	}
	if assignment.SessionID != sr.ID || assignment.ProjectID != sr.ProjectID || rejected.ValidateAssignment(assignment.ID, assignment.Revision, assignment.Data) != nil ||
		domain.OwnershipBlocks(domain.OwnershipInstance, "", rejected.InstanceID != job.InstanceID) ||
		domain.OwnershipBlocks(domain.OwnershipMachine, "", rejected.MachineID != job.MachineID) {
		return store.Record{}, false, nil
	}
	var original domain.Job
	if domain.Decode(assignment.Data, &original) != nil || !bytes.Equal(original.Input, job.Input) || recoveryID == "" && rejected.ValidateAssignment(record.ID, record.Revision, record.Data) != nil {
		return store.Record{}, false, nil
	}
	_, err = tx.ExecutionGrantForJob(record.ID)
	if err == nil {
		return store.Record{}, false, nil
	}
	if domain.SafeError(err).Code != domain.PermissionDenied {
		return store.Record{}, true, err
	}
	mr, err := tx.Get(domain.MachineKind, job.MachineID)
	if err != nil {
		return store.Record{}, true, err
	}
	machine, err := store.Decode[domain.Machine](mr)
	if err != nil {
		return store.Record{}, true, err
	}
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(input.Preparation, &preparation) != nil || domain.Decode(input.Manifest, &manifest) != nil ||
		domain.OwnershipBlocks(domain.OwnershipMachine, "", preparation.MachineID != input.MachineID) ||
		workspace.ValidatePRStartupRejection(preparation, manifest, rejected.Workspace, machine.OS) != nil {
		return store.Record{}, false, nil
	}
	ir, err := tx.Get(domain.QueueKind, input.InputID)
	if err != nil {
		return store.Record{}, true, err
	}
	queued, err := store.Decode[domain.QueuedInput](ir)
	if err != nil {
		return store.Record{}, true, err
	}
	if ir.SessionID != sr.ID || ir.ProjectID != sr.ProjectID || (recoveryID == "" && queued.Delivery != domain.InputClaimed || recoveryID != "" && queued.Delivery != domain.InputUncertain) || queued.ExecutionID != input.ExecutionID || queued.NativeRequestID != input.TurnRequestID || queued.Prompt != input.Input.Prompt || queued.Mode != input.Input.Mode || session.PendingInputs == 0 || session.PendingInputBytes < uint64(len(queued.Prompt)) {
		return store.Record{}, false, nil
	}
	queued.Delivery = domain.InputRejected
	session.PendingInputs--
	session.PendingInputBytes -= uint64(len(queued.Prompt))
	session.StartupRejection = &rejected
	session.ActiveExecutionID, session.NextExecutionIntent = "", ""
	session.Dispatch, session.Recovery, session.Outcome = domain.DispatchPaused, domain.NoRecovery, domain.ExecutionNotStarted
	if session.Archive == domain.ArchivePending {
		session.Archive = domain.Archived
	}
	if session.Problem == nil || recoveryID != "" && session.Problem.Code == domain.RecoveryRequired {
		session.Problem = startupRejectionProblem(rejected)
	}
	now := time.Now().UTC()
	job.State, job.FinishedAt, job.Problem = domain.JobFailed, &now, startupRejectionProblem(rejected)
	if rejected.Workspace.Reason == domain.Canceled {
		job.State = domain.JobCanceled
	}
	job.Output, err = json.Marshal(rejected)
	if err != nil {
		return store.Record{}, true, err
	}
	if _, err = tx.Put(domain.QueueKind, ir.ID, ir.Revision, sr.ID, sr.ProjectID, queued); err != nil {
		return store.Record{}, true, err
	}
	if _, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return store.Record{}, true, err
	}
	saved, err := tx.PutJob(record.ID, revision, record.SessionID, record.ProjectID, job)
	return store.Record{ID: saved.ID}, true, err
}

func startupRejectionProblem(rejected domain.ExecutionStartupRejection) *domain.Error {
	return domain.Fail(rejected.Workspace.Reason, "The PR input was rejected before the native agent started.", "Inspect the original PR and Worker Git access before a fresh authorized fix. This retained input will not be sent again.")
}
