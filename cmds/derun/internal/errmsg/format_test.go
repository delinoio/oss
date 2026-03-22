package errmsg

import (
	"errors"
	"strings"
	"testing"
)

func TestUsageIncludesDeterministicDetailsAndHint(t *testing.T) {
	t.Parallel()

	message := Usage(
		"unknown option",
		"use --help",
		map[string]any{
			"session_id": "01JTEST",
			"arg_count":  2,
		},
	)

	if !strings.HasPrefix(message, "invalid arguments: unknown option") {
		t.Fatalf("unexpected prefix: %q", message)
	}
	if !strings.Contains(message, "details: arg_count=2, session_id=01JTEST") {
		t.Fatalf("missing deterministic details: %q", message)
	}
	if !strings.Contains(message, "hint: use --help") {
		t.Fatalf("missing hint: %q", message)
	}
}

func TestRuntimeIncludesCauseType(t *testing.T) {
	t.Parallel()

	baseErr := errors.New("boom")
	message := Runtime("initialize store", baseErr, map[string]any{"state_root": "/tmp/derun"})

	if !strings.Contains(message, "failed to initialize store: boom") {
		t.Fatalf("unexpected runtime message: %q", message)
	}
	if !strings.Contains(message, "cause_type=*errors.errorString") {
		t.Fatalf("missing cause_type details: %q", message)
	}
	if !strings.Contains(message, "state_root=/tmp/derun") {
		t.Fatalf("missing explicit details: %q", message)
	}
}

func TestParsePreservesTokenAndAddsDetails(t *testing.T) {
	t.Parallel()

	parseErr := Parse("cursor", errors.New("invalid syntax"), map[string]any{
		"received_type":  "string",
		"received_value": "x",
	})
	if parseErr == nil {
		t.Fatalf("expected parse error")
	}
	message := parseErr.Error()
	if !strings.Contains(message, "parse cursor: invalid syntax") {
		t.Fatalf("missing parse token: %q", message)
	}
	if !strings.Contains(message, "received_type=string") || !strings.Contains(message, "received_value=x") {
		t.Fatalf("missing parse details: %q", message)
	}
}

func TestRequiredIncludesReceivedTypeAndValue(t *testing.T) {
	t.Parallel()

	err := Required("session_id", "a non-empty string", 12)
	if err == nil {
		t.Fatalf("expected required error")
	}
	message := err.Error()
	if !strings.Contains(message, "session_id is required; expected a non-empty string") {
		t.Fatalf("unexpected required message: %q", message)
	}
	if !strings.Contains(message, "received_type=int") || !strings.Contains(message, "received_value=12") {
		t.Fatalf("missing required details: %q", message)
	}
}

func TestWrapPreservesSentinel(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("session not found")
	wrapped := Wrap(sentinel, map[string]any{"session_id": "01JTEST"})
	if !errors.Is(wrapped, sentinel) {
		t.Fatalf("expected wrapped error to preserve sentinel")
	}
	if !strings.Contains(wrapped.Error(), "session not found") {
		t.Fatalf("missing sentinel text: %q", wrapped.Error())
	}
	if !strings.Contains(wrapped.Error(), "session_id=01JTEST") {
		t.Fatalf("missing details: %q", wrapped.Error())
	}
}

func TestValueSummaryEscapesAndTruncatesStrings(t *testing.T) {
	t.Parallel()

	raw := strings.Repeat("a", 10) + "\n" + strings.Repeat("b", 100)
	summary := ValueSummary(raw)
	if strings.Contains(summary, "\n") {
		t.Fatalf("value summary should be single-line, got=%q", summary)
	}
	if !strings.Contains(summary, "\\n") {
		t.Fatalf("expected escaped newline marker, got=%q", summary)
	}
	if !strings.HasSuffix(summary, "...") {
		t.Fatalf("expected truncation suffix, got=%q", summary)
	}
}

func TestCommandDetailsDoNotExposeArguments(t *testing.T) {
	t.Parallel()

	details := CommandDetails([]string{"bash", "-lc", "echo secret"})
	if details["command_name"] != "bash" {
		t.Fatalf("unexpected command_name: %v", details["command_name"])
	}
	if details["arg_count"] != 2 {
		t.Fatalf("unexpected arg_count: %v", details["arg_count"])
	}
}
