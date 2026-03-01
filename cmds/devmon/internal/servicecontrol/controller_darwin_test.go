//go:build darwin

package servicecontrol

import (
	"context"
	"fmt"
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
	writeValidConfig(t, home)

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
	if !strings.Contains(string(daemonPlistContent), "<key>KeepAlive</key>\n  <true/>") {
		t.Fatalf("expected daemon keepalive true, got=%s", string(daemonPlistContent))
	}

	menubarPlistContent, err := os.ReadFile(menubarPlistPath)
	if err != nil {
		t.Fatalf("ReadFile menubar plist returned error: %v", err)
	}
	if !strings.Contains(string(menubarPlistContent), "<key>KeepAlive</key>\n  <false/>") {
		t.Fatalf("expected menubar keepalive false, got=%s", string(menubarPlistContent))
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

func TestInstallFailsWhenConfigMissing(t *testing.T) {
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

	err = manager.Install(context.Background())
	if err == nil {
		t.Fatal("expected install to fail when config is missing")
	}
	if !strings.Contains(err.Error(), "daemon config file is missing") {
		t.Fatalf("unexpected install error: %v", err)
	}
	if runner.hasCommand("launchctl ") {
		t.Fatalf("install should fail before launchctl calls, calls=%v", runner.calls)
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

func writeValidConfig(t *testing.T, home string) {
	t.Helper()

	workspacePath := t.TempDir()
	configPath := filepath.Join(home, ".config", "devmon", "devmon.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	content := fmt.Sprintf(`version = 1

[daemon]
max_concurrent_jobs = 1
startup_run = true
log_level = "info"

[[folder]]
id = "workspace-a"
path = %q

[[folder.job]]
id = "job-a"
interval = "1m"
timeout = "30s"
script = "echo ok"
`, workspacePath)

	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}
