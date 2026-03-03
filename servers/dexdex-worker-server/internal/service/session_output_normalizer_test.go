package service

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
)

func readFixtureLines(t *testing.T, name string) []string {
	t.Helper()

	path := filepath.Join("testdata", name)
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open fixture %q: %v", name, err)
	}
	defer file.Close()

	lines := make([]string, 0, 8)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("failed to scan fixture %q: %v", name, err)
	}
	if len(lines) == 0 {
		t.Fatalf("fixture %q has no lines", name)
	}

	return lines
}

func TestNormalizeSessionOutputLinesCodexFailureMarksTurnFailedAsTerminal(t *testing.T) {
	lines := readFixtureLines(t, "codex-cli.failure.jsonl")

	events, err := NormalizeSessionOutputLines(AgentCliTypeCodexCLI, "session-codex", lines)
	if err != nil {
		t.Fatalf("NormalizeSessionOutputLines returned error: %v", err)
	}

	var found bool
	for _, event := range events {
		if event.RawEventType != "turn.failed" {
			continue
		}
		found = true
		if event.Kind != SessionOutputKindError {
			t.Fatalf("unexpected kind: got=%v want=%v", event.Kind, SessionOutputKindError)
		}
		if event.SourceEventType != SessionOutputSourceEventTypeError {
			t.Fatalf("unexpected source event type: got=%v want=%v", event.SourceEventType, SessionOutputSourceEventTypeError)
		}
		if !event.IsTerminal {
			t.Fatal("expected turn.failed to be terminal")
		}
	}
	if !found {
		t.Fatal("expected to find a turn.failed event")
	}
}

func TestNormalizeSessionOutputLinesClaudeKeepsDeltaAndFinalText(t *testing.T) {
	lines := readFixtureLines(t, "claude-code.stream.jsonl")

	events, err := NormalizeSessionOutputLines(AgentCliTypeClaudeCode, "session-claude", lines)
	if err != nil {
		t.Fatalf("NormalizeSessionOutputLines returned error: %v", err)
	}

	var hasDelta bool
	var hasFinal bool
	for _, event := range events {
		if event.SourceEventType == SessionOutputSourceEventTypeTextDelta && event.Body == "HELLO" {
			hasDelta = true
		}
		if event.SourceEventType == SessionOutputSourceEventTypeTextFinal && event.Body == "HELLO" {
			hasFinal = true
		}
	}
	if !hasDelta {
		t.Fatal("expected text delta event with body HELLO")
	}
	if !hasFinal {
		t.Fatal("expected text final event with body HELLO")
	}
}

func TestNormalizeSessionOutputLinesOpenCodePreservesStepOrder(t *testing.T) {
	lines := readFixtureLines(t, "opencode.run.jsonl")

	events, err := NormalizeSessionOutputLines(AgentCliTypeOpenCode, "session-opencode", lines)
	if err != nil {
		t.Fatalf("NormalizeSessionOutputLines returned error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("unexpected event count: got=%d want=3", len(events))
	}

	wantTypes := []SessionOutputSourceEventType{
		SessionOutputSourceEventTypeStepStarted,
		SessionOutputSourceEventTypeTextDelta,
		SessionOutputSourceEventTypeStepFinished,
	}
	for index, event := range events {
		if event.SourceEventType != wantTypes[index] {
			t.Fatalf("unexpected source event type at %d: got=%v want=%v", index, event.SourceEventType, wantTypes[index])
		}
	}
	if !events[2].IsTerminal {
		t.Fatal("expected final step_finish event to be terminal")
	}
}

func TestNormalizeSessionOutputLinesInvalidJSONBecomesParseErrorEvent(t *testing.T) {
	events, err := NormalizeSessionOutputLines(
		AgentCliTypeOpenCode,
		"session-invalid",
		[]string{"{invalid-json"},
	)
	if err != nil {
		t.Fatalf("NormalizeSessionOutputLines returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("unexpected event count: got=%d want=1", len(events))
	}

	event := events[0]
	if event.Kind != SessionOutputKindError {
		t.Fatalf("unexpected kind: got=%v want=%v", event.Kind, SessionOutputKindError)
	}
	if event.SourceEventType != SessionOutputSourceEventTypeError {
		t.Fatalf("unexpected source event type: got=%v want=%v", event.SourceEventType, SessionOutputSourceEventTypeError)
	}
	if event.IsTerminal {
		t.Fatal("expected parse failure event to be non-terminal")
	}
}

func TestNormalizeSessionOutputLinesAssignsMonotonicSourceSequence(t *testing.T) {
	lines := readFixtureLines(t, "codex-cli.failure.jsonl")

	events, err := NormalizeSessionOutputLines(AgentCliTypeCodexCLI, "session-seq", lines)
	if err != nil {
		t.Fatalf("NormalizeSessionOutputLines returned error: %v", err)
	}

	for index, event := range events {
		wantSequence := uint64(index + 1)
		if event.SourceSequence != wantSequence {
			t.Fatalf("unexpected source sequence at %d: got=%d want=%d", index, event.SourceSequence, wantSequence)
		}
	}
}

func TestNormalizeSessionOutputLinesClaudePreservesTextDeltaWhitespace(t *testing.T) {
	rawLines := []string{
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"  padded chunk  "}}}`,
	}

	events, err := NormalizeSessionOutputLines(AgentCliTypeClaudeCode, "session-claude-space", rawLines)
	if err != nil {
		t.Fatalf("NormalizeSessionOutputLines returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("unexpected event count: got=%d want=1", len(events))
	}

	event := events[0]
	if event.SourceEventType != SessionOutputSourceEventTypeTextDelta {
		t.Fatalf("unexpected source event type: got=%v want=%v", event.SourceEventType, SessionOutputSourceEventTypeTextDelta)
	}
	if event.Body != "  padded chunk  " {
		t.Fatalf("unexpected delta body: got=%q want=%q", event.Body, "  padded chunk  ")
	}
}

func TestNormalizeSessionOutputLinesClaudeConcatenatesAllAssistantTextBlocks(t *testing.T) {
	rawLines := []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"},{"type":"text","text":" world"},{"type":"tool_use","name":"noop"},{"type":"text","text":"!"}]}}`,
	}

	events, err := NormalizeSessionOutputLines(AgentCliTypeClaudeCode, "session-claude-final", rawLines)
	if err != nil {
		t.Fatalf("NormalizeSessionOutputLines returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("unexpected event count: got=%d want=1", len(events))
	}

	event := events[0]
	if event.SourceEventType != SessionOutputSourceEventTypeTextFinal {
		t.Fatalf("unexpected source event type: got=%v want=%v", event.SourceEventType, SessionOutputSourceEventTypeTextFinal)
	}
	if event.Body != "Hello world!" {
		t.Fatalf("unexpected final body: got=%q want=%q", event.Body, "Hello world!")
	}
}

func TestNormalizeSessionOutputLinesOpenCodePreservesTextDeltaWhitespace(t *testing.T) {
	rawLines := []string{
		`{"type":"text","part":{"text":"  open code chunk  "}}`,
	}

	events, err := NormalizeSessionOutputLines(AgentCliTypeOpenCode, "session-opencode-space", rawLines)
	if err != nil {
		t.Fatalf("NormalizeSessionOutputLines returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("unexpected event count: got=%d want=1", len(events))
	}

	event := events[0]
	if event.SourceEventType != SessionOutputSourceEventTypeTextDelta {
		t.Fatalf("unexpected source event type: got=%v want=%v", event.SourceEventType, SessionOutputSourceEventTypeTextDelta)
	}
	if event.Body != "  open code chunk  " {
		t.Fatalf("unexpected delta body: got=%q want=%q", event.Body, "  open code chunk  ")
	}
}

func TestNormalizeSessionOutputLinesClaudeToolOnlyAssistantDoesNotInventText(t *testing.T) {
	rawLines := []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"noop"}]}}`,
	}

	events, err := NormalizeSessionOutputLines(AgentCliTypeClaudeCode, "session-claude-tool", rawLines)
	if err != nil {
		t.Fatalf("NormalizeSessionOutputLines returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("unexpected event count: got=%d want=1", len(events))
	}

	event := events[0]
	if event.SourceEventType != SessionOutputSourceEventTypeTextFinal {
		t.Fatalf("unexpected source event type: got=%v want=%v", event.SourceEventType, SessionOutputSourceEventTypeTextFinal)
	}
	if event.Body != "" {
		t.Fatalf("expected empty body for tool-only assistant content, got=%q", event.Body)
	}
}

func TestNormalizeSessionOutputLinesIgnoresBlankLines(t *testing.T) {
	rawLines := []string{
		"",
		" ",
		`{"type":"step_start","part":{"type":"step-start"}}`,
		"",
		`{"type":"text","part":{"text":"HELLO"}}`,
		"   ",
	}

	events, err := NormalizeSessionOutputLines(AgentCliTypeOpenCode, "session-opencode-blank", rawLines)
	if err != nil {
		t.Fatalf("NormalizeSessionOutputLines returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("unexpected event count: got=%d want=2", len(events))
	}
	if events[0].SourceSequence != 1 {
		t.Fatalf("unexpected first source sequence: got=%d want=1", events[0].SourceSequence)
	}
	if events[1].SourceSequence != 2 {
		t.Fatalf("unexpected second source sequence: got=%d want=2", events[1].SourceSequence)
	}
	if events[0].Kind == SessionOutputKindError || events[1].Kind == SessionOutputKindError {
		t.Fatal("blank lines should not emit parse error events")
	}
}
