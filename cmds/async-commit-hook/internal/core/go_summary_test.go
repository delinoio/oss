package core

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestGoSummaryBoundsDistinctFailuresAndValidatesDiscardedTail(t *testing.T) {
	for _, action := range []string{"fail", "build-fail"} {
		t.Run(action, func(t *testing.T) {
			var report bytes.Buffer
			for i := 0; i < 100000; i++ {
				fmt.Fprintf(&report, "{\"Action\":%q,\"Package\":\"p\",\"Test\":\"Test%d\",\"ImportPath\":\"build%d\"}\n", action, i, i)
			}
			failures, truncated, err := parseReportSummaries(GoTest, report.Bytes(), "check", "command", "log", nil)
			if err != nil || !truncated || len(failures) == 0 || len(failures) >= 100000 {
				t.Fatal("unbounded or absent summary", len(failures), truncated, err)
			}
			used := 0
			for i, f := range failures {
				used += len(Encode(f)) + 1
				want := Hash(Encode([]any{"check", "go", [2]string{"p", fmt.Sprintf("Test%d", i)}, 1}))
				if action == "build-fail" {
					want = Hash(Encode([]any{"check", "go-build", fmt.Sprintf("build%d", i), 1}))
				}
				if f.ID != want {
					t.Fatal("retained failure identity changed", i)
				}
			}
			if used > runFailureBytes {
				t.Fatal("parse-time summary budget exceeded", used)
			}
			for _, tail := range []string{`{`, `{"Action":"unknown"}`, `{"Action":"build-fail"}`, `{"Action":"run","Package":"unfinished"}`} {
				input := append(append([]byte(nil), report.Bytes()...), tail...)
				if _, _, err := parseReportSummaries(GoTest, input, "check", "command", "log", nil); err == nil {
					t.Fatal("invalid discarded tail accepted", tail)
				}
			}
		})
	}
}

func TestGoSummaryPreservesSparseRepeatedIdentitiesAndRedaction(t *testing.T) {
	var report bytes.Buffer
	for i := 0; i < 100000; i++ {
		fmt.Fprintf(&report, "{\"Action\":\"pass\",\"Package\":\"p\",\"Test\":\"Test%d\"}\n", i)
	}
	secret := "private-value-crossing-boundary"
	body := strings.Repeat("x", failureFieldBytes-8) + secret
	fmt.Fprintf(&report, "{\"Action\":\"skip\",\"Package\":\"p\",\"Test\":\"Test0\"}\n{\"Action\":\"output\",\"Package\":\"p\",\"Test\":\"Test0\",\"Output\":%q}\n{\"Action\":\"fail\",\"Package\":\"p\",\"Test\":\"Test0\"}\n", body)
	failures, truncated, err := parseReportSummaries(GoTest, report.Bytes(), "check", "command", "log", []string{secret})
	wantID := Hash(Encode([]any{"check", "go", [2]string{"p", "Test0"}, 3}))
	wantText, _ := redactedFailureText(body, []string{secret})
	if err != nil || !truncated || len(failures) != 1 || failures[0].ID != wantID || failures[0].Message != wantText || strings.Contains(failures[0].Message, "private-") {
		t.Fatal("sparse history or pre-truncation redaction changed", failures, truncated, err)
	}
}
