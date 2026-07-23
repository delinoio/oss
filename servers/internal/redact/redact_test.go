package redact

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestHeadersRedactsAuthorizationAndForwardedUserToken(t *testing.T) {
	t.Parallel()
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer access-token")
	headers.Set("X-Delibase-Forwarded-User-Token", "forwarded-token")
	headers.Set("X-Request-Id", "request-1")
	safe := Headers(headers)
	if safe.Get("Authorization") != Replacement ||
		safe.Get("X-Delibase-Forwarded-User-Token") != Replacement {
		t.Fatalf("redacted headers = %#v", safe)
	}
	if safe.Get("X-Request-Id") != "request-1" {
		t.Fatalf("safe header changed: %#v", safe)
	}
	if headers.Get("Authorization") != "Bearer access-token" {
		t.Fatal("Headers mutated its input")
	}
}

func TestTextErrorsAndDiagnosticsRedactSensitiveData(t *testing.T) {
	t.Parallel()
	jwt := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyLTEifQ.signature123"
	raw := "Authorization: Bearer abc123 token=" + jwt +
		" email=owner@example.com card=4242 4242 4242 4242"
	safe := Text(raw)
	for _, forbidden := range []string{"abc123", jwt, "owner@example.com", "4242 4242 4242 4242"} {
		if strings.Contains(safe, forbidden) {
			t.Fatalf("Text leaked %q: %s", forbidden, safe)
		}
	}
	safeError := Error(errors.New(raw))
	if strings.Contains(safeError.Error(), "abc123") {
		t.Fatalf("Error leaked credential: %v", safeError)
	}
	diagnostic := Value("", map[string]any{
		"nested": map[string]any{"client_secret": "secret-value"},
		"error":  raw,
	}).(map[string]any)
	if diagnostic["nested"].(map[string]any)["client_secret"] != Replacement {
		t.Fatalf("diagnostic leaked secret: %#v", diagnostic)
	}
}

func TestValueRedactsTypedDiagnosticContainers(t *testing.T) {
	t.Parallel()
	type metadata map[string]string
	safe := Value("metadata", metadata{
		"authorization": "Bearer typed-secret",
		"owner":         "owner@example.com",
	}).(map[string]any)
	if safe["authorization"] != Replacement || safe["owner"] != Replacement {
		t.Fatalf("typed map leaked sensitive data: %#v", safe)
	}

	nested := Value("metadata", map[string][]string{
		"values": {"Bearer nested-secret", "safe"},
	}).(map[string]any)
	values := nested["values"].([]any)
	if values[0] != Replacement || values[1] != "safe" {
		t.Fatalf("typed slice leaked sensitive data: %#v", nested)
	}
}

func TestValuePreservesNilDiagnosticValues(t *testing.T) {
	t.Parallel()
	if safe := Value("metadata", nil); safe != nil {
		t.Fatalf("nil diagnostic = %#v", safe)
	}
	safe := Value("metadata", []any{nil}).([]any)
	if safe[0] != nil {
		t.Fatalf("nil diagnostic element = %#v", safe)
	}
}
