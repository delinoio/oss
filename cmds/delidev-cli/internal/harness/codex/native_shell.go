// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/hex"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// NativeShellVersion is the closed schema verified for explicit shell actions.
const NativeShellVersion = "0.162.0"

type nativeShellAttempt struct {
	command          domain.NativeShellCommand
	source           ContinuationCheckpoint
	before           []json.RawMessage
	observation      domain.NativeShellObservation
	displayedCommand string
	completed        bool
	terminal         bool
	cancelClaim      domain.ID
}

// NativeShellCheckpoint is separate from the ordinary execution checkpoint.
// It pins only the original history extension and never manufactures input,
// compaction, inference, process IDs or operating-system cleanup proof.
type NativeShellCheckpoint struct {
	Version       uint32                         `json:"version"`
	Source        ContinuationCheckpoint         `json:"source"`
	Context       *ContinuationContextCheckpoint `json:"context,omitempty"`
	ActionID      domain.ID                      `json:"action_id"`
	NativeTurnID  domain.ID                      `json:"native_turn_id"`
	NativeStatus  TurnStatus                     `json:"native_status"`
	BeforeCount   uint32                         `json:"before_count"`
	BeforeDigest  string                         `json:"before_digest"`
	TurnsCount    uint32                         `json:"turns_count"`
	HistoryDigest string                         `json:"history_digest"`
	RolloutPath   string                         `json:"rollout_path"`
	RolloutDigest string                         `json:"rollout_digest"`
}

func (p NativeShellCheckpoint) validate() error {
	if p.Version != 1 || p.Source.validate(ContinueAfterSuccess) != nil || p.ActionID.Validate() != nil || p.NativeTurnID.Validate() != nil || !p.NativeStatus.terminal() || p.NativeStatus == TurnFailed || p.BeforeCount == 0 || p.TurnsCount != p.BeforeCount+1 || p.TurnsCount > maxForkTurns || !contextDigest(p.BeforeDigest) || !contextDigest(p.HistoryDigest) || !contextDigest(p.RolloutDigest) || p.Context != nil && p.Context.validate() != nil {
		return domain.NativeShellRecovery()
	}
	return nil
}
func VerifyNativeShellRollout(ctx context.Context, home string, p NativeShellCheckpoint) error {
	if p.validate() != nil {
		return domain.NativeShellRecovery()
	}
	digest, err := forkRolloutDigest(ctx, home, p.RolloutPath)
	if err != nil || hex.EncodeToString(digest[:]) != p.RolloutDigest {
		return domain.NativeShellRecovery()
	}
	return nil
}

// The caller persists its exact authenticated send claim before this entry.
// This independent in-memory claim also fences partial and lost RPC replies.
func (c *Client) StartNativeShell(ctx context.Context, action domain.ID, command domain.NativeShellCommand, source ContinuationCheckpoint) (returned error) {
	defer c.recordFailure(ctx, domain.CodexExecution, &returned)
	if action.Validate() != nil || command.Validate() != nil || source.validate(ContinueAfterSuccess) != nil {
		return domain.NativeShellRecovery()
	}
	if err := c.acquireControl(ctx); err != nil {
		return err
	}
	defer func() { <-c.control }()
	s := c.execution
	if c.version != NativeShellVersion || c.sidechat != "" || c.eligibleTurnLocked(false) != nil || s == nil || s.shell != nil || s.paused || s.active != "" || s.interrupt != "" || len(s.pending) != 0 || len(c.subagents) != 0 || source.ThreadID != c.thread || source.SessionID != s.thread.SessionID || !sameEffectiveSettings(source.Effective, s.settings) {
		return domain.NativeShellRecovery()
	}
	if err := c.checkNativeStateLocked(ctx, true); err != nil {
		return err
	}
	if err := c.noForkWorkLocked(ctx, c.thread); err != nil {
		return err
	}
	before, err := c.compactionTurnsLocked(ctx)
	if err != nil || len(before) == 0 {
		return domain.NativeShellRecovery()
	}
	o := domain.NativeShellObservation{ActionID: action, SessionID: source.SessionID, ExecutionID: c.ownerID, NativeThreadID: domain.NativeIdentity(c.thread), State: domain.NativeShellClaimed, Sequence: 1}
	a := &nativeShellAttempt{command: command, source: cloneCompactionSource(source), before: before, observation: o}
	s.shell = a
	// The native API trims the command. Retain original bytes in the accepted
	// action; normalization here exactly matches the pinned native request owner.
	response, err := c.wire.Call(ctx, action, "thread/shellCommand", struct {
		Thread  domain.ID `json:"threadId"`
		Command string    `json:"command"`
		Timeout *uint64   `json:"timeoutMs,omitempty"`
	}{c.thread, command.Command, command.TimeoutMS})
	var ack map[string]json.RawMessage
	if err != nil || response.ErrorCode != nil || domain.Decode(response.Result, &ack) != nil || ack == nil || len(ack) != 0 {
		c.problem = domain.NativeShellRecovery()
		s.paused = true
		return c.problem
	}
	a.observation.Acknowledged = true
	if c.logger != nil {
		c.logger.InfoContext(ctx, "codex_native_shell_acknowledged", "owner_id", c.ownerID, "action_id", action, "access", "full-access")
	}
	return nil
}

func (c *Client) observeNativeShellLocked(native nativewire.Event) (Event, bool, error) {
	if c.execution == nil || c.execution.shell == nil {
		return Event{}, false, nil
	}
	a := c.execution.shell
	if native.Kind != nativewire.Notification {
		return Event{}, false, nil
	}
	var o *domain.NativeShellObservation
	switch native.Method {
	case "turn/started", "turn/completed":
		var p struct {
			ThreadID domain.ID       `json:"threadId"`
			Turn     json.RawMessage `json:"turn"`
		}
		if domain.Decode(native.Params, &p) != nil || p.ThreadID != c.thread {
			return Event{}, true, domain.NativeShellRecovery()
		}
		turn, err := decodeTurn(p.Turn)
		if err != nil {
			return Event{}, true, err
		}
		if native.Method == "turn/started" {
			if a.terminal || a.observation.NativeTurnID != "" || turn.Status != TurnRunning || !a.observation.Acknowledged {
				return Event{}, true, domain.NativeShellRecovery()
			}
			a.observation.NativeTurnID = domain.NativeIdentity(turn.ID)
			return Event{Kind: MetadataEvent, ThreadID: c.thread, TurnID: turn.ID}, true, nil
		}
		if a.terminal || a.observation.NativeTurnID != domain.NativeIdentity(turn.ID) || !turn.Status.terminal() || !a.completed && turn.Status != TurnInterrupted {
			return Event{}, true, domain.NativeShellRecovery()
		}
		a.terminal = true
		if turn.Status == TurnInterrupted {
			a.observation.State = domain.NativeShellCanceled
		}
		if turn.Status == TurnFailed {
			return Event{}, true, domain.NativeShellRecovery()
		}
		a.observation.Sequence++
		copy := a.observation
		o = &copy
	case "item/started", "item/completed":
		var p struct {
			ThreadID domain.ID       `json:"threadId"`
			TurnID   domain.ID       `json:"turnId"`
			Item     json.RawMessage `json:"item"`
		}
		// Decode the closed envelope separately; raw item contents stay private.
		var fields map[string]json.RawMessage
		if domain.Decode(native.Params, &fields) != nil || len(fields) < 3 || len(fields) > 4 || json.Unmarshal(fields["threadId"], &p.ThreadID) != nil || json.Unmarshal(fields["turnId"], &p.TurnID) != nil {
			return Event{}, true, domain.NativeShellRecovery()
		}
		for key, raw := range fields {
			if key == "threadId" || key == "turnId" || key == "item" {
				continue
			}
			if key != "startedAtMs" && key != "completedAtMs" || key == "startedAtMs" && native.Method != "item/started" || key == "completedAtMs" && native.Method != "item/completed" {
				return Event{}, true, domain.NativeShellRecovery()
			}
			var stamp *int64
			if json.Unmarshal(raw, &stamp) != nil || stamp != nil && *stamp < 0 {
				return Event{}, true, domain.NativeShellRecovery()
			}
		}
		p.Item = fields["item"]
		if p.ThreadID != c.thread || domain.NativeIdentity(p.TurnID) != a.observation.NativeTurnID || a.terminal {
			return Event{}, true, domain.NativeShellRecovery()
		}
		completed := native.Method == "item/completed"
		tool, err := decodeTool(p.Item, "commandExecution", completed)
		if err != nil || tool.Command == nil || tool.Command.Source != UserShellCommand || tool.Command.Cwd != c.execution.settings.Cwd || tool.Command.PluginID != nil || tool.Command.ScriptPath != nil {
			return Event{}, true, domain.NativeShellRecovery()
		}
		command := tool.Command
		if !completed {
			if a.observation.NativeItemID != "" || tool.Status != ToolRunning || command.ExitCode != nil || command.DurationMS != nil {
				return Event{}, true, domain.NativeShellRecovery()
			}
			a.observation.NativeItemID = tool.ID
			a.observation.NativeProcessID = command.ProcessID
			a.displayedCommand = command.Command
			a.observation.State = domain.NativeShellRunning
		} else {
			if a.completed || a.observation.NativeItemID != tool.ID || a.displayedCommand != command.Command || !equalOptionalString(a.observation.NativeProcessID, command.ProcessID) || tool.Status != ToolCompleted && tool.Status != ToolFailed || command.ExitCode == nil || command.DurationMS == nil || command.AggregatedOutput == nil {
				return Event{}, true, domain.NativeShellRecovery()
			}
			a.completed = true
			a.observation.State = domain.NativeShellExited
			a.observation.ExitCode = command.ExitCode
			a.observation.DurationMS = command.DurationMS
			a.observation.AggregatedOutput = command.AggregatedOutput
		}
		a.observation.Sequence++
		copy := a.observation
		o = &copy
	case "item/commandExecution/outputDelta":
		var p struct {
			ThreadID domain.ID `json:"threadId"`
			TurnID   domain.ID `json:"turnId"`
			ItemID   string    `json:"itemId"`
			Delta    string    `json:"delta"`
		}
		if domain.Decode(native.Params, &p) != nil || p.ThreadID != c.thread || domain.NativeIdentity(p.TurnID) != a.observation.NativeTurnID || p.ItemID != a.observation.NativeItemID || a.completed || a.terminal || domain.Text(p.Delta, "native shell output", domain.MaxNativeShellOutput, false) != nil || len(a.observation.Output)+len(p.Delta) > domain.MaxNativeShellOutput {
			return Event{}, true, domain.NativeShellRecovery()
		}
		a.observation.Output += p.Delta
		a.observation.Sequence++
		copy := a.observation
		o = &copy
	default:
		return Event{}, false, nil
	}
	if o == nil || o.Validate() != nil {
		return Event{}, true, domain.NativeShellRecovery()
	}
	return Event{Kind: NativeShellEvent, ThreadID: c.thread, TurnID: domain.ID(o.NativeTurnID), ItemID: o.NativeItemID, NativeShell: o, Correlated: true}, true, nil
}
func equalOptionalString(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func (c *Client) RetainNativeShellCheckpoint(ctx context.Context) (NativeShellCheckpoint, error) {
	if err := c.acquireControl(ctx); err != nil {
		return NativeShellCheckpoint{}, err
	}
	defer func() { <-c.control }()
	if c.execution == nil {
		return NativeShellCheckpoint{}, domain.NativeShellRecovery()
	}
	a := c.execution.shell
	if c.problem != nil || a == nil || !a.terminal || !a.completed || a.observation.State != domain.NativeShellExited && a.observation.State != domain.NativeShellCanceled {
		return NativeShellCheckpoint{}, domain.NativeShellRecovery()
	}
	if err := c.checkNativeStateLocked(ctx, true); err != nil {
		return NativeShellCheckpoint{}, err
	}
	turns, err := c.compactionTurnsLocked(ctx)
	if err != nil || len(turns) != len(a.before)+1 || historyDigest(turns[:len(a.before)]) != historyDigest(a.before) {
		return NativeShellCheckpoint{}, domain.NativeShellRecovery()
	}
	var last turnWire
	if domain.DecodeBounded(turns[len(turns)-1], &last, 16<<20) != nil || last.ID != domain.ID(a.observation.NativeTurnID) || !last.Status.terminal() || last.Status == TurnFailed {
		return NativeShellCheckpoint{}, domain.NativeShellRecovery()
	}
	thread, err := c.readThreadLocked(ctx, domain.NewID(), c.thread)
	var path string
	if err != nil || json.Unmarshal(thread.Path, &path) != nil {
		return NativeShellCheckpoint{}, domain.NativeShellRecovery()
	}
	digest, err := forkRolloutDigest(ctx, c.home, path)
	if err != nil {
		return NativeShellCheckpoint{}, err
	}
	return NativeShellCheckpoint{Version: 1, Source: a.source, Context: cloneContinuationContext(c.execution.contextBase), ActionID: a.observation.ActionID, NativeTurnID: last.ID, NativeStatus: last.Status, BeforeCount: uint32(len(a.before)), BeforeDigest: historyDigest(a.before), TurnsCount: uint32(len(turns)), HistoryDigest: historyDigest(turns), RolloutPath: path, RolloutDigest: hex.EncodeToString(digest[:])}, nil
}

func (c *Client) VerifyNativeShellContinuation(ctx context.Context, request domain.ID, p NativeShellCheckpoint) (Turn, error) {
	if request.Validate() != nil || p.validate() != nil {
		return Turn{}, domain.NativeShellRecovery()
	}
	if err := c.acquireControl(ctx); err != nil {
		return Turn{}, err
	}
	defer func() { <-c.control }()
	s := c.execution
	if s == nil || c.problem != nil || !s.continuationPending || s.active != "" || len(s.turns) != 0 || len(s.inputs) != 0 || len(s.pending) != 0 || p.Source.ThreadID != c.thread || p.Source.SessionID != s.thread.SessionID || !sameEffectiveSettings(p.Source.Effective, s.settings) {
		return Turn{}, domain.NativeShellRecovery()
	}
	if err := c.checkNativeStateLocked(ctx, true); err != nil {
		return Turn{}, err
	}
	turns, err := c.compactionTurnsLocked(ctx)
	if err != nil || uint32(len(turns)) != p.TurnsCount || historyDigest(turns) != p.HistoryDigest || historyDigest(turns[:p.BeforeCount]) != p.BeforeDigest {
		return Turn{}, domain.NativeShellRecovery()
	}
	var last turnWire
	if domain.DecodeBounded(turns[len(turns)-1], &last, 16<<20) != nil || last.ID != p.NativeTurnID || last.Status != p.NativeStatus {
		return Turn{}, domain.NativeShellRecovery()
	}
	// Exact full-history digests include prior native compaction and ordinary
	// input bytes. They do not replace or reinterpret the original checkpoint.
	turn := Turn{ID: p.Source.TurnID, Status: p.Source.Status}
	tracked := trackedTurn{Turn: turn, Mode: p.Source.Mode}
	for _, input := range p.Source.Inputs {
		s.inputs[input.ID] = inputAttempt{Digest: input.PromptDigest, SkillDigest: input.SkillDigest, TurnID: turn.ID}
		tracked.Inputs = append(tracked.Inputs, input.ID)
	}
	s.turns[turn.ID] = tracked
	s.turns[p.NativeTurnID] = trackedTurn{Turn: Turn{ID: p.NativeTurnID, Status: p.NativeStatus}, Mode: p.Source.Mode}
	s.continuationPending, s.paused = false, false
	// NativeShell owns an independent history proof. Context records remain
	// unchanged; future ordinary execution retains its own native context.
	s.contextBase = cloneContinuationContext(p.Context)
	return turn, nil
}

func (c *Client) CancelNativeShell(ctx context.Context, request domain.ID) error {
	if request.Validate() != nil {
		return domain.NativeShellRecovery()
	}
	if err := c.acquireControl(ctx); err != nil {
		return err
	}
	defer func() { <-c.control }()
	if c.execution == nil {
		return domain.NativeShellRecovery()
	}
	a := c.execution.shell
	if a == nil || a.terminal || a.observation.NativeTurnID == "" || a.cancelClaim != "" {
		return domain.NativeShellRecovery()
	}
	a.cancelClaim = request
	response, err := c.wire.Call(ctx, request, "turn/interrupt", struct {
		Thread domain.ID `json:"threadId"`
		Turn   domain.ID `json:"turnId"`
	}{c.thread, domain.ID(a.observation.NativeTurnID)})
	var ack map[string]json.RawMessage
	if err != nil || response.ErrorCode != nil || domain.Decode(response.Result, &ack) != nil || ack == nil || len(ack) != 0 {
		return domain.NativeShellRecovery()
	}
	return nil
}

func (c *Client) NativeShellTerminal() bool {
	if c.acquireControl(context.Background()) != nil {
		return false
	}
	defer func() { <-c.control }()
	return c.execution != nil && c.execution.shell != nil && c.execution.shell.terminal
}
