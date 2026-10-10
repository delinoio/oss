// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// GoalStatus preserves the native lifecycle; a stopped process does not prove
// that the native goal is paused, cleared or complete.
type GoalStatus string

const (
	GoalActive        GoalStatus = "active"
	GoalPaused        GoalStatus = "paused"
	GoalBlocked       GoalStatus = "blocked"
	GoalUsageLimited  GoalStatus = "usageLimited"
	GoalBudgetLimited GoalStatus = "budgetLimited"
	GoalComplete      GoalStatus = "complete"
)

func (s GoalStatus) valid() bool {
	switch s {
	case GoalActive, GoalPaused, GoalBlocked, GoalUsageLimited, GoalBudgetLimited, GoalComplete:
		return true
	default:
		return false
	}
}

// Goal is a read-only native snapshot. It contains no action receipt or cleanup
// proof; an absence read cannot acknowledge an uncertain earlier clear.
type Goal struct {
	ThreadID        domain.ID  `json:"threadId"`
	Objective       string     `json:"objective"`
	Status          GoalStatus `json:"status"`
	TokenBudget     *int64     `json:"tokenBudget"`
	TokensUsed      int64      `json:"tokensUsed"`
	TimeUsedSeconds int64      `json:"timeUsedSeconds"`
	CreatedAt       int64      `json:"createdAt"`
	UpdatedAt       int64      `json:"updatedAt"`
}

const maxGoalObjectiveCharacters = 4000

func validGoalObjective(value string) bool {
	// The original native limit counts Unicode scalar values, not UTF-8 bytes.
	return domain.Text(value, "goal objective", 4*maxGoalObjectiveCharacters, true) == nil && utf8.RuneCountInString(value) <= maxGoalObjectiveCharacters
}

// DecodeGoal requires the complete closed native snapshot. Missing counters or
// a missing nullable budget are not equivalent to zero or an unlimited goal.
func DecodeGoal(raw json.RawMessage, originalThread domain.ID) (Goal, error) {
	var wire struct {
		ThreadID        domain.ID       `json:"threadId"`
		Objective       string          `json:"objective"`
		Status          GoalStatus      `json:"status"`
		TokenBudget     json.RawMessage `json:"tokenBudget"`
		TokensUsed      *int64          `json:"tokensUsed"`
		TimeUsedSeconds *int64          `json:"timeUsedSeconds"`
		CreatedAt       *int64          `json:"createdAt"`
		UpdatedAt       *int64          `json:"updatedAt"`
	}
	if domain.Decode(raw, &wire) != nil || originalThread.Validate() != nil || wire.ThreadID != originalThread || !validGoalObjective(wire.Objective) || !wire.Status.valid() || len(wire.TokenBudget) == 0 || wire.TokensUsed == nil || *wire.TokensUsed < 0 || wire.TimeUsedSeconds == nil || *wire.TimeUsedSeconds < 0 || wire.CreatedAt == nil || *wire.CreatedAt < 0 || wire.UpdatedAt == nil || *wire.UpdatedAt < *wire.CreatedAt {
		return Goal{}, goalUncertain()
	}
	var budget *int64
	if json.Unmarshal(wire.TokenBudget, &budget) != nil || budget != nil && *budget <= 0 {
		return Goal{}, goalUncertain()
	}
	return Goal{ThreadID: wire.ThreadID, Objective: wire.Objective, Status: wire.Status, TokenBudget: budget, TokensUsed: *wire.TokensUsed, TimeUsedSeconds: *wire.TimeUsedSeconds, CreatedAt: *wire.CreatedAt, UpdatedAt: *wire.UpdatedAt}, nil
}

func goalUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The original native goal requires reconciliation.", "Retain the original goal action, native history and independent cleanup evidence; never resend an uncertain mutation.")
}

// GoalSet preserves omitted budget (keep) versus explicit null (native default)
// and a positive integer. Product callers cannot provide native provenance.
type GoalSet struct {
	Objective   *string         `json:"objective,omitempty"`
	Status      *GoalStatus     `json:"status,omitempty"`
	TokenBudget json.RawMessage `json:"tokenBudget,omitempty"`
}

func (p GoalSet) validate() error {
	if p.Objective == nil && p.Status == nil && len(p.TokenBudget) == 0 {
		return goalUncertain()
	}
	if p.Objective != nil && !validGoalObjective(strings.TrimSpace(*p.Objective)) || p.Status != nil && !p.Status.valid() {
		return goalUncertain()
	}
	if len(p.TokenBudget) > 0 {
		var value *int64
		if domain.Decode(p.TokenBudget, &value) != nil || value != nil && *value <= 0 {
			return goalUncertain()
		}
	}
	return nil
}

type GoalAction string

const (
	GoalSetAction   GoalAction = "thread/goal/set"
	GoalClearAction GoalAction = "thread/goal/clear"
)

// GoalIntent is passed to the original Worker's durable once-only claim before
// writing the native mutation. The claim must survive loss of this connection.
type GoalIntent struct {
	RequestID domain.ID  `json:"request_id"`
	ThreadID  domain.ID  `json:"thread_id"`
	Action    GoalAction `json:"action"`
	Set       *GoalSet   `json:"set,omitempty"`
}

type GoalResult struct {
	RequestID domain.ID
	ThreadID  domain.ID
	Action    GoalAction
	Goal      *Goal
	Cleared   bool
}

func (c *Client) verifyGoalsLocked(ctx context.Context) error {
	enabled, err := c.observeGoalsEnabledLocked(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return incompatible()
	}
	return nil
}

func (c *Client) observeGoalsEnabledLocked(ctx context.Context) (bool, error) {
	if !c.nativeGoals || c.sidechat != "" || c.mode != ThreadProtocol || c.thread.Validate() != nil || c.execution == nil {
		return false, incompatible()
	}
	response, err := c.wire.Call(ctx, domain.NewID(), "experimentalFeature/list", struct {
		Thread domain.ID `json:"threadId"`
		Limit  int       `json:"limit"`
	}{c.thread, 256})
	if err != nil {
		return false, err
	}
	var observed struct {
		Data []struct {
			Name           string  `json:"name"`
			Stage          string  `json:"stage"`
			DisplayName    *string `json:"displayName"`
			Description    *string `json:"description"`
			Announcement   *string `json:"announcement"`
			Enabled        *bool   `json:"enabled"`
			DefaultEnabled *bool   `json:"defaultEnabled"`
		} `json:"data"`
		Next json.RawMessage `json:"nextCursor"`
	}
	if response.ErrorCode != nil || domain.Decode(response.Result, &observed) != nil || observed.Data == nil || len(observed.Data) > 256 || string(observed.Next) != "null" {
		return false, incompatible()
	}
	seen := map[string]bool{}
	goals := false
	for _, feature := range observed.Data {
		if domain.Text(feature.Name, "native feature", 128, true) != nil || seen[feature.Name] || feature.Enabled == nil || feature.DefaultEnabled == nil || !slices.Contains([]string{"stable", "beta", "underDevelopment", "deprecated", "removed"}, feature.Stage) {
			return false, incompatible()
		}
		seen[feature.Name] = true
		if feature.Name == "goals" {
			goals = *feature.Enabled
		}
	}
	return goals, nil
}

// ReadGoal observes state without clearing an earlier mutation's uncertainty.
// It is safe only on the original authenticated, bound process and thread.
func (c *Client) ReadGoal(ctx context.Context, request domain.ID) (*Goal, error) {
	if request.Validate() != nil {
		return nil, goalUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return nil, err
	}
	defer func() { <-c.control }()
	if err := c.verifyGoalsLocked(ctx); err != nil {
		return nil, err
	}
	goal, err := c.readGoalLocked(ctx, request, c.thread)
	if err == nil {
		c.execution.goal, c.execution.goalKnown = goal, true
	}
	return goal, err
}

// MutateGoal never retries the native wire, including after a definitive error.
// Native set/clear have no idempotency receipt and can record user intent before
// failing to update the native database. Only the original claim may reconcile.
func (c *Client) MutateGoal(ctx context.Context, request domain.ID, action GoalAction, set *GoalSet, claim func(GoalIntent) error) (result GoalResult, returned error) {
	if request.Validate() != nil || claim == nil || (action != GoalSetAction && action != GoalClearAction) || (action == GoalSetAction) != (set != nil) || set != nil && set.validate() != nil {
		return result, goalUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return result, err
	}
	defer func() { <-c.control }()
	if c.problem != nil {
		return result, c.problem
	}
	if err := c.verifyGoalsLocked(ctx); err != nil {
		return result, err
	}
	if c.goalMutations[request] {
		return result, goalUncertain()
	}
	// Copy caller-owned fields before the claim; later caller mutation cannot
	// substitute parameters between a durable identity and its native send.
	var copied *GoalSet
	if set != nil {
		raw, _ := json.Marshal(set)
		copied = &GoalSet{}
		if domain.Decode(raw, copied) != nil {
			return result, goalUncertain()
		}
	}
	intent := GoalIntent{RequestID: request, ThreadID: c.thread, Action: action, Set: copied}
	params := struct {
		Thread domain.ID `json:"threadId"`
		Origin string    `json:"origin"`
		*GoalSet
	}{c.thread, "user", copied}
	// Marshal before the callback to freeze even an accidentally retained
	// pointer in the claim implementation. Native provenance is always user.
	paramsRaw, err := json.Marshal(params)
	if err != nil {
		return result, goalUncertain()
	}
	if err := claim(intent); err != nil {
		return result, err
	}
	if c.goalMutations == nil {
		c.goalMutations = map[domain.ID]bool{}
	}
	c.goalMutations[request] = true
	response, err := c.wire.Call(ctx, request, string(action), json.RawMessage(paramsRaw))
	fail := func() (GoalResult, error) {
		c.problem = goalUncertain()
		c.execution.paused = true
		return GoalResult{}, c.problem
	}
	if err != nil || response.ErrorCode != nil {
		return fail()
	}
	result = GoalResult{RequestID: request, ThreadID: c.thread, Action: action}
	if action == GoalSetAction {
		var observed struct {
			Goal json.RawMessage `json:"goal"`
		}
		if domain.Decode(response.Result, &observed) != nil {
			return fail()
		}
		goal, err := DecodeGoal(observed.Goal, c.thread)
		if err != nil {
			return fail()
		}
		result.Goal = &goal
		c.execution.goal, c.execution.goalKnown = &goal, true
	} else {
		var observed struct {
			Cleared *bool `json:"cleared"`
		}
		if domain.Decode(response.Result, &observed) != nil || observed.Cleared == nil {
			return fail()
		}
		result.Cleared = *observed.Cleared
		if result.Cleared {
			c.execution.goal, c.execution.goalKnown = nil, true
		}
	}
	return result, nil
}

// GoalObservation is ordered native state, never a mutation receipt. A cleared
// observation also occurs as the native resume snapshot when no goal exists.
type GoalObservation struct {
	Goal    *Goal
	Cleared bool
}

func (c *Client) observeGoalLocked(native nativewire.Event) (Event, error) {
	if !c.nativeGoals || c.sidechat != "" {
		return privateNative(native), nil
	}
	if c.thread.Validate() != nil || c.execution == nil {
		return Event{}, goalUncertain()
	}
	var thread domain.ID
	var turn domain.ID
	observed := &GoalObservation{}
	switch native.Method {
	case "thread/goal/updated":
		var wire struct {
			Thread domain.ID       `json:"threadId"`
			Turn   json.RawMessage `json:"turnId"`
			Goal   json.RawMessage `json:"goal"`
		}
		if domain.Decode(native.Params, &wire) != nil || wire.Thread != c.thread || len(wire.Turn) == 0 {
			return Event{}, goalUncertain()
		}
		var nullable *domain.ID
		if json.Unmarshal(wire.Turn, &nullable) != nil || nullable != nil && nullable.Validate() != nil {
			return Event{}, goalUncertain()
		}
		if nullable != nil {
			turn = *nullable
			if _, known := c.execution.turns[turn]; !known {
				return Event{}, goalUncertain()
			}
		}
		goal, err := DecodeGoal(wire.Goal, c.thread)
		if err != nil {
			return Event{}, err
		}
		observed.Goal = &goal
		thread = wire.Thread
	case "thread/goal/cleared":
		var wire struct {
			Thread domain.ID `json:"threadId"`
		}
		if domain.Decode(native.Params, &wire) != nil || wire.Thread != c.thread {
			return Event{}, goalUncertain()
		}
		observed.Cleared = true
		thread = wire.Thread
	default:
		return privateNative(native), nil
	}
	c.execution.goal, c.execution.goalKnown = observed.Goal, true
	return Event{Kind: GoalObservedEvent, ThreadID: thread, TurnID: turn, Goal: observed, Correlated: true}, nil
}

// Goal tool outputs are private native content. Validate their closed original
// shape and name but never treat their text/JSON as product authorization,
// an action receipt, a budget override or a native completion observation.
func nativeGoalToolOutput(raw json.RawMessage) bool {
	var item struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Namespace json.RawMessage `json:"namespace"`
		Output    json.RawMessage `json:"output"`
	}
	if domain.DecodeBounded(raw, &item, 32768) != nil || item.Type != "functionCallOutput" || domain.Text(item.ID, "native goal tool identity", 1024, true) != nil || !slices.Contains([]string{"create_goal", "get_goal", "update_goal"}, item.Name) || string(item.Namespace) != "null" {
		return false
	}
	var text string
	return domain.Decode(item.Output, &text) == nil && domain.Text(text, "native goal tool output", 20000, false) == nil
}
