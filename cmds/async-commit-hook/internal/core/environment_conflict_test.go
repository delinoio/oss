package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestEnvironmentDuplicateNamesAreRejectedCaseInsensitively(t *testing.T) {
	for _, secret := range []bool{false, true} {
		for _, second := range []string{"TOKEN", "Token", "token", "TOKEN_OTHER"} {
			t.Run(fmt.Sprintf("secret=%t/%s", secret, second), func(t *testing.T) {
				credential1, credential2 := "", ""
				if secret {
					credential1, credential2 = "first-credential", "second-credential"
				}
				config := fmt.Sprintf(`version=1
[checks.check]
command="exit 0"
[[checks.check.environment]]
name="TOKEN"
secret=%t
credential=%q
[[checks.check.environment]]
name=%q
secret=%t
credential=%q
`, secret, credential1, second, secret, credential2)
				_, err := ParseProject([]byte(config))
				if second == "TOKEN_OTHER" {
					if err != nil {
						t.Fatal("distinct names rejected", err)
					}
					return
				}
				var diagnostic *Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "invalid-environment" || diagnostic.Exit != 2 {
					t.Fatal("duplicate did not produce configuration error", err)
				}
				s, repo := fixture(t, config)
				if _, err := s.Submit(context.Background(), repo, "", false); !errors.As(err, &diagnostic) || diagnostic.Code != "invalid-environment" {
					t.Fatal("duplicate reached acceptance or credential resolution", err)
				}
				var count int
				if err := s.Store.DB.QueryRow("SELECT count(*) FROM runs").Scan(&count); err != nil || count != 0 {
					t.Fatal("invalid declaration persisted", count, err)
				}
			})
		}
	}
}

func TestEnvironmentSecrecyConflictsRejectedBeforeAcceptance(t *testing.T) {
	t.Setenv("ACH_CONFLICT_TOKEN", "secret-must-not-be-snapshotted")
	for _, secondName := range []string{"ACH_CONFLICT_TOKEN", "ach_conflict_token"} {
		t.Run(secondName, func(t *testing.T) {
			config := fmt.Sprintf(`version=1
[checks.first]
command="echo first"
[[checks.first.environment]]
name="ACH_CONFLICT_TOKEN"
secret=true
[checks.second]
command="echo second"
[[checks.second.environment]]
name=%q
secret=false
`, secondName)
			if _, err := ParseProject([]byte(config)); err == nil || !strings.Contains(err.Error(), "conflicting secret/public") {
				t.Fatal("conflicting classification accepted", err)
			}
			s, repo := fixture(t, config)
			if _, err := s.Submit(context.Background(), repo, "", false); err == nil {
				t.Fatal("conflicting secret was accepted")
			}
			var count int
			if err := s.Store.DB.QueryRow("SELECT count(*) FROM runs").Scan(&count); err != nil || count != 0 {
				t.Fatal("invalid context persisted", err)
			}
			for _, secret := range []bool{false, true} {
				project := Project{Version: 1, Checks: map[string]Command{
					"first":  {Command: "true", Environment: []EnvironmentInput{{Name: "ACH_CONFLICT_TOKEN", Secret: secret}}},
					"second": {Command: "true", Environment: []EnvironmentInput{{Name: secondName, Secret: secret}}},
				}}
				if err := project.Validate(); err != nil {
					t.Fatal("consistent shared input rejected", err)
				}
			}
		})
	}
}
