package retention

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/logging"
	"github.com/delinoio/oss/cmds/derun/internal/session"
	"github.com/delinoio/oss/cmds/derun/internal/state"
	"github.com/delinoio/oss/cmds/derun/internal/testutil"
)

func TestSweepRemovesOnlyExpiredCompletedSessions(t *testing.T) {
	root := testutil.TempStateRoot(t)
	store, err := state.New(root)
	if err != nil {
		t.Fatalf("state.New returned error: %v", err)
	}
	logger, err := logging.New(root)
	if err != nil {
		t.Fatalf("logging.New returned error: %v", err)
	}
	defer logger.Close()

	shortRetentionExpired := "01J0S111111111111111111111"
	longRetentionActive := "01J0S222222222222222222222"
	running := "01J0S333333333333333333333"

	if err := store.WriteMeta(session.Meta{
		SchemaVersion:    "v1alpha1",
		SessionID:        shortRetentionExpired,
		Command:          []string{"echo", "expired"},
		WorkingDirectory: "/tmp",
		StartedAt:        time.Now().UTC().Add(-2 * time.Hour),
		RetentionSeconds: int64((10 * time.Minute).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              1,
	}); err != nil {
		t.Fatalf("WriteMeta shortRetentionExpired: %v", err)
	}
	if err := store.WriteFinal(session.Final{
		SchemaVersion: "v1alpha1",
		SessionID:     shortRetentionExpired,
		State:         contracts.DerunSessionStateExited,
		EndedAt:       time.Now().UTC().Add(-20 * time.Minute),
		ExitCode:      intPtr(0),
	}); err != nil {
		t.Fatalf("WriteFinal shortRetentionExpired: %v", err)
	}

	if err := store.WriteMeta(session.Meta{
		SchemaVersion:    "v1alpha1",
		SessionID:        longRetentionActive,
		Command:          []string{"echo", "long-retention"},
		WorkingDirectory: "/tmp",
		StartedAt:        time.Now().UTC().Add(-2 * time.Hour),
		RetentionSeconds: int64((4 * time.Hour).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              1,
	}); err != nil {
		t.Fatalf("WriteMeta longRetentionActive: %v", err)
	}
	if err := store.WriteFinal(session.Final{
		SchemaVersion: "v1alpha1",
		SessionID:     longRetentionActive,
		State:         contracts.DerunSessionStateExited,
		EndedAt:       time.Now().UTC().Add(-2 * time.Hour),
		ExitCode:      intPtr(0),
	}); err != nil {
		t.Fatalf("WriteFinal longRetentionActive: %v", err)
	}

	if err := store.WriteMeta(session.Meta{
		SchemaVersion:    "v1alpha1",
		SessionID:        running,
		Command:          []string{"sleep", "1"},
		WorkingDirectory: "/tmp",
		StartedAt:        time.Now().UTC().Add(-2 * time.Hour),
		RetentionSeconds: int64((1 * time.Minute).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              os.Getpid(),
	}); err != nil {
		t.Fatalf("WriteMeta running: %v", err)
	}

	result, err := Sweep(store, 30*time.Minute, logger)
	if err != nil {
		t.Fatalf("Sweep returned error: %v", err)
	}
	if result.Removed != 1 {
		t.Fatalf("unexpected removed count: got=%d want=1", result.Removed)
	}

	shortExpiredPath := filepath.Join(root, "sessions", shortRetentionExpired)
	if _, err := os.Stat(shortExpiredPath); !os.IsNotExist(err) {
		t.Fatalf("short-retention expired session should be removed")
	}
	longRetentionPath := filepath.Join(root, "sessions", longRetentionActive)
	if _, err := os.Stat(longRetentionPath); err != nil {
		t.Fatalf("long-retention session should stay: %v", err)
	}
	runningPath := filepath.Join(root, "sessions", running)
	if _, err := os.Stat(runningPath); err != nil {
		t.Fatalf("running session should stay: %v", err)
	}

	cleanupEvents := readCleanupEventsBySession(t, root)
	assertCleanupEvent(t, cleanupEvents, shortRetentionExpired, cleanupLogResultRemoved, cleanupLogReasonExpired)
	assertCleanupEvent(t, cleanupEvents, longRetentionActive, cleanupLogResultSkipped, cleanupLogReasonNotExpired)
	assertCleanupEvent(t, cleanupEvents, running, cleanupLogResultSkipped, cleanupLogReasonActiveSession)
}

func TestSweepHandlesUnreadableSessions(t *testing.T) {
	root := testutil.TempStateRoot(t)
	store, err := state.New(root)
	if err != nil {
		t.Fatalf("state.New returned error: %v", err)
	}
	logger, err := logging.New(root)
	if err != nil {
		t.Fatalf("logging.New returned error: %v", err)
	}
	defer logger.Close()

	expiredOrphan := "01J0S444444444444444444444"
	freshOrphan := "01J0S555555555555555555555"
	expiredMalformedMeta := "01J0S666666666666666666666"

	if err := store.EnsureSessionDir(expiredOrphan); err != nil {
		t.Fatalf("EnsureSessionDir expiredOrphan: %v", err)
	}
	if err := touchSessionArtifacts(t, root, expiredOrphan, time.Now().UTC().Add(-2*time.Hour)); err != nil {
		t.Fatalf("touchSessionArtifacts expiredOrphan: %v", err)
	}

	if err := store.EnsureSessionDir(freshOrphan); err != nil {
		t.Fatalf("EnsureSessionDir freshOrphan: %v", err)
	}

	if err := store.EnsureSessionDir(expiredMalformedMeta); err != nil {
		t.Fatalf("EnsureSessionDir expiredMalformedMeta: %v", err)
	}
	malformedMetaPath := filepath.Join(root, "sessions", expiredMalformedMeta, "meta.json")
	if err := os.WriteFile(malformedMetaPath, []byte("{malformed"), 0o600); err != nil {
		t.Fatalf("WriteFile malformed meta: %v", err)
	}
	if err := touchSessionArtifacts(t, root, expiredMalformedMeta, time.Now().UTC().Add(-2*time.Hour)); err != nil {
		t.Fatalf("touchSessionArtifacts expiredMalformedMeta: %v", err)
	}

	result, err := Sweep(store, 30*time.Minute, logger)
	if err != nil {
		t.Fatalf("Sweep returned error: %v", err)
	}
	if result.Checked != 3 {
		t.Fatalf("unexpected checked count: got=%d want=3", result.Checked)
	}
	if result.Removed != 2 {
		t.Fatalf("unexpected removed count: got=%d want=2", result.Removed)
	}

	expiredOrphanPath := filepath.Join(root, "sessions", expiredOrphan)
	if _, err := os.Stat(expiredOrphanPath); !os.IsNotExist(err) {
		t.Fatalf("expired orphan session should be removed")
	}
	freshOrphanPath := filepath.Join(root, "sessions", freshOrphan)
	if _, err := os.Stat(freshOrphanPath); err != nil {
		t.Fatalf("fresh orphan session should stay: %v", err)
	}
	expiredMalformedMetaPath := filepath.Join(root, "sessions", expiredMalformedMeta)
	if _, err := os.Stat(expiredMalformedMetaPath); !os.IsNotExist(err) {
		t.Fatalf("expired malformed-meta session should be removed")
	}

	cleanupEvents := readCleanupEventsBySession(t, root)
	assertCleanupEvent(t, cleanupEvents, expiredOrphan, cleanupLogResultRemoved, cleanupLogReasonUnreadableExpired)
	assertCleanupEvent(t, cleanupEvents, freshOrphan, cleanupLogResultSkipped, cleanupLogReasonUnreadableNotExpired)
	assertCleanupEvent(t, cleanupEvents, expiredMalformedMeta, cleanupLogResultRemoved, cleanupLogReasonUnreadableExpired)
}

func intPtr(v int) *int {
	return &v
}

type cleanupEvent struct {
	Event         string `json:"event"`
	SessionID     string `json:"session_id"`
	CleanupResult string `json:"cleanup_result"`
	CleanupReason string `json:"cleanup_reason"`
}

func readCleanupEventsBySession(t *testing.T, stateRoot string) map[string]cleanupEvent {
	t.Helper()

	logPath := filepath.Join(stateRoot, "logs", "derun.log")
	logFile, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}
	defer logFile.Close()

	events := make(map[string]cleanupEvent)
	scanner := bufio.NewScanner(logFile)
	for scanner.Scan() {
		var event cleanupEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode cleanup event: %v", err)
		}
		if event.Event != "cleanup_result" || event.SessionID == "" {
			continue
		}
		events[event.SessionID] = event
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan cleanup log file: %v", err)
	}

	return events
}

func assertCleanupEvent(t *testing.T, events map[string]cleanupEvent, sessionID string, result cleanupLogResult, reason cleanupLogReason) {
	t.Helper()

	event, ok := events[sessionID]
	if !ok {
		t.Fatalf("missing cleanup event for session %s", sessionID)
	}
	if event.CleanupResult != string(result) {
		t.Fatalf("unexpected cleanup result for %s: got=%s want=%s", sessionID, event.CleanupResult, result)
	}
	if event.CleanupReason != string(reason) {
		t.Fatalf("unexpected cleanup reason for %s: got=%s want=%s", sessionID, event.CleanupReason, reason)
	}
}

func touchSessionArtifacts(t *testing.T, stateRoot string, sessionID string, touchedAt time.Time) error {
	t.Helper()

	sessionPath := filepath.Join(stateRoot, "sessions", sessionID)
	if err := os.Chtimes(sessionPath, touchedAt, touchedAt); err != nil {
		return err
	}

	artifacts, err := os.ReadDir(sessionPath)
	if err != nil {
		return err
	}
	for _, artifact := range artifacts {
		artifactPath := filepath.Join(sessionPath, artifact.Name())
		if err := os.Chtimes(artifactPath, touchedAt, touchedAt); err != nil {
			return err
		}
	}

	return nil
}
