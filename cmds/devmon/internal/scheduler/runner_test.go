package scheduler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/devmon/internal/config"
	"github.com/delinoio/oss/cmds/devmon/internal/contracts"
	"github.com/delinoio/oss/cmds/devmon/internal/executor"
	"github.com/delinoio/oss/cmds/devmon/internal/logging"
)

type fakeExecutor struct {
	executeFn func(ctx context.Context, request executor.Request) executor.Result

	mu    sync.Mutex
	calls []executor.Request
}

func (fake *fakeExecutor) Execute(ctx context.Context, request executor.Request) executor.Result {
	fake.mu.Lock()
	fake.calls = append(fake.calls, request)
	fake.mu.Unlock()

	if fake.executeFn != nil {
		return fake.executeFn(ctx, request)
	}
	return executor.Result{Outcome: contracts.DevmonRunOutcomeSuccess, ExitCode: 0, Duration: 10 * time.Millisecond}
}

func (fake *fakeExecutor) callCount() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return len(fake.calls)
}

func TestRunnerStartupAndIntervalExecution(t *testing.T) {
	logBuffer := &bytes.Buffer{}
	logger, err := logging.NewWithWriter(logBuffer, "info")
	if err != nil {
		t.Fatalf("NewWithWriter returned error: %v", err)
	}

	fake := &fakeExecutor{}
	cfg := testConfig(t, 4, true, []config.JobConfig{
		testJob("git-sync", true, "20ms", "1s", nil),
	})

	runner := NewRunner(cfg, logger, fake)
	runContext, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runner.Run(runContext)
	}()

	waitForCondition(t, time.Second, func() bool {
		return fake.callCount() >= 2
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}
}

func TestRunnerOverlapSkip(t *testing.T) {
	logBuffer := &bytes.Buffer{}
	logger, err := logging.NewWithWriter(logBuffer, "info")
	if err != nil {
		t.Fatalf("NewWithWriter returned error: %v", err)
	}

	fake := &fakeExecutor{
		executeFn: func(ctx context.Context, _ executor.Request) executor.Result {
			select {
			case <-time.After(150 * time.Millisecond):
				return executor.Result{Outcome: contracts.DevmonRunOutcomeSuccess, ExitCode: 0, Duration: 150 * time.Millisecond}
			case <-ctx.Done():
				return executor.Result{Outcome: contracts.DevmonRunOutcomeFailed, ExitCode: -1, Duration: 10 * time.Millisecond, Err: ctx.Err()}
			}
		},
	}
	cfg := testConfig(t, 4, true, []config.JobConfig{
		testJob("git-sync", true, "20ms", "1s", nil),
	})

	runner := NewRunner(cfg, logger, fake)
	runContext, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runner.Run(runContext)
	}()

	time.Sleep(220 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}

	logs := logBuffer.String()
	if !strings.Contains(logs, `"skip_reason":"overlap"`) {
		t.Fatalf("expected overlap skip log, got=%s", logs)
	}
	if !strings.Contains(logs, `"outcome":"skipped-overlap"`) {
		t.Fatalf("expected skipped-overlap outcome, got=%s", logs)
	}
}

func TestRunnerCapacitySkip(t *testing.T) {
	logBuffer := &bytes.Buffer{}
	logger, err := logging.NewWithWriter(logBuffer, "info")
	if err != nil {
		t.Fatalf("NewWithWriter returned error: %v", err)
	}

	fake := &fakeExecutor{
		executeFn: func(ctx context.Context, _ executor.Request) executor.Result {
			<-ctx.Done()
			return executor.Result{Outcome: contracts.DevmonRunOutcomeFailed, ExitCode: -1, Duration: 10 * time.Millisecond, Err: ctx.Err()}
		},
	}
	cfg := testConfig(t, 1, true, []config.JobConfig{
		testJob("job-a", true, "30s", "1s", boolPtr(true)),
		testJob("job-b", true, "30s", "1s", boolPtr(true)),
	})

	runner := NewRunner(cfg, logger, fake)
	runContext, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runner.Run(runContext)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}

	logs := logBuffer.String()
	if !strings.Contains(logs, `"skip_reason":"capacity"`) {
		t.Fatalf("expected capacity skip log, got=%s", logs)
	}
	if !strings.Contains(logs, `"outcome":"skipped-capacity"`) {
		t.Fatalf("expected skipped-capacity outcome, got=%s", logs)
	}
}

func TestRunnerTimeoutOutcome(t *testing.T) {
	logBuffer := &bytes.Buffer{}
	logger, err := logging.NewWithWriter(logBuffer, "info")
	if err != nil {
		t.Fatalf("NewWithWriter returned error: %v", err)
	}

	fake := &fakeExecutor{
		executeFn: func(ctx context.Context, _ executor.Request) executor.Result {
			<-ctx.Done()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return executor.Result{Outcome: contracts.DevmonRunOutcomeTimeout, ExitCode: -1, Duration: 50 * time.Millisecond, Err: ctx.Err()}
			}
			return executor.Result{Outcome: contracts.DevmonRunOutcomeFailed, ExitCode: -1, Duration: 50 * time.Millisecond, Err: ctx.Err()}
		},
	}
	cfg := testConfig(t, 2, true, []config.JobConfig{
		testJob("timeout-job", true, "1h", "40ms", boolPtr(true)),
	})

	runner := NewRunner(cfg, logger, fake)
	runContext, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runner.Run(runContext)
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}

	if !strings.Contains(logBuffer.String(), `"outcome":"timeout"`) {
		t.Fatalf("expected timeout outcome log, got=%s", logBuffer.String())
	}
}

func TestRunnerStopsAfterContextCancel(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))

	started := make(chan struct{}, 1)
	fake := &fakeExecutor{
		executeFn: func(ctx context.Context, _ executor.Request) executor.Result {
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return executor.Result{Outcome: contracts.DevmonRunOutcomeFailed, ExitCode: -1, Duration: 10 * time.Millisecond, Err: ctx.Err()}
		},
	}
	cfg := testConfig(t, 2, true, []config.JobConfig{
		testJob("blocking-job", true, "1h", "1h", boolPtr(true)),
	})

	runner := NewRunner(cfg, logger, fake)
	runContext, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runner.Run(runContext)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected job to start")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not stop after context cancellation")
	}
}

func testConfig(t *testing.T, maxConcurrentJobs int, startupRun bool, jobs []config.JobConfig) *config.Config {
	t.Helper()
	return &config.Config{
		Version: config.ConfigVersionV1,
		Daemon: config.DaemonConfig{
			MaxConcurrentJobs: maxConcurrentJobs,
			StartupRun:        startupRun,
			LogLevel:          "info",
		},
		Folders: []config.FolderConfig{{
			ID:   "workspace-a",
			Path: t.TempDir(),
			Jobs: jobs,
		}},
	}
}

func testJob(id string, enabled bool, interval string, timeout string, startupRun *bool) config.JobConfig {
	intervalDuration, _ := time.ParseDuration(interval)
	timeoutDuration, _ := time.ParseDuration(timeout)
	return config.JobConfig{
		ID:               id,
		Type:             contracts.DevmonJobTypeShellCommand,
		Enabled:          enabled,
		Interval:         interval,
		Timeout:          timeout,
		Shell:            "sh",
		Script:           "echo test",
		StartupRun:       startupRun,
		IntervalDuration: intervalDuration,
		TimeoutDuration:  timeoutDuration,
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
