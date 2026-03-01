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
