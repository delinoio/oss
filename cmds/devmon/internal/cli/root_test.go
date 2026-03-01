package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
