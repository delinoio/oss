package core

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
)

const publicEncodingConfig = `version=1
[checks.example]
command="exit 0"
[[checks.example.environment]]
name="ACH_TEST_PUBLIC_ENCODING"
`

func TestInvalidPublicEnvironmentIsRejectedBeforeAcceptance(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows environment cannot represent arbitrary byte strings")
	}
	s, repo := fixture(t, publicEncodingConfig)
	for _, value := range []string{"sensitive-prefix-\xff", "sensitive-prefix-\xfe", "\xc3"} {
		t.Setenv("ACH_TEST_PUBLIC_ENCODING", value)
		_, err := s.Submit(context.Background(), repo, "", false)
		var typed *Error
		if !errors.As(err, &typed) || typed.Code != "invalid-environment" || typed.Exit != 2 {
			t.Fatalf("invalid public bytes accepted: %v", err)
		}
		if strings.Contains(err.Error(), "sensitive-prefix") {
			t.Fatal("diagnostic exposed environment content")
		}
	}
	pending, err := s.Store.Pending()
	if err != nil || len(pending) != 0 {
		t.Fatal("invalid input persisted an attempt", pending, err)
	}
}

func TestPublicEnvironmentRoundTripsUnicodeEmptyAndAbsent(t *testing.T) {
	s, repo := fixture(t, publicEncodingConfig)
	fingerprints := map[string]bool{}
	for _, value := range []string{"안녕 🌍 \ufffd", "", "absent"} {
		t.Setenv("ACH_TEST_PUBLIC_ENCODING", value)
		if value == "absent" {
			if err := os.Unsetenv("ACH_TEST_PUBLIC_ENCODING"); err != nil {
				t.Fatal(err)
			}
		}
		receipt, err := s.Submit(context.Background(), repo, "", false)
		if err != nil {
			t.Fatal(err)
		}
		r, err := s.Store.Run(receipt.RunID)
		if err != nil {
			t.Fatal(err)
		}
		got, present := r.Environment["ACH_TEST_PUBLIC_ENCODING"]
		if present != (value != "absent") || (present && got != value) {
			t.Fatal("snapshot changed public input")
		}
		env, _, err := s.Personal.CommandEnvironment(r.Config.Checks["example"], r.Environment)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range env {
			if strings.HasPrefix(entry, "ACH_TEST_PUBLIC_ENCODING=") {
				found = true
				if entry != "ACH_TEST_PUBLIC_ENCODING="+value {
					t.Fatal("execution changed public input")
				}
			}
		}
		if found != present {
			t.Fatal("execution changed input presence")
		}
		if fingerprints[r.Fingerprint] {
			t.Fatal("distinct inputs share fingerprint")
		}
		fingerprints[r.Fingerprint] = true
	}
}
