package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	v1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type CodexCLI struct {
	BinPath string
	Profile string
}

func NewCodexCLI(binPath string, profile string) *CodexCLI {
	return &CodexCLI{BinPath: binPath, Profile: profile}
}

func (c *CodexCLI) ExecuteSubTask(ctx context.Context, prompt string) ([]*v1.SessionOutputEvent, error) {
	args := []string{"exec", "--json"}
	if strings.TrimSpace(c.Profile) != "" {
		args = append(args, "--profile", strings.TrimSpace(c.Profile))
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, c.BinPath, args...)
	output, err := cmd.CombinedOutput()
	lines := strings.Split(string(output), "\n")
	events := make([]*v1.SessionOutputEvent, 0, len(lines))
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		events = append(events, buildJSONOutputEvent(line))
	}
	if err != nil {
		return events, fmt.Errorf("codex exec failed: %w", err)
	}

	return events, nil
}

func buildJSONOutputEvent(line string) *v1.SessionOutputEvent {
	kind := v1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT
	body := line

	payload := map[string]any{}
	if err := json.Unmarshal([]byte(line), &payload); err == nil {
		if eventType, ok := payload["type"].(string); ok {
			switch strings.ToLower(eventType) {
			case "progress", "task_progress", "status":
				kind = v1.SessionOutputKind_SESSION_OUTPUT_KIND_PROGRESS
			case "warning":
				kind = v1.SessionOutputKind_SESSION_OUTPUT_KIND_WARNING
			case "error":
				kind = v1.SessionOutputKind_SESSION_OUTPUT_KIND_ERROR
			case "tool_call":
				kind = v1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_CALL
			case "tool_result":
				kind = v1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_RESULT
			case "plan":
				kind = v1.SessionOutputKind_SESSION_OUTPUT_KIND_PLAN_UPDATE
			}
		}

		if message, ok := payload["message"].(string); ok && strings.TrimSpace(message) != "" {
			body = message
		}
	}

	return &v1.SessionOutputEvent{
		Kind:       kind,
		Body:       body,
		OccurredAt: timestamppb.New(time.Now().UTC()),
	}
}
