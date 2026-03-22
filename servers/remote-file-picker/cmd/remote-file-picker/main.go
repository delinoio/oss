package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	remotefilepickerv1 "github.com/delinoio/oss/servers/remote-file-picker/gen/proto/remotefilepicker/v1"
	remotefilepickerv1connect "github.com/delinoio/oss/servers/remote-file-picker/gen/proto/remotefilepicker/v1/remotefilepickerv1connect"
	"github.com/delinoio/oss/servers/remote-file-picker/internal/logging"
	"github.com/delinoio/oss/servers/remote-file-picker/internal/service"
)

func main() {
	providerEnv := strings.TrimSpace(os.Getenv("STORAGE_PROVIDER"))
	provider := remotefilepickerv1.StorageProvider_STORAGE_PROVIDER_S3
	switch strings.ToLower(providerEnv) {
	case "gcs":
		provider = remotefilepickerv1.StorageProvider_STORAGE_PROVIDER_GCS
	case "s3", "":
		provider = remotefilepickerv1.StorageProvider_STORAGE_PROVIDER_S3
	default:
		log.Fatalf("unsupported STORAGE_PROVIDER: %s (must be s3 or gcs)", providerEnv)
	}

	bucket := strings.TrimSpace(os.Getenv("BUCKET"))
	if bucket == "" {
		log.Fatal("BUCKET is required")
	}

	authToken := strings.TrimSpace(os.Getenv("AUTH_TOKEN"))
	if authToken == "" {
		log.Fatal("AUTH_TOKEN is required")
	}

	addr := strings.TrimSpace(os.Getenv("ADDR"))
	if addr == "" {
		addr = "127.0.0.1:8089"
	}

	logger := logging.NewLogger()
	svc := service.New(logger, authToken, bucket, provider)

	mux := http.NewServeMux()
	uploadPath, uploadHandler := remotefilepickerv1connect.NewUploadServiceHandler(svc)
	mux.Handle(uploadPath, uploadHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("remote-file-picker server listening on %s (bucket=%s, provider=%s)", addr, bucket, provider.String())
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve remote-file-picker: %v", err)
	}
}
