package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReportSyntaxOverlappingSecretsDoNotChangeValidation(t *testing.T) {
	for _, tc := range []struct {
		name, secret, report string
		kind                 ReportKind
		state                State
	}{
		{"junit-pass", "a", `<testsuite><testcase name="a"/></testsuite>`, JUnit, Passed},
		{"junit-fail", "a", `<testsuite><testcase name="a" file="a.go" line="9"><failure message="a">a</failure></testcase></testsuite>`, JUnit, Failed},
		{"json-pass", `"`, "{\"Action\":\"pass\",\"Package\":\"p\"}\n", GoTest, Passed},
		{"json-fail", `"`, "{\"Action\":\"output\",\"Package\":\"p\",\"Test\":\"quote\\\"\",\"Output\":\"quote\\\"\"}\n{\"Action\":\"fail\",\"Package\":\"p\",\"Test\":\"quote\\\"\"}\n", GoTest, Failed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ACH_SYNTAX_SECRET", tc.secret)
			command := "cp payload report"
			if runtime.GOOS == "windows" {
				command = "Copy-Item payload report"
			}
			s, repo := fixture(t, fmt.Sprintf("version=1\n[checks.test]\ncommand=%q\n[[checks.test.environment]]\nname=\"ACH_SYNTAX_SECRET\"\nsecret=true\nrequired=true\n[[checks.test.reports]]\nkind=%q\npath=\"report\"\n", command, tc.kind))
			if err := os.WriteFile(filepath.Join(repo, "payload"), []byte(tc.report), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"add", "."}, {"-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "report fixture"}} {
				if _, err := Git(context.Background(), repo, args...); err != nil {
					t.Fatal(err)
				}
			}
			r := runFixture(t, s, repo)
			c := r.Checks[0]
			if r.State != tc.state || c.State != tc.state || len(c.Diagnostics) != 0 {
				t.Fatalf("secret corrupted parsing: %+v", r)
			}
			if s.GateRun(r).Passed != (tc.state == Passed) {
				t.Fatal("incorrect final validation")
			}
			if len(c.Reports) != 1 {
				t.Fatal("missing evidence")
			}
			page, err := s.Report(r.ID, c.Reports[0].ID, 0, 65536)
			if err != nil || page.Text != string(Redact([]byte(tc.report), []string{tc.secret})) {
				t.Fatal("unredacted evidence", err)
			}
			for _, f := range c.Failures {
				for _, field := range []string{f.Test, f.Message, f.File, f.Command} {
					if strings.Contains(field, tc.secret) {
						t.Fatal("secret in extracted report text")
					}
				}
			}
			if tc.state == Failed && len(c.Failures) != 1 {
				t.Fatal("lost structured failure", c.Failures)
			}
		})
	}
}
