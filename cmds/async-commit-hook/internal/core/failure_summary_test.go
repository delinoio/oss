package core

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"strings"
	"testing"
)

func TestLargeJUnitSummariesStayReadableWithinRunBudget(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.a]\ncommand=\"unused\"\n[checks.b]\ncommand=\"unused\"\n")
	receipt, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.Store.Run(receipt.RunID)
	report := []byte(`<testsuite><testcase name="known-test"><failure>` + strings.Repeat("한", 3*1024*1024) + `END-MARKER</failure></testcase></testsuite>`)
	for _, c := range r.Checks {
		failures, err := ParseReport(JUnit, report, c.Name, "unused", "")
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 400; i++ {
			c.Failures = append(c.Failures, failures[0])
		}
		ev, err := s.SaveEvidence(r.ID, "junit.xml", report)
		if err != nil {
			t.Fatal(err)
		}
		c.Reports = []Evidence{ev}
		c.State = Failed
		if err = s.Store.SaveCheck(c); err != nil {
			t.Fatal(err)
		}
	}
	r.State = Failed
	if err = s.Store.SaveRun(r); err != nil {
		t.Fatal(err)
	}
	stored, err := s.Store.Run(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	used := 0
	for _, c := range stored.Checks {
		for _, f := range c.Failures {
			used += len(Encode(f)) + 1
			if len(f.Message) > failureFieldBytes {
				t.Fatal("unbounded failure message")
			}
		}
		found := false
		for _, d := range c.Diagnostics {
			found = found || d.Code == "failure-summaries-truncated"
		}
		if !found {
			t.Fatal("truncation not disclosed")
		}
		page, err := s.Report(r.ID, c.Reports[0].ID, int64(len(report)-64), 64)
		if err != nil || !strings.Contains(page.Text, "END-MARKER") {
			t.Fatal("full report evidence was lost", err)
		}
	}
	if used > runFailureBytes {
		t.Fatalf("aggregate exceeds budget: %d", used)
	}
	api := &API{s: s}
	run, err := api.GetRun(context.Background(), connect.NewRequest(&pb.GetRunRequest{RunId: r.ID}))
	if err != nil {
		t.Fatal(err)
	}
	failures, err := api.GetFailures(context.Background(), connect.NewRequest(&pb.GetFailuresRequest{RunId: r.ID}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := protojson.Marshal(run.Msg)
	if err != nil || len(data) >= 8*1024*1024 {
		t.Fatal("run transport exceeds cap", err)
	}
	data, err = protojson.Marshal(failures.Msg)
	if err != nil || len(data) >= 8*1024*1024 {
		t.Fatal("failure transport exceeds cap", err)
	}
}
