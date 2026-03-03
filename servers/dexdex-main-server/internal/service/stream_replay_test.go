package service

import (
	"errors"
	"reflect"
	"testing"
)

func uint64Pointer(value uint64) *uint64 {
	return &value
}

func TestReplayWorkspaceEventsFromSequenceIsExclusive(t *testing.T) {
	events := []WorkspaceStreamEnvelope{
		{WorkspaceID: "workspace-1", Sequence: 10, EventType: StreamEventTypeTaskUpdated},
		{WorkspaceID: "workspace-1", Sequence: 11, EventType: StreamEventTypeSubTaskUpdated},
		{WorkspaceID: "workspace-1", Sequence: 12, EventType: StreamEventTypeSessionOutput},
	}

	replayed, err := ReplayWorkspaceEvents(events, uint64Pointer(11), 10)
	if err != nil {
		t.Fatalf("ReplayWorkspaceEvents returned error: %v", err)
	}

	want := []WorkspaceStreamEnvelope{{WorkspaceID: "workspace-1", Sequence: 12, EventType: StreamEventTypeSessionOutput}}
	if !reflect.DeepEqual(replayed, want) {
		t.Fatalf("unexpected replay result: got=%#v want=%#v", replayed, want)
	}
}

func TestReplayWorkspaceEventsFailsWithOutOfRangeWhenCursorIsOlderThanRetention(t *testing.T) {
	events := []WorkspaceStreamEnvelope{{WorkspaceID: "workspace-1", Sequence: 20, EventType: StreamEventTypeTaskUpdated}}

	_, err := ReplayWorkspaceEvents(events, uint64Pointer(17), 20)
	if err == nil {
		t.Fatal("expected error but got nil")
	}

	var replayError *StreamReplayError
	if !errors.As(err, &replayError) {
		t.Fatalf("expected StreamReplayError, got=%T", err)
	}
	if replayError.Code != StreamReplayErrorCodeCursorOutOfRange {
		t.Fatalf("unexpected replay error code: got=%v want=%v", replayError.Code, StreamReplayErrorCodeCursorOutOfRange)
	}
	if replayError.Cursor == nil {
		t.Fatal("expected cursor details but got nil")
	}
	if replayError.Cursor.EarliestAvailableSequence != 20 {
		t.Fatalf("unexpected earliest sequence: got=%d want=%d", replayError.Cursor.EarliestAvailableSequence, 20)
	}
}

func TestReplayWorkspaceEventsRejectsNonMonotonicSequences(t *testing.T) {
	events := []WorkspaceStreamEnvelope{
		{WorkspaceID: "workspace-1", Sequence: 10, EventType: StreamEventTypeTaskUpdated},
		{WorkspaceID: "workspace-1", Sequence: 10, EventType: StreamEventTypeSubTaskUpdated},
	}

	_, err := ReplayWorkspaceEvents(events, uint64Pointer(9), 10)
	if err == nil {
		t.Fatal("expected error but got nil")
	}

	var replayError *StreamReplayError
	if !errors.As(err, &replayError) {
		t.Fatalf("expected StreamReplayError, got=%T", err)
	}
	if replayError.Code != StreamReplayErrorCodeInvalidSequence {
		t.Fatalf("unexpected replay error code: got=%v want=%v", replayError.Code, StreamReplayErrorCodeInvalidSequence)
	}
}
