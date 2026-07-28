package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoadRequiresCanonicalAudiencesAndLifecyclePin(t *testing.T) {
	t.Parallel()
	values := validValues()
	configuration, err := Load(func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if configuration.APIOrigin != CanonicalAPIOrigin ||
		configuration.DelibaseLogtoAudience != CanonicalDelibaseOrigin {
		t.Fatalf("unexpected audiences: %#v", configuration)
	}
	for _, name := range []string{
		"REALQA_DELIBASE_LIFECYCLE_LOGTO_M2M_CLIENT_ID",
		"REALQA_IDENTITY_HASH_KEY",
		"REALQA_LOG_PSEUDONYM_KEY",
		"REALQA_GITHUB_APP_SLUG",
		"REALQA_GITHUB_OAUTH_CLIENT_SECRET",
		"REALQA_GITHUB_WEBHOOK_SECRET",
		"REALQA_GITHUB_CALLBACK_SIGNING_KEY",
		"REALQA_GITHUB_CREDENTIAL_KEY_ID",
		"REALQA_GITHUB_CREDENTIAL_WRAPPING_KEY_BASE64",
	} {
		invalid := validValues()
		delete(invalid, name)
		if _, err = Load(func(key string) (string, bool) {
			value, ok := invalid[key]
			return value, ok
		}); err == nil {
			t.Fatalf("Load() accepted missing %s", name)
		}
	}
}

func TestLoadRejectsGitHubCustomHostsAndInvalidProjectPermission(t *testing.T) {
	t.Parallel()
	for name, value := range map[string]string{
		"REALQA_GITHUB_WEB_ORIGIN":         "https://github.example.com",
		"REALQA_GITHUB_API_ORIGIN":         "https://github.example.com/api/v3",
		"REALQA_GITHUB_PROJECT_PERMISSION": "all",
	} {
		values := validValues()
		values[name] = value
		if _, err := Load(func(key string) (string, bool) {
			result, ok := values[key]
			return result, ok
		}); err == nil {
			t.Fatalf("Load() accepted %s=%s", name, value)
		}
	}
}

func validValues() map[string]string {
	return map[string]string{
		"REALQA_API_ORIGIN":                             CanonicalAPIOrigin,
		"REALQA_DATABASE_URL":                           "postgres://realqa@db.example/realqa",
		"REALQA_LOGTO_ISSUER":                           "https://tenant.logto.app/oidc",
		"REALQA_LOGTO_JWKS_URL":                         "https://tenant.logto.app/oidc/jwks",
		"REALQA_LOGTO_AUDIENCE":                         CanonicalAPIOrigin,
		"REALQA_DELIBASE_LOGTO_AUDIENCE":                CanonicalDelibaseOrigin,
		"REALQA_DELIBASE_LIFECYCLE_LOGTO_M2M_CLIENT_ID": "fixture-lifecycle-client",
		"REALQA_IDENTITY_HASH_KEY":                      strings.Repeat("i", 32),
		"REALQA_LOG_PSEUDONYM_KEY":                      strings.Repeat("p", 32),
		"REALQA_GITHUB_OAUTH_CLIENT_ID":                 "fixture-github-client",
		"REALQA_GITHUB_APP_SLUG":                        "fixture-realqa",
		"REALQA_GITHUB_OAUTH_CLIENT_SECRET":             "fixture-github-client-secret",
		"REALQA_GITHUB_WEBHOOK_SECRET":                  strings.Repeat("w", 32),
		"REALQA_GITHUB_CALLBACK_SIGNING_KEY":            strings.Repeat("s", 32),
		"REALQA_GITHUB_CREDENTIAL_KEY_ID":               "fixture-key-v1",
		"REALQA_GITHUB_CREDENTIAL_WRAPPING_KEY_BASE64": base64.StdEncoding.EncodeToString(
			[]byte(strings.Repeat("k", 32))),
	}
}
