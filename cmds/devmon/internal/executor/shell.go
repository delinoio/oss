package executor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/devmon/internal/contracts"
	"github.com/delinoio/oss/cmds/devmon/internal/logging"
)

const (
	defaultScannerBufferSize = 64 * 1024
	maxScannerBufferSize     = 1024 * 1024
)

type Request struct {
	FolderID          string
	FolderPath        string
	JobID             string
	JobType           contracts.DevmonJobType
	RunID             string
	Shell             string
	Script            string
	Interval          time.Duration
	Timeout           time.Duration
	MaxConcurrentJobs int
}

type Result struct {
	Outcome  contracts.DevmonRunOutcome
	ExitCode int
	Duration time.Duration
	Err      error
}

type Executor interface {
	Execute(ctx context.Context, request Request) Result
}

type ShellExecutor struct {
	logger *slog.Logger
	now    func() time.Time
}

func NewShellExecutor(logger *slog.Logger) *ShellExecutor {
	return &ShellExecutor{
		logger: logger,
		now:    time.Now,
	}
}

func (executor *ShellExecutor) Execute(ctx context.Context, request Request) Result {
	startTime := executor.now()
	result := Result{ExitCode: -1}

	command := exec.CommandContext(ctx, request.Shell, "-c", request.Script)
	command.Dir = request.FolderPath

	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		result.Outcome = contracts.DevmonRunOutcomeFailed
		result.Err = fmt.Errorf("stdout pipe: %w", err)
		result.Duration = executor.now().Sub(startTime)
		return result
	}

	stderrPipe, err := command.StderrPipe()
	if err != nil {
		result.Outcome = contracts.DevmonRunOutcomeFailed
		result.Err = fmt.Errorf("stderr pipe: %w", err)
		result.Duration = executor.now().Sub(startTime)
		return result
	}

	if err := command.Start(); err != nil {
		result.Outcome = contracts.DevmonRunOutcomeFailed
		result.Err = fmt.Errorf("start command: %w", err)
		result.Duration = executor.now().Sub(startTime)
		return result
	}

	var streamWaitGroup sync.WaitGroup
	streamWaitGroup.Add(2)
	go func() {
		defer streamWaitGroup.Done()
		executor.logStreamLines(request, "stdout", stdoutPipe)
	}()
	go func() {
		defer streamWaitGroup.Done()
		executor.logStreamLines(request, "stderr", stderrPipe)
	}()

	// StdoutPipe/StderrPipe contract requires reads to complete before Wait.
	// Waiting for stream readers first avoids intermittent "file already closed" errors.
	streamWaitGroup.Wait()
	waitErr := command.Wait()

	result.Duration = executor.now().Sub(startTime)
	result.ExitCode = resolveExitCode(waitErr)

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.Outcome = contracts.DevmonRunOutcomeTimeout
		result.Err = context.DeadlineExceeded
		if result.ExitCode == 0 {
			result.ExitCode = -1
		}
		return result
	}

	if waitErr != nil {
		result.Outcome = contracts.DevmonRunOutcomeFailed
		result.Err = waitErr
		return result
	}

	result.Outcome = contracts.DevmonRunOutcomeSuccess
	return result
}

func (executor *ShellExecutor) logStreamLines(request Request, stream string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, defaultScannerBufferSize), maxScannerBufferSize)

	for scanner.Scan() {
		logging.Event(
			executor.logger,
			slog.LevelInfo,
			"job_output",
			slog.String("folder_id", request.FolderID),
			slog.String("folder_path", request.FolderPath),
			slog.String("job_id", request.JobID),
			slog.String("job_type", string(request.JobType)),
			slog.String("run_id", request.RunID),
			slog.String("stream", stream),
			slog.String("line", scanner.Text()),
		)
	}

	if err := scanner.Err(); err != nil {
		logging.Event(
			executor.logger,
			slog.LevelWarn,
			"job_output_stream_error",
			slog.String("folder_id", request.FolderID),
			slog.String("folder_path", request.FolderPath),
			slog.String("job_id", request.JobID),
			slog.String("job_type", string(request.JobType)),
			slog.String("run_id", request.RunID),
			slog.String("stream", stream),
			slog.String("error", err.Error()),
		)
	}
}

func resolveExitCode(waitErr error) int {
	if waitErr == nil {
		return 0
	}

	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
