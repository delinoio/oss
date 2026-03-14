package store

import (
	"log/slog"
	"os"
	"testing"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestNewSessionStoreIsEmpty(t *testing.T) {
	s := NewSessionStore(testLogger())
	events := s.GetOutputs("nonexistent")
	if events != nil {
		t.Fatalf("expected nil for nonexistent session, got=%v", events)
	}
}

func TestAppendAndGetOutputs(t *testing.T) {
	s := NewSessionStore(testLogger())

	event1 := &dexdexv1.SessionOutputEvent{
		SessionId: "sess-1",
		Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT,
		Body:      "first output",
	}
	event2 := &dexdexv1.SessionOutputEvent{
		SessionId: "sess-1",
		Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_CALL,
		Body:      "second output",
	}

	s.AppendOutput("sess-1", event1)
	s.AppendOutput("sess-1", event2)

	events := s.GetOutputs("sess-1")
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got=%d", len(events))
	}
	if events[0].Body != "first output" {
		t.Fatalf("expected first event body=first output, got=%s", events[0].Body)
	}
	if events[1].Body != "second output" {
		t.Fatalf("expected second event body=second output, got=%s", events[1].Body)
	}
}

func TestGetOutputsReturnsCopy(t *testing.T) {
	s := NewSessionStore(testLogger())

	event := &dexdexv1.SessionOutputEvent{
		SessionId: "sess-1",
		Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT,
		Body:      "output",
	}
	s.AppendOutput("sess-1", event)

	events1 := s.GetOutputs("sess-1")
	events2 := s.GetOutputs("sess-1")

	// Modify the returned slice; should not affect future calls.
	events1[0] = nil

	if events2[0] == nil {
		t.Fatal("modifying returned slice should not affect store")
	}
}

func TestMultipleSessions(t *testing.T) {
	s := NewSessionStore(testLogger())

	s.AppendOutput("sess-a", &dexdexv1.SessionOutputEvent{
		SessionId: "sess-a",
		Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT,
		Body:      "output a",
	})
	s.AppendOutput("sess-b", &dexdexv1.SessionOutputEvent{
		SessionId: "sess-b",
		Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_ERROR,
		Body:      "output b",
	})

	eventsA := s.GetOutputs("sess-a")
	eventsB := s.GetOutputs("sess-b")

	if len(eventsA) != 1 || eventsA[0].Body != "output a" {
		t.Fatalf("unexpected events for sess-a: %v", eventsA)
	}
	if len(eventsB) != 1 || eventsB[0].Body != "output b" {
		t.Fatalf("unexpected events for sess-b: %v", eventsB)
	}
}
