package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/config"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/integrations"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/logging"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/service"
)

func Run(ctx context.Context) error {
	cfg := config.Load()
	logger := logging.New()

	logger.Info("dexdex.worker_server.starting", "component", "worker-server", "addr", cfg.Addr, "codex_bin", cfg.CodexBin)

	codexClient := integrations.NewCodexCLI(cfg.CodexBin, cfg.CodexProfile)
	executionService := service.NewExecutionService(logger, codexClient, service.ExecutionConfig{
		MaxRetry:     cfg.MaxRetry,
		RetryBackoff: time.Duration(cfg.RetryBackoffMS) * time.Millisecond,
	})

	mux := http.NewServeMux()
	service.MountExecutionService(mux, executionService)
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ready"))
	})

	handler := service.AuthMiddleware(mux, cfg.AuthToken, logger)
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	logger.Info("dexdex.worker_server.started", "component", "worker-server", "addr", cfg.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve worker server: %w", err)
	}

	logger.Info("dexdex.worker_server.stopped", "component", "worker-server", "result", "success")
	return nil
}
