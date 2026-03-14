package handler

import (
	"bytes"
	"testing"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

func TestMapExitCodeToMessage_KnownCodes(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		stderr   string
		contains string
	}{
		{"exit 1 general", 1, "", "general error"},
		{"exit 1 permission", 1, "permission denied", "permission denied"},
		{"exit 2 misuse", 2, "", "invalid arguments"},
		{"exit 126 not executable", 126, "", "not executable"},
		{"exit 127 not found", 127, "", "not found"},
		{"exit 130 sigint", 130, "", "SIGINT"},
		{"exit 137 sigkill", 137, "", "SIGKILL"},
		{"exit 143 sigterm", 143, "", "SIGTERM"},
		{"unknown with stderr", 42, "some error output", "some error output"},
		{"unknown without stderr", 42, "", "exit code 42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := mapExitCodeToMessage(tt.code, tt.stderr)
			if !containsSubstring(msg, tt.contains) {
				t.Fatalf("expected message to contain %q, got=%q", tt.contains, msg)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
	}

	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Fatalf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

func TestLimitedWriter(t *testing.T) {
	var buf bytes.Buffer
	lw := &limitedWriter{w: &buf, limit: 10}

	// Write within limit.
	n, err := lw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 bytes written, got %d", n)
	}

	// Write that exceeds limit (only 5 more bytes allowed).
	n, err = lw.Write([]byte("world!!!!!"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 bytes written, got %d", n)
	}

	// Write after limit is reached (silently discarded).
	n, _ = lw.Write([]byte("more data"))
	if n != 9 {
		t.Fatalf("expected 9 from discarded write, got %d", n)
	}

	if buf.String() != "helloworld" {
		t.Fatalf("expected buffer to be 'helloworld', got %q", buf.String())
	}
}

func TestParseAgentOutputLine_JSON(t *testing.T) {
	logger := testLogger()

	line := `{"type":"tool_use","tool":"Read","content":"reading file"}`
	event, usage := parseAgentOutputLine(line, "sess-1", logger)

	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.Kind != dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_CALL {
		t.Fatalf("expected TOOL_CALL kind, got=%v", event.Kind)
	}
	if event.Body != "reading file" {
		t.Fatalf("expected body='reading file', got=%q", event.Body)
	}
	if usage != nil {
		t.Fatal("expected nil usage for event without usage block")
	}
}

func TestParseAgentOutputLine_WithUsage(t *testing.T) {
	logger := testLogger()

	line := `{"type":"text","content":"hello","usage":{"input_tokens":100,"output_tokens":50}}`
	_, usage := parseAgentOutputLine(line, "sess-1", logger)

	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.InputTokens != 100 {
		t.Fatalf("expected input_tokens=100, got=%d", usage.InputTokens)
	}
	if usage.OutputTokens != 50 {
		t.Fatalf("expected output_tokens=50, got=%d", usage.OutputTokens)
	}
}

func TestParseAgentOutputLine_PlainText(t *testing.T) {
	logger := testLogger()

	line := "This is just plain text output"
	event, _ := parseAgentOutputLine(line, "sess-1", logger)

	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.Kind != dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT {
		t.Fatalf("expected TEXT kind, got=%v", event.Kind)
	}
	if event.Body != line {
		t.Fatalf("expected body to be the raw line")
	}
}

func TestMapEventTypeToOutputKind(t *testing.T) {
	tests := []struct {
		eventType string
		subType   string
		want      dexdexv1.SessionOutputKind
	}{
		{"assistant", "", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT},
		{"text", "", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT},
		{"tool_use", "", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_CALL},
		{"tool_result", "", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_RESULT},
		{"plan", "", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PLAN_UPDATE},
		{"plan_update", "", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PLAN_UPDATE},
		{"progress", "", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PROGRESS},
		{"error", "", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_ERROR},
		{"warning", "", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_WARNING},
		{"unknown_type", "", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT},
	}

	for _, tt := range tests {
		t.Run(tt.eventType, func(t *testing.T) {
			got := mapEventTypeToOutputKind(tt.eventType, tt.subType)
			if got != tt.want {
				t.Fatalf("mapEventTypeToOutputKind(%q, %q) = %v, want %v", tt.eventType, tt.subType, got, tt.want)
			}
		})
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (substr == "" || bytes.Contains([]byte(s), []byte(substr)))
}
