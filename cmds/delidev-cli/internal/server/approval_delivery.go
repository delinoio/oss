package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishApprovalDelivery(tx *store.Tx, job store.Record, input domain.ExecutionJobInput, actor domain.ID, progress *domain.ExecutionProgress, event domain.ExecutionEvent) (bool, error) {
	u := event.ApprovalResponse
	if u == nil {
		return false, executionEventConflict()
	}
	r, err := tx.Get(domain.InteractionKind, u.InteractionID)
	if err != nil {
		return false, err
	}
	value, err := store.Decode[domain.ExecutionInteraction](r)
	if err != nil {
		return false, err
	}
	response := value.ApprovalResponse
	if r.SessionID != input.SessionID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeItemID != u.NativeItemID || value.Type != domain.NativeApprovalInteraction || value.Closure != domain.InteractionOpen || response == nil || response.ID != u.ResponseID || response.State != domain.ApprovalResponseClaimed || response.Delivery != nil || response.Claim == nil {
		return false, executionEventConflict()
	}
	claimedJob, err := store.Decode[domain.Job](job)
	if err != nil {
		return false, err
	}
	claim := response.Claim
	if claim.ID != u.ClaimID || claim.JobID != job.ID || claim.InstanceID != claimedJob.InstanceID || claim.MachineID != input.MachineID || claim.DeviceID != actor {
		return false, executionEventConflict()
	}
	uncertain := false
	switch u.Delivery {
	case domain.ApprovalNotSent:
		response.State = domain.ApprovalResponseCanceled
	case domain.ApprovalTransmitted:
		response.State = domain.ApprovalResponseTransmitted
	case domain.ApprovalDeliveryUncertain:
		response.State = domain.ApprovalResponseUncertain
		uncertain = true
	default:
		return false, executionEventConflict()
	}
	if u.Delivery != domain.ApprovalNotSent {
		if progress.UnconfirmedResponses >= domain.MaxExecutionInteractions {
			return false, executionEventConflict()
		}
		progress.UnconfirmedResponses++
	}
	response.Delivery = &domain.ApprovalDeliveryObservation{State: u.Delivery, Sequence: event.Sequence}
	value.LastSequence = event.Sequence
	_, err = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
	return uncertain, err
}
