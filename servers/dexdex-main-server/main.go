package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	dexdexv1connect "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/service"
)

const (
	defaultServerAddress    = "127.0.0.1:7878"
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

	connectServer := service.NewConnectServer(service.ConnectServerConfig{
		Logger:          logger,
		StreamRetention: retention,
		StreamHeartbeat: heartbeat,
	})

	mux := http.NewServeMux()
	taskPath, taskHandler := dexdexv1connect.NewTaskServiceHandler(connectServer)
	eventPath, eventHandler := dexdexv1connect.NewEventStreamServiceHandler(connectServer)
	mux.Handle(taskPath, taskHandler)
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
