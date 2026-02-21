package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/delinoio/oss/servers/thenv/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("thenv server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := server.LoadConfig()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	ctx := context.Background()
	srv, err := server.New(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer srv.Close()

	httpMux := http.NewServeMux()
	httpMux.Handle("/", srv.Handler())
	httpMux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok"))
	})

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           httpMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("thenv server started", "addr", cfg.ListenAddr)
		if serveErr := httpServer.ListenAndServe(); serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Error("thenv server listen failed", "error", serveErr)
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}
