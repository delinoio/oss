// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strconv"
	"time"
	"unicode/utf8"
)

const NativeCodexGoalsV1 WorkerCapability = "native-codex-goals-v1"
const NativeGoalActionJob JobType = "native-goal-action"

type NativeGoalStatus string

const (
	NativeGoalActive        NativeGoalStatus = "active"
	NativeGoalPaused        NativeGoalStatus = "paused"
	NativeGoalBlocked       NativeGoalStatus = "blocked"
	NativeGoalUsageLimited  NativeGoalStatus = "usageLimited"
	NativeGoalBudgetLimited NativeGoalStatus = "budgetLimited"
	NativeGoalComplete      NativeGoalStatus = "complete"
)

func (s NativeGoalStatus) Valid() bool {
	switch s {
	case NativeGoalActive, NativeGoalPaused, NativeGoalBlocked, NativeGoalUsageLimited, NativeGoalBudgetLimited, NativeGoalComplete:
		return true
	default:
		return false
	}
}

type NativeGoalSnapshot struct {
	Objective       string           `json:"objective"`
	Status          NativeGoalStatus `json:"status"`
	TokenBudget     *string          `json:"token_budget"`
	TokensUsed      string           `json:"tokens_used"`
	TimeUsedSeconds string           `json:"time_used_seconds"`
	CreatedAt       string           `json:"created_at"`
	UpdatedAt       string           `json:"updated_at"`
}

func (g NativeGoalSnapshot) Validate() error {
	if Text(g.Objective, "goal objective", 16000, true) != nil || utf8.RuneCountInString(g.Objective) > 4000 || !g.Status.Valid() {
		return NativeGoalUncertain()
	}
	for _, value := range []string{g.TokensUsed, g.TimeUsedSeconds, g.CreatedAt, g.UpdatedAt} {
		n, e := strconv.ParseInt(value, 10, 64)
		if e != nil || n < 0 || strconv.FormatInt(n, 10) != value {
			return NativeGoalUncertain()
		}
	}
	created, _ := strconv.ParseInt(g.CreatedAt, 10, 64)
	updated, _ := strconv.ParseInt(g.UpdatedAt, 10, 64)
	if updated < created {
		return NativeGoalUncertain()
	}
	if g.TokenBudget != nil {
		n, e := strconv.ParseInt(*g.TokenBudget, 10, 64)
		if e != nil || n <= 0 || strconv.FormatInt(n, 10) != *g.TokenBudget {
			return NativeGoalUncertain()
		}
	}
	return nil
}

// Omitted observation is unknown; explicit null is observed absence. Neither
// acknowledges a lost action response or supplies process cleanup authority.
type NativeGoalView struct {
	Enabled              bool                  `json:"enabled"`
	SourceExecutionID    ID                    `json:"source_execution_id"`
	SourceNativeThreadID ID                    `json:"source_native_thread_id"`
	Observation          json.RawMessage       `json:"observation,omitempty"`
	ObservedAt           *time.Time            `json:"observed_at,omitempty"`
	ActionID             ID                    `json:"action_id,omitempty"`
	ActionState          NativeGoalActionState `json:"action_state,omitempty"`
	ProblemCode          Code                  `json:"problem_code,omitempty"`
}
type NativeGoalActionState string

const (
	NativeGoalAccepted       NativeGoalActionState = "accepted"
	NativeGoalClaimed        NativeGoalActionState = "claimed"
	NativeGoalAcknowledged   NativeGoalActionState = "acknowledged"
	NativeGoalUncertainState NativeGoalActionState = "uncertain"
)

type NativeGoalCommand string

const (
	NativeGoalSetCommand   NativeGoalCommand = "set"
	NativeGoalReadCommand  NativeGoalCommand = "read"
	NativeGoalClearCommand NativeGoalCommand = "clear"
)

type NativeGoalSet struct {
	Objective   *string           `json:"objective,omitempty"`
	Status      *NativeGoalStatus `json:"status,omitempty"`
	TokenBudget json.RawMessage   `json:"token_budget,omitempty"`
}

func (s NativeGoalSet) Validate() error {
	if s.Objective == nil && s.Status == nil && len(s.TokenBudget) == 0 {
		return NativeGoalUncertain()
	}
	if s.Objective != nil && (Text(*s.Objective, "goal objective", 16000, true) != nil || utf8.RuneCountInString(*s.Objective) > 4000) || s.Status != nil && !s.Status.Valid() {
		return NativeGoalUncertain()
	}
	if len(s.TokenBudget) > 0 {
		var n *int64
		if Decode(s.TokenBudget, &n) != nil || n != nil && *n <= 0 {
			return NativeGoalUncertain()
		}
	}
	return nil
}

type NativeGoalActionInput struct {
	Version        uint32            `json:"version"`
	RequestID      ID                `json:"request_id"`
	ExecutionJobID ID                `json:"execution_job_id"`
	ExecutionID    ID                `json:"execution_id"`
	NativeThreadID ID                `json:"native_thread_id"`
	Command        NativeGoalCommand `json:"command"`
	Set            *NativeGoalSet    `json:"set,omitempty"`
	ClaimID        ID                `json:"claim_id,omitempty"`
}

func (a NativeGoalActionInput) Validate() error {
	if a.Version != 1 || a.RequestID.Validate() != nil || a.ExecutionJobID.Validate() != nil || a.ExecutionID.Validate() != nil || a.NativeThreadID.Validate() != nil || a.ClaimID != "" && a.ClaimID.Validate() != nil {
		return NativeGoalUncertain()
	}
	switch a.Command {
	case NativeGoalSetCommand:
		if a.Set == nil {
			return NativeGoalUncertain()
		}
		return a.Set.Validate()
	case NativeGoalReadCommand, NativeGoalClearCommand:
		if a.Set != nil {
			return NativeGoalUncertain()
		}
	default:
		return NativeGoalUncertain()
	}
	return nil
}

type NativeGoalActionResult struct {
	Version        uint32          `json:"version"`
	ClaimID        ID              `json:"claim_id"`
	RequestID      ID              `json:"request_id"`
	ExecutionID    ID              `json:"execution_id"`
	NativeThreadID ID              `json:"native_thread_id"`
	Observation    json.RawMessage `json:"observation,omitempty"`
	Cleared        *bool           `json:"cleared,omitempty"`
	Problem        *Error          `json:"problem,omitempty"`
}

func NativeGoalUncertain() *Error {
	return Fail(RecoveryRequired, "The original native goal requires reconciliation.", "Retain the original action and native history. Do not resend an uncertain mutation or infer cleanup from a goal observation.")
}
