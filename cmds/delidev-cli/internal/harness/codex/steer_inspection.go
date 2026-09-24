package codex

import (
	"context"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type SteerDelivery string

const (
	SteerNotSent   SteerDelivery = "not-sent"
	SteerAccepted  SteerDelivery = "accepted"
	SteerUncertain SteerDelivery = "uncertain"
)

type SteerEvidence string

const (
	SteerAcknowledgment SteerEvidence = "native-acknowledgment"
	SteerHistory        SteerEvidence = "native-history"
	SteerRejection      SteerEvidence = "native-rejection"
)

// SteerObservation contains no input content. Accepted is a native delivery fact,
// not permission to resume a paused session or overwrite coordinator ownership.
type SteerObservation struct {
	RequestID domain.ID
	InputID   domain.ID
	ThreadID  domain.ID
	TurnID    domain.ID
	Delivery  SteerDelivery
	Evidence  SteerEvidence
}

type steerAttempt struct {
	operation turnOperation
	delivery  SteerDelivery
	evidence  SteerEvidence
	// Only this exact uncertainty may be cleared after native inspection. A
	// subsequent event failure replaces Client.problem and must survive proof
	// of this input's acceptance. Explicit pause/interrupt is never cleared.
	problem *domain.Error
}

func (a *steerAttempt) observation(thread domain.ID) SteerObservation {
	return SteerObservation{RequestID: a.operation.RequestID, InputID: a.operation.InputID, ThreadID: thread, TurnID: a.operation.TurnID, Delivery: a.delivery, Evidence: a.evidence}
}

// InspectSteerAcceptance reads native state after a possibly accepted attempt;
// it never sends turn/steer, starts a replacement turn or interprets absence as
// rejection. Call it with a fresh bounded inspection context after a send timeout.
// The same connection must retain the original request and exact input binding.
// Connection replacement requires a separate durable reconciliation boundary.
func (c *Client) InspectSteerAcceptance(ctx context.Context, requestID domain.ID) (result SteerObservation, returned error) {
	if err := requestID.Validate(); err != nil {
		return result, err
	}
	if err := c.acquireControl(ctx); err != nil {
		return result, err
	}
	defer func() { <-c.control }()
	if c.mode != ThreadProtocol || c.execution == nil {
		return result, unsupportedSettings()
	}
	attempt, exists := c.execution.steers[requestID]
	if !exists {
		return result, domain.Fail(domain.NotFound, "No retained native Steer attempt has this identity.", "Inspect the original request on its owning connection; do not reconstruct or resend it.")
	}
	defer func() {
		if returned != nil && domain.SafeError(returned).Code == domain.RecoveryRequired && c.problem == nil {
			c.problem = turnUncertain()
			attempt.problem = c.problem
		}
		result = attempt.observation(c.thread)
		if c.logger != nil {
			code := "ok"
			if returned != nil {
				code = string(domain.SafeError(returned).Code)
			}
			c.logger.InfoContext(ctx, "Codex native Steer inspection", "owner_id", c.ownerID, "request_id", requestID, "delivery", result.Delivery, "evidence", result.Evidence, "code", code)
		}
	}()
	clearOwnUncertainty := func() {
		if attempt.problem != nil && c.problem == attempt.problem {
			c.problem = nil
		}
	}
	if attempt.delivery == SteerNotSent || attempt.evidence == SteerHistory {
		if err := ctx.Err(); err != nil {
			return result, domain.SafeError(err)
		}
		clearOwnUncertainty()
		return result, nil
	}
	// Metadata must still name the original local conversation. Waiting flags
	// and terminal state do not invalidate acceptance, but grant no send rights.
	if err := c.inspectSteerScopeLocked(ctx); err != nil {
		return result, err
	}
	response, err := c.wire.Call(ctx, domain.NewID(), "thread/turns/list", struct {
		ThreadID      domain.ID `json:"threadId"`
		Limit         int       `json:"limit"`
		SortDirection string    `json:"sortDirection"`
		ItemsView     string    `json:"itemsView"`
	}{c.thread, 1, "desc", "full"})
	if err != nil || response.ErrorCode != nil {
		return result, turnUncertain()
	}
	turn, inputs, err := decodeLatestTurnInputs(response.Result)
	retained, known := c.execution.turns[attempt.operation.TurnID]
	if err != nil || !known || turn.ID != retained.Turn.ID || (retained.Turn.Status.terminal() && turn.Status != retained.Turn.Status) || len(retained.Inputs) != len(inputs) {
		return result, turnUncertain()
	}
	for i, id := range retained.Inputs {
		binding, known := c.execution.inputs[id]
		if !known || binding.TurnID != turn.ID || inputs[i].ID != id || inputs[i].PromptDigest != binding.Digest {
			return result, turnUncertain()
		}
	}
	if !slices.Contains(retained.Inputs, attempt.operation.InputID) {
		return result, turnUncertain()
	}
	if err := c.inspectSteerScopeLocked(ctx); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, domain.SafeError(err)
	}
	attempt.delivery, attempt.evidence = SteerAccepted, SteerHistory
	clearOwnUncertainty()
	return result, nil
}

func (c *Client) inspectSteerScopeLocked(ctx context.Context) error {
	native, err := c.readThreadLocked(ctx, domain.NewID(), c.thread)
	if err != nil {
		return turnUncertain()
	}
	state := c.execution
	if !nativePathEqual(native.Cwd, state.settings.Cwd) || native.ModelProvider != state.settings.Provider || native.SessionID != state.thread.SessionID || native.HistoryMode != LegacyHistory || native.CanAcceptDirectInput == nil || !*native.CanAcceptDirectInput || (native.Status.Type != ThreadActive && native.Status.Type != ThreadIdle) {
		return turnUncertain()
	}
	return nil
}
