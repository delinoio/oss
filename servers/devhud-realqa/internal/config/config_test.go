package config

import (
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
	}
}
