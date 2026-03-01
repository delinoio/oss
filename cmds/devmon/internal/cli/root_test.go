package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/devmon/internal/config"
	"github.com/delinoio/oss/cmds/devmon/internal/contracts"
	"github.com/delinoio/oss/cmds/devmon/internal/executor"
	"github.com/delinoio/oss/cmds/devmon/internal/servicecontrol"
	"github.com/delinoio/oss/cmds/devmon/internal/state"
)

func TestExecuteRequiresCommand(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2, got=%d", code)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("expected usage output, got=%s", stderr.String())
	}
}

func TestExecuteUnknownCommand(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"unknown"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2, got=%d", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("expected unknown command error, got=%s", stderr.String())
	}
}

func TestExecuteValidateSuccess(t *testing.T) {
	folderPath := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "devmon.toml")
	content := fmt.Sprintf(`version = 1

[daemon]
max_concurrent_jobs = 2
startup_run = true
log_level = "info"

[[folder]]
id = "workspace-a"
path = %q

[[folder.job]]
id = "git-sync"
interval = "1m"
timeout = "50s"
script = "echo ok"
`, folderPath)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := execute([]string{"validate", "--config", configPath}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "config is valid") {
		t.Fatalf("expected success message, got=%s", stdout.String())
	}
}

func TestExecuteValidateInvalidConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "devmon.toml")
	content := `version = 1

[daemon]
max_concurrent_jobs = 0
startup_run = true
log_level = "info"
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := execute([]string{"validate", "--config", configPath}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2, got=%d", code)
	}
	if !strings.Contains(stderr.String(), "validate config") {
		t.Fatalf("expected validate error message, got=%s", stderr.String())
	}
}

func TestExecuteServiceStatusSuccess(t *testing.T) {
	originalFactory := newServiceManager
	t.Cleanup(func() {
		newServiceManager = originalFactory
	})

	newServiceManager = func(_ *slog.Logger, _ ...servicecontrol.Option) (servicecontrol.Manager, error) {
		return &fakeServiceManager{
			statusSummary: servicecontrol.Summary{
				Domain:       "gui/501",
				DaemonHealth: servicecontrol.DaemonHealthRunning,
			},
		}, nil
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"service", "status"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"daemon_health": "running"`) {
		t.Fatalf("expected daemon health in output, got=%s", stdout.String())
	}
}

func TestExecuteServiceLifecycleActionsSuccess(t *testing.T) {
	testCases := []struct {
		name          string
		action        string
		expectedCalls func(manager *fakeServiceManager) bool
	}{
		{
			name:   "install",
			action: "install",
			expectedCalls: func(manager *fakeServiceManager) bool {
				return manager.installCalls == 1 &&
					manager.uninstallCalls == 0 &&
					manager.startCalls == 0 &&
					manager.stopCalls == 0 &&
					manager.statusCalls == 0
			},
		},
		{
			name:   "uninstall",
			action: "uninstall",
			expectedCalls: func(manager *fakeServiceManager) bool {
				return manager.installCalls == 0 &&
					manager.uninstallCalls == 1 &&
					manager.startCalls == 0 &&
					manager.stopCalls == 0 &&
					manager.statusCalls == 0
			},
		},
		{
			name:   "start",
			action: "start",
			expectedCalls: func(manager *fakeServiceManager) bool {
				return manager.installCalls == 0 &&
					manager.uninstallCalls == 0 &&
					manager.startCalls == 1 &&
					manager.stopCalls == 0 &&
					manager.statusCalls == 0
			},
		},
		{
			name:   "stop",
			action: "stop",
			expectedCalls: func(manager *fakeServiceManager) bool {
				return manager.installCalls == 0 &&
					manager.uninstallCalls == 0 &&
					manager.startCalls == 0 &&
					manager.stopCalls == 1 &&
					manager.statusCalls == 0
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			manager := &fakeServiceManager{}
			originalFactory := newServiceManager
			t.Cleanup(func() {
				newServiceManager = originalFactory
			})

			newServiceManager = func(_ *slog.Logger, _ ...servicecontrol.Option) (servicecontrol.Manager, error) {
				return manager, nil
			}

			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			code := execute([]string{"service", tc.action}, stdout, stderr)
			if code != 0 {
				t.Fatalf("expected exit code 0, got=%d stderr=%s", code, stderr.String())
			}

			if !tc.expectedCalls(manager) {
				t.Fatalf("unexpected method dispatch for action=%s: manager=%+v", tc.action, manager)
			}
		})
	}
}

func TestExecuteServiceActionFailure(t *testing.T) {
	testCases := []struct {
		name        string
		action      string
		configure   func(manager *fakeServiceManager)
		expectedErr string
	}{
		{
			name:   "install",
			action: "install",
			configure: func(manager *fakeServiceManager) {
				manager.installErr = errors.New("install failed")
			},
			expectedErr: "service install: install failed",
		},
		{
			name:   "uninstall",
			action: "uninstall",
			configure: func(manager *fakeServiceManager) {
				manager.uninstallErr = errors.New("uninstall failed")
			},
			expectedErr: "service uninstall: uninstall failed",
		},
		{
			name:   "start",
			action: "start",
			configure: func(manager *fakeServiceManager) {
				manager.startErr = errors.New("start failed")
			},
			expectedErr: "service start: start failed",
		},
		{
			name:   "stop",
			action: "stop",
			configure: func(manager *fakeServiceManager) {
				manager.stopErr = errors.New("stop failed")
			},
			expectedErr: "service stop: stop failed",
		},
		{
			name:   "status",
			action: "status",
			configure: func(manager *fakeServiceManager) {
				manager.statusErr = errors.New("status failed")
			},
			expectedErr: "service status: status failed",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			manager := &fakeServiceManager{
				statusSummary: servicecontrol.Summary{
					DaemonHealth: servicecontrol.DaemonHealthRunning,
				},
			}
			tc.configure(manager)

			originalFactory := newServiceManager
			t.Cleanup(func() {
				newServiceManager = originalFactory
			})

			newServiceManager = func(_ *slog.Logger, _ ...servicecontrol.Option) (servicecontrol.Manager, error) {
				return manager, nil
			}

			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			code := execute([]string{"service", tc.action}, stdout, stderr)
			if code != 1 {
				t.Fatalf("expected exit code 1, got=%d stderr=%s", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.expectedErr) {
				t.Fatalf("expected error %q, got=%s", tc.expectedErr, stderr.String())
			}
		})
	}
}

func TestExecuteServiceManagerInitFailure(t *testing.T) {
	originalFactory := newServiceManager
	t.Cleanup(func() {
		newServiceManager = originalFactory
	})

	newServiceManager = func(_ *slog.Logger, _ ...servicecontrol.Option) (servicecontrol.Manager, error) {
		return nil, errors.New("factory failed")
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := execute([]string{"service", "status"}, stdout, stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1, got=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "init service manager: factory failed") {
		t.Fatalf("expected manager init error, got=%s", stderr.String())
	}
}

func TestExecuteServiceUnknownAction(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"service", "unknown"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2, got=%d", code)
	}
	if !strings.Contains(stderr.String(), "unknown service action") {
		t.Fatalf("expected unknown service action output, got=%s", stderr.String())
	}
}

func TestExecuteServiceRejectsExtraArguments(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"service", "status", "extra"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2, got=%d", code)
	}
	if !strings.Contains(stderr.String(), "exactly one action") {
		t.Fatalf("expected extra argument error, got=%s", stderr.String())
	}
}

func TestExecuteMenubarWithArguments(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"menubar", "--unexpected"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2, got=%d", code)
	}
	if !strings.Contains(stderr.String(), "menubar does not accept arguments") {
		t.Fatalf("expected menubar argument error, got=%s", stderr.String())
	}
}

func TestExecuteMenubarRuns(t *testing.T) {
	originalRunMenubar := runMenubar
	t.Cleanup(func() {
		runMenubar = originalRunMenubar
	})

	ran := false
	runMenubar = func(_ context.Context, _ *slog.Logger) error {
		ran = true
		return nil
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"menubar"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got=%d stderr=%s", code, stderr.String())
	}
	if !ran {
		t.Fatal("expected menubar runner to be called")
	}
}

func TestExecuteMenubarRunFailure(t *testing.T) {
	originalRunMenubar := runMenubar
	t.Cleanup(func() {
		runMenubar = originalRunMenubar
	})

	runMenubar = func(_ context.Context, _ *slog.Logger) error {
		return errors.New("menubar failure")
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"menubar"}, stdout, stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1, got=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "run menubar: menubar failure") {
		t.Fatalf("expected menubar run error, got=%s", stderr.String())
	}
}

func TestExecuteDaemonGracefulShutdownWithInjectedSignalContext(t *testing.T) {
	originalNewSignalNotifyContext := newSignalNotifyContext
	originalNewDaemonRunner := newDaemonRunner
	originalNewStateStore := newStateStore
	t.Cleanup(func() {
		newSignalNotifyContext = originalNewSignalNotifyContext
		newDaemonRunner = originalNewDaemonRunner
		newStateStore = originalNewStateStore
	})

	workspacePath := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "devmon.toml")
	writeValidDaemonConfig(t, configPath, workspacePath)
	statusPath := filepath.Join(t.TempDir(), "status.json")

	fakeRunner := &fakeDaemonRunner{started: make(chan struct{}, 1)}
	newDaemonRunner = func(_ *config.Config, _ *slog.Logger, _ executor.Executor) daemonRunner {
		return fakeRunner
	}
	newStateStore = func(_ string, logger *slog.Logger) (*state.Store, error) {
		return state.NewStore(statusPath, logger)
	}

	var cancelRun context.CancelFunc
	newSignalNotifyContext = func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancelRun = cancel
		return ctx, cancel
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	done := make(chan int, 1)
	go func() {
		done <- execute([]string{"daemon", "--config", configPath}, stdout, stderr)
	}()

	select {
	case <-fakeRunner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("expected fake daemon runner to start")
	}

	if cancelRun == nil {
		t.Fatal("expected signal cancel function to be captured")
	}
	cancelRun()

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("expected exit code 0, got=%d stderr=%s", code, stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("daemon command did not stop after cancel")
	}

	if fakeRunner.runCalls != 1 {
		t.Fatalf("expected runCalls=1, got=%d", fakeRunner.runCalls)
	}
	if fakeRunner.stateStore == nil {
		t.Fatal("expected state store to be injected into daemon runner")
	}

	stateStore, err := state.NewStore(statusPath, slog.Default())
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	snapshot, err := stateStore.Read()
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if snapshot.Running {
		t.Fatalf("expected daemon running=false after shutdown, got snapshot=%+v", snapshot)
	}
	if snapshot.PID != 0 {
		t.Fatalf("expected pid=0 after shutdown, got=%d", snapshot.PID)
	}
}

type fakeServiceManager struct {
	installCalls   int
	uninstallCalls int
	startCalls     int
	stopCalls      int
	statusCalls    int

	installErr   error
	uninstallErr error
	startErr     error
	stopErr      error

	statusSummary servicecontrol.Summary
	statusErr     error
}

func (manager *fakeServiceManager) Install(_ context.Context) error {
	manager.installCalls++
	return manager.installErr
}

func (manager *fakeServiceManager) Uninstall(_ context.Context) error {
	manager.uninstallCalls++
	return manager.uninstallErr
}

func (manager *fakeServiceManager) Start(_ context.Context) error {
	manager.startCalls++
	return manager.startErr
}

func (manager *fakeServiceManager) Stop(_ context.Context) error {
	manager.stopCalls++
	return manager.stopErr
}

func (manager *fakeServiceManager) Status(_ context.Context) (servicecontrol.Summary, error) {
	manager.statusCalls++
	return manager.statusSummary, manager.statusErr
}

type fakeDaemonRunner struct {
	started    chan struct{}
	stateStore *state.Store
	runCalls   int
}

func (runner *fakeDaemonRunner) Run(ctx context.Context) error {
	runner.runCalls++
	select {
	case runner.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil
}

func (runner *fakeDaemonRunner) SetStateStore(stateStore *state.Store) {
	runner.stateStore = stateStore
}

func (runner *fakeDaemonRunner) ActiveJobs() int {
	return 0
}

func writeValidDaemonConfig(t *testing.T, configPath string, folderPath string) {
	t.Helper()

	content := fmt.Sprintf(`version = 1

[daemon]
max_concurrent_jobs = 1
startup_run = false
log_level = "info"

[[folder]]
id = "workspace-a"
path = %q

[[folder.job]]
id = "heartbeat-job"
type = %q
enabled = true
interval = "1h"
timeout = "10s"
script = "echo ok"
`, folderPath, contracts.DevmonJobTypeShellCommand)

	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}
