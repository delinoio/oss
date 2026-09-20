package core

import (
	"context"
	"strings"
	"testing"
)

func TestRepeatedDiagnosticIdentitiesCompareIndependently(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand='unused'\n")
	first := Diagnostic{Code: "report-missing", Message: "declared report unavailable: first.xml"}
	second := Diagnostic{Code: "report-missing", Message: "declared report unavailable: second.xml"}
	submit := func(diagnostics []Diagnostic) Run {
		t.Helper()
		receipt, err := s.Submit(context.Background(), repo, "", false)
		if err != nil {
			t.Fatal(err)
		}
		r, err := s.Store.Run(receipt.RunID)
		if err != nil {
			t.Fatal(err)
		}
		c := r.Checks[0]
		c.State, c.Diagnostics = Failed, diagnostics
		if err = s.Store.SaveCheck(c); err != nil {
			t.Fatal(err)
		}
		r.State = Failed
		if err = s.Store.SaveRun(r); err != nil {
			t.Fatal(err)
		}
		r, err = s.Store.Run(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	previous := submit([]Diagnostic{first, second, first})
	current := submit([]Diagnostic{second, first})
	seen := map[string]bool{}
	for _, f := range Failures(previous) {
		if seen[f.ID] {
			t.Fatal("duplicate diagnostic ID", f.ID)
		}
		seen[f.ID] = true
	}
	comparison, err := s.Compare(current.ID, previous.ID)
	if err != nil || !comparison.Available || len(comparison.Continuing) != 2 || len(comparison.Resolved) != 1 || len(comparison.New) != 0 {
		t.Fatal("diagnostics collapsed during comparison", comparison, err)
	}
	comparison, err = s.Compare(previous.ID, current.ID)
	if err != nil || len(comparison.New) != 1 || len(comparison.Continuing) != 2 {
		t.Fatal(comparison, err)
	}
	// Identity uses the original message even when display prefixes coincide.
	prefix := strings.Repeat("a", failureFieldBytes)
	current.Checks[0].Diagnostics = []Diagnostic{{Code: "report-missing", Message: prefix + "one"}, {Code: "report-missing", Message: prefix + "two"}}
	failures := Failures(current)
	if failures[0].ID == failures[1].ID || failures[0].Message != failures[1].Message {
		t.Fatal("identity was derived after truncation")
	}
}
