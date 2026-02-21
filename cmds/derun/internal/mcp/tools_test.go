package mcp

import (
	"os"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/session"
	"github.com/delinoio/oss/cmds/derun/internal/state"
	"github.com/delinoio/oss/cmds/derun/internal/testutil"
)

func TestToolHandlersIncludeSchemaVersion(t *testing.T) {
	root := testutil.TempStateRoot(t)
	store, err := state.New(root)
	if err != nil {
		t.Fatalf("state.New returned error: %v", err)
	}

	sessionID := "01J0S333333333333333333333"
	if err := store.WriteMeta(session.Meta{
		SchemaVersion:    SchemaVersion,
		SessionID:        sessionID,
		Command:          []string{"echo", "hello"},
		WorkingDirectory: "/tmp",
		StartedAt:        time.Now().UTC().Add(-time.Minute),
		RetentionSeconds: int64((24 * time.Hour).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              999,
	}); err != nil {
		t.Fatalf("WriteMeta returned error: %v", err)
	}
	if _, err := store.AppendOutput(sessionID, contracts.DerunOutputChannelStdout, []byte("hello"), time.Now().UTC()); err != nil {
		t.Fatalf("AppendOutput returned error: %v", err)
	}
	if err := store.WriteFinal(session.Final{
		SchemaVersion: SchemaVersion,
		SessionID:     sessionID,
		State:         contracts.DerunSessionStateExited,
		EndedAt:       time.Now().UTC(),
		ExitCode:      intPtr(0),
	}); err != nil {
		t.Fatalf("WriteFinal returned error: %v", err)
	}

	listPayload, err := handleListSessions(store, map[string]any{"limit": 10})
	if err != nil {
		t.Fatalf("handleListSessions returned error: %v", err)
	}
	if listPayload["schema_version"] != SchemaVersion {
		t.Fatalf("missing schema version in list payload")
	}

	getPayload, err := handleGetSession(store, map[string]any{"session_id": sessionID})
	if err != nil {
		t.Fatalf("handleGetSession returned error: %v", err)
	}
	if getPayload["schema_version"] != SchemaVersion {
		t.Fatalf("missing schema version in get payload")
	}

	readPayload, err := handleReadOutput(store, map[string]any{"session_id": sessionID, "cursor": "0", "max_bytes": 1024})
	if err != nil {
		t.Fatalf("handleReadOutput returned error: %v", err)
	}
	if readPayload["schema_version"] != SchemaVersion {
		t.Fatalf("missing schema version in read payload")
	}
	if readPayload["next_cursor"] != "5" {
		t.Fatalf("unexpected next cursor: %v", readPayload["next_cursor"])
	}

	waitPayload, err := handleWaitOutput(store, map[string]any{"session_id": sessionID, "cursor": "0", "timeout_ms": 100})
	if err != nil {
		t.Fatalf("handleWaitOutput returned error: %v", err)
	}
	if waitPayload["schema_version"] != SchemaVersion {
		t.Fatalf("missing schema version in wait payload")
	}
}

func TestHandleWaitOutputLiveTailTimesOutForActiveSession(t *testing.T) {
	root := testutil.TempStateRoot(t)
	store, err := state.New(root)
	if err != nil {
		t.Fatalf("state.New returned error: %v", err)
	}

	sessionID := "01J0S444444444444444444444"
	if err := store.WriteMeta(session.Meta{
		SchemaVersion:    SchemaVersion,
		SessionID:        sessionID,
		Command:          []string{"sleep", "1"},
		WorkingDirectory: "/tmp",
		StartedAt:        time.Now().UTC().Add(-time.Minute),
		RetentionSeconds: int64((24 * time.Hour).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              os.Getpid(),
	}); err != nil {
		t.Fatalf("WriteMeta returned error: %v", err)
	}
	if _, err := store.AppendOutput(sessionID, contracts.DerunOutputChannelStdout, []byte("hello"), time.Now().UTC()); err != nil {
		t.Fatalf("AppendOutput returned error: %v", err)
	}

	started := time.Now()
	waitPayload, err := handleWaitOutput(store, map[string]any{
		"session_id": sessionID,
		"cursor":     "5",
		"timeout_ms": 200,
	})
	if err != nil {
		t.Fatalf("handleWaitOutput returned error: %v", err)
	}

	if waitPayload["schema_version"] != SchemaVersion {
		t.Fatalf("missing schema version in wait payload")
	}
	if timedOut, ok := waitPayload["timed_out"].(bool); !ok || !timedOut {
		t.Fatalf("expected timed_out=true, got=%v", waitPayload["timed_out"])
	}
	if elapsed := time.Since(started); elapsed < 150*time.Millisecond {
		t.Fatalf("wait_output returned too early: elapsed=%v", elapsed)
	}
}

func intPtr(v int) *int {
	return &v
}
