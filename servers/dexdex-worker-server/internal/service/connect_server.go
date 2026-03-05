package service

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	connect "connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	dexdexv1connect "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
)

var (
	//go:embed testdata/codex-cli.failure.jsonl
	codexCLIFailureFixture string
	//go:embed testdata/claude-code.stream.jsonl
	claudeCodeStreamFixture string
	//go:embed testdata/opencode.run.jsonl
	openCodeRunFixture string
)

type SessionAdapterConnectServerConfig struct {
	Logger *slog.Logger
}

type SessionAdapterConnectServer struct {
	logger *slog.Logger
}

var _ dexdexv1connect.WorkerSessionAdapterServiceHandler = (*SessionAdapterConnectServer)(nil)

func NewSessionAdapterConnectServer(config SessionAdapterConnectServerConfig) *SessionAdapterConnectServer {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &SessionAdapterConnectServer{
		logger: logger,
	}
}

func (s *SessionAdapterConnectServer) NormalizeSessionOutputFixture(
	_ context.Context,
	request *connect.Request[dexdexv1.NormalizeSessionOutputFixtureRequest],
) (*connect.Response[dexdexv1.NormalizeSessionOutputFixtureResponse], error) {
	workspaceID, err := normalizeRequiredWorkerValue(request.Msg.GetWorkspaceId(), "workspace_id")
	if err != nil {
		return nil, err
	}
	unitTaskID, err := normalizeRequiredWorkerValue(request.Msg.GetUnitTaskId(), "unit_task_id")
	if err != nil {
		return nil, err
	}
	subTaskID, err := normalizeRequiredWorkerValue(request.Msg.GetSubTaskId(), "sub_task_id")
	if err != nil {
		return nil, err
	}
	sessionID, err := normalizeRequiredWorkerValue(request.Msg.GetSessionId(), "session_id")
	if err != nil {
		return nil, err
	}

	cliType, err := domainAgentCliTypeFromProto(request.Msg.GetCliType())
	if err != nil {
		return nil, err
	}

	rawJSONL, inputSource, err := resolveInputJSONL(request.Msg.GetInput())
	if err != nil {
		return nil, err
	}

	normalizedEvents, err := NormalizeSessionOutputLines(cliType, sessionID, splitJSONLLines(rawJSONL))
	if err != nil {
		if errors.Is(err, ErrUnsupportedAgentCliType) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoEvents := make([]*dexdexv1.SessionOutputEvent, 0, len(normalizedEvents))
	for _, event := range normalizedEvents {
		protoEvents = append(protoEvents, protoSessionOutputEventFromDomain(event))
	}
	sessionStatus := deriveSessionStatus(protoEvents)

	s.logger.Info(
		"dexdex.worker.normalize_session_output_fixture.success",
		"workspace_id", workspaceID,
		"unit_task_id", unitTaskID,
		"sub_task_id", subTaskID,
		"session_id", sessionID,
		"cli_type", request.Msg.GetCliType().String(),
		"input_source", inputSource,
		"event_count", len(protoEvents),
		"session_status", sessionStatus.String(),
		"result", "success",
	)

	return connect.NewResponse(&dexdexv1.NormalizeSessionOutputFixtureResponse{
		Events:        protoEvents,
		SessionStatus: sessionStatus,
	}), nil
}

func normalizeRequiredWorkerValue(rawValue string, fieldName string) (string, error) {
	value := strings.TrimSpace(rawValue)
	if value == "" {
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s is required", fieldName))
	}
	return value, nil
}

func resolveInputJSONL(input any) (string, string, error) {
	switch typedInput := input.(type) {
	case *dexdexv1.NormalizeSessionOutputFixtureRequest_FixturePreset:
		switch typedInput.FixturePreset {
		case dexdexv1.SessionAdapterFixturePreset_SESSION_ADAPTER_FIXTURE_PRESET_CODEX_CLI_FAILURE:
			return codexCLIFailureFixture, "preset/codex-cli.failure", nil
		case dexdexv1.SessionAdapterFixturePreset_SESSION_ADAPTER_FIXTURE_PRESET_CLAUDE_CODE_STREAM:
			return claudeCodeStreamFixture, "preset/claude-code.stream", nil
		case dexdexv1.SessionAdapterFixturePreset_SESSION_ADAPTER_FIXTURE_PRESET_OPENCODE_RUN:
			return openCodeRunFixture, "preset/opencode.run", nil
		default:
			return "", "", connect.NewError(connect.CodeInvalidArgument, errors.New("fixture_preset must be a supported value"))
		}
	case *dexdexv1.NormalizeSessionOutputFixtureRequest_RawJsonl:
		rawJSONL := strings.TrimSpace(typedInput.RawJsonl)
		if rawJSONL == "" {
			return "", "", connect.NewError(connect.CodeInvalidArgument, errors.New("raw_jsonl must not be empty"))
		}
		return rawJSONL, "raw_jsonl", nil
	default:
		return "", "", connect.NewError(connect.CodeInvalidArgument, errors.New("exactly one input must be provided"))
	}
}

func splitJSONLLines(rawJSONL string) []string {
	lines := make([]string, 0, 8)
	for _, line := range strings.Split(rawJSONL, "\n") {
		normalizedLine := strings.TrimRight(line, "\r")
		if strings.TrimSpace(normalizedLine) == "" {
			continue
		}
		lines = append(lines, normalizedLine)
	}

	return lines
}

func deriveSessionStatus(events []*dexdexv1.SessionOutputEvent) dexdexv1.AgentSessionStatus {
	foundTerminalEvent := false
	for _, event := range events {
		if !event.GetIsTerminal() {
			continue
		}

		foundTerminalEvent = true
		if event.GetKind() == dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_ERROR {
			return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED
		}
	}

	if foundTerminalEvent {
		return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED
	}

	return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING
}

func domainAgentCliTypeFromProto(
	cliType dexdexv1.AgentCliType,
) (AgentCliType, error) {
	switch cliType {
	case dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI:
		return AgentCliTypeCodexCLI, nil
	case dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE:
		return AgentCliTypeClaudeCode, nil
	case dexdexv1.AgentCliType_AGENT_CLI_TYPE_OPENCODE:
		return AgentCliTypeOpenCode, nil
	default:
		return 0, connect.NewError(connect.CodeInvalidArgument, errors.New("cli_type must be one of CODEX_CLI, CLAUDE_CODE, or OPENCODE"))
	}
}

func protoSessionOutputEventFromDomain(
	event NormalizedSessionOutputEvent,
) *dexdexv1.SessionOutputEvent {
	return &dexdexv1.SessionOutputEvent{
		SessionId: event.SessionID,
		Kind:      protoSessionOutputKindFromDomain(event.Kind),
		Body:      event.Body,
		Source: &dexdexv1.SessionOutputSourceMetadata{
			CliType:         protoAgentCliTypeFromDomain(event.CliType),
			SourceEventType: protoSessionOutputSourceEventTypeFromDomain(event.SourceEventType),
			SourceSequence:  event.SourceSequence,
			RawEventType:    event.RawEventType,
		},
		IsTerminal: event.IsTerminal,
	}
}

func protoAgentCliTypeFromDomain(cliType AgentCliType) dexdexv1.AgentCliType {
	switch cliType {
	case AgentCliTypeCodexCLI:
		return dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI
	case AgentCliTypeClaudeCode:
		return dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE
	case AgentCliTypeOpenCode:
		return dexdexv1.AgentCliType_AGENT_CLI_TYPE_OPENCODE
	default:
		return dexdexv1.AgentCliType_AGENT_CLI_TYPE_UNSPECIFIED
	}
}

func protoSessionOutputKindFromDomain(kind SessionOutputKind) dexdexv1.SessionOutputKind {
	switch kind {
	case SessionOutputKindText:
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT
	case SessionOutputKindPlanUpdate:
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PLAN_UPDATE
	case SessionOutputKindToolCall:
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_CALL
	case SessionOutputKindToolResult:
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_RESULT
	case SessionOutputKindProgress:
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PROGRESS
	case SessionOutputKindWarning:
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_WARNING
	case SessionOutputKindError:
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_ERROR
	default:
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_UNSPECIFIED
	}
}

func protoSessionOutputSourceEventTypeFromDomain(
	eventType SessionOutputSourceEventType,
) dexdexv1.SessionOutputSourceEventType {
	switch eventType {
	case SessionOutputSourceEventTypeRunStarted:
		return dexdexv1.SessionOutputSourceEventType_SESSION_OUTPUT_SOURCE_EVENT_TYPE_RUN_STARTED
	case SessionOutputSourceEventTypeTurnStarted:
		return dexdexv1.SessionOutputSourceEventType_SESSION_OUTPUT_SOURCE_EVENT_TYPE_TURN_STARTED
	case SessionOutputSourceEventTypeTextDelta:
		return dexdexv1.SessionOutputSourceEventType_SESSION_OUTPUT_SOURCE_EVENT_TYPE_TEXT_DELTA
	case SessionOutputSourceEventTypeTextFinal:
		return dexdexv1.SessionOutputSourceEventType_SESSION_OUTPUT_SOURCE_EVENT_TYPE_TEXT_FINAL
	case SessionOutputSourceEventTypeStepStarted:
		return dexdexv1.SessionOutputSourceEventType_SESSION_OUTPUT_SOURCE_EVENT_TYPE_STEP_STARTED
	case SessionOutputSourceEventTypeStepFinished:
		return dexdexv1.SessionOutputSourceEventType_SESSION_OUTPUT_SOURCE_EVENT_TYPE_STEP_FINISHED
	case SessionOutputSourceEventTypeResult:
		return dexdexv1.SessionOutputSourceEventType_SESSION_OUTPUT_SOURCE_EVENT_TYPE_RESULT
	case SessionOutputSourceEventTypeError:
		return dexdexv1.SessionOutputSourceEventType_SESSION_OUTPUT_SOURCE_EVENT_TYPE_ERROR
	case SessionOutputSourceEventTypeSystem:
		return dexdexv1.SessionOutputSourceEventType_SESSION_OUTPUT_SOURCE_EVENT_TYPE_SYSTEM
	default:
		return dexdexv1.SessionOutputSourceEventType_SESSION_OUTPUT_SOURCE_EVENT_TYPE_UNSPECIFIED
	}
}
