package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	thenvv1connect "github.com/delinoio/oss/servers/thenv/gen/proto/thenv/v1/thenvv1connect"
	"github.com/delinoio/oss/servers/thenv/internal/logging"
	"github.com/delinoio/oss/servers/thenv/internal/service"
)

func main() {
	ctx := context.Background()

	dbPath, err := resolveDBPath()
	if err != nil {
		log.Fatalf("resolve THENV_DB_PATH: %v", err)
	}

	masterKey := strings.TrimSpace(os.Getenv("THENV_MASTER_KEY_B64"))
	if masterKey == "" {
		log.Fatal("THENV_MASTER_KEY_B64 is required")
	}

	logger := logging.New()
	svc, err := service.New(ctx, service.Config{
		DBPath:                dbPath,
		MasterKeyBase64:       masterKey,
		BootstrapAdminSubject: strings.TrimSpace(os.Getenv("THENV_BOOTSTRAP_ADMIN_SUBJECT")),
	}, logger)
	if err != nil {
		log.Fatalf("initialize thenv service: %v", err)
	}
	defer func() {
		_ = svc.Close()
	}()

	mux := http.NewServeMux()
	bundlePath, bundleHandler := thenvv1connect.NewBundleServiceHandler(svc)
	policyPath, policyHandler := thenvv1connect.NewPolicyServiceHandler(svc)
	auditPath, auditHandler := thenvv1connect.NewAuditServiceHandler(svc)
	mux.Handle(bundlePath, bundleHandler)
	mux.Handle(policyPath, policyHandler)
	mux.Handle(auditPath, auditHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	addr := strings.TrimSpace(os.Getenv("THENV_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:8087"
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("thenv server listening on %s (db=%s)", addr, dbPath)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve thenv: %v", err)
	}
}

func resolveDBPath() (string, error) {
	if explicit := strings.TrimSpace(os.Getenv("THENV_DB_PATH")); explicit != "" {
		if err := os.MkdirAll(filepath.Dir(explicit), 0o755); err != nil {
			return "", err
		}
		return explicit, nil
	}

	stateRoot, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dbPath := filepath.Join(stateRoot, "thenv", "thenv.sqlite3")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return "", fmt.Errorf("create default db directory: %w", err)
	}
	return dbPath, nil
}
