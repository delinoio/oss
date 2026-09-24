package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type TurnStatus string

const (
	TurnRunning     TurnStatus = "inProgress"
	TurnCompleted   TurnStatus = "completed"
	TurnInterrupted TurnStatus = "interrupted"
	TurnFailed      TurnStatus = "failed"
)

func (s TurnStatus) terminal() bool {
	return s == TurnCompleted || s == TurnInterrupted || s == TurnFailed
}

type Turn struct {
	ID          domain.ID
	Status      TurnStatus
	StartedAt   *int64
	CompletedAt *int64
	DurationMS  *int64
	Problem     *domain.Error
}

type TurnAction string

const (
	StartTurnAction     TurnAction = "turn/start"
	SteerTurnAction     TurnAction = "turn/steer"
	InterruptTurnAction TurnAction = "turn/interrupt"
)

type TurnResult struct {
	RequestID domain.ID
	InputID   domain.ID
	TurnID    domain.ID
}

type turnOperation struct {
	Action      TurnAction
	RequestID   domain.ID
	InputID     domain.ID
	TurnID      domain.ID
	Mode        domain.SessionMode
	InputDigest [32]byte
}

type trackedTurn struct {
	Turn Turn
	Mode domain.SessionMode
}
type inputAttempt struct {
	Digest [32]byte
	TurnID domain.ID
}

type executionState struct {
	thread              Thread
	settings            EffectiveSettings
	active              domain.ID
	paused              bool
	continuationPending bool
	interrupt           domain.ID
	turns               map[domain.ID]trackedTurn
	inputs              map[domain.ID]inputAttempt
	pending             map[domain.ID]turnOperation
	interactions        interactionState
}

const maxTrackedTurns = 4096

func newExecutionState(thread Thread, settings EffectiveSettings) *executionState {
	// The caller can retain and edit its observation, but cannot change the
	// adapter's effective settings through pointers or slices in that result.
	settings.Sandbox.WritableRoots = slices.Clone(settings.Sandbox.WritableRoots)
	settings.Effort = copyString(settings.Effort)
	settings.ServiceTier = copyString(settings.ServiceTier)
	thread.Status.ActiveFlags = slices.Clone(thread.Status.ActiveFlags)
	if thread.DirectInput != nil {
		v := *thread.DirectInput
		thread.DirectInput = &v
	}
	return &executionState{thread: thread, settings: settings, paused: thread.Status.Type != ThreadIdle, turns: map[domain.ID]trackedTurn{}, inputs: map[domain.ID]inputAttempt{}, pending: map[domain.ID]turnOperation{}}
}
func copyString(v *string) *string {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}
func turnUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "Native turn acceptance requires reconciliation.", "Retain request, input and native turn identities; inspect native state before any explicit retry.")
}
func turnConflict() *domain.Error {
	return domain.Fail(domain.Conflict, "The native turn is not eligible for this operation.", "Use the current active turn and original input mode; wait for terminal confirmation or resume explicitly as required.")
}
func nativeTurnError(action TurnAction, code int64) *domain.Error {
	switch code {
	case -32601:
		return unsupportedSettings()
	case -32600, -32602:
		if action == SteerTurnAction || action == InterruptTurnAction {
			return turnConflict()
		}
		return domain.Fail(domain.InvalidArgument, "Codex rejected the native turn request.", "Check the supported settings and input before an explicit retry; the adapter did not substitute another operation.")
	default:
		return turnUncertain()
	}
}
func (c *Client) eligibleTurnLocked(allowRecovery bool) error {
	if c.mode != ThreadProtocol || c.execution == nil {
		return unsupportedSettings()
	}
	if !allowRecovery && c.problem != nil {
		return c.problem
	}
	if !allowRecovery && c.execution.continuationPending {
		return continuationUncertain()
	}
	if !allowRecovery && c.execution.interactions.blocksInput() {
		return interactionConflict()
	}
	if c.execution.thread.DirectInput == nil || !*c.execution.thread.DirectInput {
		return unsupportedSettings()
	}
	return nil
}
func (c *Client) checkNativeStateLocked(ctx context.Context, requireIdle bool) error {
	native, err := c.readThreadLocked(ctx, domain.NewID(), c.thread)
	if err != nil {
		return err
	}
	s := c.execution.settings
	if native.Cwd != s.Cwd && !nativePathEqual(native.Cwd, s.Cwd) || native.ModelProvider != s.Provider || native.SessionID != c.execution.thread.SessionID || native.HistoryMode != LegacyHistory || native.CanAcceptDirectInput == nil || !*native.CanAcceptDirectInput {
		c.problem = turnUncertain()
		return c.problem
	}
	if requireIdle && native.Status.Type != ThreadIdle {
		return turnConflict()
	}
	if !requireIdle && (native.Status.Type != ThreadActive || len(native.Status.ActiveFlags) != 0) {
		return turnConflict()
	}
	return nil
}
func (c *Client) reserveInputLocked(inputID domain.ID) error {
	if _, exists := c.execution.inputs[inputID]; exists {
		return domain.Fail(domain.Conflict, "This input already has a native delivery attempt.", "Reconcile the original attempt instead of sending the input again with another request identity.")
	}
	if len(c.execution.inputs) >= maxTrackedTurns || len(c.execution.turns) >= maxTrackedTurns {
		return domain.Fail(domain.ResourceExhausted, "Native execution tracking reached its bound.", "Drain and persist native state before an explicit connection replacement.")
	}
	return nil
}

type nativeTextInput struct {
	Type nativeInputType `json:"type"`
	Text string          `json:"text"`
}
type nativeInputType string

const nativeText nativeInputType = "text"

type collaborationKind string

const (
	nativeExecute collaborationKind = "default"
	nativePlan    collaborationKind = "plan"
)

type collaborationSettings struct {
	Model                 string  `json:"model"`
	Effort                *string `json:"reasoning_effort"`
	DeveloperInstructions *string `json:"developer_instructions"`
}
type collaborationMode struct {
	Mode     collaborationKind     `json:"mode"`
	Settings collaborationSettings `json:"settings"`
}
type startTurnParams struct {
	ThreadID          domain.ID         `json:"threadId"`
	Input             []nativeTextInput `json:"input"`
	ClientInputID     domain.ID         `json:"clientUserMessageId"`
	Model             string            `json:"model"`
	Effort            *string           `json:"effort,omitempty"`
	Cwd               string            `json:"cwd"`
	ApprovalPolicy    ApprovalPolicy    `json:"approvalPolicy"`
	ApprovalsReviewer string            `json:"approvalsReviewer"`
	Sandbox           Sandbox           `json:"sandboxPolicy"`
	ServiceTier       *string           `json:"serviceTier,omitempty"`
	Collaboration     collaborationMode `json:"collaborationMode"`
}

// StartTurn never uses Codex's implicit start-or-steer fallback intentionally.
// A serialized native metadata check and retained terminal observation precede
// every new send. The coordinator still owns durable input claims and account
// authority; a native acknowledgment is not a completed product execution.
func (c *Client) StartTurn(ctx context.Context, requestID, inputID domain.ID, input domain.SessionInput) (TurnResult, error) {
	result := TurnResult{RequestID: requestID, InputID: inputID}
	for _, id := range []domain.ID{requestID, inputID} {
		if err := id.Validate(); err != nil {
			return result, err
		}
	}
	if err := input.Validate(); err != nil {
		return result, err
	}
	if err := c.acquireControl(ctx); err != nil {
		return result, err
	}
	defer func() { <-c.control }()
	if err := c.eligibleTurnLocked(false); err != nil {
		return result, err
	}
	state := c.execution
	if state.paused || state.active != "" || state.interrupt != "" {
		return result, turnConflict()
	}
	if err := c.reserveInputLocked(inputID); err != nil {
		return result, err
	}
	if err := c.checkNativeStateLocked(ctx, true); err != nil {
		return result, err
	}
	s := state.settings
	mode := nativeExecute
	if input.Mode == domain.PlanMode {
		mode = nativePlan
	}
	params := startTurnParams{ThreadID: c.thread, Input: []nativeTextInput{{Type: nativeText, Text: input.Prompt}}, ClientInputID: inputID, Model: s.Model, Effort: s.Effort, Cwd: s.Cwd, ApprovalPolicy: s.ApprovalPolicy, ApprovalsReviewer: s.ApprovalsReviewer, Sandbox: s.Sandbox, ServiceTier: s.ServiceTier, Collaboration: collaborationMode{Mode: mode, Settings: collaborationSettings{Model: s.Model, Effort: s.Effort}}}
	op := turnOperation{Action: StartTurnAction, RequestID: requestID, InputID: inputID, Mode: input.Mode, InputDigest: sha256.Sum256([]byte(input.Prompt))}
	response, err := c.callTurnLocked(ctx, op, params)
	if err != nil {
		return result, err
	}
	turn, err := decodeTurnResponse(response.Result)
	if err != nil || turn.Status != TurnRunning || state.turns[turn.ID].Turn.ID != "" {
		return result, c.rejectTurnReply(ctx, StartTurnAction, requestID)
	}
	state.turns[turn.ID] = trackedTurn{Turn: turn, Mode: input.Mode}
	state.inputs[inputID] = inputAttempt{Digest: op.InputDigest, TurnID: turn.ID}
	state.active = turn.ID
	result.TurnID = turn.ID
	return result, nil
}

// Steer keeps mode, model, account and permissions unchanged and always uses
// the native expected-turn precondition. Definite rejection leaves input free
// for the coordinator to retain in its ordinary queue; no fallback is sent.
func (c *Client) Steer(ctx context.Context, requestID, inputID, expectedTurnID domain.ID, input domain.SessionInput) (TurnResult, error) {
	result := TurnResult{RequestID: requestID, InputID: inputID, TurnID: expectedTurnID}
	for _, id := range []domain.ID{requestID, inputID, expectedTurnID} {
		if err := id.Validate(); err != nil {
			return result, err
		}
	}
	if err := input.Validate(); err != nil {
		return result, err
	}
	if err := c.acquireControl(ctx); err != nil {
		return result, err
	}
	defer func() { <-c.control }()
	if err := c.eligibleTurnLocked(false); err != nil {
		return result, err
	}
	state := c.execution
	turn := state.turns[expectedTurnID]
	if state.paused || state.interrupt != "" || state.active != expectedTurnID || turn.Turn.Status != TurnRunning || turn.Mode != input.Mode {
		return result, turnConflict()
	}
	if err := c.reserveInputLocked(inputID); err != nil {
		return result, err
	}
	if err := c.checkNativeStateLocked(ctx, false); err != nil {
		return result, err
	}
	params := struct {
		ThreadID       domain.ID         `json:"threadId"`
		ExpectedTurnID domain.ID         `json:"expectedTurnId"`
		InputID        domain.ID         `json:"clientUserMessageId"`
		Input          []nativeTextInput `json:"input"`
	}{c.thread, expectedTurnID, inputID, []nativeTextInput{{Type: nativeText, Text: input.Prompt}}}
	op := turnOperation{Action: SteerTurnAction, RequestID: requestID, InputID: inputID, TurnID: expectedTurnID, Mode: input.Mode, InputDigest: sha256.Sum256([]byte(input.Prompt))}
	response, err := c.callTurnLocked(ctx, op, params)
	if err != nil {
		return result, err
	}
	var ack struct {
		TurnID domain.ID `json:"turnId"`
	}
	if domain.Decode(response.Result, &ack) != nil || ack.TurnID != expectedTurnID {
		return result, c.rejectTurnReply(ctx, SteerTurnAction, requestID)
	}
	return result, nil
}

// Interrupt requests native cancellation, never reports process cleanup or
// Archive completion. Inputs remain paused even after a terminal notification.
// A second interrupt cannot replay an uncertain/acknowledged first attempt.
func (c *Client) Interrupt(ctx context.Context, requestID, expectedTurnID domain.ID) (TurnResult, error) {
	result := TurnResult{RequestID: requestID, TurnID: expectedTurnID}
	for _, id := range []domain.ID{requestID, expectedTurnID} {
		if err := id.Validate(); err != nil {
			return result, err
		}
	}
	if err := c.acquireControl(ctx); err != nil {
		return result, err
	}
	defer func() { <-c.control }()
	if err := c.eligibleTurnLocked(true); err != nil {
		return result, err
	}
	state := c.execution
	if state.active != expectedTurnID || state.turns[expectedTurnID].Turn.Status != TurnRunning || state.interrupt != "" {
		return result, turnConflict()
	}
	state.paused = true
	state.interrupt = requestID
	op := turnOperation{Action: InterruptTurnAction, RequestID: requestID, TurnID: expectedTurnID}
	response, err := c.callTurnLocked(ctx, op, struct {
		ThreadID domain.ID `json:"threadId"`
		TurnID   domain.ID `json:"turnId"`
	}{c.thread, expectedTurnID})
	if err != nil {
		if domain.SafeError(err).Code != domain.RecoveryRequired {
			state.interrupt = ""
		}
		return result, err
	}
	var ack map[string]json.RawMessage
	if domain.Decode(response.Result, &ack) != nil || ack == nil || len(ack) != 0 {
		return result, c.rejectTurnReply(ctx, InterruptTurnAction, requestID)
	}
	return result, nil
}

func (c *Client) rejectTurnReply(ctx context.Context, action TurnAction, requestID domain.ID) error {
	c.problem = turnUncertain()
	if c.logger != nil {
		c.logger.WarnContext(ctx, "Codex native turn acknowledgment mismatch", "owner_id", c.ownerID, "request_id", requestID, "operation", action, "code", c.problem.Code)
	}
	return c.problem
}

func (c *Client) callTurnLocked(ctx context.Context, op turnOperation, params any) (response nativewire.Response, returned error) {
	state := c.execution
	if _, exists := state.pending[op.RequestID]; exists {
		return response, domain.Fail(domain.Conflict, "This native operation is awaiting reconciliation.", "Retain its original request identity and do not overwrite the pending operation.")
	}
	state.pending[op.RequestID] = op
	if op.InputID != "" {
		state.inputs[op.InputID] = inputAttempt{Digest: op.InputDigest, TurnID: op.TurnID}
	}
	defer func() {
		if c.logger != nil {
			code := "ok"
			if returned != nil {
				code = string(domain.SafeError(returned).Code)
			}
			c.logger.InfoContext(ctx, "Codex native turn transport result", "owner_id", c.ownerID, "request_id", op.RequestID, "operation", op.Action, "code", code)
		}
	}()
	response, err := c.wire.Call(ctx, op.RequestID, string(op.Action), params)
	if err == nil && response.ErrorCode != nil {
		err = nativeTurnError(op.Action, *response.ErrorCode)
	}
	if err != nil {
		if domain.SafeError(err).Code == domain.RecoveryRequired {
			c.problem = turnUncertain()
			return response, c.problem
		}
		delete(state.pending, op.RequestID)
		delete(state.inputs, op.InputID)
		return response, err
	}
	delete(state.pending, op.RequestID)
	return response, nil
}
