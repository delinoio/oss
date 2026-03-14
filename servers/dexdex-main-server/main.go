package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/config"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/handler"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/polling"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/stream"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/worker"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.LoadConfig(logger)

	// Initialize store: PostgreSQL if DEXDEX_DATABASE_URL is set, else in-memory
	var dataStore store.Store
	if cfg.DatabaseURL != "" {
		pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
		if err != nil {
			logger.Error("failed to connect to database", "error", err)
			os.Exit(1)
		}
		defer pool.Close()
		dataStore = store.NewPostgresStore(pool, logger)
		logger.Info("using PostgreSQL store")
	} else {
		memStore := store.NewMemoryStore()
		dataStore = memStore

		if cfg.SeedData {
			store.SeedData(memStore)
			logger.Info("seed data loaded")
		}
	}

	// Initialize event stream broadcaster with configurable retention buffer.
	// Use Redis-backed fan-out in SCALE deployment mode, otherwise in-process.
	var fanOut stream.EventBroadcaster
	if cfg.DeploymentMode == "SCALE" && cfg.RedisURL != "" {
		redisFanOut, err := stream.NewRedisFanOut(cfg.RedisURL, cfg.StreamRetention, logger)
		if err != nil {
			logger.Error("failed to initialize redis fan-out", "error", err)
			os.Exit(1)
		}
		fanOut = redisFanOut
		logger.Info("using Redis-backed event broadcaster")
	} else {
		fanOut = stream.NewFanOut(cfg.StreamRetention, logger)
		logger.Info("using in-process event broadcaster")
	}

	// Create worker client and dispatcher
	workerClient := worker.NewClient(cfg.WorkerServerURL, logger)
	dispatcher := worker.NewDispatcher(workerClient, dataStore, fanOut, logger)

	// Create handlers
	workspaceHandler := handler.NewWorkspaceHandler(dataStore, logger)
	taskHandler := handler.NewTaskHandler(dataStore, fanOut, dispatcher, logger)
	notificationHandler := handler.NewNotificationHandler(dataStore, fanOut, logger)
	eventStreamHandler := handler.NewEventStreamHandler(fanOut, logger)

	// Register Connect RPC service handlers
	mux := http.NewServeMux()

	wsPath, wsHandler := dexdexv1connect.NewWorkspaceServiceHandler(workspaceHandler)
	mux.Handle(wsPath, wsHandler)

	taskPath, taskHTTPHandler := dexdexv1connect.NewTaskServiceHandler(taskHandler)
	mux.Handle(taskPath, taskHTTPHandler)

	notifPath, notifHandler := dexdexv1connect.NewNotificationServiceHandler(notificationHandler)
	mux.Handle(notifPath, notifHandler)
	sessionHandler := handler.NewSessionHandler(dataStore, workerClient, dispatcher, fanOut, logger)
	sessionPath, sessionHTTPHandler := dexdexv1connect.NewSessionServiceHandler(sessionHandler)
	mux.Handle(sessionPath, sessionHTTPHandler)

	eventStreamPath, eventStreamHTTPHandler := dexdexv1connect.NewEventStreamServiceHandler(eventStreamHandler)
	mux.Handle(eventStreamPath, eventStreamHTTPHandler)

	repoHandler := handler.NewRepositoryHandler(dataStore, logger)
	repoPath, repoHTTPHandler := dexdexv1connect.NewRepositoryServiceHandler(repoHandler)
	mux.Handle(repoPath, repoHTTPHandler)

	prHandler := handler.NewPrHandler(dataStore, logger)
	prPath, prHTTPHandler := dexdexv1connect.NewPrManagementServiceHandler(prHandler)
	mux.Handle(prPath, prHTTPHandler)

	reviewAssistHandler := handler.NewReviewAssistHandler(dataStore, logger)
	reviewAssistPath, reviewAssistHTTPHandler := dexdexv1connect.NewReviewAssistServiceHandler(reviewAssistHandler)
	mux.Handle(reviewAssistPath, reviewAssistHTTPHandler)

	reviewCommentHandler := handler.NewReviewCommentHandler(dataStore, fanOut, logger)
	reviewCommentPath, reviewCommentHTTPHandler := dexdexv1connect.NewReviewCommentServiceHandler(reviewCommentHandler)
	mux.Handle(reviewCommentPath, reviewCommentHTTPHandler)

	badgeThemeHandler := handler.NewBadgeThemeHandler(dataStore, logger)
	badgeThemePath, badgeThemeHTTPHandler := dexdexv1connect.NewBadgeThemeServiceHandler(badgeThemeHandler)
	mux.Handle(badgeThemePath, badgeThemeHTTPHandler)

	settingsHandler := handler.NewSettingsHandler(dataStore, logger)
	settingsPath, settingsHTTPHandler := dexdexv1connect.NewSettingsServiceHandler(settingsHandler)
	mux.Handle(settingsPath, settingsHTTPHandler)

	// Health check endpoint
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Start PR poller in background
	ghClient := polling.NewGitHubClient(logger)
	prPoller := polling.NewPRPoller(dataStore, fanOut, ghClient, time.Duration(cfg.PRPollIntervalSec)*time.Second, logger)
	pollerCtx, pollerCancel := context.WithCancel(context.Background())
	defer pollerCancel()
	go prPoller.Start(pollerCtx)

	// Start worktree coordinator for stale cleanup
	wtCoordinator := worker.NewWorktreeCoordinator(dispatcher, workerClient, dataStore, logger)
	wtCoordinator.Start(pollerCtx)

	httpServer := &http.Server{
		Addr:              cfg.ServerAddr,
		Handler:           corsMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info("dexdex main server starting", "addr", cfg.ServerAddr)
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
