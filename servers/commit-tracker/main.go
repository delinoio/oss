package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	committrackerv1connect "github.com/delinoio/oss/servers/commit-tracker/gen/proto/committracker/v1/committrackerv1connect"
	"github.com/delinoio/oss/servers/commit-tracker/internal/logging"
	"github.com/delinoio/oss/servers/commit-tracker/internal/service"
)

func main() {
	ctx := context.Background()

	databaseURL := strings.TrimSpace(os.Getenv("COMMIT_TRACKER_DATABASE_URL"))
	if databaseURL == "" {
		log.Fatal("COMMIT_TRACKER_DATABASE_URL is required")
	}

	authToken := strings.TrimSpace(os.Getenv("COMMIT_TRACKER_AUTH_TOKEN"))
	if authToken == "" {
		log.Fatal("COMMIT_TRACKER_AUTH_TOKEN is required")
	}

	logger := logging.New()
	svc, err := service.New(ctx, service.Config{
		DatabaseURL:   databaseURL,
		AuthToken:     authToken,
		GitHubToken:   strings.TrimSpace(os.Getenv("COMMIT_TRACKER_GITHUB_TOKEN")),
		GitHubAPIBase: strings.TrimSpace(os.Getenv("COMMIT_TRACKER_GITHUB_API_BASE")),
	}, logger)
	if err != nil {
		log.Fatalf("initialize commit-tracker service: %v", err)
	}
	defer func() {
		_ = svc.Close()
	}()

	mux := http.NewServeMux()
	ingestionPath, ingestionHandler := committrackerv1connect.NewMetricIngestionServiceHandler(svc)
	queryPath, queryHandler := committrackerv1connect.NewMetricQueryServiceHandler(svc)
	reportPath, reportHandler := committrackerv1connect.NewProviderReportServiceHandler(svc)
	mux.Handle(ingestionPath, ingestionHandler)
	mux.Handle(queryPath, queryHandler)
	mux.Handle(reportPath, reportHandler)
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok"))
	})

	addr := strings.TrimSpace(os.Getenv("COMMIT_TRACKER_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:8091"
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("commit-tracker server listening on %s", addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve commit-tracker: %v", err)
	}
}
