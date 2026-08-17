package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/auth"
	"github.com/delinoio/oss/servers/devhud-api/internal/cloudflare"
	"github.com/delinoio/oss/servers/devhud-api/internal/config"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/delinoio/oss/servers/devhud-api/internal/idgen"
	"github.com/delinoio/oss/servers/devhud-api/internal/postgres"
	"github.com/delinoio/oss/servers/devhud-api/internal/r2"
	"github.com/delinoio/oss/servers/devhud-api/internal/server"
	"github.com/delinoio/oss/servers/devhud-api/internal/telemetry"
	uploadmanager "github.com/delinoio/oss/servers/devhud-api/internal/upload"
)

var version = "0.1.0-dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(context.Background(), os.Args[1:], logger); err != nil {
		logger.Error("devhud-api stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, logger *slog.Logger) error {
	if len(arguments) > 0 && arguments[0] == "migrate" {
		return runMigrations(ctx)
	}
	if len(arguments) > 0 && arguments[0] != "serve" {
		return errors.New("usage: devhud-api [serve|migrate]")
	}
	configuration, err := config.Load(version)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", configuration.ListenAddress)
	if err != nil {
		return err
	}
	defer listener.Close()

	startupContext, cancelStartup := context.WithTimeout(ctx, 15*time.Second)
	defer cancelStartup()
	pool, err := postgres.NewPool(startupContext, configuration.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	clock := domain.RealClock{}
	ids := idgen.UUIDv7{}
	repository := postgres.New(pool, ids, clock)
	current, err := repository.SchemaCurrent(startupContext)
	if err != nil {
		return err
	}
	if !current {
		return errors.New("database migrations are not current; run devhud-api migrate")
	}
	verifier, err := auth.NewLogtoVerifier(startupContext, configuration.LogtoIssuer, configuration.LogtoAudience, configuration.IdentityHMACKeys)
	if err != nil {
		return err
	}
	providers, err := telemetry.New(startupContext, "devhud-api", configuration.APIVersion)
	if err != nil {
		return err
	}
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), configuration.ShutdownTimeout)
		defer cancel()
		if err := providers.Shutdown(shutdownContext); err != nil {
			logger.Warn("telemetry shutdown failed", "error", err)
		}
	}()
	var uploads *uploadmanager.Service
	if configuration.R2Endpoint != "" {
		objectStore, err := r2.New(startupContext, r2.Config{
			Endpoint: configuration.R2Endpoint, AccessKeyID: configuration.R2AccessKeyID,
			SecretAccessKey: configuration.R2SecretAccessKey, StagingBucket: configuration.R2StagingBucket,
			PublicBucket: configuration.R2PublicBucket,
		})
		if err != nil {
			return err
		}
		cdn := cloudflare.New(&http.Client{Timeout: 10 * time.Second}, configuration.CloudflareAPIToken, configuration.CloudflareZoneID, configuration.CloudflareRateRuleID, configuration.PublicAssetBaseURL)
		if configuration.Environment == config.EnvironmentProduction {
			if err := objectStore.ValidateCORS(startupContext); err != nil {
				return err
			}
			if err := cdn.ValidatePublicRateLimit(startupContext); err != nil {
				return err
			}
		}
		cursorCodec, err := uploadmanager.NewCursorCodec(configuration.IdentityHMACKeys[0])
		if err != nil {
			return err
		}
		uploads = uploadmanager.NewService(repository, objectStore, cdn, cursorCodec, clock, logger, configuration.PublicAssetBaseURL, r2.RemovalPNG())
	}

	httpServer, err := server.New(server.Dependencies{
		Config: configuration, Repository: repository, Verifier: verifier, Clock: clock,
		IDs: ids, Logger: logger, MetricsHandler: providers.MetricsHandler, Uploads: uploads,
	})
	if err != nil {
		return err
	}
	stopContext, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("devhud-api listening", "address", configuration.ListenAddress)
	return serveUntilStopped(stopContext, httpServer, listener, configuration.ShutdownTimeout, logger)
}

func serveUntilStopped(ctx context.Context, httpServer *http.Server, listener net.Listener, shutdownTimeout time.Duration, logger *slog.Logger) error {
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- httpServer.Serve(listener)
	}()

	select {
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			logger.Error("HTTP shutdown failed", "error", err)
			if closeErr := httpServer.Close(); closeErr != nil {
				logger.Error("HTTP force-close failed", "error", closeErr)
			}
		}
		if err := <-serveErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func runMigrations(ctx context.Context) error {
	databaseURL, err := config.LoadDatabaseURL()
	if err != nil {
		return err
	}
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return postgres.Migrate(ctx, pool)
}
