package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func validateOpenCodeReplyAcceptance(tx *store.Tx, input domain.ExecutionJobInput, value domain.ExecutionInteraction, evidence *domain.OpenCodeReplyEvidence) error {
	if value.OpenCode == nil {
		if evidence != nil || input.Configuration.Harness == domain.OpenCode {
			return executionEventConflict()
		}
		return nil
	}
	if input.Configuration.Harness != domain.OpenCode || input.Version != 4 && !domain.ValidNativeVersionMetadata(input.Installation.Version) || evidence == nil || evidence.Validate(value.Type) != nil || evidence.ProposalEventID != value.OpenCode.NativeEventID || evidence.NativeRequestID != value.NativeRequestID.Text || value.Closure != domain.InteractionOpen {
		return executionEventConflict()
	}
	var question *domain.OpenCodeQuestionResponse
	var permission *domain.OpenCodePermissionResponse
	switch value.Type {
	case domain.UserQuestionInteraction:
		if value.Response == nil || value.ApprovalResponse != nil || value.Response.Input.ValidateInteraction(value) != nil || value.Response.Delivery == nil || value.Response.Delivery.State != domain.QuestionTransmitted {
			return executionEventConflict()
		}
		question = value.Response.Input.OpenCode
	case domain.NativeApprovalInteraction:
		if value.ApprovalResponse == nil || value.Response != nil || value.ApprovalResponse.Input.ValidateInteraction(value) != nil || value.ApprovalResponse.Delivery == nil || value.ApprovalResponse.Delivery.State != domain.ApprovalTransmitted {
			return executionEventConflict()
		}
		permission = value.ApprovalResponse.Input.OpenCode
	default:
		return executionEventConflict()
	}
	digest, err := domain.OpenCodeResponseDigest(question, permission)
	if err != nil || digest != evidence.BodyDigest {
		return executionEventConflict()
	}
	exists, err := tx.HasOpenCodeInteractionEvent(input.SessionID, input.ExecutionID, evidence.NativeEventID)
	if err != nil {
		return err
	}
	if exists {
		return executionEventConflict()
	}
	return nil
}
