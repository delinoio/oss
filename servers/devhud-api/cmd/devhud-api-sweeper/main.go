package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/adminassets"
	"github.com/delinoio/oss/servers/devhud-api/internal/cloudflare"
	"github.com/delinoio/oss/servers/devhud-api/internal/config"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/delinoio/oss/servers/devhud-api/internal/idgen"
	"github.com/delinoio/oss/servers/devhud-api/internal/postgres"
	"github.com/delinoio/oss/servers/devhud-api/internal/r2"
	"github.com/delinoio/oss/servers/devhud-api/internal/sweeper"
	uploadmanager "github.com/delinoio/oss/servers/devhud-api/internal/upload"
)

const (
	sweepStartupTimeout   = 15 * time.Second
	sweepIterationTimeout = 25 * time.Second
)

var version = "0.1.0-dev"

type sweepRunner interface {
	RunOnce(context.Context) (sweeper.Result, error)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	// Retain the administrator asset embed in this independently distributed
	// artifact so its SBOM/provenance closure matches the API release input.
	_ = adminassets.Embedded()
	logger.Info("devhud-api-sweeper starting", "version", version)
	if err := run(context.Background(), os.Args[1:], logger); err != nil {
		logger.Error("devhud-api-sweeper stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, logger *slog.Logger) error {
	runOnce := false
	for _, argument := range arguments {
		if argument == "--once" {
			runOnce = true
		} else {
			return errors.New("usage: devhud-api-sweeper [--once]")
		}
	}
	configuration, err := config.LoadSweeper(runOnce)
	if err != nil {
		return err
	}
	stopContext, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	startupContext, cancelStartup := newStartupContext(stopContext)
	pool, err := postgres.NewSweeperPool(startupContext, configuration.DatabaseURL)
	if err != nil {
		cancelStartup()
		return err
	}
	defer pool.Close()
	clock := domain.RealClock{}
	repository := postgres.New(pool, idgen.UUIDv7{}, clock)
	current, err := repository.SchemaCurrent(startupContext)
	if err != nil {
		cancelStartup()
		return err
	}
	if !current {
		cancelStartup()
		return errors.New("database migrations are not current")
	}
	objectStore, err := r2.New(startupContext, r2.Config{
		Endpoint: configuration.R2Endpoint, AccessKeyID: configuration.R2AccessKeyID,
		SecretAccessKey: configuration.R2SecretAccessKey, StagingBucket: configuration.R2StagingBucket,
		PublicBucket: configuration.R2PublicBucket,
	})
	if err != nil {
		cancelStartup()
		return err
	}
	cursorCodec, err := uploadmanager.NewCursorCodec([]byte(configuration.R2SecretAccessKey))
	if err != nil {
		cancelStartup()
		return err
	}
	cdn := cloudflare.New(&http.Client{Timeout: 10 * time.Second}, configuration.CloudflareAPIToken, configuration.CloudflareZoneID, configuration.CloudflareRateRuleID, configuration.PublicAssetBaseURL)
	uploads := uploadmanager.NewService(repository, objectStore, cdn, cursorCodec, clock, logger, configuration.PublicAssetBaseURL, r2.RemovalPNG())
	cancelStartup()
	worker, err := sweeper.New(repository, repository, []domain.AccountPurgeAdapter{uploads}, clock, logger, configuration.BatchSize, sweeper.WithUploadStaging(uploads))
	if err != nil {
		return err
	}
	if configuration.RunOnce {
		return sweep(stopContext, worker, logger)
	}
	ticker := time.NewTicker(configuration.Interval)
	defer ticker.Stop()
	for {
		if err := sweep(stopContext, worker, logger); err != nil {
			logger.ErrorContext(stopContext, "sweep failed", "error", err)
		}
		select {
		case <-stopContext.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func newStartupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, sweepStartupTimeout)
}

func sweep(ctx context.Context, worker sweepRunner, logger *slog.Logger) error {
	iterationContext, cancel := context.WithTimeout(ctx, sweepIterationTimeout)
	defer cancel()
	result, err := worker.RunOnce(iterationContext)
	if err != nil {
		return err
	}
	logger.InfoContext(iterationContext, "sweep completed",
		"lock_acquired", result.LockAcquired,
		"accounts_claimed", result.AccountsClaimed,
		"accounts_purged", result.AccountsPurged,
		"request_logs_deleted", result.RequestLogsDeleted,
		"audit_events_deleted", result.AuditEventsDeleted,
		"crash_reports_deleted", result.CrashReportsDeleted,
		"staging_objects_deleted", result.StagingObjectsDeleted,
		"upload_removals_completed", result.UploadRemovalsCompleted,
	)
	return nil
}
