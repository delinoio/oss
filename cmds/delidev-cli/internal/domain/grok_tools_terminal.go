package domain

import (
	"math"
	"strconv"
)

const GrokClosedFirstTools GrokTerminalKind = "closed-first-tools"

type GrokToolsReason string

const (
	GrokToolsEndTurn            GrokToolsReason = "end_turn"
	GrokToolsPermissionRejected GrokToolsReason = "PermissionRejected"
)

type GrokFileRejection struct {
	Tool   string `json:"tool"`
	Reason string `json:"reason"`
}

// Tools retain their own native terminal and reported aggregate. There is no
// text-only history digest, synthetic Plan gate or continuation checkpoint.
type GrokToolsTerminal struct {
	Kind          GrokTerminalKind   `json:"kind"`
	NativeEventID string             `json:"native_event_id"`
	TimestampMS   string             `json:"timestamp_ms"`
	ElapsedMS     string             `json:"elapsed_ms"`
	Model         string             `json:"model"`
	Reason        GrokToolsReason    `json:"reason"`
	Counts        GrokResponseCounts `json:"counts"`
	TotalTokens   string             `json:"total_tokens"`
	ModelCalls    string             `json:"model_calls"`
	APIDurationMS string             `json:"api_duration_ms"`
	Turns         string             `json:"turns"`
	Rejection     *GrokFileRejection `json:"rejection,omitempty"`
}

func (v GrokToolsTerminal) Outcome() ExecutionOutcome {
	if v.Reason == GrokToolsPermissionRejected {
		return ExecutionStopped
	}
	return ExecutionSucceeded
}
func (v GrokToolsTerminal) Validate(thread string) error {
	if v.Kind != GrokClosedFirstTools || Text(v.Model, "native model", 256, true) != nil {
		return invalidGrokContent()
	}
	if _, e := GrokEventIndex(v.NativeEventID, thread); e != nil {
		return e
	}
	calls, ok := grokCount(v.ModelCalls)
	if !ok || calls == 0 || calls > 128 || v.Turns != v.ModelCalls {
		return invalidGrokContent()
	}
	if n, ok := grokCount(v.TimestampMS); !ok || n > 253402300799999 {
		return invalidGrokContent()
	}
	for _, s := range []string{v.ElapsedMS, v.TotalTokens, v.APIDurationMS} {
		if _, ok := grokCount(s); !ok {
			return invalidGrokContent()
		}
	}
	if (GrokResponseUsage{Ordinal: 1, Counts: v.Counts}).Validate() != nil {
		return invalidGrokContent()
	}
	if v.Reason == GrokToolsEndTurn && v.Rejection == nil {
		return nil
	}
	if v.Reason != GrokToolsPermissionRejected || v.Rejection == nil || v.Rejection.Tool != "write" || Text(v.Rejection.Reason, "native rejection reason", 4096, true) != nil {
		return invalidGrokContent()
	}
	return nil
}

// Sum only observed model-response primitives with checked exact arithmetic.
// Context estimates and reported totalTokens are never derived or added here.
func (v GrokResponseCounts) Add(other GrokResponseCounts) (GrokResponseCounts, error) {
	a := []string{v.Input, v.Output, v.CachedRead, v.CacheCreation, v.Reasoning}
	b := []string{other.Input, other.Output, other.CachedRead, other.CacheCreation, other.Reasoning}
	for i := range a {
		if a[i] == "" {
			a[i] = "0"
		}
		x, ok := grokCount(a[i])
		y, ok2 := grokCount(b[i])
		if !ok || !ok2 || y > math.MaxUint64-x {
			return v, invalidGrokContent()
		}
		a[i] = strconv.FormatUint(x+y, 10)
	}
	return GrokResponseCounts{Input: a[0], Output: a[1], CachedRead: a[2], CacheCreation: a[3], Reasoning: a[4]}, nil
}
