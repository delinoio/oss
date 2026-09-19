package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

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
