package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func requireCredentialUnavailable(t *testing.T, path string) {
	t.Helper()
	p := Personal{Credentials: map[string]Credential{"test": {File: path}}}
	c := Command{Environment: []EnvironmentInput{{Name: "TOKEN", Secret: true, Credential: "test", Required: true}}}
	env, secrets, err := p.CommandEnvironment(c, nil)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "credential-unavailable" || typed.Exit != 3 {
		t.Fatalf("expected bounded credential failure: %v", err)
	}
	if env != nil || secrets != nil || strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "sensitive-value") {
		t.Fatal("credential error exposed data")
	}
}

func TestCredentialFilesAreRegularBoundedAndRedacted(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private-token")
	requireCredentialUnavailable(t, path)
	requireCredentialUnavailable(t, root)
	for _, n := range []int{0, 1, maxCredentialFileBytes} {
		value := strings.Repeat("x", n)
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		p := Personal{Credentials: map[string]Credential{"test": {File: path}}}
		env, secrets, err := p.CommandEnvironment(Command{Environment: []EnvironmentInput{{Name: "TOKEN", Secret: true, Credential: "test"}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range env {
			if entry == "TOKEN="+value {
				found = true
			}
		}
		if !found || (n > 0 && (len(secrets) != 1 || secrets[0] != value)) {
			t.Fatal("bounded credential lost value or redaction input")
		}
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("sensitive-value", maxCredentialFileBytes)), 0600); err != nil {
		t.Fatal(err)
	}
	requireCredentialUnavailable(t, path)
	link := filepath.Join(root, "link")
	if err := os.Symlink(path, link); err == nil {
		requireCredentialUnavailable(t, link)
	}
}
