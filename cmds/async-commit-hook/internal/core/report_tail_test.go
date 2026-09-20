package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestGoReportLargeRepeatedOutputUsesBoundedTailAllocations(t *testing.T) {
	var report, combined bytes.Buffer
	enc := json.NewEncoder(&report)
	emit := func(action, output string) {
		t.Helper()
		if err := enc.Encode(map[string]string{"Action": action, "Package": "p", "Test": "TestLarge", "Output": output}); err != nil {
			t.Fatal(err)
		}
	}
	emit("run", "")
	for i := 0; i < 50000; i++ {
		s := fmt.Sprintf("%06d: repeated output\n", i)
		combined.WriteString(s)
		emit("output", s)
	}
	emit("fail", "")
	expected := combined.String()
	expected = strings.TrimSpace(expected[len(expected)-reportOutputLimit:])
	// Summary bounding now happens in the parser, after selecting the same raw tail.
	expected, _ = redactedFailureText(expected, nil)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	failures, err := ParseReport(GoTest, report.Bytes(), "check", "go test -json", "log")
	runtime.ReadMemStats(&after)
	if err != nil || len(failures) != 1 || failures[0].Message != expected {
		t.Fatal("chronological report tail was lost", err)
	}
	// The prior concatenation implementation allocates multiple GiB for this
	// valid report. This generous byte budget checks allocation complexity, not
	// machine speed, and includes JSON decoding plus race instrumentation.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 256*1024*1024 {
		t.Fatalf("report tail allocated %d bytes", allocated)
	}
}
func TestReportOutputTailWrapAndOversizedEvents(t *testing.T) {
	var tail reportOutputTail
	var all strings.Builder
	for _, s := range []string{"short", strings.Repeat("x", reportOutputLimit+17), "한국어🙂", strings.Repeat("y", reportOutputLimit-3), "last"} {
		all.WriteString(s)
		tail.append(s)
		expected := all.String()
		if len(expected) > reportOutputLimit {
			expected = expected[len(expected)-reportOutputLimit:]
		}
		if tail.String() != expected || len(tail.data) > reportOutputLimit || cap(tail.data) > reportOutputLimit {
			t.Fatal("tail ordering or memory bound violated")
		}
	}
}

func TestGoReportAcceptsLargeSingleEvents(t *testing.T) {
	for _, action := range []string{"pass", "fail"} {
		t.Run(action, func(t *testing.T) {
			body := strings.Repeat("x", 5*1024*1024) + "END-MARKER"
			event, err := json.Marshal(map[string]string{"Action": "output", "Package": "p", "Test": "large", "Output": body})
			if err != nil {
				t.Fatal(err)
			}
			report := append([]byte(`{"Action":"run","Package":"p","Test":"large"}`+"\r\n"), event...)
			report = append(report, []byte("\r\n"+`{"Action":"`+action+`","Package":"p","Test":"large"}`)...)
			failures, err := ParseReport(GoTest, report, "test", "go test -json", "log")
			if err != nil {
				t.Fatal("valid large event rejected", err)
			}
			if action == "pass" && len(failures) != 0 {
				t.Fatal(failures)
			}
			expected, _ := redactedFailureText(body[len(body)-reportOutputLimit:], nil)
			if action == "fail" && (len(failures) != 1 || failures[0].Message != expected) {
				t.Fatal("large event lost bounded failure tail")
			}
			if _, err = ParseReport(GoTest, report[:len(report)-1], "test", "go test -json", "log"); err == nil {
				t.Fatal("truncated final event accepted")
			}
		})
	}
}
