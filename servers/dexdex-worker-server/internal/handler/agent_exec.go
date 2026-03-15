package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

// agentExecOptions configures timeout and idle detection for agent execution.
type agentExecOptions struct {
	ExecTimeoutSec int
	IdleTimeoutSec int
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
	usePlanMode bool,
) (*agentCommand, error) {
	var args []string
	effectivePrompt := prompt

	if usePlanMode {
		switch agentType {
		case dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE:
			effectivePrompt = "Plan mode enabled. First provide a concrete implementation plan, wait for approval, then execute.\n\n" + prompt
		case dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI:
			effectivePrompt = "Plan mode enabled. Output a clear step-by-step plan and wait for explicit approval before modifying files.\n\n" + prompt
		default:
			return nil, fmt.Errorf("agent %s does not support plan mode", agentType.String())
		}
	}

	switch agentType {
	case dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE:
		if parentSessionID != "" {
			// Fork mode: resume from parent session with new prompt
			args = []string{"claude", "--json", "--output-format", "stream-json",
				"--resume", parentSessionID, "-p", effectivePrompt}
		} else {
			args = []string{"claude", "--json", "--output-format", "stream-json", "-p", effectivePrompt}
		}
		for _, dir := range attachedDirs {
			args = append(args, "--add-dir", dir)
		}
	case dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI:
		if parentSessionID != "" {
			return nil, fmt.Errorf("agent %s does not support session forking", agentType.String())
		}
		args = []string{"codex", "--json", "-p", effectivePrompt}
	case dexdexv1.AgentCliType_AGENT_CLI_TYPE_OPENCODE:
		if usePlanMode {
			return nil, fmt.Errorf("agent %s does not support plan mode", agentType.String())
		}
		if parentSessionID != "" {
			return nil, fmt.Errorf("agent %s does not support session forking", agentType.String())
		}
		args = []string{"opencode", "--json", "-p", effectivePrompt}
	default:
		return nil, fmt.Errorf("unsupported agent CLI type: %s", agentType.String())
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = primaryDir
	if usePlanMode {
		cmd.Env = append(os.Environ(),
			"DEXDEX_PLAN_MODE=1",
			fmt.Sprintf("DEXDEX_PLAN_MODE_AGENT=%s", agentType.String()),
		)
	}

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
// normalized events. It handles input relay from the inputCh, execution timeout,
// idle detection, and stderr capture. Returns the final agent session status.
func runAgentProcess(
	ctx context.Context,
	ac *agentCommand,
	sessionID string,
	inputCh chan string,
	stream *connect.ServerStream[dexdexv1.StartExecutionResponse],
	sessionStore *store.SessionStore,
	usageAccumulator *normalize.UsageAccumulator,
	logger *slog.Logger,
	opts agentExecOptions,
) dexdexv1.AgentSessionStatus {
	// Apply execution timeout.
	execTimeout := time.Duration(opts.ExecTimeoutSec) * time.Second
	if execTimeout <= 0 {
		execTimeout = 30 * time.Minute
	}
	execCtx, execCancel := context.WithTimeout(ctx, execTimeout)
	defer execCancel()

	// Override the command's context with the timeout-aware one.
	// Since exec.CommandContext was already called with the parent ctx,
	// we cancel via the idle watchdog or exec timeout below.

	stdout, err := ac.cmd.StdoutPipe()
	if err != nil {
		logger.Error("failed to get stdout pipe", "session_id", sessionID, "error", err)
		return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED
	}

	// Capture stderr in a bounded buffer.
	var stderrBuf bytes.Buffer
	stderrWriter := &limitedWriter{w: &stderrBuf, limit: 64 * 1024} // 64KB max
	ac.cmd.Stderr = stderrWriter

	if err := ac.cmd.Start(); err != nil {
		logger.Error("failed to start agent process", "session_id", sessionID, "error", err)
		return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED
	}

	// Track last output time for idle detection.
	var lastOutputTime atomic.Int64
	lastOutputTime.Store(time.Now().UnixNano())

	// Idle watchdog goroutine.
	idleTimeout := time.Duration(opts.IdleTimeoutSec) * time.Second
	if idleTimeout <= 0 {
		idleTimeout = 5 * time.Minute
	}
	var idleTimedOut atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				lastNano := lastOutputTime.Load()
				elapsed := time.Since(time.Unix(0, lastNano))
				if elapsed > idleTimeout {
					logger.Warn("agent process idle timeout exceeded, killing process",
						"session_id", sessionID,
						"idle_seconds", int(elapsed.Seconds()),
						"idle_timeout_seconds", opts.IdleTimeoutSec,
					)
					idleTimedOut.Store(true)
					execCancel()
					return
				}
			case <-execCtx.Done():
				return
			}
		}
	}()

	// Relay input in a separate goroutine.
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
			case <-execCtx.Done():
				return
			}
		}
	}()

	// Read NDJSON output line by line.
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer for large output lines

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		lastOutputTime.Store(time.Now().UnixNano())

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
			_ = stream.Send(&dexdexv1.StartExecutionResponse{
				Event: &dexdexv1.StartExecutionResponse_SessionOutput{
					SessionOutput: event,
				},
			})
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Warn("scanner error reading agent output", "session_id", sessionID, "error", err)
	}

	// Wait for process to finish.
	waitErr := ac.cmd.Wait()

	// Wait for idle watchdog to finish.
	wg.Wait()

	// Capture stderr content for error reporting.
	stderrContent := strings.TrimSpace(stderrBuf.String())
	if stderrContent != "" {
		logger.Info("agent stderr output",
			"session_id", sessionID,
			"stderr_length", len(stderrContent),
			"stderr_preview", truncate(stderrContent, 512),
		)
	}

	if waitErr != nil {
		if ctx.Err() != nil {
			logger.Info("agent process cancelled by parent context", "session_id", sessionID)
			return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_CANCELLED
		}
		if idleTimedOut.Load() {
			errMsg := fmt.Sprintf("agent process killed: no output for %d seconds", opts.IdleTimeoutSec)
			emitErrorEvent(stream, sessionID, errMsg)
			logger.Warn("agent process killed due to idle timeout", "session_id", sessionID)
			return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED
		}
		if execCtx.Err() != nil {
			errMsg := fmt.Sprintf("agent process killed: execution timeout after %d seconds", opts.ExecTimeoutSec)
			emitErrorEvent(stream, sessionID, errMsg)
			logger.Warn("agent process killed due to execution timeout", "session_id", sessionID)
			return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED
		}

		// Map exit codes to specific error messages.
		exitCode := mapExitCode(waitErr)
		errMsg := mapExitCodeToMessage(exitCode, stderrContent)
		emitErrorEvent(stream, sessionID, errMsg)
		logger.Error("agent process exited with error",
			"session_id", sessionID,
			"exit_code", exitCode,
			"error", waitErr,
			"stderr_preview", truncate(stderrContent, 256),
		)
		return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED
	}

	return dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED
}

// emitErrorEvent sends an error output event through the execution stream.
func emitErrorEvent(stream *connect.ServerStream[dexdexv1.StartExecutionResponse], sessionID, message string) {
	_ = stream.Send(&dexdexv1.StartExecutionResponse{
		Event: &dexdexv1.StartExecutionResponse_SessionOutput{
			SessionOutput: &dexdexv1.SessionOutputEvent{
				SessionId: sessionID,
				Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_ERROR,
				Body:      message,
			},
		},
	})
}

// mapExitCode extracts the exit code from exec.ExitError, defaulting to -1.
func mapExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// mapExitCodeToMessage converts known exit codes to human-readable messages.
func mapExitCodeToMessage(exitCode int, stderr string) string {
	switch exitCode {
	case 1:
		if strings.Contains(stderr, "permission") || strings.Contains(stderr, "Permission") {
			return "agent process failed: permission denied"
		}
		return "agent process failed: general error (exit code 1)"
	case 2:
		return "agent process failed: invalid arguments or misuse (exit code 2)"
	case 126:
		return "agent process failed: command not executable (exit code 126)"
	case 127:
		return "agent process failed: command not found (exit code 127)"
	case 130:
		return "agent process interrupted by signal (SIGINT)"
	case 137:
		return "agent process killed (SIGKILL, possibly OOM)"
	case 143:
		return "agent process terminated (SIGTERM)"
	default:
		if stderr != "" {
			return fmt.Sprintf("agent process failed (exit code %d): %s", exitCode, truncate(stderr, 256))
		}
		return fmt.Sprintf("agent process failed with exit code %d", exitCode)
	}
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

// limitedWriter wraps a writer and stops writing after the limit is reached.
type limitedWriter struct {
	w       io.Writer
	limit   int
	written int
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	remaining := lw.limit - lw.written
	if remaining <= 0 {
		return len(p), nil // discard silently
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	n, err := lw.w.Write(p)
	lw.written += n
	return n, err
}

// truncate shortens a string to the given max length, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
