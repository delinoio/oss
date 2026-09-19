package core

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestRepeatedGoTestFailuresHaveStableDistinctIdentities(t *testing.T) {
	parse := func(firstResult, message string) []Failure {
		t.Helper()
		var report bytes.Buffer
		encoder := json.NewEncoder(&report)
		emit := func(action, test, output string) {
			t.Helper()
			if err := encoder.Encode(map[string]string{"Action": action, "Package": "example/package", "Test": test, "Output": output}); err != nil {
				t.Fatal(err)
			}
		}
		emit("start", "", "")
		for _, action := range []string{firstResult, "fail"} {
			emit("run", "TestRepeated", "")
			emit("output", "TestRepeated", message)
			emit(action, "TestRepeated", "")
		}
		emit("fail", "", "")
		failures, err := ParseReport(GoTest, report.Bytes(), "check", "go test -json -count=2", "")
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, f := range failures {
			if seen[f.ID] {
				t.Fatal("repeated failure ID", f.ID)
			}
			seen[f.ID] = true
		}
		return failures
	}
	prior := parse("fail", "old diagnostic")
	changed := parse("fail", "new diagnostic")
	if len(prior) != 3 {
		t.Fatal("missing test or package failures", prior)
	}
	for i := range prior {
		if prior[i].ID != changed[i].ID {
			t.Fatal("diagnostic changed failure identity")
		}
	}
	current := parse("pass", "new diagnostic")
	if len(current) != 2 || current[0].ID != prior[1].ID || current[1].ID != prior[2].ID {
		t.Fatal("repaired first iteration renumbered later failures")
	}
	skipped := parse("skip", "new diagnostic")
	if len(skipped) != 2 || skipped[0].ID != prior[1].ID {
		t.Fatal("skipped first iteration renumbered later failure")
	}
	s, repo := fixture(t, "version=1\n[checks.check]\ncommand='unused'\n")
	store := func(failures []Failure) string {
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
		c.State, c.Failures = Failed, failures
		if err = s.Store.SaveCheck(c); err != nil {
			t.Fatal(err)
		}
		r.State = Failed
		if err = s.Store.SaveRun(r); err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	oldID, nextID := store(prior), store(current)
	diff, err := s.Compare(nextID, oldID)
	if err != nil || !diff.Available || len(diff.Continuing) != 2 || len(diff.Resolved) != 1 || len(diff.New) != 0 {
		t.Fatal("repeated failures collapsed", diff, err)
	}
	diff, err = s.Compare(oldID, nextID)
	if err != nil || len(diff.New) != 1 || len(diff.Continuing) != 2 || len(diff.Resolved) != 0 {
		t.Fatal("reverse comparison collapsed", diff, err)
	}
}
