package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishQuestionDelivery(tx *store.Tx, job store.Record, input domain.ExecutionJobInput, actor domain.ID, progress *domain.ExecutionProgress, event domain.ExecutionEvent) (bool, error) {
	u := event.QuestionResponse
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
	response := value.Response
	if r.SessionID != input.SessionID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeItemID != u.NativeItemID || value.Type != domain.UserQuestionInteraction || value.Closure != domain.InteractionOpen || response == nil || response.ID != u.ResponseID || response.State != domain.QuestionResponseClaimed || response.Delivery != nil || response.Claim == nil {
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
	case domain.QuestionNotSent:
		response.State = domain.QuestionResponseCanceled
	case domain.QuestionTransmitted:
		response.State = domain.QuestionResponseTransmitted
	case domain.QuestionDeliveryUncertain:
		response.State = domain.QuestionResponseUncertain
		uncertain = true
	default:
		return false, executionEventConflict()
	}
	if u.Delivery != domain.QuestionNotSent {
		if progress.UnconfirmedResponses >= domain.MaxExecutionInteractions {
			return false, executionEventConflict()
		}
		progress.UnconfirmedResponses++
	}
	response.Delivery = &domain.QuestionDeliveryObservation{State: u.Delivery, Sequence: event.Sequence}
	value.LastSequence = event.Sequence
	_, err = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
	return uncertain, err
}

func publishQuestionAcceptance(tx *store.Tx, job store.Record, input domain.ExecutionJobInput, actor domain.ID, progress *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	u := event.QuestionAcceptance
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
	response := value.Response
	if r.SessionID != input.SessionID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeItemID != u.NativeItemID || value.Type != domain.UserQuestionInteraction || response == nil || response.ID != u.ResponseID || response.Claim == nil || response.Delivery == nil || response.Acceptance != nil || (response.State != domain.QuestionResponseTransmitted && response.State != domain.QuestionResponseUncertain) || (response.Delivery.State != domain.QuestionTransmitted && response.Delivery.State != domain.QuestionDeliveryUncertain) || response.Delivery.Sequence >= event.Sequence || progress.UnconfirmedResponses == 0 {
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
	response.State = domain.QuestionResponseAccepted
	response.Acceptance = &domain.QuestionAcceptanceObservation{Evidence: u.Evidence, Sequence: event.Sequence}
	progress.UnconfirmedResponses--
	// This fact does not clear earlier Stop, transport, Worker-loss or other
	// recovery gates, and never changes native closure or cleanup evidence.
	value.LastSequence = event.Sequence
	_, err = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
	return err
}
