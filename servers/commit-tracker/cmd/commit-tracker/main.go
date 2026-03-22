package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"

	"github.com/delinoio/oss/servers/commit-tracker/gen/proto/committracker/v1/committrackerv1connect"
	"github.com/delinoio/oss/servers/commit-tracker/internal/contracts"
	"github.com/delinoio/oss/servers/commit-tracker/internal/logging"
	"github.com/delinoio/oss/servers/commit-tracker/internal/service"
)

func main() {
	logger := logging.New()

	if err := run(logger); err != nil {
		logger.Error("fatal error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	// Read configuration from environment variables.
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	authToken := os.Getenv("AUTH_TOKEN")
	if authToken == "" {
		return fmt.Errorf("AUTH_TOKEN is required")
	}

	githubToken := os.Getenv("GITHUB_TOKEN")

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:8088"
	}

	// Initialize the service.
	svc, err := service.New(service.Config{
		DatabaseURL: dbURL,
		AuthToken:   authToken,
		GithubToken: githubToken,
	}, logger)
	if err != nil {
		return fmt.Errorf("initialize service: %w", err)
	}
	defer svc.Close()

	// Create auth interceptor.
	authInterceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			token := req.Header().Get("Authorization")
			if token != "Bearer "+authToken {
				logger.Warn("authentication failed",
					slog.String("event", contracts.EventAuthFailure),
					slog.String("procedure", req.Spec().Procedure),
				)
				return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("invalid or missing authorization token"))
			}
			return next(ctx, req)
		})
	})

	handlerOpts := connect.WithInterceptors(authInterceptor)

	// Register Connect RPC handlers.
	mux := http.NewServeMux()

	ingestionPath, ingestionHandler := committrackerv1connect.NewMetricIngestionServiceHandler(svc, handlerOpts)
	mux.Handle(ingestionPath, ingestionHandler)

	queryPath, queryHandler := committrackerv1connect.NewMetricQueryServiceHandler(svc, handlerOpts)
	mux.Handle(queryPath, queryHandler)

	reportPath, reportHandler := committrackerv1connect.NewProviderReportServiceHandler(svc, handlerOpts)
	mux.Handle(reportPath, reportHandler)

	// Health check endpoint (no auth required).
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("health check", slog.String("event", contracts.EventHealthCheck))
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return context.Background()
		},
	}

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("server starting",
			slog.String("event", contracts.EventServerStart),
			slog.String("addr", addr),
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server listen error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down server", slog.String("event", contracts.EventServerStop))

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	return srv.Shutdown(shutdownCtx)
}
