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
