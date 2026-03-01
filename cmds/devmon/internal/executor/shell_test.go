package executor

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/devmon/internal/contracts"
	"github.com/delinoio/oss/cmds/devmon/internal/logging"
)

func TestShellExecutorSuccessAndOutputLogging(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not available in PATH")
	}

	logBuffer := &bytes.Buffer{}
	logger, err := logging.NewWithWriter(logBuffer, "info")
	if err != nil {
		t.Fatalf("NewWithWriter returned error: %v", err)
	}

	shellExecutor := NewShellExecutor(logger)
	result := shellExecutor.Execute(context.Background(), Request{
		FolderID:   "workspace-a",
		FolderPath: t.TempDir(),
		JobID:      "git-sync",
		JobType:    contracts.DevmonJobTypeShellCommand,
		RunID:      "run-1",
		Shell:      "sh",
		Script:     "printf 'hello-out\\n'; printf 'hello-err\\n' >&2",
		Interval:   time.Minute,
		Timeout:    30 * time.Second,
	})
	if result.Outcome != contracts.DevmonRunOutcomeSuccess {
		t.Fatalf("unexpected outcome: %s (err=%v)", result.Outcome, result.Err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}

	logs := logBuffer.String()
	if !strings.Contains(logs, `"event":"job_output"`) {
		t.Fatalf("expected job_output event in logs, got=%s", logs)
	}
	if !strings.Contains(logs, `"stream":"stdout"`) {
		t.Fatalf("expected stdout stream log, got=%s", logs)
	}
	if !strings.Contains(logs, `"stream":"stderr"`) {
		t.Fatalf("expected stderr stream log, got=%s", logs)
	}
}

func TestShellExecutorFailure(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not available in PATH")
	}

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	shellExecutor := NewShellExecutor(logger)

	result := shellExecutor.Execute(context.Background(), Request{
		FolderID:   "workspace-a",
		FolderPath: t.TempDir(),
		JobID:      "failing-job",
		JobType:    contracts.DevmonJobTypeShellCommand,
		RunID:      "run-1",
		Shell:      "sh",
		Script:     "exit 7",
		Interval:   time.Minute,
		Timeout:    30 * time.Second,
	})
	if result.Outcome != contracts.DevmonRunOutcomeFailed {
		t.Fatalf("unexpected outcome: %s", result.Outcome)
	}
	if result.ExitCode != 7 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}
}

func TestShellExecutorTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell timeout behavior differs on windows test environments")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not available in PATH")
	}

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	shellExecutor := NewShellExecutor(logger)

	timeoutContext, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result := shellExecutor.Execute(timeoutContext, Request{
		FolderID:   "workspace-a",
		FolderPath: filepath.Clean(t.TempDir()),
		JobID:      "timeout-job",
		JobType:    contracts.DevmonJobTypeShellCommand,
		RunID:      "run-1",
		Shell:      "sh",
		Script:     "while true; do :; done",
		Interval:   time.Minute,
		Timeout:    30 * time.Second,
	})
	if result.Outcome != contracts.DevmonRunOutcomeTimeout {
		t.Fatalf("expected timeout outcome, got=%s (err=%v)", result.Outcome, result.Err)
	}
}
