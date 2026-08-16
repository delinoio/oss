package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/config"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/delinoio/oss/servers/devhud-api/internal/idgen"
	"github.com/delinoio/oss/servers/devhud-api/internal/postgres"
	"github.com/delinoio/oss/servers/devhud-api/internal/sweeper"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
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
	pool, err := postgres.NewSweeperPool(ctx, configuration.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	clock := domain.RealClock{}
	repository := postgres.New(pool, idgen.UUIDv7{}, clock)
	current, err := repository.SchemaCurrent(ctx)
	if err != nil {
		return err
	}
	if !current {
		return errors.New("database migrations are not current")
	}
	worker, err := sweeper.New(repository, repository, nil, clock, logger, configuration.BatchSize)
	if err != nil {
		return err
	}
	if configuration.RunOnce {
		return sweep(ctx, worker, logger)
	}
	stopContext, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
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

func sweep(ctx context.Context, worker *sweeper.Sweeper, logger *slog.Logger) error {
	result, err := worker.RunOnce(ctx)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "sweep completed",
		"lock_acquired", result.LockAcquired,
		"accounts_claimed", result.AccountsClaimed,
		"accounts_purged", result.AccountsPurged,
		"request_logs_deleted", result.RequestLogsDeleted,
		"audit_events_deleted", result.AuditEventsDeleted,
	)
	return nil
}
