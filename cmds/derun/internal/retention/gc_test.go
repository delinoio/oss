package retention

import (
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
}

func intPtr(v int) *int {
	return &v
}
