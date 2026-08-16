package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestDevelopmentConfigurationUsesFixedPortAndExactRedirect(t *testing.T) {
	setValidEnvironment(t)
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
	for _, host := range []string{"localhost", "localhost.", "127.0.0.1", "::1"} {
		if !IsLoopbackHost(host) {
			t.Errorf("%q was not recognized as loopback", host)
		}
	}
	if IsLoopbackHost("127.0.0.2.example.com") {
		t.Fatal("deceptive hostname was recognized as loopback")
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
}
