package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/delinoio/oss/servers/dexdex-main-server/internal/broker"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/config"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/contracts"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/logging"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/repository"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/service"
)

func Run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.New()
	logger.Info(
		"dexdex.main_server.starting",
		"component", "main-server",
		"deployment_mode", cfg.DeploymentMode,
		"addr", cfg.Addr,
		"worker_addr", cfg.WorkerAddr,
	)

	store, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	eventBroker, closeBroker, err := openBroker(cfg, store)
	if err != nil {
		return err
	}
	defer closeBroker()

	server := service.NewServer(logger, store, eventBroker, cfg.DeploymentMode)

	mux := http.NewServeMux()
	server.Mount(mux)
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ready"))
	})

	handler := service.AuthMiddleware(mux, cfg.AuthToken, logger)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	logger.Info("dexdex.main_server.started", "component", "main-server", "addr", cfg.Addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve main server: %w", err)
	}

	logger.Info("dexdex.main_server.stopped", "component", "main-server", "result", "success")
	return nil
}

func openStore(cfg config.Config) (*repository.Store, error) {
	if cfg.DeploymentMode == contracts.DeploymentModeScale {
		store, err := repository.NewPostgres(cfg.PostgresDSN)
		if err != nil {
			return nil, err
		}
		return store, nil
	}

	store, err := repository.NewSQLite(cfg.SQLitePath)
	if err != nil {
		return nil, err
	}
	return store, nil
}

func openBroker(cfg config.Config, store *repository.Store) (broker.Broker, func(), error) {
	if cfg.DeploymentMode == contracts.DeploymentModeScale {
		redisBroker := broker.NewRedis(store, cfg.RedisAddr, cfg.RedisStreamPrefix)
		return redisBroker, func() { _ = redisBroker.Close() }, nil
	}

	inProcBroker := broker.NewInProc(store)
	return inProcBroker, func() {}, nil
}
