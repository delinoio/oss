package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/api"
	"github.com/delinoio/oss/servers/delibase/internal/config"
	"github.com/delinoio/oss/servers/delibase/internal/database"
	"github.com/delinoio/oss/servers/delibase/internal/logging"
	serverruntime "github.com/delinoio/oss/servers/delibase/internal/runtime"
	"github.com/delinoio/oss/servers/delibase/internal/service"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/safelog"
)

const databaseStartupTimeout = 30 * time.Second

type startupStage string

const (
	stageConfiguration  startupStage = "configuration"
	stageLogging        startupStage = "logging"
	stageDatabase       startupStage = "database"
	stageAuthentication startupStage = "authentication"
	stageHandler        startupStage = "handler"
	stageListener       startupStage = "listener"
	stageRuntime        startupStage = "runtime"
)

type startupError struct {
	stage startupStage
}

func (failure *startupError) Error() string { return "delibase startup failed" }

func main() {
	logger := logging.New(os.Stderr, slog.LevelInfo)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.LookupEnv, logger); err != nil {
		stage := startupStage("unknown")
		var failure *startupError
		if errors.As(err, &failure) {
			stage = failure.stage
		}
		logger.Error(
			"delibase startup failed",
			"event", "startup_failure",
			"failure_stage", string(stage),
		)
		os.Exit(1)
	}
}

func run(ctx context.Context, lookup config.LookupEnv, logger *slog.Logger) error {
	configuration, err := config.Load(lookup)
	if err != nil {
		return &startupError{stage: stageConfiguration}
	}
	if _, err := safelog.NewPseudonymizer(configuration.LogPseudonymKey); err != nil {
		return &startupError{stage: stageLogging}
	}
	databaseCtx, cancelDatabase := context.WithTimeout(ctx, databaseStartupTimeout)
	store, err := database.Open(databaseCtx, configuration.DatabaseURL)
	cancelDatabase()
	if err != nil {
		return &startupError{stage: stageDatabase}
	}
	defer store.Close()

	keys, err := auth.NewJWKS(auth.JWKSConfig{URL: configuration.LogtoJWKSURL})
	if err != nil {
		return &startupError{stage: stageAuthentication}
	}
	validator, err := auth.NewValidator(auth.Config{
		Issuer:    configuration.LogtoIssuer,
		Audience:  configuration.LogtoAudience,
		KeySource: keys,
	})
	if err != nil {
		return &startupError{stage: stageAuthentication}
	}
	handler, err := api.New(api.Dependencies{
		Authentication: validator,
		Health:         store,
		Services:       service.Dependencies{Store: store},
		CORSOrigins:    configuration.CORSAllowedOrigins,
		Logger:         logger,
	})
	if err != nil {
		return &startupError{stage: stageHandler}
	}
	listener, err := net.Listen("tcp", configuration.HTTPAddress)
	if err != nil {
		return &startupError{stage: stageListener}
	}
	if err := serverruntime.Serve(
		ctx,
		listener,
		handler,
		logger,
		configuration.ShutdownTimeout,
	); err != nil {
		return &startupError{stage: stageRuntime}
	}
	return nil
}
