package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevelMatrix(t *testing.T) {
	testCases := []struct {
		name          string
		input         string
		expectedLevel slog.Level
		expectErr     bool
	}{
		{name: "debug", input: "debug", expectedLevel: slog.LevelDebug},
		{name: "info", input: "info", expectedLevel: slog.LevelInfo},
		{name: "empty defaults to info", input: "", expectedLevel: slog.LevelInfo},
		{name: "warn", input: "warn", expectedLevel: slog.LevelWarn},
		{name: "error", input: "error", expectedLevel: slog.LevelError},
		{name: "unsupported", input: "trace", expectedLevel: slog.LevelInfo, expectErr: true},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			level, err := parseLevel(tc.input)
			if tc.expectErr {
				if err == nil {
					t.Fatal("expected parseLevel to fail")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLevel returned error: %v", err)
			}
			if level != tc.expectedLevel {
				t.Fatalf("expected level=%v, got=%v", tc.expectedLevel, level)
			}
		})
	}
}

func TestNewWithWriterRenamesTimeToTimestamp(t *testing.T) {
	logBuffer := &bytes.Buffer{}
	logger, err := NewWithWriter(logBuffer, "info")
	if err != nil {
		t.Fatalf("NewWithWriter returned error: %v", err)
	}

	Event(logger, slog.LevelInfo, "job_run_completed", slog.String("job_id", "job-a"))

	var payload map[string]any
	if err := json.Unmarshal(logBuffer.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if _, hasTimestamp := payload["timestamp"]; !hasTimestamp {
		t.Fatalf("expected timestamp key, payload=%v", payload)
	}
	if _, hasTime := payload["time"]; hasTime {
		t.Fatalf("expected time key to be replaced by timestamp, payload=%v", payload)
	}
	if payload["event"] != "job_run_completed" {
		t.Fatalf("expected event attr to be injected, payload=%v", payload)
	}
}

func TestEventNilLoggerNoop(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("expected Event with nil logger to be no-op, panic=%v", recovered)
		}
	}()

	Event(nil, slog.LevelInfo, "event_will_be_ignored", slog.String("k", "v"))
}

func TestNewWithWriterRejectsUnsupportedLevel(t *testing.T) {
	_, err := NewWithWriter(&bytes.Buffer{}, "trace")
	if err == nil {
		t.Fatal("expected unsupported level error")
	}
	if !strings.Contains(err.Error(), "unsupported log level") {
		t.Fatalf("unexpected error: %v", err)
	}
}
