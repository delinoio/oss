package main

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/handler"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/stream"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/worker"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Initialize in-memory store
	memStore := store.NewMemoryStore()

	// Seed data if DEXDEX_SEED_DATA=true
	if strings.EqualFold(strings.TrimSpace(os.Getenv("DEXDEX_SEED_DATA")), "true") {
		store.SeedData(memStore)
		logger.Info("seed data loaded")
	}

	// Initialize event stream fan-out with 1000-event retention buffer
	fanOut := stream.NewFanOut(1000, logger)

	// Create handlers
	workspaceHandler := handler.NewWorkspaceHandler(memStore, logger)
	taskHandler := handler.NewTaskHandler(memStore, fanOut, logger)
	notificationHandler := handler.NewNotificationHandler(memStore, fanOut, logger)
	eventStreamHandler := handler.NewEventStreamHandler(fanOut, logger)

	// Register Connect RPC service handlers
	mux := http.NewServeMux()

	wsPath, wsHandler := dexdexv1connect.NewWorkspaceServiceHandler(workspaceHandler)
	mux.Handle(wsPath, wsHandler)

	taskPath, taskHTTPHandler := dexdexv1connect.NewTaskServiceHandler(taskHandler)
	mux.Handle(taskPath, taskHTTPHandler)

	notifPath, notifHandler := dexdexv1connect.NewNotificationServiceHandler(notificationHandler)
	mux.Handle(notifPath, notifHandler)

	workerClient := worker.NewClient(logger)
	sessionHandler := handler.NewSessionHandler(memStore, workerClient, fanOut, logger)
	sessionPath, sessionHTTPHandler := dexdexv1connect.NewSessionServiceHandler(sessionHandler)
	mux.Handle(sessionPath, sessionHTTPHandler)

	eventStreamPath, eventStreamHTTPHandler := dexdexv1connect.NewEventStreamServiceHandler(eventStreamHandler)
	mux.Handle(eventStreamPath, eventStreamHTTPHandler)

	repoHandler := handler.NewRepositoryHandler(memStore, logger)
	repoPath, repoHTTPHandler := dexdexv1connect.NewRepositoryServiceHandler(repoHandler)
	mux.Handle(repoPath, repoHTTPHandler)

	prHandler := handler.NewPrHandler(memStore, logger)
	prPath, prHTTPHandler := dexdexv1connect.NewPrManagementServiceHandler(prHandler)
	mux.Handle(prPath, prHTTPHandler)

	reviewAssistHandler := handler.NewReviewAssistHandler(memStore, logger)
	reviewAssistPath, reviewAssistHTTPHandler := dexdexv1connect.NewReviewAssistServiceHandler(reviewAssistHandler)
	mux.Handle(reviewAssistPath, reviewAssistHTTPHandler)

	reviewCommentHandler := handler.NewReviewCommentHandler(memStore, logger)
	reviewCommentPath, reviewCommentHTTPHandler := dexdexv1connect.NewReviewCommentServiceHandler(reviewCommentHandler)
	mux.Handle(reviewCommentPath, reviewCommentHTTPHandler)

	// Health check endpoint
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	addr := strings.TrimSpace(os.Getenv("DEXDEX_MAIN_SERVER_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:7878"
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           corsMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info("dexdex main server starting", "addr", addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

// corsMiddleware adds CORS headers for local development (localhost:5991).
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "http://localhost:5991" || origin == "https://localhost:5991" || origin == "http://localhost:1420" || origin == "https://localhost:1420" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms, Grpc-Timeout, X-Grpc-Web, X-User-Agent")
			w.Header().Set("Access-Control-Expose-Headers", "Grpc-Status, Grpc-Message, Grpc-Status-Details-Bin")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
