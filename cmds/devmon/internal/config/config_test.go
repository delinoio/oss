package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadValidConfig(t *testing.T) {
	folderPath := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "devmon.toml")

	content := fmt.Sprintf(`version = 1

[daemon]
max_concurrent_jobs = 2
startup_run = true
log_level = "debug"

[[folder]]
id = "workspace-a"
path = %q

[[folder.job]]
id = "git-sync"
type = "shell-command"
interval = "1m"
timeout = "50s"
shell = "sh"
script = '''
git fetch --prune origin
echo done
'''
startup_run = false
`, folderPath)

	writeConfigFile(t, configPath, content)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Version != ConfigVersionV1 {
		t.Fatalf("unexpected version: %d", cfg.Version)
	}
	if cfg.Daemon.MaxConcurrentJobs != 2 {
		t.Fatalf("unexpected max_concurrent_jobs: %d", cfg.Daemon.MaxConcurrentJobs)
	}
	if cfg.Daemon.LogLevel != "debug" {
		t.Fatalf("unexpected log level: %s", cfg.Daemon.LogLevel)
	}

	if len(cfg.Folders) != 1 {
		t.Fatalf("unexpected folder count: %d", len(cfg.Folders))
	}
	folder := cfg.Folders[0]
	if folder.ID != "workspace-a" {
		t.Fatalf("unexpected folder id: %s", folder.ID)
	}
	if !filepath.IsAbs(folder.Path) {
		t.Fatalf("folder path should be absolute after load: %s", folder.Path)
	}
	if len(folder.Jobs) != 1 {
		t.Fatalf("unexpected job count: %d", len(folder.Jobs))
	}

	job := folder.Jobs[0]
	if job.ID != "git-sync" {
		t.Fatalf("unexpected job id: %s", job.ID)
	}
	if job.IntervalDuration.String() != "1m0s" {
		t.Fatalf("unexpected interval duration: %s", job.IntervalDuration)
	}
	if job.TimeoutDuration.String() != "50s" {
		t.Fatalf("unexpected timeout duration: %s", job.TimeoutDuration)
	}
	if job.StartupRun == nil || *job.StartupRun {
		t.Fatal("expected startup_run override to false")
	}
	if !strings.Contains(job.Script, "git fetch --prune origin") {
		t.Fatalf("expected script payload to be parsed, got: %q", job.Script)
	}
}

func TestLoadAppliesJobDefaults(t *testing.T) {
	folderPath := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "devmon.toml")

	content := fmt.Sprintf(`version = 1

[daemon]
max_concurrent_jobs = 1
startup_run = true
log_level = "info"

[[folder]]
id = "workspace-a"
path = %q

[[folder.job]]
id = "cargo-clean"
interval = "6h"
timeout = "5m"
script = "cargo clean"
`, folderPath)

	writeConfigFile(t, configPath, content)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	job := cfg.Folders[0].Jobs[0]
	if !job.Enabled {
		t.Fatal("expected enabled to default to true")
	}
	if job.Shell != "sh" {
		t.Fatalf("expected shell default to sh, got: %s", job.Shell)
	}
	if job.StartupRun != nil {
		t.Fatal("expected startup_run to be nil when omitted")
	}
}

func TestLoadRejectsInvalidInterval(t *testing.T) {
	folderPath := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "devmon.toml")

	content := fmt.Sprintf(`version = 1

[daemon]
max_concurrent_jobs = 1
startup_run = true
log_level = "info"

[[folder]]
id = "workspace-a"
path = %q

[[folder.job]]
id = "git-sync"
interval = "not-a-duration"
timeout = "50s"
script = "echo ok"
`, folderPath)
	writeConfigFile(t, configPath, content)

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected invalid interval error")
	}
	if !strings.Contains(err.Error(), "interval parse failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsMissingFolderPath(t *testing.T) {
	missingFolder := filepath.Join(t.TempDir(), "missing")
	configPath := filepath.Join(t.TempDir(), "devmon.toml")

	content := fmt.Sprintf(`version = 1

[daemon]
max_concurrent_jobs = 1
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
`, missingFolder)
	writeConfigFile(t, configPath, content)

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected missing folder path error")
	}
	if !strings.Contains(err.Error(), "path is invalid") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsDuplicateJobID(t *testing.T) {
	folderPath := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "devmon.toml")

	content := fmt.Sprintf(`version = 1

[daemon]
max_concurrent_jobs = 1
startup_run = true
log_level = "info"

[[folder]]
id = "workspace-a"
path = %q

[[folder.job]]
id = "git-sync"
interval = "1m"
timeout = "50s"
script = "echo first"

[[folder.job]]
id = "git-sync"
interval = "1m"
timeout = "50s"
script = "echo second"
`, folderPath)
	writeConfigFile(t, configPath, content)

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected duplicate job id error")
	}
	if !strings.Contains(err.Error(), "duplicate job id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsUnsupportedJobType(t *testing.T) {
	folderPath := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "devmon.toml")

	content := fmt.Sprintf(`version = 1

[daemon]
max_concurrent_jobs = 1
startup_run = true
log_level = "info"

[[folder]]
id = "workspace-a"
path = %q

[[folder.job]]
id = "git-sync"
type = "git-sync"
interval = "1m"
timeout = "50s"
script = "echo ok"
`, folderPath)
	writeConfigFile(t, configPath, content)

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected unsupported job type error")
	}
	if !strings.Contains(err.Error(), "type must be \"shell-command\"") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func writeConfigFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}
