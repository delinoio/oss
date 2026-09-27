package claude

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type ProgressKind string
type SessionStatus string
type CompactResult string
type NativeSeconds string

const (
	ThinkingTokensEstimated ProgressKind  = "thinking-tokens-estimated"
	SessionStatusObserved   ProgressKind  = "session-status"
	ToolProgressObserved    ProgressKind  = "tool-progress"
	ToolSummaryObserved     ProgressKind  = "tool-summary"
	SessionRequesting       SessionStatus = "requesting"
	SessionCompacting       SessionStatus = "compacting"
	CompactSucceeded        CompactResult = "success"
	CompactFailed           CompactResult = "failed"
)

func (n *NativeSeconds) UnmarshalJSON(raw []byte) error {
	if len(raw) == 0 || len(raw) > 64 || raw[0] < '0' || raw[0] > '9' || !json.Valid(raw) {
		return lifecycleUncertain()
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || value < 0 || math.IsInf(value, 0) || math.IsNaN(value) {
		return lifecycleUncertain()
	}
	*n = NativeSeconds(raw)
	return nil
}

type NativeRetryObservation struct {
	AgentID       string  `json:"agent_id"`
	Attempt       uint64  `json:"attempt"`
	MaxRetries    uint64  `json:"max_retries"`
	DelayMS       uint64  `json:"retry_delay_ms"`
	ErrorStatus   *uint16 `json:"error_status"`
	ErrorCategory string  `json:"error_category"`
}

func (r *NativeRetryObservation) UnmarshalJSON(raw []byte) error {
	type plain NativeRetryObservation
	var value plain
	if decodeNativeObject(raw, &value) != nil || domain.Text(value.AgentID, "native retry agent", 1024, true) != nil || domain.Text(value.ErrorCategory, "native retry category", 1024, true) != nil || (value.ErrorStatus != nil && (*value.ErrorStatus < 400 || *value.ErrorStatus > 599)) {
		return lifecycleUncertain()
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for _, key := range []string{"attempt", "max_retries", "retry_delay_ms", "error_status"} {
		if field, ok := fields[key]; !ok || (key != "error_status" && bytes.Equal(bytes.TrimSpace(field), []byte("null"))) {
			return lifecycleUncertain()
		}
	}
	*r = NativeRetryObservation(value)
	return nil
}

// Progress remains native observation. A permission-mode report cannot grant
// a permission change, a tool summary cannot complete tools, and a retry report
// cannot authorize an adapter retry, account fallback or a new input.
// These counters are native streaming estimates, not provider usage or billing.
// Preserve their original values without attributing them to a provider message.
type NativeThinkingEstimate struct {
	Tokens uint64
	Delta  uint64
}

type NativeProgressObservation struct {
	Thinking       *NativeThinkingEstimate
	Kind           ProgressKind
	Status         *SessionStatus
	Permission     *NativePermission
	CompactResult  *CompactResult
	CompactError   *string
	ToolID         string
	ToolName       string
	ParentToolID   *string
	Elapsed        *NativeSeconds
	TaskID         *string
	Heartbeat      *bool
	SubagentType   *string
	Retry          *NativeRetryObservation
	Summary        *string
	PrecedingTools []string
}

func (b *ExecutionBinding) observeProgress(event StreamEvent) (*NativeProgressObservation, error) {
	// Native requesting status precedes the original user replay. It is an
	// activity observation and must never acknowledge that input.
	if !b.initialized || (event.Type != "system" && !b.accepted) {
		return nil, lifecycleUncertain()
	}
	switch event.Type {
	case "system":
		var header struct {
			Subtype string `json:"subtype"`
		}
		if json.Unmarshal(event.Body, &header) != nil {
			return nil, lifecycleUncertain()
		}
		if header.Subtype == "thinking_tokens" {
			var value struct {
				Type    string    `json:"type"`
				Session domain.ID `json:"session_id"`
				UUID    string    `json:"uuid"`
				Subtype string    `json:"subtype"`
				Tokens  *uint64   `json:"estimated_tokens"`
				Delta   *uint64   `json:"estimated_tokens_delta"`
			}
			if !b.accepted || decodeNativeObject(event.Body, &value) != nil || value.Tokens == nil || value.Delta == nil {
				return nil, lifecycleUncertain()
			}
			return &NativeProgressObservation{Kind: ThinkingTokensEstimated, Thinking: &NativeThinkingEstimate{Tokens: *value.Tokens, Delta: *value.Delta}}, nil
		}
		return decodeSessionProgress(event.Body)
	case "tool_progress":
		var value struct {
			Type         string                  `json:"type"`
			Session      domain.ID               `json:"session_id"`
			UUID         string                  `json:"uuid"`
			ID           string                  `json:"tool_use_id"`
			Name         string                  `json:"tool_name"`
			Parent       *string                 `json:"parent_tool_use_id"`
			Elapsed      *NativeSeconds          `json:"elapsed_time_seconds"`
			Task         *string                 `json:"task_id"`
			Heartbeat    *bool                   `json:"heartbeat"`
			SubagentType *string                 `json:"subagent_type"`
			Retry        *NativeRetryObservation `json:"subagent_retry"`
		}
		if decodeNativeObject(event.Body, &value) != nil || value.Elapsed == nil || !taskTexts(value.SubagentType) {
			return nil, lifecycleUncertain()
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(event.Body, &fields)
		if len(fields["parent_tool_use_id"]) == 0 || (value.Parent != nil && domain.Text(*value.Parent, "native parent tool", 1024, true) != nil) {
			return nil, lifecycleUncertain()
		}
		tool, ok := b.content.tools[value.ID]
		parent := ""
		if value.Parent != nil {
			parent = *value.Parent
		}
		if !ok || tool.name != value.Name || tool.parent != parent {
			return nil, lifecycleUncertain()
		}
		activeTask := false
		if value.Task != nil {
			task, exists := b.tasks[*value.Task]
			if !exists || task.tool != value.ID || task.status.terminal() {
				return nil, lifecycleUncertain()
			}
			activeTask = true
		}
		if tool.finished && !activeTask {
			return nil, lifecycleUncertain()
		}
		return &NativeProgressObservation{Kind: ToolProgressObserved, ToolID: value.ID, ToolName: value.Name, ParentToolID: value.Parent, Elapsed: value.Elapsed, TaskID: value.Task, Heartbeat: value.Heartbeat, SubagentType: value.SubagentType, Retry: value.Retry}, nil
	case "tool_use_summary":
		var value struct {
			Type    string    `json:"type"`
			Session domain.ID `json:"session_id"`
			UUID    string    `json:"uuid"`
			Summary *string   `json:"summary"`
			Tools   []string  `json:"preceding_tool_use_ids"`
		}
		if decodeNativeObject(event.Body, &value) != nil || value.Summary == nil || !taskTexts(value.Summary) || len(value.Tools) == 0 || !uniqueText(value.Tools, 128, 1024) {
			return nil, lifecycleUncertain()
		}
		for _, id := range value.Tools {
			if b.content.tools[id].name == "" {
				return nil, lifecycleUncertain()
			}
		}
		return &NativeProgressObservation{Kind: ToolSummaryObserved, Summary: value.Summary, PrecedingTools: value.Tools}, nil
	default:
		return nil, lifecycleUncertain()
	}
}

func decodeSessionProgress(raw json.RawMessage) (*NativeProgressObservation, error) {
	var value struct {
		Type          string            `json:"type"`
		Session       domain.ID         `json:"session_id"`
		UUID          string            `json:"uuid"`
		Subtype       string            `json:"subtype"`
		Status        *SessionStatus    `json:"status"`
		Permission    *NativePermission `json:"permissionMode"`
		CompactResult *CompactResult    `json:"compact_result"`
		CompactError  *string           `json:"compact_error"`
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if decodeNativeObject(raw, &value) != nil || value.Subtype != "status" || len(fields["status"]) == 0 || (value.Status != nil && !slices.Contains([]SessionStatus{SessionRequesting, SessionCompacting}, *value.Status)) || (value.Permission != nil && !validNativePermission(*value.Permission)) || (value.CompactResult != nil && !slices.Contains([]CompactResult{CompactSucceeded, CompactFailed}, *value.CompactResult)) || !taskTexts(value.CompactError) {
		return nil, lifecycleUncertain()
	}
	return &NativeProgressObservation{Kind: SessionStatusObserved, Status: value.Status, Permission: value.Permission, CompactResult: value.CompactResult, CompactError: value.CompactError}, nil
}
