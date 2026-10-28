// SPDX-License-Identifier: Apache-2.0
package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	env := map[string]string{"TAIROUTER_PUBLIC_URL": "https://router.example", "TAIROUTER_DATABASE_URL": "postgres://user:secret@localhost/router", "TAIROUTER_OIDC_ISSUER": "https://identity.example/realm", "TAIROUTER_OIDC_CLIENT_ID": "router", "TAIROUTER_ENCRYPTION_ACTIVE_KEY": "v1", "TAIROUTER_ENCRYPTION_KEYS": `{"v1":"` + base64.StdEncoding.EncodeToString(make([]byte, 32)) + `"}`, "TAIROUTER_SMTP_ADDR": "127.0.0.1:1025", "TAIROUTER_SMTP_FROM": "router@example.test", "TAIROUTER_R2_ENDPOINT": "https://objects.example", "TAIROUTER_R2_BUCKET": "images", "TAIROUTER_R2_ACCESS_KEY_ID": "key", "TAIROUTER_R2_SECRET_ACCESS_KEY": "secret"}
	get := func(k string) string { return env[k] }
	if _, e := Load(get); e != nil {
		t.Fatal(e)
	}
	oldEndpoint := env["TAIROUTER_R2_ENDPOINT"]
	env["TAIROUTER_R2_ENDPOINT"] = "http://127.0.0.1:18090"
	if _, e := Load(get); e == nil {
		t.Fatal("HTTP R2 endpoint accepted before artifact startup")
	}
	env["TAIROUTER_R2_ENDPOINT"] = oldEndpoint
	for _, name := range []string{"TAIROUTER_PUBLIC_URL", "TAIROUTER_DATABASE_URL", "TAIROUTER_OIDC_ISSUER", "TAIROUTER_ENCRYPTION_KEYS", "TAIROUTER_SMTP_ADDR", "TAIROUTER_R2_ENDPOINT"} {
		old := env[name]
		env[name] = "secret-invalid-value"
		_, e := Load(get)
		if e == nil || strings.Contains(e.Error(), "secret-invalid-value") {
			t.Fatalf("unsafe validation for %s: %v", name, e)
		}
		env[name] = old
	}
}
func TestOriginValidation(t *testing.T) {
	for _, s := range []string{"http://public.example", "https://user:pass@example.test", "https://example.test/path", "https://example.test?token=secret", "//example.test", "https://example.test#fragment"} {
		if validOrigin(s) {
			t.Fatalf("accepted %s", s)
		}
	}
	for _, s := range []string{"https://example.test", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		if !validOrigin(s) {
			t.Fatalf("rejected %s", s)
		}
	}
}
