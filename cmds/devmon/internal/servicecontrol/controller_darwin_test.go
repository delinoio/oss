//go:build darwin

package servicecontrol

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/devmon/internal/paths"
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

func TestInstallFailsWhenConfigInvalid(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeInvalidConfig(t, home)

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
		t.Fatal("expected install to fail for invalid daemon config")
	}
	if !strings.Contains(err.Error(), "daemon config validation failed") {
		t.Fatalf("unexpected install error: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("install should fail before running command runners, calls=%v", runner.calls)
	}
}

func TestUninstallBootoutsAndRemovesPlists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	launchAgentsPath := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(launchAgentsPath, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	daemonPlistPath := filepath.Join(launchAgentsPath, paths.DaemonLaunchAgentLabel+".plist")
	menubarPlistPath := filepath.Join(launchAgentsPath, paths.MenubarLaunchAgentLabel+".plist")
	if err := os.WriteFile(daemonPlistPath, []byte("daemon"), 0o644); err != nil {
		t.Fatalf("WriteFile daemon plist returned error: %v", err)
	}
	if err := os.WriteFile(menubarPlistPath, []byte("menubar"), 0o644); err != nil {
		t.Fatalf("WriteFile menubar plist returned error: %v", err)
	}

	runner := &fakeCommandRunner{}
	manager, err := NewManager(
		slog.Default(),
		WithCommandRunner(runner),
		WithStateReader(staticStateReader{snapshot: state.Snapshot{SchemaVersion: state.SchemaVersionV1}}),
	)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	if err := manager.Uninstall(context.Background()); err != nil {
		t.Fatalf("Uninstall returned error: %v", err)
	}

	if _, err := os.Stat(daemonPlistPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected daemon plist to be removed, err=%v", err)
	}
	if _, err := os.Stat(menubarPlistPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected menubar plist to be removed, err=%v", err)
	}

	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if !runner.hasCommand("launchctl bootout " + domain + "/" + paths.DaemonLaunchAgentLabel) {
		t.Fatalf("expected daemon bootout call, calls=%v", runner.calls)
	}
	if !runner.hasCommand("launchctl bootout " + domain + "/" + paths.MenubarLaunchAgentLabel) {
		t.Fatalf("expected menubar bootout call, calls=%v", runner.calls)
	}
}

func TestStopBootoutsDaemonAgent(t *testing.T) {
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

	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}

	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if !runner.hasCommand("launchctl bootout " + domain + "/" + paths.DaemonLaunchAgentLabel) {
		t.Fatalf("expected daemon bootout call, calls=%v", runner.calls)
	}
}

func TestStartFailsWhenDaemonPlistMissing(t *testing.T) {
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

	err = manager.Start(context.Background())
	if err == nil {
		t.Fatal("expected start to fail when daemon plist is missing")
	}
	if !strings.Contains(err.Error(), "daemon launch agent is not installed") {
		t.Fatalf("unexpected start error: %v", err)
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

func TestStatusReportsStoppedWhenAgentNotLoadedAndNotRunning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	snapshot := state.Snapshot{
		SchemaVersion: state.SchemaVersionV1,
		Running:       false,
		PID:           0,
	}

	runner := &fakeCommandRunner{}
	runner.addPrefixError("launchctl print ", errors.New("not loaded"))

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
	if summary.DaemonHealth != DaemonHealthStopped {
		t.Fatalf("expected daemon health stopped, got=%s (summary=%+v)", summary.DaemonHealth, summary)
	}
}

func TestStatusReportsErrorWhenHeartbeatIsStale(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	snapshot := state.Snapshot{
		SchemaVersion:   state.SchemaVersionV1,
		Running:         true,
		PID:             os.Getpid(),
		LastHeartbeatAt: now.Add(-1 * time.Minute).Format(time.RFC3339Nano),
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
	if summary.DaemonHealth != DaemonHealthError {
		t.Fatalf("expected daemon health error, got=%s (summary=%+v)", summary.DaemonHealth, summary)
	}
	if !strings.Contains(summary.Message, "heartbeat stale") {
		t.Fatalf("expected stale heartbeat message, got=%q", summary.Message)
	}
}

func TestStatusReportsErrorWhenPIDIsNotRunning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	snapshot := state.Snapshot{
		SchemaVersion:   state.SchemaVersionV1,
		Running:         true,
		PID:             2147483647,
		LastHeartbeatAt: now.Add(-1 * time.Second).Format(time.RFC3339Nano),
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
	if summary.DaemonHealth != DaemonHealthError {
		t.Fatalf("expected daemon health error, got=%s (summary=%+v)", summary.DaemonHealth, summary)
	}
	if !strings.Contains(summary.Message, "pid not running") {
		t.Fatalf("expected dead pid message, got=%q", summary.Message)
	}
}

func TestStatusReportsErrorWhenStateReadFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	runner := &fakeCommandRunner{}
	manager, err := NewManager(
		slog.Default(),
		WithCommandRunner(runner),
		WithStateReader(staticStateReader{err: errors.New("state read failed")}),
		WithNowFn(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	summary, err := manager.Status(context.Background())
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if summary.DaemonHealth != DaemonHealthError {
		t.Fatalf("expected daemon health error, got=%s (summary=%+v)", summary.DaemonHealth, summary)
	}
	if !strings.Contains(summary.Message, "read status file: state read failed") {
		t.Fatalf("unexpected status message: %q", summary.Message)
	}
}

func TestStatusUsesLastErrorMessageWhenDaemonIsHealthy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	snapshot := state.Snapshot{
		SchemaVersion:   state.SchemaVersionV1,
		Running:         true,
		PID:             os.Getpid(),
		LastHeartbeatAt: now.Add(-1 * time.Second).Format(time.RFC3339Nano),
		LastError:       "last run timeout",
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
	if summary.Message != "last run timeout" {
		t.Fatalf("expected last error fallback message, got=%q", summary.Message)
	}
}

type fakeCommandResponse struct {
	output []byte
	err    error
}

type fakePrefixResponse struct {
	prefix   string
	response fakeCommandResponse
}

type fakeCommandRunner struct {
	calls           []string
	exactResponses  map[string]fakeCommandResponse
	prefixResponses []fakePrefixResponse
}

func (runner *fakeCommandRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name
	if len(args) > 0 {
		call += " " + strings.Join(args, " ")
	}
	runner.calls = append(runner.calls, call)

	if response, ok := runner.exactResponses[call]; ok {
		return response.output, response.err
	}
	for _, entry := range runner.prefixResponses {
		if strings.HasPrefix(call, entry.prefix) {
			return entry.response.output, entry.response.err
		}
	}
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

func (runner *fakeCommandRunner) addPrefixError(prefix string, err error) {
	runner.prefixResponses = append(runner.prefixResponses, fakePrefixResponse{
		prefix:   prefix,
		response: fakeCommandResponse{err: err},
	})
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

func writeInvalidConfig(t *testing.T, home string) {
	t.Helper()

	configPath := filepath.Join(home, ".config", "devmon", "devmon.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	content := `version = 1

[daemon]
max_concurrent_jobs = 0
startup_run = true
log_level = "info"
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}
