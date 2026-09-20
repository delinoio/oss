package core

import (
	"fmt"
	"testing"
)

func TestAggregateCheckStateHasExplicitOrderIndependentPrecedence(t *testing.T) {
	states := []State{Interrupted, Cancelled, Replaced, Expired, Failed, Blocked, Passed}
	for i, higher := range states {
		for _, lower := range states[i:] {
			for _, checks := range [][]Check{{{Name: "a", State: higher}, {Name: "z", State: lower}}, {{Name: "a", State: lower}, {Name: "z", State: higher}}} {
				if got, count := aggregateCheckState(checks); got != higher || count != 2 {
					t.Fatalf("%+v => %s, %d", checks, got, count)
				}
			}
		}
	}
	if got, count := aggregateCheckState([]Check{{State: Skipped}}); got != Failed || count != 0 {
		t.Fatal(got, count)
	}
	if got, _ := aggregateCheckState([]Check{{State: Passed}, {State: Failed, Optional: true}, {State: Blocked, Optional: true}}); got != Passed {
		t.Fatal("optional failure changed gate", got)
	}
	for _, state := range []State{Interrupted, Cancelled, Replaced} {
		if got, _ := aggregateCheckState([]Check{{State: Passed}, {State: state, Optional: true}}); got != state {
			t.Fatal("optional lifecycle outcome hidden", got)
		}
	}
}

func TestFailedPrerequisiteWinsRegardlessOfCheckNames(t *testing.T) {
	for _, names := range [][2]string{{"a_root", "z_dependent"}, {"z_root", "a_dependent"}} {
		s, repo := fixture(t, fmt.Sprintf("version=1\n[checks.%s]\ncommand=\"exit 1\"\n[checks.%s]\ncommand=\"echo must-not-run\"\ndepends_on=[%q]\n", names[0], names[1], names[0]))
		r := runFixture(t, s, repo)
		if r.State != Failed {
			t.Fatalf("same graph changed outcome by name: %+v", r)
		}
		for _, c := range r.Checks {
			if c.Name == names[1] && (c.State != Blocked || c.StartedAt != nil) {
				t.Fatal("dependent executed", c)
			}
		}
	}
}
