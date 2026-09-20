package core

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestManagedGuardCannotBeDeclaredAsEnvironmentInput(t *testing.T) {
	for _, name := range []string{"ACH_MANAGED", "ach_managed", "Ach_Managed"} {
		for _, secret := range []bool{false, true} {
			for _, optional := range []bool{false, true} {
				for _, platform := range []string{"linux", "windows", "darwin"} {
					credential := ""
					if secret {
						credential = "unresolved-reference"
					}
					config := fmt.Sprintf("version=1\n[checks.test]\ncommand=\"echo guard\"\nos=[%q]\noptional=%t\n[[checks.test.environment]]\nname=%q\nsecret=%t\ncredential=%q\nrequired=%t\n", platform, optional, name, secret, credential, !optional)
					_, err := ParseProject([]byte(config))
					var typed *Error
					if !errors.As(err, &typed) || typed.Code != "invalid-environment" || typed.Exit != 2 {
						t.Fatalf("reserved declaration accepted: %s: %v", config, err)
					}
				}
			}
		}
	}
	config := "version=1\n[checks.test]\ncommand=\"echo guard\"\n[[checks.test.environment]]\nname=\"ACH_MANAGED\"\n"
	s, repo := fixture(t, config)
	if _, err := s.Submit(context.Background(), repo, "", false); err == nil {
		t.Fatal("accepted committed reserved input")
	}
	var count int
	if err := s.Store.DB.QueryRow("SELECT count(*) FROM runs").Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid acceptance persisted: %d, %v", count, err)
	}
	if _, err := ParseProject([]byte("version=1\n[checks.test]\ncommand=\"echo fine\"\n[[checks.test.environment]]\nname=\"ACH_MANAGED_OTHER\"\n")); err != nil {
		t.Fatal(err)
	}
}
