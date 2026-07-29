package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoadAcceptsLogtoOIDCIssuerPath(t *testing.T) {
	t.Parallel()
	configuration, err := Load(validLookup(map[string]string{
		"DECK_LOGTO_ISSUER":   "https://tenant.logto.app/oidc",
		"DECK_LOGTO_JWKS_URL": "https://tenant.logto.app/oidc/jwks",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if configuration.LogtoIssuer != "https://tenant.logto.app/oidc" {
		t.Fatalf("issuer = %q", configuration.LogtoIssuer)
	}
}

func TestLoadRejectsUnsafeLogtoIssuer(t *testing.T) {
	t.Parallel()
	for _, issuer := range []string{
		"http://tenant.logto.app/oidc",
		"https://user@tenant.logto.app/oidc",
		"https://tenant.logto.app/oidc?token=secret",
		"https://tenant.logto.app/oidc#fragment",
	} {
		issuer := issuer
		t.Run(issuer, func(t *testing.T) {
			t.Parallel()
			if _, err := Load(validLookup(map[string]string{
				"DECK_LOGTO_ISSUER": issuer,
			})); err == nil {
				t.Fatalf("unsafe issuer %q was accepted", issuer)
			}
		})
	}
}

func validLookup(overrides map[string]string) LookupEnv {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	values := map[string]string{
		"DECK_DATABASE_URL":                           "postgres://deck.invalid/deck",
		"DECK_LOGTO_ISSUER":                           "https://tenant.logto.app/oidc",
		"DECK_LOGTO_JWKS_URL":                         "https://tenant.logto.app/oidc/jwks",
		"DECK_DELIBASE_LIFECYCLE_LOGTO_M2M_CLIENT_ID": "lifecycle-client",
		"DECK_ENCRYPTION_KEY":                         key,
		"DECK_HASHING_KEY":                            key,
		"DECK_LOG_PSEUDONYM_KEY":                      key,
		"DECK_GITHUB_APP_CLIENT_ID":                   "fixture-client",
		"DECK_GITHUB_APP_CLIENT_SECRET":               "fixture-client-secret",
		"DECK_GITHUB_APP_SLUG":                        "deck-fixture",
		"DECK_GITHUB_WEBHOOK_SECRET":                  key,
		"DECK_GITHUB_CALLBACK_SIGNING_KEY":            key,
	}
	for name, value := range overrides {
		values[name] = value
	}
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
