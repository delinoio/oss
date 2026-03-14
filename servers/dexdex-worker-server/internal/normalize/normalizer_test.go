package normalize

import (
	"log/slog"
	"os"
	"testing"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestNormalizeTextKind(t *testing.T) {
	n := NewOutputNormalizer(testLogger())
	event := n.Normalize(RawAgentOutput{
		SessionID: "sess-1",
		Text:      "hello world",
		Kind:      "text",
	})

	if event.SessionId != "sess-1" {
		t.Fatalf("expected session_id=sess-1, got=%s", event.SessionId)
	}
	if event.Kind != dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT {
		t.Fatalf("expected kind=TEXT, got=%v", event.Kind)
	}
	if event.Body != "hello world" {
		t.Fatalf("expected body=hello world, got=%s", event.Body)
	}
}

func TestNormalizeToolCallKind(t *testing.T) {
	n := NewOutputNormalizer(testLogger())
	event := n.Normalize(RawAgentOutput{
		SessionID: "sess-2",
		Text:      "calling tool",
		Kind:      "tool_call",
	})

	if event.Kind != dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_CALL {
		t.Fatalf("expected kind=TOOL_CALL, got=%v", event.Kind)
	}
}

func TestNormalizeToolResultKind(t *testing.T) {
	n := NewOutputNormalizer(testLogger())
	event := n.Normalize(RawAgentOutput{
		SessionID: "sess-3",
		Text:      "tool result",
		Kind:      "tool_result",
	})

	if event.Kind != dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_RESULT {
		t.Fatalf("expected kind=TOOL_RESULT, got=%v", event.Kind)
	}
}

func TestNormalizePlanKind(t *testing.T) {
	n := NewOutputNormalizer(testLogger())
	event := n.Normalize(RawAgentOutput{
		SessionID: "sess-4",
		Text:      "plan update",
		Kind:      "plan",
	})

	if event.Kind != dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PLAN_UPDATE {
		t.Fatalf("expected kind=PLAN_UPDATE, got=%v", event.Kind)
	}
}

func TestNormalizeProgressKind(t *testing.T) {
	n := NewOutputNormalizer(testLogger())
	event := n.Normalize(RawAgentOutput{
		SessionID: "sess-5",
		Text:      "50%",
		Kind:      "progress",
	})

	if event.Kind != dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PROGRESS {
		t.Fatalf("expected kind=PROGRESS, got=%v", event.Kind)
	}
}

func TestNormalizeWarningKind(t *testing.T) {
	n := NewOutputNormalizer(testLogger())
	event := n.Normalize(RawAgentOutput{
		SessionID: "sess-6",
		Text:      "deprecation",
		Kind:      "warning",
	})

	if event.Kind != dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_WARNING {
		t.Fatalf("expected kind=WARNING, got=%v", event.Kind)
	}
}

func TestNormalizeErrorKind(t *testing.T) {
	n := NewOutputNormalizer(testLogger())
	event := n.Normalize(RawAgentOutput{
		SessionID: "sess-7",
		Text:      "crash",
		Kind:      "error",
	})

	if event.Kind != dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_ERROR {
		t.Fatalf("expected kind=ERROR, got=%v", event.Kind)
	}
}

func TestNormalizeUnknownKindMapsToUnspecified(t *testing.T) {
	n := NewOutputNormalizer(testLogger())
	event := n.Normalize(RawAgentOutput{
		SessionID: "sess-8",
		Text:      "unknown",
		Kind:      "banana",
	})

	if event.Kind != dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_UNSPECIFIED {
		t.Fatalf("expected kind=UNSPECIFIED for unknown kind, got=%v", event.Kind)
	}
}

func TestNormalizeAllKindMappings(t *testing.T) {
	n := NewOutputNormalizer(testLogger())

	tests := []struct {
		kind     string
		expected dexdexv1.SessionOutputKind
	}{
		{"text", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT},
		{"tool_call", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_CALL},
		{"tool_result", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_RESULT},
		{"plan", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PLAN_UPDATE},
		{"progress", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PROGRESS},
		{"warning", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_WARNING},
		{"error", dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_ERROR},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			event := n.Normalize(RawAgentOutput{
				SessionID: "test",
				Text:      "body",
				Kind:      tt.kind,
			})
			if event.Kind != tt.expected {
				t.Fatalf("kind=%s: expected %v, got=%v", tt.kind, tt.expected, event.Kind)
			}
		})
	}
}
