//go:build darwin

package servicecontrol

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/devmon/internal/state"
)

func TestInstallWritesPlistsAndRunsLaunchctl(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	runner := &fakeCommandRunner{}
	manager, err := NewManager(
		slog.Default(),
		WithCommandRunner(runner),
		WithStateReader(staticStateReader{snapshot: state.Snapshot{SchemaVersion: state.SchemaVersionV1}}),
	)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	if err := manager.Install(context.Background()); err != nil {
		t.Fatalf("Install returned error: %v", err)
	}

	daemonPlistPath := filepath.Join(home, "Library", "LaunchAgents", "io.delino.devmon.daemon.plist")
	menubarPlistPath := filepath.Join(home, "Library", "LaunchAgents", "io.delino.devmon.menubar.plist")

	if _, err := os.Stat(daemonPlistPath); err != nil {
		t.Fatalf("daemon plist missing: %v", err)
	}
	if _, err := os.Stat(menubarPlistPath); err != nil {
		t.Fatalf("menubar plist missing: %v", err)
	}

	daemonPlistContent, err := os.ReadFile(daemonPlistPath)
	if err != nil {
		t.Fatalf("ReadFile daemon plist returned error: %v", err)
	}
	if !strings.Contains(string(daemonPlistContent), "io.delino.devmon.daemon") {
		t.Fatalf("unexpected daemon plist content: %s", string(daemonPlistContent))
	}

	if !runner.hasCommand("plutil -lint " + daemonPlistPath) {
		t.Fatalf("expected plutil lint for daemon plist, calls=%v", runner.calls)
	}
	if !runner.hasCommand("launchctl bootstrap gui/") {
		t.Fatalf("expected launchctl bootstrap call, calls=%v", runner.calls)
	}
	if !runner.hasCommand("launchctl kickstart -k gui/") {
		t.Fatalf("expected launchctl kickstart call, calls=%v", runner.calls)
	}
}

func TestStatusReportsRunningWhenHeartbeatIsFresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	snapshot := state.Snapshot{
		SchemaVersion:   state.SchemaVersionV1,
		Running:         true,
		PID:             os.Getpid(),
		LastHeartbeatAt: now.Add(-2 * time.Second).Format(time.RFC3339Nano),
	}

	runner := &fakeCommandRunner{}
	manager, err := NewManager(
		slog.Default(),
		WithCommandRunner(runner),
		WithStateReader(staticStateReader{snapshot: snapshot}),
		WithNowFn(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	summary, err := manager.Status(context.Background())
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}

	if summary.DaemonHealth != DaemonHealthRunning {
		t.Fatalf("expected daemon health running, got=%s (summary=%+v)", summary.DaemonHealth, summary)
	}
	if !summary.Daemon.Loaded {
		t.Fatal("expected daemon launch agent to be loaded")
	}
}

type fakeCommandRunner struct {
	calls []string
}

func (runner *fakeCommandRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name
	if len(args) > 0 {
		call += " " + strings.Join(args, " ")
	}
	runner.calls = append(runner.calls, call)
	return []byte("ok"), nil
}

func (runner *fakeCommandRunner) hasCommand(prefix string) bool {
	for _, call := range runner.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}

type staticStateReader struct {
	snapshot state.Snapshot
	err      error
}

func (reader staticStateReader) Read() (state.Snapshot, error) {
	return reader.snapshot, reader.err
}
