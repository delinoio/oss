package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/normalize"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/store"
)

// agentCommand wraps an os/exec.Cmd with a reference to its stdin pipe.
type agentCommand struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

// buildAgentCommand creates the agent CLI command based on the agent type.
// When parentSessionID is non-empty, fork mode is activated (Claude Code --resume).
func buildAgentCommand(
	ctx context.Context,
	agentType dexdexv1.AgentCliType,
	primaryDir string,
	attachedDirs []string,
	prompt string,
	sessionID string,
	parentSessionID string,
) (*agentCommand, error) {
	var args []string

	switch agentType {
	case dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE:
		if parentSessionID != "" {
			// Fork mode: resume from parent session with new prompt
			args = []string{"claude", "--json", "--output-format", "stream-json",
				"--resume", parentSessionID, "-p", prompt}
		} else {
			args = []string{"claude", "--json", "--output-format", "stream-json", "-p", prompt}
		}
		for _, dir := range attachedDirs {
			args = append(args, "--add-dir", dir)
		}
	case dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI:
		if parentSessionID != "" {
			return nil, fmt.Errorf("agent %s does not support session forking", agentType.String())
		}
		args = []string{"codex", "--json", "-p", prompt}
	case dexdexv1.AgentCliType_AGENT_CLI_TYPE_OPENCODE:
		if parentSessionID != "" {
			return nil, fmt.Errorf("agent %s does not support session forking", agentType.String())
		}
		args = []string{"opencode", "--json", "-p", prompt}
	default:
		return nil, fmt.Errorf("unsupported agent CLI type: %s", agentType.String())
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = primaryDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	return &agentCommand{cmd: cmd, stdin: stdin}, nil
}

// claudeStreamEvent represents a single NDJSON event from Claude Code's stream output.
type claudeStreamEvent struct {
	Type    string            `json:"type"`
	SubType string            `json:"subtype,omitempty"`
	Message string            `json:"message,omitempty"`
	Content string            `json:"content,omitempty"`
	Tool    string            `json:"tool,omitempty"`
	Result  string            `json:"result,omitempty"`
	Usage   *claudeUsageBlock `json:"usage,omitempty"`
}

// claudeUsageBlock represents the usage block in Claude Code NDJSON output.
type claudeUsageBlock struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// runAgentProcess starts the agent command, reads its NDJSON stdout, and streams
// normalized events. It handles input relay from the inputCh and returns the
// final agent session status.
func runAgentProcess(
	ctx context.Context,
	ac *agentCommand,
	sessionID string,
	inputCh chan string,
	stream *connect.ServerStream[dexdexv1.ExecutionEvent],
	sessionStore *store.SessionStore,
	usageAccumulator *normalize.UsageAccumulator,
	logger *slog.Logger,
) dexdexv1.AgentSessionStatus {
	stdout, err := ac.cmd.StdoutPipe()
	if err != nil {
		logger.Error("failed to get stdout pipe", "session_id", sessionID, "error", err)
		return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED
	}

	if err := ac.cmd.Start(); err != nil {
		logger.Error("failed to start agent process", "session_id", sessionID, "error", err)
		return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED
	}

	// Relay input in a separate goroutine
	go func() {
		for {
			select {
			case input, ok := <-inputCh:
				if !ok {
					return
				}
				if _, err := fmt.Fprintln(ac.stdin, input); err != nil {
					logger.Warn("failed to write input to agent stdin",
						"session_id", sessionID, "error", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Read NDJSON output line by line
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer for large output lines

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		event, usage := parseAgentOutputLine(line, sessionID, logger)

		// Track usage if present in this event.
		if usage != nil && usageAccumulator != nil {
			usageAccumulator.AccumulateUsage(sessionID, usage.InputTokens, usage.OutputTokens)
			logger.Debug("accumulated usage from agent output",
				"session_id", sessionID,
				"input_tokens", usage.InputTokens,
				"output_tokens", usage.OutputTokens,
			)
		}

		if event != nil {
			// Store output event
			sessionStore.AppendOutput(sessionID, event)

			// Stream to caller
			_ = stream.Send(&dexdexv1.ExecutionEvent{
				Event: &dexdexv1.ExecutionEvent_SessionOutput{
					SessionOutput: event,
				},
			})
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Warn("scanner error reading agent output", "session_id", sessionID, "error", err)
	}

	// Wait for process to finish
	if err := ac.cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			logger.Info("agent process cancelled", "session_id", sessionID)
			return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_CANCELLED
		}
		logger.Error("agent process exited with error", "session_id", sessionID, "error", err)
		return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED
	}

	return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED
}

// parseAgentOutputLine parses a single NDJSON line from agent output.
// Returns the session output event and optional usage data extracted from the line.
func parseAgentOutputLine(line, sessionID string, logger *slog.Logger) (*dexdexv1.SessionOutputEvent, *claudeUsageBlock) {
	var evt claudeStreamEvent
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		// Not JSON - treat as plain text output
		return &dexdexv1.SessionOutputEvent{
			SessionId: sessionID,
			Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT,
			Body:      line,
		}, nil
	}

	kind := mapEventTypeToOutputKind(evt.Type, evt.SubType)
	body := evt.Content
	if body == "" {
		body = evt.Message
	}
	if body == "" {
		body = evt.Result
	}
	if body == "" {
		body = line // fall back to raw line
	}

	return &dexdexv1.SessionOutputEvent{
		SessionId: sessionID,
		Kind:      kind,
		Body:      body,
	}, evt.Usage
}

// mapEventTypeToOutputKind maps Claude Code stream event types to proto output kinds.
func mapEventTypeToOutputKind(eventType, subType string) dexdexv1.SessionOutputKind {
	switch eventType {
	case "assistant", "text":
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT
	case "tool_use":
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_CALL
	case "tool_result":
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_RESULT
	case "plan", "plan_update":
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PLAN_UPDATE
	case "progress":
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PROGRESS
	case "error":
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_ERROR
	case "warning":
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_WARNING
	default:
		return dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT
	}
}
