package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// RespondApproval transmits one exact offered command/file decision. The caller
// must journal the response claim first. Delivery and closure are not proof that
// native code accepted the decision or installed a requested policy amendment.
func (c *Client) RespondApproval(ctx context.Context, responseID, interactionID, turnID domain.ID, decision ApprovalDecision) (InteractionStatus, error) {
	return c.respondApproval(ctx, responseID, interactionID, turnID, func(request *ApprovalRequest) (any, error) {
		if err := validateApprovalDecision(request, decision); err != nil {
			return nil, err
		}
		return struct {
			Decision ApprovalDecision `json:"decision"`
		}{decision}, nil
	})
}

// GrantPermissions preserves the native permission request and explicit grant
// scope. It cannot approve a command/file decision or create a broader sandbox.
func (c *Client) GrantPermissions(ctx context.Context, responseID, interactionID, turnID domain.ID, grant PermissionGrant) (InteractionStatus, error) {
	return c.respondApproval(ctx, responseID, interactionID, turnID, func(request *ApprovalRequest) (any, error) {
		if request == nil || request.Kind != PermissionsApproval {
			return nil, interactionConflict()
		}
		if err := grant.validate(request.Permissions); err != nil {
			return nil, err
		}
		return grant, nil
	})
}

func (c *Client) respondApproval(ctx context.Context, responseID, interactionID, turnID domain.ID, validate func(*ApprovalRequest) (any, error)) (InteractionStatus, error) {
	for _, id := range []domain.ID{responseID, interactionID, turnID} {
		if err := id.Validate(); err != nil {
			return InteractionStatus{}, err
		}
	}
	if err := c.acquireControl(ctx); err != nil {
		return InteractionStatus{}, err
	}
	defer func() { <-c.control }()
	if c.mode != ThreadProtocol || c.execution == nil {
		return InteractionStatus{}, unsupportedSettings()
	}
	if c.problem != nil {
		return InteractionStatus{}, c.problem
	}
	s := &c.execution.interactions
	owned := s.arrivals[interactionID]
	if owned == nil || owned.kind != ApprovalInteraction || owned.status.TurnID != turnID || c.execution.active != turnID || c.execution.paused || c.execution.interrupt != "" || owned.status.Closure != InteractionOpen || owned.status.Delivery != QuestionNotSent || owned.status.ResponseID != "" || s.responses[responseID] {
		return InteractionStatus{}, interactionConflict()
	}
	if len(s.responses) >= maxTrackedInteractions {
		return InteractionStatus{}, domain.Fail(domain.ResourceExhausted, "Native response tracking reached its bound.", "Reconcile pending responses before replacing the native connection.")
	}
	response, err := validate(owned.approval)
	if err != nil {
		return owned.status, err
	}
	raw, err := json.Marshal(response)
	if err != nil || len(raw) > maxAnswerBytes {
		return owned.status, domain.Fail(domain.ResourceExhausted, "The native approval response exceeds its bound.", "Keep the exact original decision within the supported response limit.")
	}
	if ctx.Err() != nil {
		return owned.status, domain.SafeError(ctx.Err())
	}
	s.responses[responseID] = true
	owned.status.ResponseID = responseID
	owned.answerDigest = sha256.Sum256(raw)
	// Use the validated immutable bytes for both commitment and pipe delivery.
	// No request command, path, rule, permission profile or response enters logs.
	err = c.wire.Reply(ctx, owned.native, json.RawMessage(raw))
	if err == nil {
		owned.status.Delivery = QuestionTransmitted
	} else if domain.SafeError(err).Code == domain.RecoveryRequired {
		owned.status.Delivery = QuestionDeliveryUncertain
		c.execution.paused = true
		c.problem = domain.Fail(domain.RecoveryRequired, "Native approval response delivery requires reconciliation.", "Retain the original response identity and inspect native state before another send; request closure alone does not confirm acceptance.")
		err = c.problem
	}
	if c.logger != nil {
		c.logger.InfoContext(ctx, "Codex approval response delivery observed", "owner_id", c.ownerID, "interaction_id", interactionID, "response_id", responseID, "turn_id", turnID, "approval_kind", owned.approval.Kind, "delivery", owned.status.Delivery)
	}
	return owned.status, err
}
