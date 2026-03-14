package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/handler"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/normalize"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	addr := os.Getenv("DEXDEX_WORKER_SERVER_ADDR")
	if addr == "" {
		addr = "127.0.0.1:7879"
	}

	sessionStore := store.NewSessionStore(logger)

	// Seed sample data when DEXDEX_SEED_DATA=true.
	if strings.EqualFold(os.Getenv("DEXDEX_SEED_DATA"), "true") {
		seedSampleData(sessionStore, logger)
	}

	sessionHandler := handler.NewSessionServiceHandler(sessionStore, logger)

	mux := http.NewServeMux()

	// Register SessionService Connect RPC handler.
	path, h := dexdexv1connect.NewSessionServiceHandler(sessionHandler)
	mux.Handle(path, h)

	// Health check endpoint.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// CORS middleware for dev origins.
	corsHandler := corsMiddleware(mux)

	server := &http.Server{
		Addr:              addr,
		Handler:           corsHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info("dexdex worker server starting",
		"component", "worker-server",
		"addr", addr,
	)

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutting down worker server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown error", "error", err)
		}
	}

	logger.Info("worker server stopped")
}

// corsMiddleware adds CORS headers for development origins.
func corsMiddleware(next http.Handler) http.Handler {
	allowedOrigins := map[string]bool{
		"http://localhost:5991": true,
		"http://localhost:7878": true,
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms")
			w.Header().Set("Access-Control-Max-Age", "3600")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// seedSampleData populates the session store with sample outputs for development.
func seedSampleData(sessionStore *store.SessionStore, logger *slog.Logger) {
	normalizer := normalize.NewOutputNormalizer(logger)

	sampleOutputs := []normalize.RawAgentOutput{
		{SessionID: "seed-session-1", Text: "Analyzing repository structure...", Kind: "text"},
		{SessionID: "seed-session-1", Text: "Reading src/main.ts", Kind: "tool_call"},
		{SessionID: "seed-session-1", Text: "File contents: export function main() { ... }", Kind: "tool_result"},
		{SessionID: "seed-session-1", Text: "Planning implementation approach", Kind: "plan"},
		{SessionID: "seed-session-1", Text: "Implementation 50% complete", Kind: "progress"},
		{SessionID: "seed-session-1", Text: "Unused import detected in utils.ts", Kind: "warning"},
		{SessionID: "seed-session-2", Text: "Starting code review session", Kind: "text"},
		{SessionID: "seed-session-2", Text: "Running linter", Kind: "tool_call"},
		{SessionID: "seed-session-2", Text: "Build failed: type error in handler.go", Kind: "error"},
	}

	for _, raw := range sampleOutputs {
		event := normalizer.Normalize(raw)
		sessionStore.AppendOutput(raw.SessionID, event)
	}

	logger.Info("seeded sample session data",
		"session_count", 2,
		"event_count", len(sampleOutputs),
	)
}
