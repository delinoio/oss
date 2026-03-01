package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/delinoio/oss/cmds/devmon/internal/config"
	"github.com/delinoio/oss/cmds/devmon/internal/contracts"
	"github.com/delinoio/oss/cmds/devmon/internal/executor"
	"github.com/delinoio/oss/cmds/devmon/internal/logging"
)

type Runner struct {
	config   *config.Config
	logger   *slog.Logger
	executor executor.Executor

	semaphore chan struct{}

	mu         sync.Mutex
	running    map[string]bool
	activeJobs int
	sequence   atomic.Uint64

	runWaitGroup       sync.WaitGroup
	schedulerWaitGroup sync.WaitGroup
}

type scheduledJob struct {
	folder config.FolderConfig
	job    config.JobConfig
}

func NewRunner(cfg *config.Config, logger *slog.Logger, commandExecutor executor.Executor) *Runner {
	return &Runner{
		config:    cfg,
		logger:    logger,
		executor:  commandExecutor,
		semaphore: make(chan struct{}, cfg.Daemon.MaxConcurrentJobs),
		running:   make(map[string]bool),
	}
}

func (runner *Runner) Run(ctx context.Context) error {
	if runner.config == nil {
		return fmt.Errorf("config is nil")
	}
	if runner.executor == nil {
		return fmt.Errorf("executor is nil")
	}

	jobs := runner.flattenJobs()
	if len(jobs) == 0 {
		logging.Event(runner.logger, slog.LevelWarn, "runner_no_jobs")
	}

	for _, item := range jobs {
		if runner.shouldRunOnStartup(item.job) {
			runner.trigger(ctx, item, "startup")
		}
	}

	tickers := make([]*time.Ticker, 0, len(jobs))
	for _, item := range jobs {
		if !item.job.Enabled {
			continue
		}

		ticker := time.NewTicker(item.job.IntervalDuration)
		tickers = append(tickers, ticker)
		runner.schedulerWaitGroup.Add(1)

		go func(currentJob scheduledJob, currentTicker *time.Ticker) {
			defer runner.schedulerWaitGroup.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case <-currentTicker.C:
					runner.trigger(ctx, currentJob, "interval")
				}
			}
		}(item, ticker)
	}

	<-ctx.Done()
	for _, ticker := range tickers {
		ticker.Stop()
	}

	runner.schedulerWaitGroup.Wait()
	runner.runWaitGroup.Wait()
	return nil
}

func (runner *Runner) trigger(ctx context.Context, job scheduledJob, triggerSource string) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	runID := runner.nextRunID()

	if !job.job.Enabled {
		runner.logSkip(job, runID, triggerSource, contracts.DevmonRunOutcomeSkippedDisabled, "disabled", runner.currentActiveJobs())
		return
	}

	jobKey := jobKey(job)

	runner.mu.Lock()
	if runner.running[jobKey] {
		activeJobs := runner.activeJobs
		runner.mu.Unlock()
		runner.logSkip(job, runID, triggerSource, contracts.DevmonRunOutcomeSkippedOverlap, "overlap", activeJobs)
		return
	}

	select {
	case runner.semaphore <- struct{}{}:
		runner.running[jobKey] = true
		runner.activeJobs++
		activeJobs := runner.activeJobs
		runner.mu.Unlock()

		runner.runWaitGroup.Add(1)
		go runner.executeRun(ctx, job, runID, triggerSource, jobKey, activeJobs)
	default:
		activeJobs := runner.activeJobs
		runner.mu.Unlock()
		runner.logSkip(job, runID, triggerSource, contracts.DevmonRunOutcomeSkippedCapacity, "capacity", activeJobs)
	}
}

func (runner *Runner) executeRun(
	ctx context.Context,
	job scheduledJob,
	runID string,
	triggerSource string,
	jobKey string,
	activeJobsAtStart int,
) {
	defer runner.runWaitGroup.Done()

	defer func() {
		<-runner.semaphore
		runner.mu.Lock()
		delete(runner.running, jobKey)
		runner.activeJobs--
		runner.mu.Unlock()
	}()

	logging.Event(
		runner.logger,
		slog.LevelInfo,
		"job_run_started",
		slog.String("folder_id", job.folder.ID),
		slog.String("folder_path", job.folder.Path),
		slog.String("job_id", job.job.ID),
		slog.String("job_type", string(job.job.Type)),
		slog.String("run_id", runID),
		slog.String("outcome", "running"),
		slog.Int64("duration_ms", 0),
		slog.String("interval", job.job.Interval),
		slog.Int64("timeout_ms", job.job.TimeoutDuration.Milliseconds()),
		slog.Int("max_concurrent_jobs", runner.config.Daemon.MaxConcurrentJobs),
		slog.Int("active_jobs", activeJobsAtStart),
		slog.String("trigger", triggerSource),
	)

	runContext := ctx
	cancelRunContext := func() {}
	if job.job.TimeoutDuration > 0 {
		runContext, cancelRunContext = context.WithTimeout(ctx, job.job.TimeoutDuration)
	}
	defer cancelRunContext()

	result := runner.executor.Execute(
		runContext,
		executor.Request{
			FolderID:          job.folder.ID,
			FolderPath:        job.folder.Path,
			JobID:             job.job.ID,
			JobType:           job.job.Type,
			RunID:             runID,
			Shell:             job.job.Shell,
			Script:            job.job.Script,
			Interval:          job.job.IntervalDuration,
			Timeout:           job.job.TimeoutDuration,
			MaxConcurrentJobs: runner.config.Daemon.MaxConcurrentJobs,
		},
	)

	errorText := ""
	if result.Err != nil {
		errorText = result.Err.Error()
	}

	logging.Event(
		runner.logger,
		slog.LevelInfo,
		"job_run_completed",
		slog.String("folder_id", job.folder.ID),
		slog.String("folder_path", job.folder.Path),
		slog.String("job_id", job.job.ID),
		slog.String("job_type", string(job.job.Type)),
		slog.String("run_id", runID),
		slog.String("outcome", string(result.Outcome)),
		slog.Int64("duration_ms", result.Duration.Milliseconds()),
		slog.String("interval", job.job.Interval),
		slog.Int64("timeout_ms", job.job.TimeoutDuration.Milliseconds()),
		slog.Int("exit_code", result.ExitCode),
		slog.String("error", errorText),
		slog.Int("max_concurrent_jobs", runner.config.Daemon.MaxConcurrentJobs),
		slog.Int("active_jobs", runner.currentActiveJobs()),
		slog.String("trigger", triggerSource),
	)
}

func (runner *Runner) logSkip(
	job scheduledJob,
	runID string,
	triggerSource string,
	outcome contracts.DevmonRunOutcome,
	skipReason string,
	activeJobs int,
) {
	logging.Event(
		runner.logger,
		slog.LevelInfo,
		"job_run_skipped",
		slog.String("folder_id", job.folder.ID),
		slog.String("folder_path", job.folder.Path),
		slog.String("job_id", job.job.ID),
		slog.String("job_type", string(job.job.Type)),
		slog.String("run_id", runID),
		slog.String("outcome", string(outcome)),
		slog.Int64("duration_ms", 0),
		slog.String("interval", job.job.Interval),
		slog.Int64("timeout_ms", job.job.TimeoutDuration.Milliseconds()),
		slog.String("skip_reason", skipReason),
		slog.Int("max_concurrent_jobs", runner.config.Daemon.MaxConcurrentJobs),
		slog.Int("active_jobs", activeJobs),
		slog.String("trigger", triggerSource),
	)
}

func (runner *Runner) nextRunID() string {
	sequence := runner.sequence.Add(1)
	return fmt.Sprintf("run-%d-%d", time.Now().UTC().UnixNano(), sequence)
}

func (runner *Runner) shouldRunOnStartup(job config.JobConfig) bool {
	if job.StartupRun != nil {
		return *job.StartupRun
	}
	return runner.config.Daemon.StartupRun
}

func (runner *Runner) currentActiveJobs() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.activeJobs
}

func (runner *Runner) flattenJobs() []scheduledJob {
	jobs := make([]scheduledJob, 0)
	for _, folder := range runner.config.Folders {
		for _, job := range folder.Jobs {
			jobs = append(jobs, scheduledJob{folder: folder, job: job})
		}
	}
	return jobs
}

func jobKey(job scheduledJob) string {
	return fmt.Sprintf("%s/%s", job.folder.ID, job.job.ID)
}
