package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	dexdexv1connect "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/service"
)

const (
	defaultServerAddress    = "127.0.0.1:7878"
	defaultWorkerServerURL  = "http://127.0.0.1:7879"
	defaultWorkerRPCTimeout = 30 * time.Second
	defaultStreamRetention  = 256
	defaultStreamHeartbeat  = 15 * time.Second
	defaultReadHeaderTimout = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	address := strings.TrimSpace(os.Getenv("DEXDEX_MAIN_SERVER_ADDR"))
	if address == "" {
		address = defaultServerAddress
	}

	retention, err := positiveIntFromEnv("DEXDEX_MAIN_STREAM_RETENTION", defaultStreamRetention)
	if err != nil {
		logger.Error("dexdex.main.config.invalid", "env", "DEXDEX_MAIN_STREAM_RETENTION", "error", err.Error())
		os.Exit(1)
	}

	heartbeat, err := durationFromEnv("DEXDEX_MAIN_STREAM_HEARTBEAT_INTERVAL", defaultStreamHeartbeat)
	if err != nil {
		logger.Error("dexdex.main.config.invalid", "env", "DEXDEX_MAIN_STREAM_HEARTBEAT_INTERVAL", "error", err.Error())
		os.Exit(1)
	}

	workerServerURL, err := absoluteHTTPURLFromEnv("DEXDEX_WORKER_SERVER_URL", defaultWorkerServerURL)
	if err != nil {
		logger.Error("dexdex.main.config.invalid", "env", "DEXDEX_WORKER_SERVER_URL", "error", err.Error())
		os.Exit(1)
	}

	workerHTTPClient := &http.Client{
		Timeout: defaultWorkerRPCTimeout,
	}
	workerSessionAdapterClient := dexdexv1connect.NewWorkerSessionAdapterServiceClient(workerHTTPClient, workerServerURL)

	connectServer := service.NewConnectServer(service.ConnectServerConfig{
		Logger:               logger,
		StreamRetention:      retention,
		StreamHeartbeat:      heartbeat,
		WorkerSessionAdapter: workerSessionAdapterClient,
	})

	mux := http.NewServeMux()
	workspacePath, workspaceHandler := dexdexv1connect.NewWorkspaceServiceHandler(connectServer)
	repositoryPath, repositoryHandler := dexdexv1connect.NewRepositoryServiceHandler(connectServer)
	taskPath, taskHandler := dexdexv1connect.NewTaskServiceHandler(connectServer)
	sessionPath, sessionHandler := dexdexv1connect.NewSessionServiceHandler(connectServer)
	prPath, prHandler := dexdexv1connect.NewPrManagementServiceHandler(connectServer)
	reviewAssistPath, reviewAssistHandler := dexdexv1connect.NewReviewAssistServiceHandler(connectServer)
	reviewCommentPath, reviewCommentHandler := dexdexv1connect.NewReviewCommentServiceHandler(connectServer)
	badgeThemePath, badgeThemeHandler := dexdexv1connect.NewBadgeThemeServiceHandler(connectServer)
	notificationPath, notificationHandler := dexdexv1connect.NewNotificationServiceHandler(connectServer)
	eventPath, eventHandler := dexdexv1connect.NewEventStreamServiceHandler(connectServer)
	mux.Handle(workspacePath, workspaceHandler)
	mux.Handle(repositoryPath, repositoryHandler)
	mux.Handle(taskPath, taskHandler)
	mux.Handle(sessionPath, sessionHandler)
	mux.Handle(prPath, prHandler)
	mux.Handle(reviewAssistPath, reviewAssistHandler)
	mux.Handle(reviewCommentPath, reviewCommentHandler)
	mux.Handle(badgeThemePath, badgeThemeHandler)
	mux.Handle(notificationPath, notificationHandler)
	mux.Handle(eventPath, eventHandler)
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok"))
	})

	httpServer := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: defaultReadHeaderTimout,
	}

	logger.Info(
		"dexdex main server started",
		"component", "main-server",
		"address", address,
		"worker_server_url", workerServerURL,
		"worker_rpc_timeout", defaultWorkerRPCTimeout.String(),
		"stream_retention", retention,
		"stream_heartbeat_interval", heartbeat.String(),
		"result", "success",
	)

	if serveErr := httpServer.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		logger.Error("dexdex main server stopped with error", "error", serveErr.Error(), "result", "failure")
		os.Exit(1)
	}
}

func positiveIntFromEnv(envKey string, defaultValue int) (int, error) {
	rawValue := strings.TrimSpace(os.Getenv(envKey))
	if rawValue == "" {
		return defaultValue, nil
	}

	parsedValue, err := strconv.Atoi(rawValue)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", envKey, err)
	}
	if parsedValue <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", envKey)
	}

	return parsedValue, nil
}

func durationFromEnv(envKey string, defaultValue time.Duration) (time.Duration, error) {
	rawValue := strings.TrimSpace(os.Getenv(envKey))
	if rawValue == "" {
		return defaultValue, nil
	}

	parsedValue, err := time.ParseDuration(rawValue)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration (for example: 5s, 500ms): %w", envKey, err)
	}
	if parsedValue <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", envKey)
	}

	return parsedValue, nil
}

func absoluteHTTPURLFromEnv(envKey string, defaultValue string) (string, error) {
	rawValue := strings.TrimSpace(os.Getenv(envKey))
	if rawValue == "" {
		rawValue = defaultValue
	}

	parsedValue, err := url.Parse(rawValue)
	if err != nil || parsedValue == nil {
		return "", fmt.Errorf("%s must be a valid absolute URL", envKey)
	}
	if parsedValue.Scheme != "http" && parsedValue.Scheme != "https" {
		return "", fmt.Errorf("%s must use http or https scheme", envKey)
	}
	if parsedValue.Host == "" {
		return "", fmt.Errorf("%s must include host", envKey)
	}

	return parsedValue.String(), nil
}
