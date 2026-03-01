package state

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func TestStoreHasSessionMetadata(t *testing.T) {
	root := testutil.TempStateRoot(t)
	store, err := New(root)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	sessionID := "01J0S222222222222222222222"
	hasMetadata, err := store.HasSessionMetadata(sessionID)
	if err != nil {
		t.Fatalf("HasSessionMetadata returned error: %v", err)
	}
	if hasMetadata {
		t.Fatalf("expected hasMetadata=false before writes")
	}

	meta := session.Meta{
		SchemaVersion:    "v1alpha1",
		SessionID:        sessionID,
		Command:          []string{"echo", "ok"},
		WorkingDirectory: "/tmp",
		StartedAt:        time.Now().UTC(),
		RetentionSeconds: int64((24 * time.Hour).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              100,
	}
	if err := store.WriteMeta(meta); err != nil {
		t.Fatalf("WriteMeta returned error: %v", err)
	}

	hasMetadata, err = store.HasSessionMetadata(sessionID)
	if err != nil {
		t.Fatalf("HasSessionMetadata returned error after WriteMeta: %v", err)
	}
	if !hasMetadata {
		t.Fatalf("expected hasMetadata=true after WriteMeta")
	}

	sessionIDFinalOnly := "01J0S333333333333333333333"
	final := session.Final{
		SchemaVersion: "v1alpha1",
		SessionID:     sessionIDFinalOnly,
		State:         contracts.DerunSessionStateExited,
		EndedAt:       time.Now().UTC(),
	}
	if err := store.WriteFinal(final); err != nil {
		t.Fatalf("WriteFinal returned error: %v", err)
	}

	hasMetadata, err = store.HasSessionMetadata(sessionIDFinalOnly)
	if err != nil {
		t.Fatalf("HasSessionMetadata returned error with final-only metadata: %v", err)
	}
	if !hasMetadata {
		t.Fatalf("expected hasMetadata=true when final.json exists")
	}
}

func TestStoreGetSessionStateWithoutFinalFromPID(t *testing.T) {
	root := testutil.TempStateRoot(t)
	store, err := New(root)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	startingSessionID := "01J0S454545454545454545454"
	startingMeta := session.Meta{
		SchemaVersion:    "v1alpha1",
		SessionID:        startingSessionID,
		Command:          []string{"sleep", "1"},
		WorkingDirectory: "/tmp",
		StartedAt:        time.Now().UTC().Add(-time.Minute),
		RetentionSeconds: int64((24 * time.Hour).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              0,
	}
	if err := store.WriteMeta(startingMeta); err != nil {
		t.Fatalf("WriteMeta starting session returned error: %v", err)
	}

	startingDetail, err := store.GetSession(startingSessionID)
	if err != nil {
		t.Fatalf("GetSession for starting session returned error: %v", err)
	}
	if startingDetail.State != contracts.DerunSessionStateStarting {
		t.Fatalf("unexpected starting session state: got=%s want=%s", startingDetail.State, contracts.DerunSessionStateStarting)
	}

	deadPID := os.Getpid() + 100000
	for processAlive(deadPID) {
		deadPID += 100000
	}
	failedSessionID := "01J0S565656565656565656565"
	failedMeta := session.Meta{
		SchemaVersion:    "v1alpha1",
		SessionID:        failedSessionID,
		Command:          []string{"sleep", "1"},
		WorkingDirectory: "/tmp",
		StartedAt:        time.Now().UTC().Add(-time.Minute),
		RetentionSeconds: int64((24 * time.Hour).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              deadPID,
	}
	if err := store.WriteMeta(failedMeta); err != nil {
		t.Fatalf("WriteMeta failed session returned error: %v", err)
	}

	failedDetail, err := store.GetSession(failedSessionID)
	if err != nil {
		t.Fatalf("GetSession for failed session returned error: %v", err)
	}
	if failedDetail.State != contracts.DerunSessionStateFailed {
		t.Fatalf("unexpected failed session state: got=%s want=%s", failedDetail.State, contracts.DerunSessionStateFailed)
	}
}

func TestStoreRejectsTraversalSessionIDAcrossEntrypoints(t *testing.T) {
	root := testutil.TempStateRoot(t)
	store, err := New(root)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	invalidSessionIDs := []string{
		"..",
		"../escape",
		"with/slash",
		`with\backslash`,
	}

	operations := []struct {
		name string
		run  func(sessionID string) error
	}{
		{
			name: "WriteMeta",
			run: func(sessionID string) error {
				return store.WriteMeta(newTestMeta(sessionID))
			},
		},
		{
			name: "WriteFinal",
			run: func(sessionID string) error {
				return store.WriteFinal(newTestFinal(sessionID))
			},
		},
		{
			name: "AppendOutput",
			run: func(sessionID string) error {
				_, err := store.AppendOutput(sessionID, contracts.DerunOutputChannelStdout, []byte("payload"), time.Now().UTC())
				return err
			},
		},
		{
			name: "ReadOutput",
			run: func(sessionID string) error {
				_, _, _, err := store.ReadOutput(sessionID, 0, 1024)
				return err
			},
		},
		{
			name: "GetSession",
			run: func(sessionID string) error {
				_, err := store.GetSession(sessionID)
				return err
			},
		},
		{
			name: "HasSessionMetadata",
			run: func(sessionID string) error {
				_, err := store.HasSessionMetadata(sessionID)
				return err
			},
		},
	}

	sanitizer := strings.NewReplacer("/", "_", "\\", "_")
	for _, operation := range operations {
		operation := operation
		for _, sessionID := range invalidSessionIDs {
			sessionID := sessionID
			t.Run(operation.name+"_"+sanitizer.Replace(sessionID), func(t *testing.T) {
				err := operation.run(sessionID)
				if err == nil {
					t.Fatalf("expected error for invalid session id: %q", sessionID)
				}
				if !strings.Contains(err.Error(), "session id") {
					t.Fatalf("unexpected error for session id %q: %v", sessionID, err)
				}
			})
		}
	}
}

func TestStoreRejectsSessionDirectorySymlinkEscape(t *testing.T) {
	requireSymlinkSupport(t)

	root := testutil.TempStateRoot(t)
	store, err := New(root)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	sessionID := "01J0S444444444444444444444"
	outsideDir := filepath.Join(t.TempDir(), "outside-session")
	if err := os.MkdirAll(outsideDir, 0o700); err != nil {
		t.Fatalf("mkdir outside dir: %v", err)
	}
	sessionPath := filepath.Join(root, "sessions", sessionID)
	if err := os.Symlink(outsideDir, sessionPath); err != nil {
		t.Fatalf("create session directory symlink: %v", err)
	}

	assertSymlinkEscapeError(t, store.WriteMeta(newTestMeta(sessionID)))
	assertSymlinkEscapeError(t, store.WriteFinal(newTestFinal(sessionID)))
	_, appendErr := store.AppendOutput(sessionID, contracts.DerunOutputChannelStdout, []byte("payload"), time.Now().UTC())
	assertSymlinkEscapeError(t, appendErr)
	_, getErr := store.GetSession(sessionID)
	assertSymlinkEscapeError(t, getErr)
	_, _, _, readErr := store.ReadOutput(sessionID, 0, 1024)
	assertSymlinkEscapeError(t, readErr)
}

func TestStoreRejectsSessionArtifactSymlinkEscape(t *testing.T) {
	requireSymlinkSupport(t)

	t.Run("meta file", func(t *testing.T) {
		store, sessionID, sessionDir := newStoreWithSessionDir(t, "01J0S555555555555555555555")
		outsideMeta := filepath.Join(t.TempDir(), "meta.json")
		if err := os.WriteFile(outsideMeta, []byte("{}"), 0o600); err != nil {
			t.Fatalf("write outside meta file: %v", err)
		}
		if err := os.Symlink(outsideMeta, filepath.Join(sessionDir, metaFileName)); err != nil {
			t.Fatalf("create meta symlink: %v", err)
		}
		assertSymlinkEscapeError(t, store.WriteMeta(newTestMeta(sessionID)))
	})

	t.Run("final file", func(t *testing.T) {
		store, sessionID, sessionDir := newStoreWithSessionDir(t, "01J0S666666666666666666666")
		outsideFinal := filepath.Join(t.TempDir(), "final.json")
		if err := os.WriteFile(outsideFinal, []byte("{}"), 0o600); err != nil {
			t.Fatalf("write outside final file: %v", err)
		}
		if err := os.Symlink(outsideFinal, filepath.Join(sessionDir, finalFileName)); err != nil {
			t.Fatalf("create final symlink: %v", err)
		}
		assertSymlinkEscapeError(t, store.WriteFinal(newTestFinal(sessionID)))
	})

	t.Run("append lock file", func(t *testing.T) {
		store, sessionID, sessionDir := newStoreWithSessionDir(t, "01J0S777777777777777777777")
		outsideLock := filepath.Join(t.TempDir(), "append.lock")
		if err := os.WriteFile(outsideLock, []byte(""), 0o600); err != nil {
			t.Fatalf("write outside lock file: %v", err)
		}
		if err := os.Symlink(outsideLock, filepath.Join(sessionDir, lockFileName)); err != nil {
			t.Fatalf("create lock symlink: %v", err)
		}
		_, err := store.AppendOutput(sessionID, contracts.DerunOutputChannelStdout, []byte("payload"), time.Now().UTC())
		assertSymlinkEscapeError(t, err)
	})

	t.Run("output file append", func(t *testing.T) {
		store, sessionID, sessionDir := newStoreWithSessionDir(t, "01J0S888888888888888888888")
		outsideOutput := filepath.Join(t.TempDir(), "output.bin")
		if err := os.WriteFile(outsideOutput, []byte(""), 0o600); err != nil {
			t.Fatalf("write outside output file: %v", err)
		}
		if err := os.Symlink(outsideOutput, filepath.Join(sessionDir, outputFileName)); err != nil {
			t.Fatalf("create output symlink: %v", err)
		}
		_, err := store.AppendOutput(sessionID, contracts.DerunOutputChannelStdout, []byte("payload"), time.Now().UTC())
		assertSymlinkEscapeError(t, err)
	})

	t.Run("output file append with dangling target", func(t *testing.T) {
		store, sessionID, sessionDir := newStoreWithSessionDir(t, "01J0S898989898989898989898")
		outsideRoot := filepath.Join(t.TempDir(), "outside")
		if err := os.MkdirAll(outsideRoot, 0o700); err != nil {
			t.Fatalf("mkdir outside root: %v", err)
		}
		danglingTarget := filepath.Join(outsideRoot, "new-output.bin")
		if err := os.Symlink(danglingTarget, filepath.Join(sessionDir, outputFileName)); err != nil {
			t.Fatalf("create dangling output symlink: %v", err)
		}

		_, err := store.AppendOutput(sessionID, contracts.DerunOutputChannelStdout, []byte("payload"), time.Now().UTC())
		assertSymlinkEscapeError(t, err)
		if _, statErr := os.Stat(danglingTarget); !os.IsNotExist(statErr) {
			t.Fatalf("dangling target should not be created, statErr=%v", statErr)
		}
	})

	t.Run("output file read", func(t *testing.T) {
		store, sessionID, sessionDir := newStoreWithSessionDir(t, "01J0S999999999999999999999")
		if _, err := store.AppendOutput(sessionID, contracts.DerunOutputChannelStdout, []byte("hello"), time.Now().UTC()); err != nil {
			t.Fatalf("AppendOutput setup returned error: %v", err)
		}

		outputPath := filepath.Join(sessionDir, outputFileName)
		if err := os.Remove(outputPath); err != nil {
			t.Fatalf("remove output file: %v", err)
		}
		outsideOutput := filepath.Join(t.TempDir(), "output.bin")
		if err := os.WriteFile(outsideOutput, []byte("hello"), 0o600); err != nil {
			t.Fatalf("write outside output file: %v", err)
		}
		if err := os.Symlink(outsideOutput, outputPath); err != nil {
			t.Fatalf("create output symlink: %v", err)
		}

		_, _, _, err := store.ReadOutput(sessionID, 0, 1024)
		assertSymlinkEscapeError(t, err)
	})

	t.Run("index file append", func(t *testing.T) {
		store, sessionID, sessionDir := newStoreWithSessionDir(t, "01J0T111111111111111111111")
		outsideIndex := filepath.Join(t.TempDir(), "index.jsonl")
		if err := os.WriteFile(outsideIndex, []byte(""), 0o600); err != nil {
			t.Fatalf("write outside index file: %v", err)
		}
		if err := os.Symlink(outsideIndex, filepath.Join(sessionDir, indexFileName)); err != nil {
			t.Fatalf("create index symlink: %v", err)
		}
		_, err := store.AppendOutput(sessionID, contracts.DerunOutputChannelStdout, []byte("payload"), time.Now().UTC())
		assertSymlinkEscapeError(t, err)
	})

	t.Run("index file read", func(t *testing.T) {
		store, sessionID, sessionDir := newStoreWithSessionDir(t, "01J0T222222222222222222222")
		if _, err := store.AppendOutput(sessionID, contracts.DerunOutputChannelStdout, []byte("hello"), time.Now().UTC()); err != nil {
			t.Fatalf("AppendOutput setup returned error: %v", err)
		}

		indexPath := filepath.Join(sessionDir, indexFileName)
		if err := os.Remove(indexPath); err != nil {
			t.Fatalf("remove index file: %v", err)
		}
		outsideIndex := filepath.Join(t.TempDir(), "index.jsonl")
		if err := os.WriteFile(outsideIndex, []byte(""), 0o600); err != nil {
			t.Fatalf("write outside index file: %v", err)
		}
		if err := os.Symlink(outsideIndex, indexPath); err != nil {
			t.Fatalf("create index symlink: %v", err)
		}

		_, _, _, err := store.ReadOutput(sessionID, 0, 1024)
		assertSymlinkEscapeError(t, err)
	})
}

func TestStoreAllowsInSessionSymlinkTargets(t *testing.T) {
	requireSymlinkSupport(t)

	store, sessionID, sessionDir := newStoreWithSessionDir(t, "01J0T333333333333333333333")
	internalArtifactsDir := filepath.Join(sessionDir, "artifacts")
	if err := os.MkdirAll(internalArtifactsDir, 0o700); err != nil {
		t.Fatalf("mkdir internal artifacts dir: %v", err)
	}

	if err := os.Symlink(filepath.Join(internalArtifactsDir, lockFileName), filepath.Join(sessionDir, lockFileName)); err != nil {
		t.Fatalf("create lock symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(internalArtifactsDir, outputFileName), filepath.Join(sessionDir, outputFileName)); err != nil {
		t.Fatalf("create output symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(internalArtifactsDir, indexFileName), filepath.Join(sessionDir, indexFileName)); err != nil {
		t.Fatalf("create index symlink: %v", err)
	}

	if _, err := store.AppendOutput(sessionID, contracts.DerunOutputChannelStdout, []byte("hello"), time.Now().UTC()); err != nil {
		t.Fatalf("AppendOutput returned error: %v", err)
	}
	chunks, nextCursor, eof, err := store.ReadOutput(sessionID, 0, 1024)
	if err != nil {
		t.Fatalf("ReadOutput returned error: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("unexpected chunk count: got=%d want=1", len(chunks))
	}
	if nextCursor != 5 {
		t.Fatalf("unexpected next cursor: got=%d want=5", nextCursor)
	}
	if !eof {
		t.Fatalf("expected eof=true")
	}
}

func ptr(v int) *int {
	return &v
}

func newTestMeta(sessionID string) session.Meta {
	return session.Meta{
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
}

func newTestFinal(sessionID string) session.Final {
	return session.Final{
		SchemaVersion: "v1alpha1",
		SessionID:     sessionID,
		State:         contracts.DerunSessionStateExited,
		EndedAt:       time.Now().UTC(),
		ExitCode:      ptr(0),
	}
}

func newStoreWithSessionDir(t *testing.T, sessionID string) (*Store, string, string) {
	t.Helper()
	root := testutil.TempStateRoot(t)
	store, err := New(root)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := store.EnsureSessionDir(sessionID); err != nil {
		t.Fatalf("EnsureSessionDir returned error: %v", err)
	}
	sessionDir := filepath.Join(root, "sessions", sessionID)
	return store, sessionID, sessionDir
}

func requireSymlinkSupport(t *testing.T) {
	t.Helper()
	probeDir := t.TempDir()
	targetDir := filepath.Join(probeDir, "target-dir")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	dirLink := filepath.Join(probeDir, "dir-link")
	if err := os.Symlink(targetDir, dirLink); err != nil {
		t.Skipf("symlink not supported in test environment: %v", err)
	}
}

func assertSymlinkEscapeError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected symlink escape error")
	}
	if !strings.Contains(err.Error(), "symlink") || !strings.Contains(err.Error(), "escape") {
		t.Fatalf("expected symlink escape error, got: %v", err)
	}
}
