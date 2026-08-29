package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestDevelopmentConfigurationUsesFixedPortAndExactRedirect(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("DEVHUD_PUBLIC_ASSET_BASE_URL", "http://127.0.0.1:9000")
	configuration, err := Load("test-version")
	if err != nil {
		t.Fatal(err)
	}
	if configuration.ListenAddress != DevelopmentAddress {
		t.Fatalf("listen address = %q, want %q", configuration.ListenAddress, DevelopmentAddress)
	}
	if configuration.AdminRedirectURI != "http://localhost:46306/auth/callback" {
		t.Fatalf("admin redirect changed: %q", configuration.AdminRedirectURI)
	}
}

func TestDevelopmentPortCannotBeOverridden(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("DEVHUD_LISTEN_ADDRESS", "127.0.0.1:9999")
	_, err := Load("test")
	if err == nil || !strings.Contains(err.Error(), DevelopmentAddress) {
		t.Fatalf("Load error = %v, want fixed-address error", err)
	}
}

func TestHTTPSRequiredOutsideLoopback(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("DEVHUD_PUBLIC_ASSET_BASE_URL", "http://assets.example.com")
	_, err := Load("test")
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("Load error = %v, want HTTPS error", err)
	}
	for _, host := range []string{"localhost", "localhost.", "127.0.0.1", "127.0.0.2", "::1"} {
		if !IsLoopbackHost(host) {
			t.Errorf("%q was not recognized as loopback", host)
		}
	}
	if IsLoopbackHost("127.0.0.2.example.com") {
		t.Fatal("deceptive hostname was recognized as loopback")
	}
}

func TestPublicAssetBaseRejectsPathToKeepStableURLsExact(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("DEVHUD_PUBLIC_ASSET_BASE_URL", "https://assets.example.com/images")
	if _, err := Load("test"); err == nil || !strings.Contains(err.Error(), "must not contain a path") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestPartiallyConfiguredUploadAdaptersAreRejected(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("DEVHUD_R2_SECRET_ACCESS_KEY", "")
	if _, err := Load("test"); err == nil || !strings.Contains(err.Error(), "DEVHUD_R2_SECRET_ACCESS_KEY") {
		t.Fatalf("Load error = %v, want incomplete upload adapter error", err)
	}
}

func TestProductionRejectsLoopbackHTTPURLs(t *testing.T) {
	tests := map[string]string{
		"DEVHUD_PUBLIC_API_URL":        "http://localhost:46307",
		"DEVHUD_LOGTO_ISSUER":          "http://127.0.0.1:3001/oidc",
		"DEVHUD_PUBLIC_ASSET_BASE_URL": "http://[::1]:9000",
		"DEVHUD_ADMIN_REDIRECT_URI":    "http://localhost:46306/auth/callback",
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("DEVHUD_ENVIRONMENT", "production")
			t.Setenv("DEVHUD_LISTEN_ADDRESS", "0.0.0.0:8080")
			t.Setenv("DEVHUD_PUBLIC_API_URL", "https://api.example.com")
			t.Setenv("DEVHUD_LOGTO_ISSUER", "https://issuer.example.com/oidc")
			t.Setenv("DEVHUD_ADMIN_REDIRECT_URI", "https://api.example.com/admin/auth/callback")
			t.Setenv("DEVHUD_TRUSTED_PROXY_CIDRS", "10.0.0.0/8")
			t.Setenv(name, value)

			_, err := Load("test")
			if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "HTTPS") {
				t.Fatalf("Load error = %v, want %s HTTPS error", err, name)
			}
		})
	}
}

func TestProductionRequiresTrustedProxyCIDRs(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("DEVHUD_ENVIRONMENT", "production")
	t.Setenv("DEVHUD_LISTEN_ADDRESS", "0.0.0.0:8080")
	t.Setenv("DEVHUD_PUBLIC_API_URL", "https://api.example.com")
	t.Setenv("DEVHUD_LOGTO_ISSUER", "https://issuer.example.com/oidc")
	t.Setenv("DEVHUD_ADMIN_REDIRECT_URI", "https://api.example.com/admin/auth/callback")

	if _, err := Load("test"); err == nil || !strings.Contains(err.Error(), "DEVHUD_TRUSTED_PROXY_CIDRS") {
		t.Fatalf("Load error = %v, want trusted-proxy requirement", err)
	}
	t.Setenv("DEVHUD_TRUSTED_PROXY_CIDRS", "10.0.0.0/8")
	if _, err := Load("test"); err != nil {
		t.Fatalf("Load with trusted proxy: %v", err)
	}
}

func TestSweeperR2EndpointUsesEnvironmentURLPolicy(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		endpoint    string
		wantError   string
	}{
		{name: "development loopback HTTP", environment: "development", endpoint: "http://127.0.0.1:9000"},
		{name: "development external HTTP", environment: "development", endpoint: "http://r2.example.com", wantError: "HTTPS outside loopback"},
		{name: "production loopback HTTP", environment: "production", endpoint: "http://127.0.0.1:9000", wantError: "must use HTTPS"},
		{name: "credentials", environment: "production", endpoint: "https://user:password@r2.example.com", wantError: "without credentials, query, or fragment"},
		{name: "query", environment: "production", endpoint: "https://r2.example.com?token=secret", wantError: "without credentials, query, or fragment"},
		{name: "fragment", environment: "production", endpoint: "https://r2.example.com#secret", wantError: "without credentials, query, or fragment"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("DEVHUD_ENVIRONMENT", test.environment)
			t.Setenv("DEVHUD_R2_ENDPOINT", test.endpoint)
			_, err := LoadSweeper(false)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("LoadSweeper error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("LoadSweeper error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestIdentityFingerprintRotation(t *testing.T) {
	oldKey := []byte("01234567890123456789012345678901")
	newKey := []byte("abcdefghijklmnopqrstuvwxyz123456")
	candidates := IdentityFingerprintCandidates([][]byte{newKey, oldKey}, "issuer", "subject")
	if len(candidates) != 2 {
		t.Fatalf("candidate count = %d", len(candidates))
	}
	if string(IdentityFingerprint([][]byte{newKey, oldKey}, "issuer", "subject")) != string(candidates[0]) {
		t.Fatal("new fingerprints did not use the active key")
	}
	if string(candidates[0]) == string(candidates[1]) {
		t.Fatal("rotated keys produced the same fingerprint")
	}
}

func setValidEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("DEVHUD_ENVIRONMENT", "development")
	t.Setenv("DEVHUD_DATABASE_URL", "postgres://localhost/devhud")
	t.Setenv("DEVHUD_PUBLIC_API_URL", "http://localhost:46307")
	t.Setenv("DEVHUD_LOGTO_ISSUER", "http://localhost:3001/oidc")
	t.Setenv("DEVHUD_LOGTO_AUDIENCE", "https://devhud.example/api")
	t.Setenv("DEVHUD_LOGTO_DESKTOP_CLIENT_ID", "desktop")
	t.Setenv("DEVHUD_LOGTO_IOS_CLIENT_ID", "ios")
	t.Setenv("DEVHUD_LOGTO_ANDROID_CLIENT_ID", "android")
	t.Setenv("DEVHUD_LOGTO_ADMIN_CLIENT_ID", "admin")
	t.Setenv("DEVHUD_ADMIN_REDIRECT_URI", "http://localhost:46306/auth/callback")
	t.Setenv("DEVHUD_PUBLIC_ASSET_BASE_URL", "https://assets.example.com")
	t.Setenv("DEVHUD_IDENTITY_HMAC_KEYS", base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901")))
	t.Setenv("DEVHUD_R2_ENDPOINT", "https://account.r2.cloudflarestorage.com")
	t.Setenv("DEVHUD_R2_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("DEVHUD_R2_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("DEVHUD_R2_STAGING_BUCKET", "devhud-staging")
	t.Setenv("DEVHUD_R2_PUBLIC_BUCKET", "devhud-public")
	t.Setenv("DEVHUD_CLOUDFLARE_API_TOKEN", "test-cloudflare-token")
	t.Setenv("DEVHUD_CLOUDFLARE_ZONE_ID", "test-zone")
	t.Setenv("DEVHUD_CLOUDFLARE_RATE_LIMIT_RULE_ID", "test-rule")
}
