package config

import (
	"strings"
	"testing"
)

func validEnvironment() map[string]string {
	return map[string]string{
		"DELIBASE_API_ORIGIN":              CanonicalAPIOrigin,
		"DELIBASE_CORS_ALLOWED_ORIGINS":    "https://deli.dev",
		"DELIBASE_CATALOG_PATH":            "catalog.json",
		"DELIBASE_DATABASE_URL":            "postgres://user:database-secret@localhost:5432/delibase",
		"DELIBASE_LOGTO_ISSUER":            "https://identity.example.com/oidc",
		"DELIBASE_LOGTO_AUDIENCE":          CanonicalAPIOrigin,
		"DELIBASE_LOGTO_JWKS_URL":          "https://identity.example.com/oidc/jwks",
		"DELIBASE_LOGTO_M2M_CLIENT_ID":     "service-client",
		"DELIBASE_LOGTO_M2M_CLIENT_SECRET": "logto-secret",
		"DELIBASE_POLAR_ACCESS_TOKEN":      "polar-token",
		"DELIBASE_POLAR_WEBHOOK_SECRET":    "webhook-secret-0123456789",
		"DELIBASE_POLAR_PRODUCT_ID":        "polar-product",
		"DELIBASE_LOG_PSEUDONYM_KEY":       strings.Repeat("k", 32),
	}
}

func TestLoadRequiresExplicitSandboxOptIn(t *testing.T) {
	t.Parallel()
	values := validEnvironment()
	values["DELIBASE_POLAR_ENVIRONMENT"] = "sandbox"
	configuration, err := Load(lookup(values))
	if err != nil || !configuration.PolarSandbox {
		t.Fatalf("sandbox configuration = %#v, %v", configuration, err)
	}
	values["DELIBASE_POLAR_ENVIRONMENT"] = "staging"
	if _, err := Load(lookup(values)); err == nil {
		t.Fatal("unsupported Polar environment was accepted")
	}
}

func lookup(values map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func TestLoadValidEnvironment(t *testing.T) {
	t.Parallel()
	configuration, err := Load(lookup(validEnvironment()))
	if err != nil {
		t.Fatal(err)
	}
	if configuration.APIOrigin != CanonicalAPIOrigin ||
		configuration.HTTPAddress != ":8080" ||
		len(configuration.CORSAllowedOrigins) != 1 {
		t.Fatalf("configuration = %#v", configuration)
	}
}

func TestLoadFailsClosedWithoutEveryRequiredVariable(t *testing.T) {
	t.Parallel()
	for name := range validEnvironment() {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			values := validEnvironment()
			delete(values, name)
			if _, err := Load(lookup(values)); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("Load() error = %v", err)
			}
		})
	}
}

func TestLoadRejectsInvalidValuesWithoutLeakingThem(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		variable string
		value    string
	}{
		{name: "audience", variable: "DELIBASE_LOGTO_AUDIENCE", value: "https://wrong.example.com"},
		{name: "api origin", variable: "DELIBASE_API_ORIGIN", value: "https://wrong.example.com"},
		{name: "database", variable: "DELIBASE_DATABASE_URL", value: "secret-database-value"},
		{name: "jwks", variable: "DELIBASE_LOGTO_JWKS_URL", value: "https://user:secret@example.com"},
		{name: "cors", variable: "DELIBASE_CORS_ALLOWED_ORIGINS", value: "*"},
		{name: "pseudonym key", variable: "DELIBASE_LOG_PSEUDONYM_KEY", value: "short-secret"},
		{name: "address", variable: "DELIBASE_HTTP_ADDRESS", value: "bad-address"},
		{name: "shutdown", variable: "DELIBASE_SHUTDOWN_TIMEOUT", value: "forever"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values := validEnvironment()
			values[test.variable] = test.value
			_, err := Load(lookup(values))
			if err == nil {
				t.Fatal("Load() succeeded")
			}
			if strings.Contains(err.Error(), test.value) {
				t.Fatalf("error leaked configured value: %v", err)
			}
		})
	}
}
