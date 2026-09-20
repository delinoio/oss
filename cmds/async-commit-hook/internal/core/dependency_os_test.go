package core

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestDependencyApplicabilityCoversEveryDependentOS(t *testing.T) {
	platforms := []string{"darwin", "linux", "windows"}
	list := func(mask int) []string {
		out := []string{}
		for i, name := range platforms {
			if mask&(1<<i) != 0 {
				out = append(out, name)
			}
		}
		return out
	}
	for dependent := 0; dependent < 8; dependent++ {
		for prerequisite := 0; prerequisite < 8; prerequisite++ {
			t.Run(fmt.Sprintf("dependent-%d/prerequisite-%d", dependent, prerequisite), func(t *testing.T) {
				configuration := fmt.Sprintf("version=1\n[checks.prepare]\ncommand='unused'\nos=%s\noptional=true\n[checks.test]\ncommand='unused'\nos=%s\ndepends_on=['prepare']\n", Encode(list(prerequisite)), Encode(list(dependent)))
				_, err := ParseProject([]byte(configuration))
				d, p := dependent, prerequisite
				if d == 0 {
					d = 7
				}
				if p == 0 {
					p = 7
				}
				valid := d & ^p == 0
				if valid && err != nil {
					t.Fatal("valid subset rejected", err)
				}
				if !valid {
					var typed *Error
					if !errors.As(err, &typed) || typed.Code != "invalid-dependency" || typed.Exit != 2 {
						t.Fatal("contradictory graph accepted", err)
					}
				}
			})
		}
	}
}

func TestInapplicablePrerequisiteRejectsAcceptanceOnEveryHost(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.prepare]\ncommand='unused'\nos=['linux']\n[checks.test]\ncommand='unused'\nos=['windows']\ndepends_on=['prepare']\n")
	if _, err := s.Submit(context.Background(), repo, "", false); err == nil {
		t.Fatal("invalid graph accepted")
	}
	var count int
	if err := s.Store.DB.QueryRow("SELECT count(*) FROM runs").Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid graph persisted a receipt", count, err)
	}
}
