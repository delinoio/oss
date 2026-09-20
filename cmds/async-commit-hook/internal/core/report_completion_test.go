package core

import (
	"fmt"
	"strings"
	"testing"
)

func TestGoReportCompletedEntriesPreserveCompletenessAndIterations(t *testing.T) {
	var report strings.Builder
	report.WriteString(`{"Action":"start","Package":"p"}` + "\n")
	for i := 0; i < 100000; i++ {
		fmt.Fprintf(&report, "{\"Action\":\"run\",\"Package\":\"p\",\"Test\":\"Test%d\"}\n{\"Action\":\"pass\",\"Package\":\"p\",\"Test\":\"Test%d\"}\n", i, i)
	}
	report.WriteString(`{"Action":"pass","Package":"p"}` + "\n")
	if failures, err := parseGoTest([]byte(report.String()), "check", "command", "log"); err != nil || len(failures) != 0 {
		t.Fatal(failures, err)
	}
	// Reopening an already completed identity must restore unfinished tracking.
	reopened := report.String() + `{"Action":"run","Package":"p","Test":"Test1"}` + "\n"
	if _, err := parseGoTest([]byte(reopened), "check", "command", "log"); err == nil || !strings.Contains(err.Error(), "unfinished") {
		t.Fatal("unfinished repeated test lost", err)
	}
	finished := reopened + `{"Action":"fail","Package":"p","Test":"Test1"}` + "\n"
	failures, err := parseGoTest([]byte(finished), "check", "command", "log")
	want := Hash(Encode([]any{"check", "go", [2]string{"p", "Test1"}, 2}))
	if err != nil || len(failures) != 1 || failures[0].ID != want {
		t.Fatal("completed occurrence was not retained", failures, err)
	}
	if _, err := parseGoTest([]byte(reopened+`{"Action":"skip","Package":"p","Test":"Test1"}`), "check", "command", "log"); err != nil {
		t.Fatal(err)
	}
}
