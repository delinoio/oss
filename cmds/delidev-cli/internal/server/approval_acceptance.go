package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishApprovalAcceptance(tx *store.Tx, job store.Record, input domain.ExecutionJobInput, actor domain.ID, progress *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	u := event.ApprovalAcceptance
	if u == nil {
		return executionEventConflict()
	}
	r, err := tx.Get(domain.InteractionKind, u.InteractionID)
	if err != nil {
		return err
	}
	value, err := store.Decode[domain.ExecutionInteraction](r)
	if err != nil {
		return err
	}
	response := value.ApprovalResponse
	if r.SessionID != input.SessionID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeItemID != u.NativeItemID || value.Type != domain.NativeApprovalInteraction || value.Approval == nil || value.Approval.Codex == nil || value.Approval.Codex.Kind != domain.CodexPermissionsApproval || response == nil || response.Input.Grant == nil || response.Input.Validate(value.Approval) != nil || response.ID != u.ResponseID || response.Claim == nil || response.Delivery == nil || response.Acceptance != nil || (response.State != domain.ApprovalResponseTransmitted && response.State != domain.ApprovalResponseUncertain) || (response.Delivery.State != domain.ApprovalTransmitted && response.Delivery.State != domain.ApprovalDeliveryUncertain) || response.Delivery.Sequence >= event.Sequence || progress.UnconfirmedResponses == 0 {
		return executionEventConflict()
	}
	claimedJob, err := store.Decode[domain.Job](job)
	if err != nil {
		return err
	}
	claim := response.Claim
	if claim.ID != u.ClaimID || claim.JobID != job.ID || claim.InstanceID != claimedJob.InstanceID || claim.MachineID != input.MachineID || claim.DeviceID != actor {
		return executionEventConflict()
	}
	response.State = domain.ApprovalResponseAccepted
	response.Acceptance = &domain.ApprovalAcceptanceObservation{Evidence: u.Evidence, Sequence: event.Sequence}
	progress.UnconfirmedResponses--
	// This fact does not clear earlier Stop, transport, Worker-loss or other
	// recovery gates, and never changes native closure or cleanup evidence.
	value.LastSequence = event.Sequence
	_, err = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
	return err
}
