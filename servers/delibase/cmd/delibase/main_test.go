package main

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/servers/delibase/internal/config"
)

func TestRunFailsAtConfigurationBeforeStartingDependencies(t *testing.T) {
	t.Parallel()
	err := run(
		context.Background(),
		func(string) (string, bool) { return "", false },
		slog.New(slog.DiscardHandler),
	)
	var failure *startupError
	if !errors.As(err, &failure) {
		t.Fatalf("run() error = %T", err)
	}
	if failure.stage != stageConfiguration {
		t.Fatalf("startup stage = %q", failure.stage)
	}
	if failure.safeDetail != "config: DELIBASE_API_ORIGIN is required" {
		t.Fatalf("safe startup detail = %q", failure.safeDetail)
	}
}

func TestRunRejectsMissingCatalogBeforeDatabaseStartup(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"DELIBASE_API_ORIGIN":              config.CanonicalAPIOrigin,
		"DELIBASE_CORS_ALLOWED_ORIGINS":    "https://deli.dev",
		"DELIBASE_CATALOG_PATH":            filepath.Join(t.TempDir(), "missing.json"),
		"DELIBASE_DATABASE_URL":            "postgres://user:password@localhost:5432/delibase",
		"DELIBASE_LOGTO_ISSUER":            "https://identity.example.com/oidc",
		"DELIBASE_LOGTO_AUDIENCE":          config.CanonicalAPIOrigin,
		"DELIBASE_LOGTO_JWKS_URL":          "https://identity.example.com/oidc/jwks",
		"DELIBASE_LOGTO_M2M_CLIENT_ID":     "service-client",
		"DELIBASE_LOGTO_M2M_CLIENT_SECRET": "logto-secret",
		"DELIBASE_POLAR_ACCESS_TOKEN":      "polar-token",
		"DELIBASE_POLAR_WEBHOOK_SECRET":    "webhook-secret",
		"DELIBASE_LOG_PSEUDONYM_KEY":       strings.Repeat("k", 32),
	}
	err := run(
		context.Background(),
		func(name string) (string, bool) {
			value, ok := values[name]
			return value, ok
		},
		slog.New(slog.DiscardHandler),
	)
	var failure *startupError
	if !errors.As(err, &failure) {
		t.Fatalf("run() error = %T", err)
	}
	if failure.stage != stageCatalog {
		t.Fatalf("startup stage = %q", failure.stage)
	}
	if failure.safeDetail != "catalog: file is unavailable" {
		t.Fatalf("safe startup detail = %q", failure.safeDetail)
	}
}
