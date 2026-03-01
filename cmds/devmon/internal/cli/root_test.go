package cli

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/devmon/internal/servicecontrol"
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

type fakeServiceManager struct {
	statusSummary servicecontrol.Summary
	statusErr     error
}

func (manager *fakeServiceManager) Install(_ context.Context) error {
	return nil
}

func (manager *fakeServiceManager) Uninstall(_ context.Context) error {
	return nil
}

func (manager *fakeServiceManager) Start(_ context.Context) error {
	return nil
}

func (manager *fakeServiceManager) Stop(_ context.Context) error {
	return nil
}

func (manager *fakeServiceManager) Status(_ context.Context) (servicecontrol.Summary, error) {
	return manager.statusSummary, manager.statusErr
}
