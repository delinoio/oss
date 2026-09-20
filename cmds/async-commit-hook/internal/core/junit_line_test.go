package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestJUnitLineNumbersRemainWithinWireRange(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"1", 1}, {"42", 42}, {"2147483647", 2147483647},
		{"0", 0}, {"-1", 0}, {"-2147483648", 0},
		{"2147483648", 0}, {"4294967297", 0},
		{"9223372036854775808", 0}, {strings.Repeat("9", 100), 0},
		{"unknown", 0}, {"", 0},
	}
	var report strings.Builder
	report.WriteString("<testsuite>")
	for i, tc := range cases {
		fmt.Fprintf(&report, `<testcase name="case-%d" file="test.go" line="%s"><failure>failed</failure></testcase>`, i, tc.input)
	}
	report.WriteString("</testsuite>")
	failures, err := ParseReport(JUnit, []byte(report.String()), "test", "echo test", "")
	if err != nil || len(failures) != len(cases) {
		t.Fatalf("locations must not discard report failures: %+v %v", failures, err)
	}
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"echo test\"\n")
	receipt, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Store.Run(receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	c := r.Checks[0]
	c.Failures = failures
	if err = s.Store.SaveCheck(c); err != nil {
		t.Fatal(err)
	}
	r, err = s.Store.Run(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	wire := wireFailures(r.Checks[0].Failures)
	for i, tc := range cases {
		if failures[i].Line != tc.want || r.Checks[0].Failures[i].Line != tc.want || int(wire[i].Line) != tc.want || wire[i].File != "test.go" {
			t.Errorf("line %q: parsed=%d stored=%d wire=%d want=%d", tc.input, failures[i].Line, r.Checks[0].Failures[i].Line, wire[i].Line, tc.want)
		}
	}
	// Legacy records bypassed input validation. Reads and direct adapters must
	// still omit their bad location instead of narrowing it into a false line.
	c.Failures[0].Line = 1 << 31
	if _, err = s.Store.DB.Exec("UPDATE checks SET record=? WHERE id=?", Encode(c), c.ID); err != nil {
		t.Fatal(err)
	}
	r, err = s.Store.Run(r.ID)
	if err != nil || r.Checks[0].Failures[0].Line != 0 || wireFailures(c.Failures)[0].Line != 0 {
		t.Fatalf("legacy line escaped validation: %+v %v", r, err)
	}
}
