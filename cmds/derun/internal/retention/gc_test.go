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

	expired := "01J0S111111111111111111111"
	active := "01J0S222222222222222222222"

	if err := store.WriteMeta(session.Meta{
		SchemaVersion:    "v1alpha1",
		SessionID:        expired,
		Command:          []string{"echo", "expired"},
		WorkingDirectory: "/tmp",
		StartedAt:        time.Now().UTC().Add(-2 * time.Hour),
		RetentionSeconds: int64((24 * time.Hour).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              1,
	}); err != nil {
		t.Fatalf("WriteMeta expired: %v", err)
	}
	if err := store.WriteFinal(session.Final{
		SchemaVersion: "v1alpha1",
		SessionID:     expired,
		State:         contracts.DerunSessionStateExited,
		EndedAt:       time.Now().UTC().Add(-2 * time.Hour),
		ExitCode:      intPtr(0),
	}); err != nil {
		t.Fatalf("WriteFinal expired: %v", err)
	}

	if err := store.WriteMeta(session.Meta{
		SchemaVersion:    "v1alpha1",
		SessionID:        active,
		Command:          []string{"sleep", "1"},
		WorkingDirectory: "/tmp",
		StartedAt:        time.Now().UTC().Add(-2 * time.Hour),
		RetentionSeconds: int64((24 * time.Hour).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              os.Getpid(),
	}); err != nil {
		t.Fatalf("WriteMeta active: %v", err)
	}

	result, err := Sweep(store, 30*time.Minute, logger)
	if err != nil {
		t.Fatalf("Sweep returned error: %v", err)
	}
	if result.Removed != 1 {
		t.Fatalf("unexpected removed count: got=%d want=1", result.Removed)
	}

	expiredPath := filepath.Join(root, "sessions", expired)
	if _, err := os.Stat(expiredPath); !os.IsNotExist(err) {
		t.Fatalf("expired session should be removed")
	}
	activePath := filepath.Join(root, "sessions", active)
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("active session should stay: %v", err)
	}
}

func intPtr(v int) *int {
	return &v
}
