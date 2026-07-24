package main

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/api"
	"github.com/delinoio/oss/servers/delibase/internal/catalog"
	"github.com/delinoio/oss/servers/delibase/internal/config"
	"github.com/delinoio/oss/servers/delibase/internal/contracts"
	"github.com/delinoio/oss/servers/delibase/internal/database"
	"github.com/delinoio/oss/servers/delibase/internal/logging"
	"github.com/delinoio/oss/servers/delibase/internal/logto"
	"github.com/delinoio/oss/servers/delibase/internal/polar"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	serverruntime "github.com/delinoio/oss/servers/delibase/internal/runtime"
	"github.com/delinoio/oss/servers/delibase/internal/service"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
)

const databaseStartupTimeout = 30 * time.Second

type startupStage string

const (
	stageConfiguration  startupStage = "configuration"
	stageCatalog        startupStage = "catalog"
	stageLogging        startupStage = "logging"
	stageDatabase       startupStage = "database"
	stageAuthentication startupStage = "authentication"
	stageHandler        startupStage = "handler"
	stageListener       startupStage = "listener"
	stageRuntime        startupStage = "runtime"
)

type startupError struct {
	stage      startupStage
	safeDetail string
}

func (failure *startupError) Error() string { return "delibase startup failed" }

type workerRandom struct{}

func (workerRandom) Float64() float64 { return rand.Float64() }

type workerTokenGenerator struct{}

func (workerTokenGenerator) New() (uuid.UUID, error) { return uuidv7.New() }

func main() {
	logger := logging.New(os.Stderr, slog.LevelInfo)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.LookupEnv, logger); err != nil {
		stage := startupStage("unknown")
		attributes := []any{
			"event", "startup_failure",
		}
		var failure *startupError
		if errors.As(err, &failure) {
			stage = failure.stage
			if failure.safeDetail != "" {
				attributes = append(attributes, "error_detail", failure.safeDetail)
			}
		}
		attributes = append(attributes, "failure_stage", string(stage))
		logger.Error(
			"delibase startup failed",
			attributes...,
		)
		os.Exit(1)
	}
}

func run(ctx context.Context, lookup config.LookupEnv, logger *slog.Logger) error {
	configuration, err := config.Load(lookup)
	if err != nil {
		return &startupError{
			stage:      stageConfiguration,
			safeDetail: err.Error(),
		}
	}
	pseudonymizer, err := safelog.NewPseudonymizer(configuration.LogPseudonymKey)
	if err != nil {
		return &startupError{stage: stageLogging}
	}
	catalogSpecification, err := catalog.Load(configuration.CatalogPath)
	if err != nil {
		return &startupError{
			stage:      stageCatalog,
			safeDetail: err.Error(),
		}
	}
	databaseCtx, cancelDatabase := context.WithTimeout(ctx, databaseStartupTimeout)
	store, err := database.Open(databaseCtx, configuration.DatabaseURL)
	if err != nil {
		cancelDatabase()
		return &startupError{stage: stageDatabase}
	}
	defer store.Close()
	if err := store.SyncCatalog(databaseCtx, catalogSpecification); err != nil {
		cancelDatabase()
		return &startupError{stage: stageCatalog}
	}
	cancelDatabase()

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
	identityManager, err := logto.New(
		configuration.LogtoIssuer,
		configuration.LogtoM2MClientID,
		configuration.LogtoM2MClientSecret,
		nil,
	)
	if err != nil {
		return &startupError{stage: stageAuthentication}
	}
	polarClient, err := polar.New(configuration.PolarAccessToken, nil)
	if err != nil {
		return &startupError{stage: stageConfiguration}
	}
	serviceDependencies := service.Dependencies{
		Store:           store,
		Clock:           contracts.SystemClock{},
		PolarCustomers:  polarClient,
		IdentityManager: identityManager,
		Pseudonymizer:   pseudonymizer,
		Logger:          logger,
	}
	handler, err := api.New(api.Dependencies{
		Authentication: validator,
		Health:         store,
		Services:       serviceDependencies,
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
	storage, err := reliability.NewPostgreSQLStorage(store.Queries())
	if err != nil {
		return &startupError{stage: stageRuntime}
	}
	registry := reliability.NewRegistry()
	if err := registry.Register(
		reliability.HandlerDeleteAccount,
		service.NewAccountDeletionHandler(store, identityManager),
	); err != nil {
		return &startupError{stage: stageRuntime}
	}
	if err := registry.Register(
		reliability.HandlerDeleteOrganization,
		service.NewOrganizationDeletionHandler(store),
	); err != nil {
		return &startupError{stage: stageRuntime}
	}
	if err := registry.Register(
		reliability.HandlerPolarCancelSubscription,
		service.NewPolarCancellationHandler(store.Queries(), polarClient),
	); err != nil {
		return &startupError{stage: stageRuntime}
	}
	worker, err := reliability.NewWorker(reliability.WorkerConfig{
		Storage:        storage,
		Registry:       registry,
		Clock:          serviceDependencies.Clock,
		Random:         workerRandom{},
		TokenGenerator: workerTokenGenerator{},
		Logger:         logger,
		LeaseDuration:  time.Minute,
		BaseBackoff:    time.Second,
		MaxBackoff:     15 * time.Minute,
		PollInterval:   time.Second,
		Queues: []reliability.Queue{
			reliability.QueueIntegrationOutbox,
			reliability.QueueDeletionJob,
		},
	})
	if err != nil {
		return &startupError{stage: stageRuntime}
	}
	go func() {
		_ = worker.Run(ctx)
	}()
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
