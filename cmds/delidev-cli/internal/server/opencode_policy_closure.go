package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// An automatic native policy closure is not another owner response. Verify
// original observed direct-response context without assigning a guessed rule
// owner or copying feedback into another request.
func validateOpenCodePolicyClosure(tx *store.Tx, input domain.ExecutionJobInput, current domain.ExecutionInteraction, proof *domain.OpenCodePolicyClosure) error {
	if input.Configuration.Harness != domain.OpenCode || current.OpenCode == nil || current.Type != domain.NativeApprovalInteraction || current.OpenCode.Permission == nil || current.Response != nil || current.Closure != domain.InteractionOpen || proof == nil || proof.Validate() != nil || proof.ProposalEventID != current.OpenCode.NativeEventID {
		return executionEventConflict()
	}
	if current.ApprovalResponse != nil && (current.ApprovalResponse.State != domain.ApprovalResponseQueued || current.ApprovalResponse.Claim != nil || current.ApprovalResponse.Delivery != nil || current.ApprovalResponse.Acceptance != nil) {
		return executionEventConflict()
	}
	exists, err := tx.HasOpenCodeInteractionEvent(input.SessionID, input.ExecutionID, proof.NativeEventID)
	if err != nil {
		return err
	}
	if exists {
		return executionEventConflict()
	}
	for _, reference := range proof.Sources {
		record, err := tx.Get(domain.InteractionKind, reference.InteractionID)
		if err != nil {
			return err
		}
		source, err := store.Decode[domain.ExecutionInteraction](record)
		if err != nil {
			return err
		}
		response := source.ApprovalResponse
		if domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(record.SessionID), record.SessionID != input.SessionID) ||
			source.ExecutionID != input.ExecutionID || source.NativeThreadID != current.NativeThreadID || source.NativeTurnID != current.NativeTurnID || source.Type != domain.NativeApprovalInteraction || source.OpenCode == nil || source.OpenCode.Permission == nil || source.NativeRequestID.Text != reference.NativeRequestID || source.NativeRequestID.Text == current.NativeRequestID.Text || source.Closure != domain.InteractionNativeClosed || response == nil || response.State != domain.ApprovalResponseAccepted || response.Input.OpenCode == nil || response.Input.OpenCode.Decision != proof.Decision || response.Acceptance == nil || response.Acceptance.OpenCode == nil || response.Acceptance.OpenCode.Validate(domain.NativeApprovalInteraction) != nil || response.Input.ValidateInteraction(source) != nil {
			return executionEventConflict()
		}
		if proof.Decision == domain.OpenCodePermissionAlways && len(source.OpenCode.Permission.Always) == 0 {
			return executionEventConflict()
		}
	}
	return nil
}
