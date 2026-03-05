package main

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	dexdexv1connect "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/service"
)

const (
	defaultServerAddress    = "127.0.0.1:7879"
	defaultReadHeaderTimout = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	address := strings.TrimSpace(os.Getenv("DEXDEX_WORKER_SERVER_ADDR"))
	if address == "" {
		address = defaultServerAddress
	}

	connectServer := service.NewSessionAdapterConnectServer(service.SessionAdapterConnectServerConfig{
		Logger: logger,
	})

	mux := http.NewServeMux()
	sessionAdapterPath, sessionAdapterHandler := dexdexv1connect.NewWorkerSessionAdapterServiceHandler(connectServer)
	mux.Handle(sessionAdapterPath, sessionAdapterHandler)
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
		"dexdex worker server started",
		"component", "worker-server",
		"address", address,
		"result", "success",
	)

	if serveErr := httpServer.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		logger.Error("dexdex worker server stopped with error", "error", serveErr.Error(), "result", "failure")
		os.Exit(1)
	}
}
