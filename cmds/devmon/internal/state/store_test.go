package state

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/devmon/internal/contracts"
)

func TestStoreLifecycleAndRunUpdates(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "status.json")
	store, err := NewStore(storePath, slog.Default())
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}

	baseTime := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	store.nowFn = func() time.Time {
		return baseTime
	}

	if err := store.MarkDaemonStarted(1234); err != nil {
		t.Fatalf("MarkDaemonStarted returned error: %v", err)
	}
	if err := store.MarkHeartbeat(1234, 2); err != nil {
		t.Fatalf("MarkHeartbeat returned error: %v", err)
	}
	if err := store.MarkRunStarted("workspace-a", "job-a", 2); err != nil {
		t.Fatalf("MarkRunStarted returned error: %v", err)
	}

	baseTime = baseTime.Add(2 * time.Second)
	if err := store.MarkRunCompleted(RunCompletedInput{
		Outcome:    contracts.DevmonRunOutcomeSuccess,
		FolderID:   "workspace-a",
		JobID:      "job-a",
		DurationMS: 250,
		ActiveJobs: 1,
	}); err != nil {
		t.Fatalf("MarkRunCompleted returned error: %v", err)
	}

	baseTime = baseTime.Add(2 * time.Second)
	if err := store.MarkRunSkipped(RunSkippedInput{
		Outcome:    contracts.DevmonRunOutcomeSkippedCapacity,
		FolderID:   "workspace-a",
		JobID:      "job-b",
		SkipReason: "capacity",
		ActiveJobs: 1,
	}); err != nil {
		t.Fatalf("MarkRunSkipped returned error: %v", err)
	}

	snapshot, err := store.Read()
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}

	if !snapshot.Running {
		t.Fatal("expected daemon to be marked as running")
	}
	if snapshot.PID != 1234 {
		t.Fatalf("expected pid=1234, got=%d", snapshot.PID)
	}
	if snapshot.ActiveJobs != 1 {
		t.Fatalf("expected active_jobs=1, got=%d", snapshot.ActiveJobs)
	}
	if snapshot.LastRun == nil {
		t.Fatal("expected last run to be set")
	}
	if snapshot.LastRun.Outcome != contracts.DevmonRunOutcomeSuccess {
		t.Fatalf("expected success outcome, got=%s", snapshot.LastRun.Outcome)
	}
	if snapshot.LastSkip == nil {
		t.Fatal("expected last skip to be set")
	}
	if snapshot.LastSkip.SkipReason != "capacity" {
		t.Fatalf("expected skip reason capacity, got=%s", snapshot.LastSkip.SkipReason)
	}
}

func TestMarkDaemonStartedRefreshesStartedAtOnRestart(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "status.json")
	store, err := NewStore(storePath, slog.Default())
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}

	firstStart := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	currentTime := firstStart
	store.nowFn = func() time.Time {
		return currentTime
	}

	if err := store.MarkDaemonStarted(1111); err != nil {
		t.Fatalf("MarkDaemonStarted (first) returned error: %v", err)
	}
	if err := store.MarkDaemonStopped(1111); err != nil {
		t.Fatalf("MarkDaemonStopped returned error: %v", err)
	}

	secondStart := firstStart.Add(15 * time.Minute)
	currentTime = secondStart
	if err := store.MarkDaemonStarted(2222); err != nil {
		t.Fatalf("MarkDaemonStarted (second) returned error: %v", err)
	}

	snapshot, err := store.Read()
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}

	expectedStartedAt := secondStart.Format(time.RFC3339Nano)
	if snapshot.StartedAt != expectedStartedAt {
		t.Fatalf("expected started_at=%s, got=%s", expectedStartedAt, snapshot.StartedAt)
	}
	if snapshot.PID != 2222 {
		t.Fatalf("expected pid=2222, got=%d", snapshot.PID)
	}
	if !snapshot.Running {
		t.Fatal("expected daemon to be marked as running after restart")
	}
}

func TestMarkDaemonStoppedRequiresOwningPID(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "status.json")
	store, err := NewStore(storePath, slog.Default())
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}

	currentTime := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	store.nowFn = func() time.Time {
		return currentTime
	}

	if err := store.MarkDaemonStarted(1111); err != nil {
		t.Fatalf("MarkDaemonStarted (first daemon) returned error: %v", err)
	}
	if err := store.MarkHeartbeat(1111, 2); err != nil {
		t.Fatalf("MarkHeartbeat (first daemon) returned error: %v", err)
	}

	currentTime = currentTime.Add(15 * time.Second)
	if err := store.MarkDaemonStarted(2222); err != nil {
		t.Fatalf("MarkDaemonStarted (second daemon) returned error: %v", err)
	}
	if err := store.MarkHeartbeat(2222, 3); err != nil {
		t.Fatalf("MarkHeartbeat (second daemon) returned error: %v", err)
	}
	expectedUpdatedAt := currentTime.Format(time.RFC3339Nano)

	currentTime = currentTime.Add(15 * time.Second)
	if err := store.MarkDaemonStopped(1111); err != nil {
		t.Fatalf("MarkDaemonStopped (first daemon) returned error: %v", err)
	}

	snapshotAfterStaleStop, err := store.Read()
	if err != nil {
		t.Fatalf("Read (after stale stop) returned error: %v", err)
	}

	if !snapshotAfterStaleStop.Running {
		t.Fatal("expected daemon to remain running after stale stop attempt")
	}
	if snapshotAfterStaleStop.PID != 2222 {
		t.Fatalf("expected pid=2222 after stale stop attempt, got=%d", snapshotAfterStaleStop.PID)
	}
	if snapshotAfterStaleStop.ActiveJobs != 3 {
		t.Fatalf("expected active_jobs=3 after stale stop attempt, got=%d", snapshotAfterStaleStop.ActiveJobs)
	}
	if snapshotAfterStaleStop.UpdatedAt != expectedUpdatedAt {
		t.Fatalf(
			"expected updated_at to remain %s after stale stop attempt, got=%s",
			expectedUpdatedAt,
			snapshotAfterStaleStop.UpdatedAt,
		)
	}

	currentTime = currentTime.Add(10 * time.Second)
	if err := store.MarkDaemonStopped(2222); err != nil {
		t.Fatalf("MarkDaemonStopped (second daemon) returned error: %v", err)
	}

	stoppedSnapshot, err := store.Read()
	if err != nil {
		t.Fatalf("Read (after owner stop) returned error: %v", err)
	}

	if stoppedSnapshot.Running {
		t.Fatal("expected daemon to be marked as stopped when owner pid stops")
	}
	if stoppedSnapshot.PID != 0 {
		t.Fatalf("expected pid=0 after owner stop, got=%d", stoppedSnapshot.PID)
	}
	if stoppedSnapshot.ActiveJobs != 0 {
		t.Fatalf("expected active_jobs=0 after owner stop, got=%d", stoppedSnapshot.ActiveJobs)
	}
	expectedStoppedAt := currentTime.Format(time.RFC3339Nano)
	if stoppedSnapshot.UpdatedAt != expectedStoppedAt {
		t.Fatalf("expected updated_at=%s after owner stop, got=%s", expectedStoppedAt, stoppedSnapshot.UpdatedAt)
	}
}

func TestIsHeartbeatFresh(t *testing.T) {
	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	freshSnapshot := Snapshot{
		LastHeartbeatAt: now.Add(-5 * time.Second).Format(time.RFC3339Nano),
	}
	if !IsHeartbeatFresh(freshSnapshot, now, 10*time.Second) {
		t.Fatal("expected heartbeat to be fresh")
	}

	staleSnapshot := Snapshot{
		LastHeartbeatAt: now.Add(-40 * time.Second).Format(time.RFC3339Nano),
	}
	if IsHeartbeatFresh(staleSnapshot, now, 10*time.Second) {
		t.Fatal("expected heartbeat to be stale")
	}
}
