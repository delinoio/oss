// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type ShellDelivery string

const (
	ShellSendIntent   ShellDelivery = "send-intent"
	ShellAcknowledged ShellDelivery = "acknowledged"
	ShellRejected     ShellDelivery = "rejected"
	ShellUncertain    ShellDelivery = "uncertain"
)

// ShellObservation keeps RPC delivery separate from the native turn and process
// outcome. None of these facts proves that the original process was cleaned up.
type ShellObservation struct {
	RequestID domain.ID
	ThreadID  domain.ID
	TurnID    domain.ID
	Delivery  ShellDelivery
	Terminal  bool
}

type shellAttempt struct {
	observation   ShellObservation
	items         map[string]ToolStatus
	interruptSent bool
	command       string
}

func shellUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "Native shell delivery requires reconciliation.", "Inspect the original shell operation and owned processes; never send its command again automatically.")
}

// ShellCommand consumes a previously persisted, authenticated human command
// claim supplied by the Worker controller. It cannot derive that claim from an
// agent message or ordinary input. The command runs outside the thread sandbox;
// the controller must disclose this before accepting the human action.
// Retain the claim before calling this method, including when delivery fails.
func (c *Client) ShellCommand(ctx context.Context, requestID, expectedThread domain.ID, command string, timeoutMS *uint64) (observation ShellObservation, returned error) {
	defer c.recordFailure(ctx, domain.CodexExecution, &returned)
	observation = ShellObservation{RequestID: requestID, ThreadID: expectedThread}
	if requestID.Validate() != nil || expectedThread.Validate() != nil || domain.Text(command, "native shell command", 64<<10, true) != nil || timeoutMS != nil && *timeoutMS > 3600000 {
		return observation, domain.Fail(domain.InvalidArgument, "Invalid native shell command.", "Use a bounded explicit command and a timeout of at most one hour.")
	}
	if err := c.acquireControl(ctx); err != nil {
		return observation, err
	}
	defer func() { <-c.control }()
	if err := c.eligibleTurnLocked(false); err != nil {
		return observation, err
	}
	if expectedThread != c.thread || c.sidechat != "" {
		return observation, domain.Fail(domain.PermissionDenied, "This native thread cannot run this shell command.", "Use the original ordinary session; read-only Sidechat cannot run full-access commands.")
	}
	s := c.execution
	if _, exists := s.shells[requestID]; exists {
		return observation, turnConflict()
	}
	if s.active != "" || s.interrupt != "" || s.paused || s.activeShell != "" || len(s.shells) >= maxTrackedTurns {
		return observation, turnConflict()
	}
	if err := c.checkNativeStateLocked(ctx, true); err != nil {
		return observation, err
	}
	if s.shells == nil {
		s.shells = make(map[domain.ID]*shellAttempt)
	}
	a := &shellAttempt{observation: observation, command: command, items: make(map[string]ToolStatus)}
	a.observation.Delivery = ShellSendIntent
	s.shells[requestID], s.activeShell = a, requestID
	response, err := c.wire.Call(ctx, requestID, "thread/shellCommand", struct {
		ThreadID  domain.ID `json:"threadId"`
		Command   string    `json:"command"`
		TimeoutMS *uint64   `json:"timeoutMs,omitempty"`
	}{expectedThread, command, timeoutMS})
	if err != nil {
		a.observation.Delivery, c.problem = ShellUncertain, shellUncertain()
		return a.observation, c.problem
	}
	if response.ErrorCode != nil {
		problem := nativeTurnError(StartTurnAction, *response.ErrorCode)
		if problem.Code == domain.RecoveryRequired {
			a.observation.Delivery, c.problem = ShellUncertain, shellUncertain()
			return a.observation, c.problem
		}
		a.observation.Delivery, s.activeShell = ShellRejected, ""
		return a.observation, problem
	}
	if !emptyShellResponse(response.Result) {
		a.observation.Delivery, c.problem = ShellUncertain, shellUncertain()
		return a.observation, c.problem
	}
	a.observation.Delivery = ShellAcknowledged
	if c.logger != nil {
		c.logger.InfoContext(ctx, "Codex native shell delivery observed", "owner_id", c.ownerID, "request_id", requestID, "delivery", a.observation.Delivery)
	}
	return a.observation, nil
}

func emptyShellResponse(raw json.RawMessage) bool {
	var value *struct{}
	return domain.Decode(raw, &value) == nil && value != nil
}

// InspectShell is a local observation read. It never retries the native RPC or
// treats late acknowledgment as permission to send another command.
func (c *Client) InspectShell(ctx context.Context, requestID domain.ID) (ShellObservation, error) {
	if err := c.acquireControl(ctx); err != nil {
		return ShellObservation{}, err
	}
	defer func() { <-c.control }()
	if c.execution == nil || c.execution.shells[requestID] == nil {
		return ShellObservation{}, domain.Fail(domain.NotFound, "The original shell operation is unavailable.", "Keep its original Worker and connection identity.")
	}
	return c.execution.shells[requestID].observation, nil
}

func (c *Client) observeShellLocked(native nativewire.Event) (Event, bool, error) {
	if c.execution == nil {
		return Event{}, false, nil
	}
	s := c.execution
	if native.Kind == nativewire.LateResponse {
		var id domain.ID
		if json.Unmarshal(native.ID, &id) != nil {
			return Event{}, false, nil
		}
		a := s.shells[id]
		if a == nil {
			return Event{}, false, nil
		}
		if a.observation.Delivery != ShellUncertain {
			return Event{}, true, incompatible()
		}
		if native.Response.ErrorCode == nil {
			if !emptyShellResponse(native.Response.Result) {
				return Event{}, true, incompatible()
			}
			a.observation.Delivery = ShellAcknowledged
		} else if nativeTurnError(StartTurnAction, *native.Response.ErrorCode).Code != domain.RecoveryRequired {
			if a.observation.TurnID != "" {
				return Event{}, true, incompatible()
			}
			a.observation.Delivery = ShellRejected
		}
		// The original recovery fence survives all late responses.
		return shellEvent(a, Event{}), true, nil
	}
	a := s.shells[s.activeShell]
	if a == nil || native.Kind != nativewire.Notification {
		return Event{}, false, nil
	}
	switch native.Method {
	case "turn/started", "turn/completed":
		var p struct {
			ThreadID domain.ID       `json:"threadId"`
			Turn     json.RawMessage `json:"turn"`
		}
		if domain.Decode(native.Params, &p) != nil || p.ThreadID.Validate() != nil {
			return Event{}, true, incompatible()
		}
		if p.ThreadID != c.thread {
			return privateNative(native), true, nil
		}
		turn, err := decodeTurn(p.Turn)
		if err != nil {
			return Event{}, true, err
		}
		if native.Method == "turn/started" {
			if turn.Status != TurnRunning || a.observation.TurnID != "" && a.observation.TurnID != turn.ID || s.turns[turn.ID].Turn.ID != "" {
				return Event{}, true, incompatible()
			}
			a.observation.TurnID = turn.ID
		} else {
			if !turn.Status.terminal() || a.observation.TurnID != turn.ID || a.observation.Terminal {
				return Event{}, true, incompatible()
			}
			for _, status := range a.items {
				if status == ToolRunning {
					return Event{}, true, shellUncertain()
				}
			}
			a.observation.Terminal = true
			// Retain the shell owner after terminal observation. Only an explicit
			// original cleanup proof may release admission; terminal is not cleanup.
		}
		return shellEvent(a, Event{Turn: &turn}), true, nil
	case "item/started", "item/completed":
		var p struct {
			ThreadID      domain.ID       `json:"threadId"`
			TurnID        domain.ID       `json:"turnId"`
			Item          json.RawMessage `json:"item"`
			StartedAtMS   *int64          `json:"startedAtMs,omitempty"`
			CompletedAtMS *int64          `json:"completedAtMs,omitempty"`
			DurationMS    *int64          `json:"durationMs,omitempty"`
		}
		if domain.Decode(native.Params, &p) != nil || p.ThreadID.Validate() != nil || p.TurnID.Validate() != nil {
			return Event{}, true, incompatible()
		}
		if p.ThreadID != c.thread {
			return privateNative(native), true, nil
		}
		if p.TurnID != a.observation.TurnID || a.observation.Terminal {
			return Event{}, true, incompatible()
		}
		completed := native.Method == "item/completed"
		if !completed && (p.StartedAtMS == nil || *p.StartedAtMS < 0 || p.CompletedAtMS != nil || p.DurationMS != nil) || completed && (p.CompletedAtMS == nil || *p.CompletedAtMS < 0 || p.StartedAtMS != nil || p.DurationMS != nil) {
			return Event{}, true, incompatible()
		}
		tool, err := decodeTool(p.Item, "commandExecution", completed)
		if err != nil || tool.Command == nil || tool.Command.Source != UserShellCommand || tool.Command.Command != a.command || !nativePathEqual(tool.Command.Cwd, s.settings.Cwd) {
			return Event{}, true, incompatible()
		}
		previous, exists := a.items[tool.ID]
		if completed && (!exists || previous != ToolRunning || tool.Status == ToolRunning) || !completed && (exists || tool.Status != ToolRunning) {
			return Event{}, true, incompatible()
		}
		a.items[tool.ID] = tool.Status
		return shellEvent(a, Event{Tool: tool, ItemID: tool.ID}), true, nil
	case "item/commandExecution/outputDelta":
		var p struct {
			ThreadID domain.ID `json:"threadId"`
			TurnID   domain.ID `json:"turnId"`
			ItemID   string    `json:"itemId"`
			Delta    *string   `json:"delta"`
		}
		if domain.Decode(native.Params, &p) != nil || p.ThreadID.Validate() != nil || p.TurnID.Validate() != nil || p.Delta == nil || domain.Text(*p.Delta, "native shell output", nativewire.MaxFrame, false) != nil {
			return Event{}, true, incompatible()
		}
		if p.ThreadID != c.thread {
			return privateNative(native), true, nil
		}
		if p.TurnID != a.observation.TurnID || a.observation.Terminal || a.items[p.ItemID] != ToolRunning {
			return Event{}, true, incompatible()
		}
		return shellEvent(a, Event{ItemID: p.ItemID, TextDelta: *p.Delta, ToolOutputKind: CommandTool}), true, nil
	}
	return Event{}, false, nil
}

func shellEvent(a *shellAttempt, event Event) Event {
	value := a.observation
	event.Kind, event.Shell, event.RequestID, event.ThreadID, event.TurnID, event.Correlated = NativeShellEvent, &value, value.RequestID, value.ThreadID, value.TurnID, true
	return event
}

// InterruptShell fences one original turn cancellation before writing to native.
// A transport acknowledgment never proves terminal state or process cleanup.
func (c *Client) InterruptShell(ctx context.Context, requestID domain.ID) error {
	if err := c.acquireControl(ctx); err != nil {
		return err
	}
	defer func() { <-c.control }()
	if c.execution == nil {
		return shellUncertain()
	}
	a := c.execution.shells[requestID]
	if a == nil || a.observation.TurnID == "" || a.observation.Terminal || a.interruptSent {
		return shellUncertain()
	}
	a.interruptSent = true
	reply, err := c.wire.Call(ctx, domain.NewID(), "turn/interrupt", struct {
		ThreadID domain.ID `json:"threadId"`
		TurnID   domain.ID `json:"turnId"`
	}{a.observation.ThreadID, a.observation.TurnID})
	if err != nil || reply.ErrorCode != nil || !emptyShellResponse(reply.Result) {
		return shellUncertain()
	}
	return nil
}
