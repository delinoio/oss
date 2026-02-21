package state

import (
	"encoding/base64"
	"strconv"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/session"
	"github.com/delinoio/oss/cmds/derun/internal/testutil"
)

func TestStoreAppendAndReadOutput(t *testing.T) {
	root := testutil.TempStateRoot(t)
	store, err := New(root)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	sessionID := "01J0S111111111111111111111"
	meta := session.Meta{
		SchemaVersion:    "v1alpha1",
		SessionID:        sessionID,
		Command:          []string{"echo", "ok"},
		WorkingDirectory: "/tmp",
		StartedAt:        time.Now().UTC().Add(-time.Minute),
		RetentionSeconds: int64((24 * time.Hour).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              123,
	}
	if err := store.WriteMeta(meta); err != nil {
		t.Fatalf("WriteMeta returned error: %v", err)
	}
	if _, err := store.AppendOutput(sessionID, contracts.DerunOutputChannelStdout, []byte("hello"), time.Now().UTC()); err != nil {
		t.Fatalf("AppendOutput stdout returned error: %v", err)
	}
	if _, err := store.AppendOutput(sessionID, contracts.DerunOutputChannelStderr, []byte("world"), time.Now().UTC()); err != nil {
		t.Fatalf("AppendOutput stderr returned error: %v", err)
	}

	chunks, nextCursor, eof, err := store.ReadOutput(sessionID, 0, 7)
	if err != nil {
		t.Fatalf("ReadOutput returned error: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("unexpected chunk count: got=%d want=2", len(chunks))
	}
	if nextCursor != 7 {
		t.Fatalf("unexpected next cursor: got=%d want=7", nextCursor)
	}
	if eof {
		t.Fatalf("expected eof=false")
	}

	decoded0, err := base64.StdEncoding.DecodeString(chunks[0].DataBase64)
	if err != nil {
		t.Fatalf("decode chunk 0: %v", err)
	}
	if string(decoded0) != "hello" {
		t.Fatalf("unexpected chunk 0 payload: %s", string(decoded0))
	}
	decoded1, err := base64.StdEncoding.DecodeString(chunks[1].DataBase64)
	if err != nil {
		t.Fatalf("decode chunk 1: %v", err)
	}
	if string(decoded1) != "wo" {
		t.Fatalf("unexpected chunk 1 payload: %s", string(decoded1))
	}

	summaries, total, err := store.ListSessions("", 10)
	if err != nil {
		t.Fatalf("ListSessions returned error: %v", err)
	}
	if total != 1 || len(summaries) != 1 {
		t.Fatalf("unexpected list result: total=%d len=%d", total, len(summaries))
	}

	final := session.Final{
		SchemaVersion: "v1alpha1",
		SessionID:     sessionID,
		State:         contracts.DerunSessionStateExited,
		EndedAt:       time.Now().UTC(),
		ExitCode:      ptr(0),
	}
	if err := store.WriteFinal(final); err != nil {
		t.Fatalf("WriteFinal returned error: %v", err)
	}

	detail, err := store.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if detail.OutputBytes != 10 {
		t.Fatalf("unexpected output bytes: got=%d want=10", detail.OutputBytes)
	}
	if detail.ChunkCount != 2 {
		t.Fatalf("unexpected chunk count stats: got=%d want=2", detail.ChunkCount)
	}
	if detail.ExitCode == nil || *detail.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %v", detail.ExitCode)
	}

	remainingChunks, next, eof, err := store.ReadOutput(sessionID, nextCursor, 1024)
	if err != nil {
		t.Fatalf("ReadOutput second read returned error: %v", err)
	}
	if !eof {
		t.Fatalf("expected eof=true")
	}
	if next != 10 {
		t.Fatalf("unexpected next cursor after second read: got=%d want=10", next)
	}
	if len(remainingChunks) != 1 {
		t.Fatalf("unexpected second read chunks length: got=%d want=1", len(remainingChunks))
	}
	if remainingChunks[0].StartCursor != strconv.FormatUint(nextCursor, 10) {
		t.Fatalf("unexpected start cursor: got=%s want=%d", remainingChunks[0].StartCursor, nextCursor)
	}
}

func ptr(v int) *int {
	return &v
}
