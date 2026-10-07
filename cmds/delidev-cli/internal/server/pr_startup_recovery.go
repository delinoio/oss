package server

import (
	"bytes"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func prStartupRecoveryRequest(tx *store.Tx, serverID domain.ID, sr store.Record, session domain.Session) (domain.ExecutionRecoveryRequest, error) {
	var result domain.ExecutionRecoveryRequest
	fail := domain.StartupRejectionUncertain
	if session.InitialExecution == nil || session.CurrentExecution != nil || session.Execution != nil || session.StartupRejection != nil || session.Workspace != domain.Worktree || session.Preparation == nil || session.Preparation.State != domain.PreparationReady || session.PendingSteerID != "" || session.Archive == domain.Archived || session.Outcome != domain.ExecutionFailed || session.Dispatch != domain.DispatchPaused || (session.Recovery != domain.NeedsRecovery && session.Recovery != domain.Reconciling) || session.ActiveExecutionID != session.InitialExecution.ID {
		return result, fail()
	}
	original, err := tx.SessionExecutionJob(sr.ID, session.InitialExecution.ID)
	if err != nil {
		return result, err
	}
	job, err := store.Decode[domain.Job](original)
	if err != nil || job.Type != domain.ExecuteSessionJob || job.State != domain.JobUncertain || job.Problem == nil || job.Problem.Code != domain.RecoveryRequired || len(job.Output) != 0 ||
		domain.OwnershipBlocks(domain.OwnershipMachine, "", job.MachineID != session.MachineID) ||
		job.AssignedDeviceID.Validate() != nil {
		return result, fail()
	}
	assignment, err := tx.JobAssignment(original.ID)
	if err != nil {
		return result, err
	}
	claim, err := store.Decode[domain.Job](assignment)
	var input domain.ExecutionJobInput
	if err != nil || claim.Type != domain.ExecuteSessionJob || claim.State != domain.JobClaimed ||
		domain.OwnershipBlocks(domain.OwnershipInstance, "", claim.InstanceID != job.InstanceID) ||
		domain.OwnershipBlocks(domain.OwnershipDevice, "", claim.AssignedDeviceID != job.AssignedDeviceID) ||
		domain.OwnershipBlocks(domain.OwnershipMachine, "", claim.MachineID != job.MachineID) ||
		assignment.SessionID != sr.ID || assignment.ProjectID != sr.ProjectID || !bytes.Equal(claim.Input, job.Input) || domain.Decode(claim.Input, &input) != nil || input.Validate() != nil || input.Continuation != nil || !session.OwnsExecution(input) {
		return result, fail()
	}
	if _, err := tx.ExecutionGrantForJob(original.ID); err == nil {
		return result, fail()
	} else if domain.SafeError(err).Code != domain.PermissionDenied {
		return result, err
	}
	dr, err := tx.Get(domain.DeviceKind, claim.AssignedDeviceID)
	if err != nil {
		return result, err
	}
	device, err := store.Decode[domain.Device](dr)
	if err != nil || domain.OwnershipBlocks(domain.OwnershipActor, "", device.Type != domain.WorkerDevice) || device.Revoked ||
		domain.OwnershipBlocks(domain.OwnershipMachine, "", device.MachineID != job.MachineID) {
		return result, fail()
	}
	_, machine, err := activeMachine(tx, job.MachineID)
	if err != nil {
		return result, err
	}
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(input.Preparation, &preparation) != nil || domain.Decode(input.Manifest, &manifest) != nil || preparation.Type != domain.Worktree || preparation.SessionID != sr.ID ||
		domain.OwnershipBlocks(domain.OwnershipMachine, "", preparation.MachineID != session.MachineID) ||
		len(preparation.Repositories) != 1 || preparation.Repositories[0].PRTarget == nil || workspace.ValidateResult(preparation, manifest, machine.OS) != nil {
		return result, fail()
	}
	ir, err := tx.Get(domain.QueueKind, input.InputID)
	if err != nil {
		return result, err
	}
	queued, err := store.Decode[domain.QueuedInput](ir)
	if err != nil || ir.SessionID != sr.ID || ir.ProjectID != sr.ProjectID || queued.Delivery != domain.InputUncertain || queued.ExecutionID != input.ExecutionID || queued.NativeRequestID != input.TurnRequestID || queued.Prompt != input.Input.Prompt || queued.Mode != input.Input.Mode || session.PendingInputs == 0 || session.PendingInputBytes < uint64(len(queued.Prompt)) {
		return result, fail()
	}
	result = domain.ExecutionRecoveryRequest{
		Version: 1, Startup: &domain.PRStartupRecoveryReference{ExecutionID: input.ExecutionID, InputID: input.InputID},
		ServerID: serverID, DeviceID: claim.AssignedDeviceID, InstanceID: claim.InstanceID, JobID: original.ID, SessionID: sr.ID, MachineID: job.MachineID,
		AssignmentRevision: assignment.Revision, AssignmentDigest: continuationDigest(assignment.Data), AssignmentInputDigest: continuationDigest(claim.Input),
		ConfigurationDigest: input.ConfigurationDigest, AccountID: input.AccountID, ConnectionID: input.ConnectionID, Preparation: input.Preparation, Manifest: input.Manifest,
	}
	return result, result.Validate()
}

func validatePRStartupRecoveryResult(tx *store.Tx, record store.Record, job domain.Job, expected domain.ExecutionRecoveryRequest, raw []byte) error {
	var evidence domain.PRStartupRecoveryEvidence
	if domain.Decode(raw, &evidence) != nil || evidence.Validate(expected) != nil || expected.JobID != job.ParentID || expected.SessionID != record.SessionID ||
		domain.OwnershipBlocks(domain.OwnershipMachine, "", expected.MachineID != job.MachineID) ||
		domain.OwnershipBlocks(domain.OwnershipDevice, "", job.AssignedDeviceID != expected.DeviceID) {
		return domain.StartupRejectionUncertain()
	}
	sr, session, err := sessionRecord(tx, record.SessionID)
	if err != nil {
		return err
	}
	if session.ExecutionRecoveryJobID != record.ID || session.Recovery != domain.Reconciling {
		return domain.StartupRejectionUncertain()
	}
	fresh, err := prStartupRecoveryRequest(tx, expected.ServerID, sr, session)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(expected)
	b, _ := json.Marshal(fresh)
	if !bytes.Equal(a, b) {
		return domain.StartupRejectionUncertain()
	}
	assignment, err := tx.JobAssignment(expected.JobID)
	if err != nil {
		return err
	}
	if evidence.Rejection.ValidateAssignment(assignment.ID, assignment.Revision, assignment.Data) != nil {
		return domain.StartupRejectionUncertain()
	}
	_, machine, err := activeMachine(tx, expected.MachineID)
	if err != nil {
		return err
	}
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(expected.Preparation, &preparation) != nil || domain.Decode(expected.Manifest, &manifest) != nil || workspace.ValidatePRStartupRejection(preparation, manifest, evidence.Rejection.Workspace, machine.OS) != nil {
		return domain.StartupRejectionUncertain()
	}
	return nil
}

func finishPRStartupRecovery(tx *store.Tx, record store.Record, job domain.Job) error {
	var evidence domain.PRStartupRecoveryEvidence
	if domain.Decode(job.Output, &evidence) != nil {
		return domain.StartupRejectionUncertain()
	}
	original, err := tx.Get(domain.JobKind, job.ParentID)
	if err != nil {
		return err
	}
	previous, err := store.Decode[domain.Job](original)
	if err != nil {
		return err
	}
	_, handled, err := settlePRStartupRejection(tx, original, previous, original.Revision, evidence.Rejection, record.ID)
	if err != nil {
		return err
	}
	if !handled {
		return domain.StartupRejectionUncertain()
	}
	return nil
}
