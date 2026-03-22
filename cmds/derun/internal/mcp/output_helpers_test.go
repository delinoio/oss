package mcp

import (
	"strings"
	"testing"
	"time"
)

func TestParseRequiredSessionID(t *testing.T) {
	t.Parallel()

	_, err := parseRequiredSessionID(map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "session_id is required") {
		t.Fatalf("expected required session_id error, got=%v", err)
	}
	if !strings.Contains(err.Error(), "details:") || !strings.Contains(err.Error(), "received_type=<nil>") {
		t.Fatalf("expected required session_id details, got=%v", err)
	}

	sessionID, err := parseRequiredSessionID(map[string]any{"session_id": "01JTEST"})
	if err != nil {
		t.Fatalf("parseRequiredSessionID returned error: %v", err)
	}
	if sessionID != "01JTEST" {
		t.Fatalf("unexpected session id: got=%q", sessionID)
	}
}

func TestParseCursor(t *testing.T) {
	t.Parallel()

	t.Run("optional missing", func(t *testing.T) {
		cursor, err := parseCursor(map[string]any{}, false)
		if err != nil {
			t.Fatalf("parseCursor returned error: %v", err)
		}
		if cursor != 0 {
			t.Fatalf("unexpected cursor: got=%d", cursor)
		}
	})

	t.Run("optional non string ignored", func(t *testing.T) {
		cursor, err := parseCursor(map[string]any{"cursor": 12}, false)
		if err != nil {
			t.Fatalf("parseCursor returned error: %v", err)
		}
		if cursor != 0 {
			t.Fatalf("unexpected cursor: got=%d", cursor)
		}
	})

	t.Run("optional parse error", func(t *testing.T) {
		_, err := parseCursor(map[string]any{"cursor": "not-a-number"}, false)
		if err == nil || !strings.Contains(err.Error(), "parse cursor") {
			t.Fatalf("expected parse cursor error, got=%v", err)
		}
		if !strings.Contains(err.Error(), "received_type=string") || !strings.Contains(err.Error(), "details:") {
			t.Fatalf("expected parse cursor details, got=%v", err)
		}
	})

	t.Run("required missing", func(t *testing.T) {
		_, err := parseCursor(map[string]any{}, true)
		if err == nil || !strings.Contains(err.Error(), "cursor is required") {
			t.Fatalf("expected cursor required error, got=%v", err)
		}
		if !strings.Contains(err.Error(), "details:") || !strings.Contains(err.Error(), "received_type=<nil>") {
			t.Fatalf("expected cursor required details, got=%v", err)
		}
	})

	t.Run("required parse error", func(t *testing.T) {
		_, err := parseCursor(map[string]any{"cursor": "invalid"}, true)
		if err == nil || !strings.Contains(err.Error(), "parse cursor") {
			t.Fatalf("expected parse cursor error, got=%v", err)
		}
	})
}

func TestParsePositiveIntDefault(t *testing.T) {
	t.Parallel()

	value, err := parsePositiveIntDefault(map[string]any{}, "max_bytes", 64)
	if err != nil {
		t.Fatalf("parsePositiveIntDefault returned error: %v", err)
	}
	if value != 64 {
		t.Fatalf("unexpected default value: got=%d", value)
	}

	value, err = parsePositiveIntDefault(map[string]any{"max_bytes": 128}, "max_bytes", 64)
	if err != nil {
		t.Fatalf("parsePositiveIntDefault returned error: %v", err)
	}
	if value != 128 {
		t.Fatalf("unexpected parsed value: got=%d", value)
	}

	value, err = parsePositiveIntDefault(map[string]any{"max_bytes": -1}, "max_bytes", 64)
	if err != nil {
		t.Fatalf("parsePositiveIntDefault returned error: %v", err)
	}
	if value != 64 {
		t.Fatalf("negative input should keep default: got=%d", value)
	}

	_, err = parsePositiveIntDefault(map[string]any{"max_bytes": 0.5}, "max_bytes", 64)
	if err == nil || !strings.Contains(err.Error(), "parse max_bytes") {
		t.Fatalf("expected parse max_bytes error, got=%v", err)
	}
	if !strings.Contains(err.Error(), "received_type=float64") || !strings.Contains(err.Error(), "details:") {
		t.Fatalf("expected parse max_bytes details, got=%v", err)
	}
}

func TestParseWaitTimeout(t *testing.T) {
	t.Parallel()

	timeout, err := parseWaitTimeout(map[string]any{})
	if err != nil {
		t.Fatalf("parseWaitTimeout returned error: %v", err)
	}
	if timeout != DefaultWaitTimeout {
		t.Fatalf("unexpected default timeout: got=%v want=%v", timeout, DefaultWaitTimeout)
	}

	timeout, err = parseWaitTimeout(map[string]any{"timeout_ms": int((MaxWaitTimeout + 2*time.Second) / time.Millisecond)})
	if err != nil {
		t.Fatalf("parseWaitTimeout returned error: %v", err)
	}
	if timeout != MaxWaitTimeout {
		t.Fatalf("timeout should be capped: got=%v want=%v", timeout, MaxWaitTimeout)
	}

	_, err = parseWaitTimeout(map[string]any{"timeout_ms": 0.5})
	if err == nil || !strings.Contains(err.Error(), "parse timeout_ms") {
		t.Fatalf("expected parse timeout_ms error, got=%v", err)
	}
	if !strings.Contains(err.Error(), "received_type=float64") || !strings.Contains(err.Error(), "details:") {
		t.Fatalf("expected parse timeout_ms details, got=%v", err)
	}
}

func TestBuildOutputPayload(t *testing.T) {
	t.Parallel()

	withoutWait := buildOutputPayload("session-1", nil, 42, true, outputPayloadOptions{})
	if withoutWait["next_cursor"] != "42" {
		t.Fatalf("unexpected next_cursor: %v", withoutWait["next_cursor"])
	}
	if _, ok := withoutWait["timed_out"]; ok {
		t.Fatalf("timed_out should be omitted when includeWait=false")
	}
	if _, ok := withoutWait["waited_ms"]; ok {
		t.Fatalf("waited_ms should be omitted when includeWait=false")
	}

	withWait := buildOutputPayload("session-2", nil, 7, false, outputPayloadOptions{includeWait: true, timedOut: true, waitedMS: 123})
	if timedOut, ok := withWait["timed_out"].(bool); !ok || !timedOut {
		t.Fatalf("expected timed_out=true, got=%v", withWait["timed_out"])
	}
	if waitedMS, ok := withWait["waited_ms"].(int64); !ok || waitedMS != 123 {
		t.Fatalf("unexpected waited_ms: %v", withWait["waited_ms"])
	}
}
