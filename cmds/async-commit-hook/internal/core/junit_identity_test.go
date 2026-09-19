package core

import (
	"context"
	"strings"
	"testing"
)

func TestJUnitDuplicateFailuresHaveStableDistinctIdentities(t *testing.T) {
	first := `<testsuites><testsuite name="outer"><testsuite name="shared"><testcase classname="C" name="same"><failure>one</failure><failure>two</failure><error>three</error></testcase><testcase classname="C" name="same"><failure>four</failure></testcase></testsuite><testsuite name="shared"><testcase classname="C" name="same"><failure>five</failure></testcase></testsuite></testsuite><testsuite name="other"><testsuite name="shared"><testcase classname="C" name="same"><failure>six</failure></testcase></testsuite></testsuite></testsuites>`
	parse := func(report string) []Failure {
		t.Helper()
		failures, err := ParseReport(JUnit, []byte(report), "check", "command", "")
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, f := range failures {
			if seen[f.ID] {
				t.Fatal("duplicate failure ID", f.ID)
			}
			seen[f.ID] = true
		}
		return failures
	}
	old := parse(first)
	if len(old) != 6 || old[0].Test != "outer/shared/C/same" || old[5].Test != "other/shared/C/same" {
		t.Fatalf("missing ancestry: %+v", old)
	}
	repeated := parse(strings.ReplaceAll(first, "one", "changed diagnostic"))
	for i := range old {
		if old[i].ID != repeated[i].ID {
			t.Fatal("diagnostic changed identity")
		}
	}
	next := parse(strings.Replace(first, "<failure>two</failure>", "", 1))
	// Remove one indistinguishable duplicate: one should resolve, and every
	// remaining failure should continue, including the distinct error element.
	s, repo := fixture(t, "version=1\n[checks.check]\ncommand=\"echo check\"\n")
	store := func(failures []Failure) string {
		t.Helper()
		receipt, err := s.Submit(context.Background(), repo, "HEAD", false)
		if err != nil {
			t.Fatal(err)
		}
		r, err := s.Store.Run(receipt.RunID)
		if err != nil {
			t.Fatal(err)
		}
		c := r.Checks[0]
		c.State = Failed
		c.Failures = failures
		if err = s.Store.SaveCheck(c); err != nil {
			t.Fatal(err)
		}
		r.State = Failed
		if err = s.Store.SaveRun(r); err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	prior, current := store(old), store(next)
	diff, err := s.Compare(current, prior)
	if err != nil || !diff.Available || len(diff.Continuing) != 5 || len(diff.Resolved) != 1 || len(diff.New) != 0 {
		t.Fatalf("duplicate comparison collapsed: %+v %v", diff, err)
	}
	diff, err = s.Compare(prior, current)
	if err != nil || len(diff.New) != 1 || len(diff.Continuing) != 5 || len(diff.Resolved) != 0 {
		t.Fatalf("duplicate comparison reversed: %+v %v", diff, err)
	}
}
