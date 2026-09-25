package claude

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// encoding/json accepts case-insensitive field aliases even when unknown
// fields are disabled. Native control facts require their exact profile keys;
// otherwise differently cased identity/settings fields can overwrite them.
func decodeNativeObject(raw []byte, target any) error {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || fields == nil {
		return lifecycleUncertain()
	}
	shape := reflect.TypeOf(target).Elem()
	allowed := make(map[string]bool, shape.NumField())
	for i := 0; i < shape.NumField(); i++ {
		name, _, _ := strings.Cut(shape.Field(i).Tag.Get("json"), ",")
		allowed[name] = true
	}
	for name := range fields {
		if !allowed[name] {
			return lifecycleUncertain()
		}
	}
	return domain.Decode(raw, target)
}

func (b *ExecutionBinding) validateSystemInit(raw []byte) error {
	var init struct {
		Type           string            `json:"type"`
		Subtype        string            `json:"subtype"`
		Session        domain.ID         `json:"session_id"`
		UUID           string            `json:"uuid"`
		Cwd            string            `json:"cwd"`
		Model          string            `json:"model"`
		Permission     NativePermission  `json:"permissionMode"`
		Source         string            `json:"apiKeySource"`
		Version        string            `json:"claude_code_version"`
		OutputStyle    string            `json:"output_style"`
		Tools          []string          `json:"tools"`
		MCPServers     []json.RawMessage `json:"mcp_servers"`
		Commands       []string          `json:"slash_commands"`
		Terminal       []string          `json:"terminal_slash_commands"`
		Agents         []string          `json:"agents"`
		Skills         []string          `json:"skills"`
		Plugins        []json.RawMessage `json:"plugins"`
		Capabilities   []string          `json:"capabilities"`
		AnalyticsOff   *bool             `json:"analytics_disabled"`
		FeedbackOff    *bool             `json:"product_feedback_disabled"`
		Memory         map[string]string `json:"memory_paths"`
		FastMode       string            `json:"fast_mode_state"`
		FastModeReason string            `json:"fast_mode_disabled_reason"`
	}
	if decodeNativeObject(raw, &init) != nil || init.Cwd != b.workspace || init.Model != b.model || init.Permission != b.permission || init.Source != "ANTHROPIC_API_KEY" || init.Version != SupportedVersion || init.OutputStyle != "default" ||
		init.MCPServers == nil || len(init.MCPServers) != 0 || init.Plugins == nil || len(init.Plugins) != 0 || init.AnalyticsOff == nil || !*init.AnalyticsOff || init.FeedbackOff == nil || !*init.FeedbackOff || init.FastMode != "off" || init.FastModeReason != "sdk_opt_in_required" ||
		!uniqueText(init.Tools, 256, 256) || !uniqueText(init.Commands, 256, 256) || !uniqueText(init.Terminal, 256, 256) || !uniqueText(init.Agents, 256, 256) || !uniqueText(init.Skills, 256, 256) || !uniqueText(init.Capabilities, 256, 256) {
		return lifecycleUncertain()
	}
	for _, tool := range []string{"Bash", "Read", "Write", "AskUserQuestion", "EnterPlanMode", "ExitPlanMode"} {
		if !slices.Contains(init.Tools, tool) {
			return lifecycleUncertain()
		}
	}
	if !slices.Contains(init.Commands, "compact") || !slices.Contains(init.Capabilities, "msg_lifecycle_v1") || len(init.Memory) != 1 || filepath.Clean(init.Memory["auto"]) != filepath.Join(b.home, "projects", "delidev", "memory") {
		return lifecycleUncertain()
	}
	return nil
}

// TerminalReason is the pinned native loop's reason, not a fabricated common
// outcome. In particular, success subtypes can carry API errors, and deferred
// work or a stopped hook must not become successful task completion.
type TerminalReason string

const (
	Completed                      TerminalReason = "completed"
	AbortedStreaming               TerminalReason = "aborted_streaming"
	AbortedTools                   TerminalReason = "aborted_tools"
	BlockingLimit                  TerminalReason = "blocking_limit"
	RapidRefillBreaker             TerminalReason = "rapid_refill_breaker"
	PromptTooLong                  TerminalReason = "prompt_too_long"
	ImageError                     TerminalReason = "image_error"
	ModelError                     TerminalReason = "model_error"
	APIError                       TerminalReason = "api_error"
	MalformedToolUseExhausted      TerminalReason = "malformed_tool_use_exhausted"
	StopHookPrevented              TerminalReason = "stop_hook_prevented"
	HookStopped                    TerminalReason = "hook_stopped"
	ToolDeferred                   TerminalReason = "tool_deferred"
	MaxTurns                       TerminalReason = "max_turns"
	BackgroundRequested            TerminalReason = "background_requested"
	BudgetExhausted                TerminalReason = "budget_exhausted"
	StructuredOutputRetryExhausted TerminalReason = "structured_output_retry_exhausted"
	ToolDeferredUnavailable        TerminalReason = "tool_deferred_unavailable"
	TurnSetupFailed                TerminalReason = "turn_setup_failed"
)

type ResultKind string

const (
	ResultSuccess          ResultKind = "success"
	ResultExecutionError   ResultKind = "error_during_execution"
	ResultMaxTurns         ResultKind = "error_max_turns"
	ResultMaxBudget        ResultKind = "error_max_budget_usd"
	ResultStructuredOutput ResultKind = "error_max_structured_output_retries"
)

type NativeResult struct {
	Kind   ResultKind
	Reason TerminalReason
	Error  bool
}

func (r NativeResult) Successful() bool {
	return r.Kind == ResultSuccess && r.Reason == Completed && !r.Error
}

func (r NativeResult) cancelsCommand() bool {
	failed, _ := terminalError(r.Reason)
	return failed || r.Reason == AbortedStreaming || r.Reason == AbortedTools
}

func terminalError(reason TerminalReason) (isError, known bool) {
	switch reason {
	case BlockingLimit, RapidRefillBreaker, PromptTooLong, ImageError, ModelError, APIError, MalformedToolUseExhausted, BudgetExhausted, StructuredOutputRetryExhausted, ToolDeferredUnavailable, TurnSetupFailed:
		return true, true
	case Completed, AbortedStreaming, AbortedTools, StopHookPrevented, HookStopped, ToolDeferred, MaxTurns, BackgroundRequested:
		return false, true
	default:
		return false, false
	}
}

func (b *ExecutionBinding) validateResult(raw []byte) (NativeResult, bool, error) {
	// Keep final output, diagnostic strings, permission denials, provider usage
	// and costs private until their dedicated adapters validate each family.
	var result struct {
		Type                   string          `json:"type"`
		Kind                   ResultKind      `json:"subtype"`
		Session                domain.ID       `json:"session_id"`
		UUID                   string          `json:"uuid"`
		Input                  domain.ID       `json:"user_message_uuid"`
		Reason                 TerminalReason  `json:"terminal_reason"`
		Error                  *bool           `json:"is_error"`
		StopReason             *string         `json:"stop_reason"`
		Duration               *uint64         `json:"duration_ms"`
		APIDuration            *uint64         `json:"duration_api_ms"`
		Turns                  *uint64         `json:"num_turns"`
		Text                   json.RawMessage `json:"result"`
		Errors                 json.RawMessage `json:"errors"`
		Status                 *int            `json:"api_error_status"`
		Cost                   json.RawMessage `json:"total_cost_usd"`
		Usage                  json.RawMessage `json:"usage"`
		ModelUsage             json.RawMessage `json:"modelUsage"`
		Denials                json.RawMessage `json:"permission_denials"`
		StructuredOutput       json.RawMessage `json:"structured_output"`
		DeferredTool           json.RawMessage `json:"deferred_tool_use"`
		Origin                 json.RawMessage `json:"origin"`
		FastMode               string          `json:"fast_mode_state"`
		FastModeReason         string          `json:"fast_mode_disabled_reason"`
		TTFT                   *uint64         `json:"ttft_ms"`
		StreamTTFT             *uint64         `json:"ttft_stream_ms"`
		TimeToRequest          *uint64         `json:"time_to_request_ms"`
		RequestSent            *float64        `json:"request_sent_wall_ms"`
		TimeToRequestFromSpawn *uint64         `json:"time_to_request_from_spawn_ms"`
		WarmSpareClaimed       *bool           `json:"warm_spare_claimed"`
		TimeOrigin             *float64        `json:"time_origin_ms"`
	}
	if decodeNativeObject(raw, &result) != nil || b.finished || (result.Input != "" && result.Input != b.input) || result.Error == nil || result.Duration == nil || result.APIDuration == nil || result.Turns == nil || len(result.Origin) != 0 || result.FastMode != "off" || result.FastModeReason != "sdk_opt_in_required" ||
		!slices.Contains([]CommandState{CommandStarted, CommandCompleted, CommandCancelled}, b.command) || !slices.Contains([]ResultKind{ResultSuccess, ResultExecutionError, ResultMaxTurns, ResultMaxBudget, ResultStructuredOutput}, result.Kind) {
		return NativeResult{}, false, lifecycleUncertain()
	}
	if result.Input == "" {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if _, present := fields["user_message_uuid"]; present || !*result.Error {
			return NativeResult{}, false, lifecycleUncertain()
		}
	}
	isError, known := terminalError(result.Reason)
	if !known || (isError && !*result.Error) || (result.Kind != ResultSuccess && !*result.Error) || (result.StopReason != nil && domain.Text(*result.StopReason, "native stop reason", 128, false) != nil) || (result.Status != nil && (*result.Status < 400 || *result.Status > 599 || !*result.Error)) {
		return NativeResult{}, false, lifecycleUncertain()
	}
	var text *string
	var messages []string
	if result.Kind == ResultSuccess {
		if len(result.Errors) != 0 || json.Unmarshal(result.Text, &text) != nil || text == nil {
			return NativeResult{}, false, lifecycleUncertain()
		}
	} else if len(result.Text) != 0 || json.Unmarshal(result.Errors, &messages) != nil || messages == nil {
		return NativeResult{}, false, lifecycleUncertain()
	}
	if (result.Kind == ResultMaxTurns && result.Reason != MaxTurns) || (result.Kind == ResultMaxBudget && result.Reason != BudgetExhausted) || (result.Kind == ResultStructuredOutput && result.Reason != StructuredOutputRetryExhausted && result.Reason != AbortedStreaming && result.Reason != AbortedTools) {
		return NativeResult{}, false, lifecycleUncertain()
	}
	value := NativeResult{Kind: result.Kind, Reason: result.Reason, Error: *result.Error}
	if (b.command == CommandCompleted && value.cancelsCommand()) || (b.command == CommandCancelled && !value.cancelsCommand()) {
		return NativeResult{}, false, lifecycleUncertain()
	}
	if value.Successful() && (!b.initialized || !b.accepted || b.command == CommandCancelled || len(result.Errors) != 0 || len(result.DeferredTool) != 0) {
		return NativeResult{}, false, lifecycleUncertain()
	}
	return value, result.Input == b.input, nil
}
