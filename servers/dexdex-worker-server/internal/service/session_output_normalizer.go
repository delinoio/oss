package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

type AgentCliType uint8

const (
	AgentCliTypeCodexCLI AgentCliType = iota + 1
	AgentCliTypeClaudeCode
	AgentCliTypeOpenCode
)

func (c AgentCliType) String() string {
	switch c {
	case AgentCliTypeCodexCLI:
		return "codex-cli"
	case AgentCliTypeClaudeCode:
		return "claude-code"
	case AgentCliTypeOpenCode:
		return "opencode"
	default:
		return "unknown"
	}
}

type SessionOutputKind uint8

const (
	SessionOutputKindText SessionOutputKind = iota + 1
	SessionOutputKindPlanUpdate
	SessionOutputKindToolCall
	SessionOutputKindToolResult
	SessionOutputKindProgress
	SessionOutputKindWarning
	SessionOutputKindError
)

type SessionOutputSourceEventType uint8

const (
	SessionOutputSourceEventTypeRunStarted SessionOutputSourceEventType = iota + 1
	SessionOutputSourceEventTypeTurnStarted
	SessionOutputSourceEventTypeTextDelta
	SessionOutputSourceEventTypeTextFinal
	SessionOutputSourceEventTypeStepStarted
	SessionOutputSourceEventTypeStepFinished
	SessionOutputSourceEventTypeResult
	SessionOutputSourceEventTypeError
	SessionOutputSourceEventTypeSystem
)

type NormalizedSessionOutputEvent struct {
	SessionID       string
	Kind            SessionOutputKind
	Body            string
	CliType         AgentCliType
	SourceEventType SessionOutputSourceEventType
	SourceSequence  uint64
	RawEventType    string
	IsTerminal      bool
}

var ErrUnsupportedAgentCliType = errors.New("unsupported agent cli type")

func NormalizeSessionOutputLines(
	cli AgentCliType,
	sessionID string,
	rawLines []string,
) ([]NormalizedSessionOutputEvent, error) {
	switch cli {
	case AgentCliTypeCodexCLI, AgentCliTypeClaudeCode, AgentCliTypeOpenCode:
	default:
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedAgentCliType, cli)
	}

	events := make([]NormalizedSessionOutputEvent, 0, len(rawLines))
	for index, rawLine := range rawLines {
		sequence := uint64(index + 1)
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" {
			parseEvent := parseFailureEvent(cli, sessionID, sequence, errors.New("empty line"))
			events = append(events, parseEvent)
			continue
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
			parseEvent := parseFailureEvent(cli, sessionID, sequence, err)
			events = append(events, parseEvent)
			continue
		}

		normalized, known := normalizeKnownEvent(cli, sessionID, sequence, payload)
		if !known {
			normalized = unknownEvent(cli, sessionID, sequence, payload)
			slog.Warn(
				"session_output.normalize.unknown_event",
				"cli_type", normalized.CliType.String(),
				"session_id", sessionID,
				"source_sequence", normalized.SourceSequence,
				"raw_event_type", normalized.RawEventType,
			)
		}

		if normalized.IsTerminal {
			slog.Info(
				"session_output.normalize.terminal_event",
				"cli_type", normalized.CliType.String(),
				"session_id", sessionID,
				"source_sequence", normalized.SourceSequence,
				"raw_event_type", normalized.RawEventType,
			)
		}

		events = append(events, normalized)
	}

	return events, nil
}

func parseFailureEvent(
	cli AgentCliType,
	sessionID string,
	sourceSequence uint64,
	err error,
) NormalizedSessionOutputEvent {
	slog.Warn(
		"session_output.normalize.parse_failure",
		"cli_type", cli.String(),
		"session_id", sessionID,
		"source_sequence", sourceSequence,
		"raw_event_type", "json.parse_error",
		"error", err.Error(),
	)

	return NormalizedSessionOutputEvent{
		SessionID:       sessionID,
		Kind:            SessionOutputKindError,
		Body:            fmt.Sprintf("failed to parse source event: %v", err),
		CliType:         cli,
		SourceEventType: SessionOutputSourceEventTypeError,
		SourceSequence:  sourceSequence,
		RawEventType:    "json.parse_error",
		IsTerminal:      false,
	}
}

func normalizeKnownEvent(
	cli AgentCliType,
	sessionID string,
	sourceSequence uint64,
	payload map[string]any,
) (NormalizedSessionOutputEvent, bool) {
	switch cli {
	case AgentCliTypeCodexCLI:
		return normalizeCodexEvent(sessionID, sourceSequence, payload)
	case AgentCliTypeClaudeCode:
		return normalizeClaudeEvent(sessionID, sourceSequence, payload)
	case AgentCliTypeOpenCode:
		return normalizeOpenCodeEvent(sessionID, sourceSequence, payload)
	default:
		return NormalizedSessionOutputEvent{}, false
	}
}

func normalizeCodexEvent(
	sessionID string,
	sourceSequence uint64,
	payload map[string]any,
) (NormalizedSessionOutputEvent, bool) {
	rawType := getString(payload, "type")
	base := newBaseEvent(AgentCliTypeCodexCLI, sessionID, sourceSequence, rawType)

	switch rawType {
	case "thread.started":
		base.Kind = SessionOutputKindProgress
		base.SourceEventType = SessionOutputSourceEventTypeRunStarted
		base.Body = getString(payload, "thread_id")
		if base.Body == "" {
			base.Body = "thread started"
		}
		return base, true
	case "turn.started":
		base.Kind = SessionOutputKindProgress
		base.SourceEventType = SessionOutputSourceEventTypeTurnStarted
		base.Body = "turn started"
		return base, true
	case "error":
		base.Kind = SessionOutputKindError
		base.SourceEventType = SessionOutputSourceEventTypeError
		base.Body = getString(payload, "message")
		if base.Body == "" {
			base.Body = "codex error"
		}
		return base, true
	case "turn.failed":
		base.Kind = SessionOutputKindError
		base.SourceEventType = SessionOutputSourceEventTypeError
		base.IsTerminal = true
		base.Body = nestedString(payload, "error", "message")
		if base.Body == "" {
			base.Body = getString(payload, "message")
		}
		if base.Body == "" {
			base.Body = "turn failed"
		}
		return base, true
	case "result":
		base.Kind = SessionOutputKindText
		base.SourceEventType = SessionOutputSourceEventTypeResult
		base.IsTerminal = true
		base.Body = getString(payload, "result")
		if base.Body == "" {
			base.Body = "result"
		}
		return base, true
	default:
		return NormalizedSessionOutputEvent{}, false
	}
}

func normalizeClaudeEvent(
	sessionID string,
	sourceSequence uint64,
	payload map[string]any,
) (NormalizedSessionOutputEvent, bool) {
	rawType := getString(payload, "type")
	base := newBaseEvent(AgentCliTypeClaudeCode, sessionID, sourceSequence, rawType)

	switch rawType {
	case "system":
		base.Kind = SessionOutputKindProgress
		base.SourceEventType = SessionOutputSourceEventTypeSystem
		base.Body = getString(payload, "subtype")
		if base.Body == "" {
			base.Body = "system event"
		}
		return base, true
	case "assistant":
		base.Kind = SessionOutputKindText
		base.SourceEventType = SessionOutputSourceEventTypeTextFinal
		base.Body = concatClaudeAssistantText(payload)
		if base.Body == "" {
			base.Body = "assistant message"
		}
		return base, true
	case "result":
		base.Kind = SessionOutputKindText
		base.SourceEventType = SessionOutputSourceEventTypeResult
		base.IsTerminal = true
		base.Body = getString(payload, "result")
		if base.Body == "" {
			base.Body = "result"
		}
		return base, true
	case "stream_event":
		eventType := nestedString(payload, "event", "type")
		if eventType == "" {
			eventType = "unknown"
		}
		base.RawEventType = "stream_event/" + eventType

		if eventType == "content_block_delta" && nestedString(payload, "event", "delta", "type") == "text_delta" {
			base.Kind = SessionOutputKindText
			base.SourceEventType = SessionOutputSourceEventTypeTextDelta
			base.Body = nestedStringRaw(payload, "event", "delta", "text")
			return base, true
		}

		return NormalizedSessionOutputEvent{}, false
	default:
		return NormalizedSessionOutputEvent{}, false
	}
}

func normalizeOpenCodeEvent(
	sessionID string,
	sourceSequence uint64,
	payload map[string]any,
) (NormalizedSessionOutputEvent, bool) {
	rawType := getString(payload, "type")
	base := newBaseEvent(AgentCliTypeOpenCode, sessionID, sourceSequence, rawType)

	switch rawType {
	case "step_start":
		base.Kind = SessionOutputKindProgress
		base.SourceEventType = SessionOutputSourceEventTypeStepStarted
		base.Body = nestedString(payload, "part", "type")
		if base.Body == "" {
			base.Body = "step started"
		}
		return base, true
	case "text":
		base.Kind = SessionOutputKindText
		base.SourceEventType = SessionOutputSourceEventTypeTextDelta
		base.Body = nestedStringRaw(payload, "part", "text")
		return base, true
	case "step_finish":
		base.Kind = SessionOutputKindProgress
		base.SourceEventType = SessionOutputSourceEventTypeStepFinished
		base.Body = nestedString(payload, "part", "reason")
		base.IsTerminal = base.Body == "stop"
		if base.Body == "" {
			base.Body = "step finished"
		}
		return base, true
	default:
		return NormalizedSessionOutputEvent{}, false
	}
}

func unknownEvent(
	cli AgentCliType,
	sessionID string,
	sourceSequence uint64,
	payload map[string]any,
) NormalizedSessionOutputEvent {
	rawType := getString(payload, "type")
	if cli == AgentCliTypeClaudeCode && rawType == "stream_event" {
		nestedType := nestedString(payload, "event", "type")
		if nestedType != "" {
			rawType = "stream_event/" + nestedType
		}
	}
	if rawType == "" {
		rawType = "unknown"
	}

	return NormalizedSessionOutputEvent{
		SessionID:       sessionID,
		Kind:            SessionOutputKindWarning,
		Body:            "unsupported source event preserved for diagnostics",
		CliType:         cli,
		SourceEventType: SessionOutputSourceEventTypeSystem,
		SourceSequence:  sourceSequence,
		RawEventType:    rawType,
		IsTerminal:      false,
	}
}

func newBaseEvent(
	cli AgentCliType,
	sessionID string,
	sourceSequence uint64,
	rawEventType string,
) NormalizedSessionOutputEvent {
	if rawEventType == "" {
		rawEventType = "unknown"
	}

	return NormalizedSessionOutputEvent{
		SessionID:      sessionID,
		CliType:        cli,
		SourceSequence: sourceSequence,
		RawEventType:   rawEventType,
	}
}

func concatClaudeAssistantText(payload map[string]any) string {
	contentArray, ok := nestedArray(payload, "message", "content")
	if !ok {
		return ""
	}

	var builder strings.Builder
	for _, item := range contentArray {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if getString(itemMap, "type") != "text" {
			continue
		}
		builder.WriteString(getStringRaw(itemMap, "text"))
	}

	return builder.String()
}

func nestedArray(payload map[string]any, keys ...string) ([]any, bool) {
	current := any(payload)
	for _, key := range keys {
		typedMap, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = typedMap[key]
		if !ok {
			return nil, false
		}
	}

	typedArray, ok := current.([]any)
	return typedArray, ok
}

func nestedString(payload map[string]any, keys ...string) string {
	current := any(payload)
	for _, key := range keys {
		typedMap, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = typedMap[key]
		if !ok {
			return ""
		}
	}

	stringValue, ok := current.(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(stringValue)
}

func nestedStringRaw(payload map[string]any, keys ...string) string {
	current := any(payload)
	for _, key := range keys {
		typedMap, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = typedMap[key]
		if !ok {
			return ""
		}
	}

	stringValue, ok := current.(string)
	if !ok {
		return ""
	}

	return stringValue
}

func getString(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok {
		return ""
	}

	stringValue, ok := value.(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(stringValue)
}

func getStringRaw(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok {
		return ""
	}

	stringValue, ok := value.(string)
	if !ok {
		return ""
	}

	return stringValue
}
