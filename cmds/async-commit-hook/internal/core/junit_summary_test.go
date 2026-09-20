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

func TestJUnitFailureBudgetContinuesValidatingDiscardedTail(t *testing.T) {
	prefix := "<testsuite><testcase>" + strings.Repeat("<failure/>", 100000)
	failures, truncated, err := parseReportSummaries(JUnit, []byte(prefix+"</testcase></testsuite>"), "check", "command", "log", nil)
	if err != nil || !truncated || len(failures) == 0 || len(failures) >= 100000 {
		t.Fatal("unbounded or missing failure prefix", len(failures), truncated, err)
	}
	used := 0
	for _, failure := range failures {
		used += len(Encode(failure)) + 1
	}
	if used > runFailureBytes {
		t.Fatal("parse-time failure budget exceeded", used)
	}
	for _, tail := range []string{"<failure></error></testcase></testsuite>", "</testcase><testsuite failures=\"-1\"/></testsuite>", "</testcase></testsuite>trailing text"} {
		if _, _, err := parseReportSummaries(JUnit, []byte(prefix+tail), "check", "command", "log", nil); err == nil {
			t.Fatal("invalid discarded tail accepted")
		}
	}
}

func TestJUnitSummaryRedactsBeforeFieldTruncation(t *testing.T) {
	secret := "private-value-crossing-boundary"
	body := strings.Repeat("x", failureFieldBytes-8) + secret
	report := []byte("<testsuite><testcase><failure>" + body + "</failure></testcase></testsuite>")
	failures, truncated, err := parseReportSummaries(JUnit, report, "check", "command", "log", []string{secret})
	if err != nil || !truncated || len(failures) != 1 {
		t.Fatal(failures, truncated, err)
	}
	want, _ := redactedFailureText(body, []string{secret})
	if failures[0].Message != want || strings.Contains(failures[0].Message, "private-") {
		t.Fatal("secret prefix leaked or redaction changed")
	}
}

func TestBoundedReportSummaryRetainsPaginatedEvidenceAndDiagnostic(t *testing.T) {
	for _, kind := range []ReportKind{JUnit, GoTest} {
		t.Run(string(kind), func(t *testing.T) { testBoundedReportEvidence(t, kind) })
	}
}

func testBoundedReportEvidence(t *testing.T, kind ReportKind) {
	command := "cp payload report"
	if runtime.GOOS == "windows" {
		command = "Copy-Item payload report"
	}
	s, repo := fixture(t, fmt.Sprintf("version=1\n[checks.test]\ncommand=%q\n[[checks.test.reports]]\nkind=%q\npath=\"report\"\n", command, kind))
	var report strings.Builder
	if kind == JUnit {
		report.WriteString("<testsuite>")
		for i := 0; i < 20000; i++ {
			fmt.Fprintf(&report, "<testcase name=\"test-%d\"><failure/></testcase>", i)
		}
		report.WriteString("</testsuite><!-- END-MARKER -->")
	} else {
		for i := 0; i < 20000; i++ {
			fmt.Fprintf(&report, "{\"Action\":\"fail\",\"Package\":\"p\",\"Test\":\"Test%d\"}\n", i)
		}
		report.WriteString(`{"Action":"output","Package":"p","Output":"END-MARKER"}`)
	}
	if err := os.WriteFile(filepath.Join(repo, "payload"), []byte(report.String()), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "many failures"}} {
		if _, err := Git(context.Background(), repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	run := runFixture(t, s, repo)
	check := run.Checks[0]
	if run.State != Failed || len(check.Reports) != 1 || len(check.Failures) == 0 {
		t.Fatal("failure evidence lost", run.State)
	}
	found := false
	for _, diagnostic := range check.Diagnostics {
		found = found || diagnostic.Code == "failure-summaries-truncated"
	}
	if !found {
		t.Fatal("parse-time truncation was not disclosed")
	}
	page, err := s.Report(run.ID, check.Reports[0].ID, int64(report.Len()-64), 64)
	if err != nil || !strings.Contains(page.Text, "END-MARKER") {
		t.Fatal("complete report tail was not retained", err)
	}
}
