package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Successful single-use approval execution is committed with its tool record.
// This avoids a second publication whose loss could strand already retained
// proof. Generic completion, repeated callbacks and remembered policies cannot
// satisfy this pinned, original-response-specific evidence profile.
func publishSingleUseApprovalExecution(tx *store.Tx, job store.Record, input domain.ExecutionJobInput, actor domain.ID, progress *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	if event.Kind != domain.ExecutionToolCompleted || input.Configuration.Harness != domain.Codex || input.Installation.Version != domain.CodexProtocolVersion || event.Tool.Snapshot.Status != domain.ToolCompleted {
		return nil
	}
	ids, err := tx.ExecutionItemInteractions(input.ExecutionID, event.NativeThreadID, event.NativeTurnID, event.Tool.NativeID)
	if err != nil {
		return err
	}
	if len(ids) != 1 {
		return nil
	}
	r, err := tx.Get(domain.InteractionKind, ids[0])
	if err != nil {
		return err
	}
	value, err := store.Decode[domain.ExecutionInteraction](r)
	if err != nil {
		return err
	}
	response := value.ApprovalResponse
	if r.SessionID != input.SessionID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeItemID != event.Tool.NativeID || value.Type != domain.NativeApprovalInteraction || value.Closure != domain.InteractionNativeClosed || value.Approval == nil || value.Approval.Harness != domain.Codex || value.Approval.Version != domain.CodexProtocolVersion || value.Approval.Codex == nil || response == nil || response.Input.Decision == nil || response.Input.Decision.Kind != domain.CodexApprovalAccept || response.Input.Validate(value.Approval) != nil || response.Claim == nil || response.Delivery == nil || response.Acceptance != nil || (response.State != domain.ApprovalResponseTransmitted && response.State != domain.ApprovalResponseUncertain) || (response.Delivery.State != domain.ApprovalTransmitted && response.Delivery.State != domain.ApprovalDeliveryUncertain) || response.Delivery.Sequence >= event.Sequence || value.LastSequence >= event.Sequence {
		return nil
	}
	tool, approval := event.Tool.Snapshot, value.Approval.Codex
	var evidence domain.ApprovalAcceptanceEvidence
	switch approval.Kind {
	case domain.CodexCommandApproval:
		request, command := approval.Command, tool.Command
		if request == nil || tool.Kind != domain.CommandTool || command == nil || request.Kind != domain.CodexExecuteCommandApproval || request.ApprovalID != nil || request.Network != nil || request.Command == nil || request.Cwd == nil || *request.Command != command.Command || *request.Cwd != command.Cwd || command.Source != domain.ExecStartupCommand || command.ExitCode == nil || *command.ExitCode != 0 {
			return nil
		}
		evidence = domain.NativeApprovedCommand
	case domain.CodexFileApproval:
		if tool.Kind != domain.PatchTool || len(tool.Changes) == 0 {
			return nil
		}
		evidence = domain.NativeApprovedPatch
	default:
		return nil
	}
	retained, err := tx.Get(domain.MessageKind, event.Tool.ID)
	if err != nil {
		return err
	}
	message, err := store.Decode[domain.ExecutionMessage](retained)
	if err != nil {
		return err
	}
	if message.FirstSequence >= value.FirstSequence {
		return nil
	}
	claimedJob, err := store.Decode[domain.Job](job)
	if err != nil {
		return err
	}
	claim := response.Claim
	if claim.JobID != job.ID || claim.InstanceID != claimedJob.InstanceID || claim.MachineID != input.MachineID || claim.DeviceID != actor || progress.UnconfirmedResponses == 0 {
		return executionEventConflict()
	}
	response.State = domain.ApprovalResponseAccepted
	response.Acceptance = &domain.ApprovalAcceptanceObservation{Evidence: evidence, Sequence: event.Sequence}
	progress.UnconfirmedResponses--
	value.LastSequence = event.Sequence
	_, err = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
	return err
}
