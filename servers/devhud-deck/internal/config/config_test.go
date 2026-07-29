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

func TestLoadAcceptsVersionedPreviousEncryptionKeys(t *testing.T) {
	t.Parallel()
	previous := base64.StdEncoding.EncodeToString(
		[]byte(strings.Repeat("p", 32)))
	configuration, err := Load(validLookup(map[string]string{
		"DECK_ENCRYPTION_KEY_ID":        "managed-v2",
		"DECK_ENCRYPTION_PREVIOUS_KEYS": `{"managed-v1":"` + previous + `"}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(configuration.PreviousEncryptionKeys) != 1 ||
		string(configuration.PreviousEncryptionKeys["managed-v1"]) !=
			strings.Repeat("p", 32) {
		t.Fatalf("previous encryption keys = %#v",
			configuration.PreviousEncryptionKeys)
	}
}

func TestLoadRejectsInvalidPreviousEncryptionKeys(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		`{"managed-v1":"not-base64"}`,
		`{"managed v1":"a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s="}`,
		`{"managed-v1":"a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s="}`,
	} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if _, err := Load(validLookup(map[string]string{
				"DECK_ENCRYPTION_KEY_ID":        "managed-v1",
				"DECK_ENCRYPTION_PREVIOUS_KEYS": value,
			})); err == nil {
				t.Fatalf("invalid previous keyring %q was accepted", value)
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
		"DECK_ENCRYPTION_KEY_ID":                      "managed-v1",
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
