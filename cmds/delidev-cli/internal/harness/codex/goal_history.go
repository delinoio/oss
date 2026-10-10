// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Goal history is private source evidence. A digest pins the complete native
// history, including native user-goal annotations, without rebuilding it from
// product prompts or interpreting text markers as user authorization.
type GoalHistoryCheckpoint struct {
	Version       uint32          `json:"version"`
	Goal          json.RawMessage `json:"goal"`
	TurnsCount    uint32          `json:"turns_count"`
	HistoryDigest string          `json:"history_digest"`
	RolloutPath   string          `json:"rollout_path"`
	RolloutDigest string          `json:"rollout_digest"`
	InputTurnID   domain.ID       `json:"input_turn_id"`
	OwnedTurns    []domain.ID     `json:"owned_turns"`
}

func (p GoalHistoryCheckpoint) Valid(thread domain.ID) bool { return p.validate(thread) == nil }
func (p GoalHistoryCheckpoint) validate(thread domain.ID) error {
	if p.Version != 1 || len(p.Goal) == 0 || p.TurnsCount == 0 || p.TurnsCount > maxForkTurns || !contextDigest(p.HistoryDigest) || !contextDigest(p.RolloutDigest) || p.InputTurnID.Validate() != nil || len(p.OwnedTurns) > maxTrackedTurns {
		return goalUncertain()
	}
	seen := map[domain.ID]bool{}
	for _, turn := range p.OwnedTurns {
		if turn.Validate() != nil || seen[turn] {
			return goalUncertain()
		}
		seen[turn] = true
	}
	if string(p.Goal) != "null" {
		goal, err := DecodeGoal(p.Goal, thread)
		if err != nil || goal.Status == GoalActive {
			return goalUncertain()
		}
	}
	return nil
}
func goalJSON(goal *Goal) json.RawMessage { raw, _ := json.Marshal(goal); return raw }
func sameGoalSnapshot(raw json.RawMessage, goal *Goal, thread domain.ID) bool {
	if string(raw) == "null" {
		return goal == nil
	}
	original, err := DecodeGoal(raw, thread)
	return err == nil && goal != nil && original.ThreadID == goal.ThreadID && original.Objective == goal.Objective && original.Status == goal.Status && equalOptionalInt64(original.TokenBudget, goal.TokenBudget) && original.TokensUsed == goal.TokensUsed && original.TimeUsedSeconds == goal.TimeUsedSeconds && original.CreatedAt == goal.CreatedAt && original.UpdatedAt == goal.UpdatedAt
}
func equalOptionalInt64(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func (p GoalHistoryCheckpoint) matches(turns []json.RawMessage) bool {
	return uint32(len(turns)) == p.TurnsCount && historyDigest(turns) == p.HistoryDigest
}

// Native goal turns can contain source-owned context without a product client
// input ID. Such items remain opaque native history and never become accepted
// product input. Any attributed input still must match its original binding.
func decodeGoalTurnInputs(raw json.RawMessage, reader func([]json.RawMessage) (domain.SessionInput, error)) (Turn, []HistoricalInput, error) {
	var wire turnWire
	if domain.DecodeBounded(raw, &wire, 16<<20) != nil || wire.ItemsView != "full" {
		return Turn{}, nil, goalUncertain()
	}
	turn, err := decodeTurn(raw)
	if err != nil || !turn.Status.terminal() {
		return Turn{}, nil, goalUncertain()
	}
	var inputs []HistoricalInput
	seen := map[domain.ID]bool{}
	for _, rawItem := range wire.Items {
		var identity struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(rawItem, &identity) != nil {
			return Turn{}, nil, goalUncertain()
		}
		if identity.Type != "userMessage" {
			continue
		}
		var item struct {
			Type     string            `json:"type"`
			ID       string            `json:"id"`
			ClientID json.RawMessage   `json:"clientId"`
			Content  []json.RawMessage `json:"content"`
		}
		if domain.Decode(rawItem, &item) != nil || domain.Text(item.ID, "native history item", 1024, true) != nil || len(item.ClientID) == 0 || item.Content == nil {
			return Turn{}, nil, goalUncertain()
		}
		var client *domain.ID
		if domain.Decode(item.ClientID, &client) != nil {
			return Turn{}, nil, goalUncertain()
		}
		if client == nil {
			continue
		}
		if client.Validate() != nil || seen[*client] {
			return Turn{}, nil, goalUncertain()
		}
		plain, skills, err := nativeInputSkills(item.Content)
		if err != nil {
			return Turn{}, nil, err
		}
		decoded, err := reader(plain)
		if err != nil {
			return Turn{}, nil, err
		}
		seen[*client] = true
		inputs = append(inputs, HistoricalInput{ID: *client, PromptDigest: decoded.InputDigest(), SkillDigest: nativeSkillDigest(skills)})
	}
	return turn, inputs, nil
}

func (c *Client) RetainGoalHistory(ctx context.Context, source ContinuationCheckpoint) (*GoalHistoryCheckpoint, error) {
	if !c.nativeGoals || source.validate(ResumeAfterTerminal) != nil {
		return nil, goalUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return nil, err
	}
	defer func() { <-c.control }()
	state := c.execution
	if state == nil || c.problem != nil || state.active != "" || state.interactions.blocksInput() || len(state.pending) != 0 || source.ThreadID != c.thread || source.SessionID != state.thread.SessionID || !sameEffectiveSettings(source.Effective, state.settings) || !state.goalKnown || state.goal != nil && state.goal.Status == GoalActive {
		return nil, goalUncertain()
	}
	turns, err := c.compactionTurnsLocked(ctx)
	if err != nil {
		return nil, err
	}
	proof := &GoalHistoryCheckpoint{Version: 1, Goal: goalJSON(state.goal), TurnsCount: uint32(len(turns)), HistoryDigest: historyDigest(turns)}
	var previous *GoalHistoryCheckpoint
	if state.goalBase != nil {
		previous = state.goalBase
		if previous.validate(c.thread) != nil || int(previous.TurnsCount) >= len(turns) || !previous.matches(turns[:previous.TurnsCount]) {
			return nil, goalUncertain()
		}
	}
	if previous != nil {
		proof.OwnedTurns = slices.Clone(previous.OwnedTurns)
	}
	found := map[domain.ID]bool{}
	for n, raw := range turns {
		turn, inputs, err := decodeGoalTurnInputs(raw, c.nativeImageInput)
		if err != nil {
			return nil, err
		}
		if previous != nil && n < int(previous.TurnsCount) {
			continue
		}
		tracked, known := state.turns[turn.ID]
		if !known || tracked.Turn.Status != turn.Status || found[turn.ID] {
			return nil, goalUncertain()
		}
		found[turn.ID] = true
		if state.goalTurns[turn.ID] {
			if len(inputs) != 0 {
				return nil, goalUncertain()
			}
			proof.OwnedTurns = append(proof.OwnedTurns, turn.ID)
		} else {
			expected := []HistoricalInput{}
			for _, id := range tracked.Inputs {
				binding, ok := state.inputs[id]
				if !ok || binding.TurnID != turn.ID {
					return nil, goalUncertain()
				}
				expected = append(expected, HistoricalInput{ID: id, PromptDigest: binding.Digest, SkillDigest: binding.SkillDigest})
			}
			if !slices.Equal(inputs, expected) || len(expected) == 0 {
				return nil, goalUncertain()
			}
			if turn.ID == source.TurnID || proof.InputTurnID == "" {
				proof.InputTurnID = turn.ID
			}
		}
	}
	// The source terminal may be the original product turn, with no automatic
	// iteration. It still pins native goal annotations and non-active status.
	if proof.InputTurnID == "" {
		if previous == nil {
			return nil, goalUncertain()
		}
		proof.InputTurnID = previous.InputTurnID
	}
	if !found[source.TurnID] {
		return nil, goalUncertain()
	}
	native, err := c.readThreadLocked(ctx, domain.NewID(), c.thread)
	if err != nil || native.Status.Type != ThreadIdle {
		return nil, goalUncertain()
	}
	if json.Unmarshal(native.Path, &proof.RolloutPath) != nil {
		return nil, goalUncertain()
	}
	digest, err := forkRolloutDigest(ctx, c.home, proof.RolloutPath)
	if err != nil {
		return nil, err
	}
	proof.RolloutDigest = hex.EncodeToString(digest[:])
	if proof.validate(c.thread) != nil {
		return nil, goalUncertain()
	}
	return proof, nil
}

func (c *Client) readGoalLocked(ctx context.Context, request, thread domain.ID) (*Goal, error) {
	response, err := c.wire.Call(ctx, request, "thread/goal/get", struct {
		Thread domain.ID `json:"threadId"`
	}{thread})
	if err != nil {
		return nil, err
	}
	var observed struct {
		Goal json.RawMessage `json:"goal"`
	}
	if response.ErrorCode != nil || domain.Decode(response.Result, &observed) != nil || len(observed.Goal) == 0 {
		return nil, goalUncertain()
	}
	if string(observed.Goal) == "null" {
		return nil, nil
	}
	goal, err := DecodeGoal(observed.Goal, thread)
	if err != nil {
		return nil, err
	}
	return &goal, nil
}

// InspectOriginalGoal does not Resume, bind or adopt a native thread. In
// particular, active/uncertain goal recovery never executes the native Resume
// operation, whose idle hook can immediately start another automatic turn.
func (c *Client) InspectOriginalGoal(ctx context.Context, source ContinuationCheckpoint) (*Goal, error) {
	if !c.nativeGoals || c.sidechat != "" || c.mode != ThreadProtocol || source.validate(ResumeAfterTerminal) != nil {
		return nil, goalUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return nil, err
	}
	defer func() { <-c.control }()
	native, err := c.readThreadLocked(ctx, domain.NewID(), source.ThreadID)
	if err != nil || native.ID != source.ThreadID || native.SessionID != source.SessionID || !nativePathEqual(native.Cwd, source.Effective.Cwd) || native.ModelProvider != source.Effective.Provider {
		return nil, goalUncertain()
	}
	return c.readGoalLocked(ctx, domain.NewID(), source.ThreadID)
}
func (c *Client) VerifyGoalBeforeResume(ctx context.Context, source ContinuationCheckpoint) error {
	if source.GoalHistory == nil {
		return nil
	}
	if source.GoalHistory.validate(source.ThreadID) != nil {
		return goalUncertain()
	}
	digest, err := forkRolloutDigest(ctx, c.home, source.GoalHistory.RolloutPath)
	if err != nil || hex.EncodeToString(digest[:]) != source.GoalHistory.RolloutDigest {
		return goalUncertain()
	}
	goal, err := c.InspectOriginalGoal(ctx, source)
	if err != nil || goal != nil && goal.Status == GoalActive || !sameGoalSnapshot(source.GoalHistory.Goal, goal, source.ThreadID) {
		return goalUncertain()
	}
	return nil
}
