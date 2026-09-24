package codex

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// InspectInteractionResponse checks the native conversation after a possibly
// delivered response. Its authority is the original arrival and response on
// this connection, never an ID reconstructed after process replacement. Native
// history currently omits the exact question/permission outputs: absence,
// request closure and generic historical tool completion cannot confirm them.
// Only dedicated live evidence observed by NextEvent confirms a reply;
// that original event must still pass the caller's durable publication path.
// Inspection never consumes events, replies again or clears a pause/problem.
func (c *Client) InspectInteractionResponse(ctx context.Context, responseID, interactionID domain.ID) (result InteractionStatus, returned error) {
	for _, id := range []domain.ID{responseID, interactionID} {
		if err := id.Validate(); err != nil {
			return result, err
		}
	}
	defer func() {
		if c.logger != nil {
			code := "ok"
			if returned != nil {
				code = string(domain.SafeError(returned).Code)
			}
			c.logger.InfoContext(ctx, "Codex native interaction response inspection", "owner_id", c.ownerID, "interaction_id", interactionID, "response_id", responseID, "delivery", result.Delivery, "accepted", result.Accepted, "code", code)
		}
	}()
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, pending, err := c.inspectInteractionResponse(bounded, responseID, interactionID)
	if err != nil || pending == nil {
		return result, err
	}
	// Native control and the caller's publication lock must be free while the
	// ordinary event reader processes closure, output and terminal observations.
	// Never pump or synthesize these events inside an inspection operation.
	select {
	case <-pending.ready:
		return pending.status, nil
	case <-bounded.Done():
		// Retain proof that raced the deadline/process close; closing ready
		// publishes the immutable snapshot before any subsequent cleanup.
		select {
		case <-pending.ready:
			return pending.status, nil
		default:
			return result, responseInspectionUncertain()
		}
	}
}

func (c *Client) inspectInteractionResponse(ctx context.Context, responseID, interactionID domain.ID) (result InteractionStatus, pending *interactionAcceptance, returned error) {
	if err := c.acquireControl(ctx); err != nil {
		return result, nil, err
	}
	defer func() { <-c.control }()
	if c.mode != ThreadProtocol || c.execution == nil {
		return result, nil, unsupportedSettings()
	}
	owned := c.execution.interactions.arrivals[interactionID]
	if owned == nil || owned.status.ResponseID != responseID || !c.execution.interactions.responses[responseID] || (owned.status.Delivery != QuestionTransmitted && owned.status.Delivery != QuestionDeliveryUncertain) {
		return result, nil, interactionConflict()
	}
	defer func() {
		result = owned.status
		if returned != nil && domain.SafeError(returned).Code == domain.RecoveryRequired {
			c.execution.paused = true
			if c.problem == nil {
				c.problem = responseInspectionUncertain()
			}
		}
	}()
	if _, err := c.inspectRetainedTurnLocked(ctx, owned.status.TurnID); err != nil {
		return result, nil, responseInspectionUncertain()
	}
	if err := ctx.Err(); err != nil {
		return result, nil, domain.SafeError(err)
	}
	if c.wire.Err() != nil {
		return result, nil, responseInspectionUncertain()
	}
	if owned.status.Accepted {
		return result, nil, nil
	}
	if owned.status.Delivery != QuestionDeliveryUncertain || owned.acceptance == nil {
		return result, nil, responseInspectionUncertain()
	}
	c.execution.paused = true
	if c.problem == nil {
		c.problem = responseInspectionUncertain()
	}
	return result, owned.acceptance, nil
}

func responseInspectionUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "Native response acceptance remains uncertain after inspection.", "Retain the original response and its pending evidence; do not resend it or resume subsequent input without reconciliation.")
}

// Verify the complete latest turn and exact retained input order/digests inside
// two matching root reads. These reads establish scope, not interaction-answer
// evidence, turn completion publication or permission for another mutation.
func (c *Client) inspectRetainedTurnLocked(ctx context.Context, turnID domain.ID) (trackedTurn, error) {
	if err := c.inspectRetainedThreadLocked(ctx); err != nil {
		return trackedTurn{}, err
	}
	response, err := c.wire.Call(ctx, domain.NewID(), "thread/turns/list", struct {
		ThreadID      domain.ID `json:"threadId"`
		Limit         int       `json:"limit"`
		SortDirection string    `json:"sortDirection"`
		ItemsView     string    `json:"itemsView"`
	}{c.thread, 1, "desc", "full"})
	if err != nil || response.ErrorCode != nil {
		return trackedTurn{}, turnUncertain()
	}
	turn, inputs, err := decodeLatestTurnInputs(response.Result)
	retained, known := c.execution.turns[turnID]
	if err != nil || !known || turn.ID != retained.Turn.ID || (retained.Turn.Status.terminal() && turn.Status != retained.Turn.Status) || len(retained.Inputs) != len(inputs) {
		return trackedTurn{}, turnUncertain()
	}
	for i, id := range retained.Inputs {
		binding, known := c.execution.inputs[id]
		if !known || binding.TurnID != turn.ID || inputs[i].ID != id || inputs[i].PromptDigest != binding.Digest {
			return trackedTurn{}, turnUncertain()
		}
	}
	if err := c.inspectRetainedThreadLocked(ctx); err != nil {
		return trackedTurn{}, err
	}
	return retained, nil
}
