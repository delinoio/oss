package core

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestLifecycleRejectsChangedCredentialReferences(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	original := map[string]Credential{"token": {Env: "ACH_TEST_OLD_TOKEN"}, "file": {File: filepath.Join(t.TempDir(), "token")}}
	s.Personal.Credentials = original
	_, leave, err := s.Enter("daemon")
	if err != nil {
		t.Fatal(err)
	}
	defer leave()
	initial := s.configHash()
	// A second config with identical mappings can reuse the same owner. Values
	// are deliberately unresolved, including a missing referenced file.
	s.Paths.Config = filepath.Join(t.TempDir(), "other.toml")
	t.Setenv("ACH_TEST_OLD_TOKEN", "rotated secret")
	s.Personal.Credentials = map[string]Credential{"file": original["file"], "token": original["token"]}
	if s.configHash() != initial {
		t.Fatal("equivalent references changed identity")
	}
	if err := s.Start(Daemon); err != nil {
		t.Fatal(err)
	}
	for _, refs := range []map[string]Credential{
		{"token": {Env: "ACH_TEST_NEW_TOKEN"}, "file": original["file"]},
		{"token": original["token"], "file": {File: filepath.Join(t.TempDir(), "other")}},
		{"token": original["token"]},
	} {
		s.Personal.Credentials = refs
		_, cleanup, enterErr := s.Enter("cli")
		if cleanup != nil {
			cleanup()
		}
		for _, err := range []error{enterErr, s.Start(Daemon)} {
			var typed *Error
			if !errors.As(err, &typed) || typed.Code != "configuration-active" || typed.Exit != 2 {
				t.Fatalf("changed references reused active owner: %v", err)
			}
		}
	}
	leave()
	_, cleanup, err := s.Enter("worker")
	if err != nil {
		t.Fatal("new references must work after old owner exits", err)
	}
	cleanup()
}
