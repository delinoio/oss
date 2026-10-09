// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"slices"
)

// RevertedContext is private replacement-history evidence, never replay input.
type RevertedContext struct {
	ActionID        domain.ID   `json:"action_id"`
	BeforeTurnID    domain.ID   `json:"before_turn_id"`
	InputID         domain.ID   `json:"input_id"`
	PromptDigest    string      `json:"prompt_digest"`
	RetainedTurnIDs []domain.ID `json:"retained_turn_ids"`
}

func (p RevertedContext) validate() error {
	if domain.UniqueIDs([]domain.ID{p.ActionID, p.BeforeTurnID, p.InputID}) != nil || !contextDigest(p.PromptDigest) || p.RetainedTurnIDs == nil || len(p.RetainedTurnIDs) > maxForkTurns {
		return compactionUncertain()
	}
	seen := map[domain.ID]bool{}
	for _, id := range p.RetainedTurnIDs {
		if id.Validate() != nil || id == p.BeforeTurnID || seen[id] {
			return compactionUncertain()
		}
		seen[id] = true
	}
	return nil
}

// RevertThread requires a caller-owned durable send claim. A lost response or
// changed replacement history never authorizes another native mutation.
func (c *Client) RevertThread(ctx context.Context, action domain.ID, source ContinuationCheckpoint, target HistoricalInput, before domain.ID, claim func(RevertIntent) error) (result CompactedCheckpoint, returned error) {
	defer c.recordFailure(ctx, domain.CodexHistory, &returned)
	if !c.revertHistory || action.Validate() != nil || before.Validate() != nil || target.ID.Validate() != nil || source.validate(ResumeAfterTerminal) != nil {
		return result, compactionUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return result, err
	}
	defer func() { <-c.control }()
	state := c.execution
	if state == nil || c.problem != nil || state.continuationPending || state.paused || state.active != "" || state.interrupt != "" || state.compaction != nil || len(state.pending) != 0 || len(c.subagents) != 0 || state.interactions.blocksInput() || source.ThreadID != c.thread || source.SessionID != state.thread.SessionID || !sameEffectiveSettings(source.Effective, state.settings) {
		return result, compactionUncertain()
	}
	if err := c.checkNativeStateLocked(ctx, true); err != nil {
		return result, err
	}
	if err := c.noForkWorkLocked(ctx, c.thread); err != nil {
		return result, err
	}
	history, err := c.contextTurnsLocked(ctx, "asc", nil, false)
	if err != nil {
		return result, err
	}
	index := -1
	for n, raw := range history {
		turn, err := decodeTurn(raw)
		if err != nil {
			return result, err
		}
		if turn.ID == before {
			_, inputs, e := decodeLatestTurnInputs(marshalForkPage([]json.RawMessage{raw}), c.nativeImageInput)
			if e != nil || len(inputs) == 0 || (inputs[0].ID != target.ID || inputs[0].PromptDigest != target.PromptDigest || target.SkillDigest != ([32]byte{}) && inputs[0].SkillDigest != target.SkillDigest) {
				return result, compactionUncertain()
			}
			index = n
		}
	}
	if index < 0 || claim == nil {
		return result, compactionUncertain()
	}
	intent := RevertIntent{Version: 1, ActionID: action, Source: source, Target: target, BeforeTurnID: before, ExpectedHistory: slices.Clone(history[:index])}
	if err := claim(intent); err != nil {
		return result, err
	}
	response, err := c.wire.Call(ctx, action, "thread/revert", struct {
		Thread domain.ID `json:"threadId"`
		Before domain.ID `json:"beforeTurnId"`
	}{c.thread, before})
	fail := func() (CompactedCheckpoint, error) {
		c.problem = compactionUncertain()
		state.paused = true
		return CompactedCheckpoint{}, c.problem
	}
	if err != nil || response.ErrorCode != nil {
		return fail()
	}
	var wire struct {
		Thread json.RawMessage `json:"thread"`
		Turns  json.RawMessage `json:"turnsBackwardsCursor"`
		Items  json.RawMessage `json:"itemsBackwardsCursor"`
	}
	if domain.Decode(response.Result, &wire) != nil || len(wire.Turns) == 0 || len(wire.Items) == 0 {
		return fail()
	}
	observed, e := decodeThread(wire.Thread, c.version)
	if e != nil || observed.ID != c.thread || observed.SessionID != source.SessionID || observed.Status.Type != ThreadIdle || observed.ParentThreadID != nil || observed.ForkedFromID != nil || observed.Cwd != source.Effective.Cwd || observed.ModelProvider != source.Effective.Provider {
		return fail()
	}
	var cursor, items *string
	if json.Unmarshal(wire.Turns, &cursor) != nil || json.Unmarshal(wire.Items, &items) != nil || cursor != nil && domain.Text(*cursor, "native revert cursor", 4096, true) != nil || items != nil && domain.Text(*items, "native item cursor", 4096, true) != nil || (index == 0) != (cursor == nil) {
		return fail()
	}
	after, e := c.contextTurnsLocked(ctx, "desc", cursor, true)
	if e != nil || !slices.EqualFunc(after, history[:index], equivalentForkJSON) {
		return fail()
	}
	// Re-read without the response cursor as an independent current-context check.
	current, e := c.contextTurnsLocked(ctx, "asc", nil, true)
	if e != nil || !slices.EqualFunc(after, current, equivalentForkJSON) || c.checkNativeStateLocked(ctx, true) != nil {
		return fail()
	}
	var path string
	if json.Unmarshal(observed.Path, &path) != nil {
		return fail()
	}
	digest, e := forkRolloutDigest(ctx, c.home, path)
	if e != nil {
		return fail()
	}
	ids := []domain.ID{}
	for _, raw := range after {
		turn, e := decodeTurn(raw)
		if e != nil {
			return fail()
		}
		ids = append(ids, turn.ID)
	}
	result = CompactedCheckpoint{Version: 2, Source: source, Records: []CompactionRecord{}, TurnsCount: uint32(len(after)), HistoryDigest: historyDigest(after), RolloutPath: path, RolloutDigest: hex.EncodeToString(digest[:]), Revert: &RevertedContext{ActionID: action, BeforeTurnID: before, InputID: target.ID, PromptDigest: hex.EncodeToString(target.PromptDigest[:]), RetainedTurnIDs: ids}}
	if result.Revert.validate() != nil {
		return fail()
	}
	// This process owns no next input. Its replacement must verify this checkpoint.
	state.paused = true
	return result, nil
}

func (c *Client) verifyRevertedContinuation(ctx context.Context, request domain.ID, p CompactedCheckpoint) (Turn, error) {
	if !c.revertHistory || request.Validate() != nil || p.Version != 2 || p.Revert == nil || p.Revert.validate() != nil || p.Source.validate(ResumeAfterTerminal) != nil || len(p.Records) != 0 || !contextDigest(p.HistoryDigest) || p.TurnsCount != uint32(len(p.Revert.RetainedTurnIDs)) {
		return Turn{}, compactionUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return Turn{}, err
	}
	defer func() { <-c.control }()
	s := c.execution
	if s == nil || !s.continuationPending || s.paused || s.active != "" || len(s.turns) != 0 || len(s.inputs) != 0 || len(s.pending) != 0 || s.interactions.blocksInput() || c.problem != nil || c.thread != p.Source.ThreadID || !sameEffectiveSettings(s.settings, p.Source.Effective) {
		return Turn{}, compactionUncertain()
	}
	fail := func() (Turn, error) { c.problem = compactionUncertain(); s.paused = true; return Turn{}, c.problem }
	if c.checkNativeStateLocked(ctx, true) != nil || c.noForkWorkLocked(ctx, c.thread) != nil {
		return fail()
	}
	turns, err := c.contextTurnsLocked(ctx, "asc", nil, true)
	if err != nil || uint32(len(turns)) != p.TurnsCount || historyDigest(turns) != p.HistoryDigest {
		return fail()
	}
	for n, raw := range turns {
		turn, e := decodeTurn(raw)
		if e != nil || turn.ID != p.Revert.RetainedTurnIDs[n] {
			return fail()
		}
	}
	if c.checkNativeStateLocked(ctx, true) != nil || ctx.Err() != nil {
		return fail()
	}
	var last Turn
	if len(turns) > 0 {
		var inputs []HistoricalInput
		last, inputs, err = decodeLatestTurnInputs(marshalForkPage(turns[len(turns)-1:]), c.nativeImageInput)
		if err != nil {
			return fail()
		}
		tracked := trackedTurn{Turn: last, Mode: p.Source.Mode}
		for _, input := range inputs {
			s.inputs[input.ID] = inputAttempt{Digest: input.PromptDigest, SkillDigest: input.SkillDigest, TurnID: last.ID}
			tracked.Inputs = append(tracked.Inputs, input.ID)
		}
		s.turns[last.ID] = tracked
	}
	s.continuationPending, s.paused = false, false
	return last, nil
}

// RevertIntent is a private exact send claim. Recovery can only inspect it.
type RevertIntent struct {
	Version         uint32                 `json:"version"`
	ActionID        domain.ID              `json:"action_id"`
	Source          ContinuationCheckpoint `json:"source"`
	Target          HistoricalInput        `json:"target"`
	BeforeTurnID    domain.ID              `json:"before_turn_id"`
	ExpectedHistory []json.RawMessage      `json:"expected_history"`
}

func (c *Client) ReconcileRevert(ctx context.Context, intent RevertIntent) (CompactedCheckpoint, error) {
	if !c.revertHistory || intent.Version != 1 || intent.ActionID.Validate() != nil || intent.BeforeTurnID.Validate() != nil || intent.Source.validate(ResumeAfterTerminal) != nil || intent.ExpectedHistory == nil || len(intent.ExpectedHistory) > maxForkTurns || c.thread != intent.Source.ThreadID || c.execution == nil || !sameEffectiveSettings(c.execution.settings, intent.Source.Effective) {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return CompactedCheckpoint{}, err
	}
	defer func() { <-c.control }()
	if c.problem != nil || c.execution.active != "" || len(c.execution.pending) != 0 || c.execution.interactions.blocksInput() || len(c.subagents) != 0 || c.checkNativeStateLocked(ctx, true) != nil || c.noForkWorkLocked(ctx, c.thread) != nil {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	history, err := c.contextTurnsLocked(ctx, "asc", nil, true)
	if err != nil || !slices.EqualFunc(history, intent.ExpectedHistory, equivalentForkJSON) {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	reply, err := c.wire.Call(ctx, domain.NewID(), "thread/read", struct {
		Thread domain.ID `json:"threadId"`
		Turns  bool      `json:"includeTurns"`
	}{c.thread, false})
	var envelope struct {
		Thread json.RawMessage `json:"thread"`
	}
	if err != nil || reply.ErrorCode != nil || domain.Decode(reply.Result, &envelope) != nil {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	thread, err := decodeThread(envelope.Thread, c.version)
	if err != nil || thread.ID != c.thread || thread.SessionID != intent.Source.SessionID || thread.Status.Type != ThreadIdle || thread.ParentThreadID != nil || thread.ForkedFromID != nil || thread.Cwd != intent.Source.Effective.Cwd || thread.ModelProvider != intent.Source.Effective.Provider {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	var path string
	if json.Unmarshal(thread.Path, &path) != nil {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	digest, err := forkRolloutDigest(ctx, c.home, path)
	if err != nil {
		return CompactedCheckpoint{}, err
	}
	ids := []domain.ID{}
	for _, raw := range history {
		turn, e := decodeTurn(raw)
		if e != nil || turn.ID == intent.BeforeTurnID {
			return CompactedCheckpoint{}, compactionUncertain()
		}
		ids = append(ids, turn.ID)
	}
	p := CompactedCheckpoint{Version: 2, Source: intent.Source, Records: []CompactionRecord{}, TurnsCount: uint32(len(history)), HistoryDigest: historyDigest(history), RolloutPath: path, RolloutDigest: hex.EncodeToString(digest[:]), Revert: &RevertedContext{ActionID: intent.ActionID, BeforeTurnID: intent.BeforeTurnID, InputID: intent.Target.ID, PromptDigest: hex.EncodeToString(intent.Target.PromptDigest[:]), RetainedTurnIDs: ids}}
	if VerifyRevertIntent(intent, p) != nil || c.checkNativeStateLocked(ctx, true) != nil {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	c.execution.paused = true
	return p, nil
}

// VerifyRevertIntent joins the original private send claim to replacement
// history. Neither a public result nor a rewritten checkpoint can replace it.
func VerifyRevertIntent(intent RevertIntent, p CompactedCheckpoint) error {
	if intent.Version != 1 || intent.ActionID.Validate() != nil || intent.Target.ID.Validate() != nil || intent.BeforeTurnID.Validate() != nil || intent.ExpectedHistory == nil || len(intent.ExpectedHistory) > maxForkTurns || p.Version != 2 || p.Revert == nil || p.Revert.validate() != nil || p.Source.validate(ResumeAfterTerminal) != nil || p.Revert.ActionID != intent.ActionID || p.Revert.InputID != intent.Target.ID || p.Revert.BeforeTurnID != intent.BeforeTurnID || p.Revert.PromptDigest != hex.EncodeToString(intent.Target.PromptDigest[:]) || p.TurnsCount != uint32(len(intent.ExpectedHistory)) || p.HistoryDigest != historyDigest(intent.ExpectedHistory) || len(p.Revert.RetainedTurnIDs) != len(intent.ExpectedHistory) {
		return compactionUncertain()
	}
	a, _ := json.Marshal(intent.Source)
	b, _ := json.Marshal(p.Source)
	if !slices.Equal(a, b) {
		return compactionUncertain()
	}
	size := 0
	for n, raw := range intent.ExpectedHistory {
		size += len(raw)
		turn, e := decodeTurn(raw)
		if e != nil || !turn.Status.terminal() || turn.ID != p.Revert.RetainedTurnIDs[n] || size > 4<<20 {
			return compactionUncertain()
		}
	}
	return nil
}
