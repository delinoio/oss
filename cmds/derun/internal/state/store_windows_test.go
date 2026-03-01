//go:build windows

package state

import (
	"os"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/session"
	"github.com/delinoio/oss/cmds/derun/internal/testutil"
)

func TestGetSessionKeepsRunningWhenPIDIsAlive(t *testing.T) {
	root := testutil.TempStateRoot(t)
	store, err := New(root)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	sessionID := "01J0W111111111111111111111"
	meta := session.Meta{
		SchemaVersion:    "v1alpha1",
		SessionID:        sessionID,
		Command:          []string{"cmd", "/c", "echo", "ok"},
		WorkingDirectory: "C:\\",
		StartedAt:        time.Now().UTC().Add(-time.Minute),
		RetentionSeconds: int64((24 * time.Hour).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              os.Getpid(),
	}
	if err := store.WriteMeta(meta); err != nil {
		t.Fatalf("WriteMeta returned error: %v", err)
	}

	detail, err := store.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if detail.State != contracts.DerunSessionStateRunning {
		t.Fatalf("unexpected session state: got=%s want=%s", detail.State, contracts.DerunSessionStateRunning)
	}
	if detail.EndedAt != nil {
		t.Fatalf("expected EndedAt to remain nil for running session")
	}
}

func TestAppendOutputBlocksWhileAppendLockIsHeld(t *testing.T) {
	root := testutil.TempStateRoot(t)
	store, err := New(root)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	sessionID := "01J0W222222222222222222222"
	meta := session.Meta{
		SchemaVersion:    "v1alpha1",
		SessionID:        sessionID,
		Command:          []string{"cmd", "/c", "echo", "ok"},
		WorkingDirectory: "C:\\",
		StartedAt:        time.Now().UTC().Add(-time.Minute),
		RetentionSeconds: int64((24 * time.Hour).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              os.Getpid(),
	}
	if err := store.WriteMeta(meta); err != nil {
		t.Fatalf("WriteMeta returned error: %v", err)
	}

	lockPath, err := store.sessionFile(sessionID, lockFileName)
	if err != nil {
		t.Fatalf("sessionFile lock path: %v", err)
	}
	lockHandle, err := lockFile(lockPath)
	if err != nil {
		t.Fatalf("lockFile returned error: %v", err)
	}
	lockHeld := true
	t.Cleanup(func() {
		if !lockHeld {
			return
		}
		_ = unlockFile(lockHandle)
	})

	type appendResult struct {
		offset uint64
		err    error
	}
	appendResultCh := make(chan appendResult, 1)
	go func() {
		offset, appendErr := store.AppendOutput(
			sessionID,
			contracts.DerunOutputChannelStdout,
			[]byte("payload"),
			time.Now().UTC(),
		)
		appendResultCh <- appendResult{offset: offset, err: appendErr}
	}()

	select {
	case result := <-appendResultCh:
		t.Fatalf("AppendOutput returned while lock was held: offset=%d err=%v", result.offset, result.err)
	case <-time.After(200 * time.Millisecond):
	}

	if err := unlockFile(lockHandle); err != nil {
		t.Fatalf("unlockFile returned error: %v", err)
	}
	lockHeld = false

	var result appendResult
	select {
	case result = <-appendResultCh:
	case <-time.After(3 * time.Second):
		t.Fatalf("AppendOutput did not complete after lock release")
	}
	if result.err != nil {
		t.Fatalf("AppendOutput returned error after lock release: %v", result.err)
	}
	if result.offset != 0 {
		t.Fatalf("unexpected first chunk offset: got=%d want=0", result.offset)
	}

	chunks, nextCursor, eof, err := store.ReadOutput(sessionID, 0, 1024)
	if err != nil {
		t.Fatalf("ReadOutput returned error: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("unexpected chunk count: got=%d want=1", len(chunks))
	}
	if nextCursor != uint64(len("payload")) {
		t.Fatalf("unexpected next cursor: got=%d want=%d", nextCursor, len("payload"))
	}
	if !eof {
		t.Fatalf("expected eof=true after single append")
	}
}
