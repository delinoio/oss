package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestSynthesizedDiagnosticsShareFailureSummaryBounds(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand='"+strings.Repeat("x", 100*1024)+"'\n")
	receipt, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Store.Run(receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	c := r.Checks[0]
	for i := 0; i < 400; i++ {
		c.Diagnostics = append(c.Diagnostics, Diagnostic{Code: "report-missing", Message: fmt.Sprintf("report unavailable: %d", i)})
	}
	c.State = Failed
	if err = s.Store.SaveCheck(c); err != nil {
		t.Fatal(err)
	}
	r, err = s.Store.Run(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Both newly synthesized and legacy structured failures share one budget.
	r.Checks[0].Failures = []Failure{{ID: "legacy", Message: strings.Repeat("한", 10*1024)}}
	failures := Failures(r)
	used := 0
	for _, f := range failures {
		used += len(Encode(f)) + 1
		for _, value := range []string{f.Check, f.Test, f.Command, f.Message, f.File} {
			if len(value) > failureFieldBytes || !utf8.ValidString(value) {
				t.Fatal("unbounded failure field")
			}
		}
	}
	if used > runFailureBytes || len(failures) >= 401 {
		t.Fatal("aggregate omission failed", used, len(failures))
	}
	if failures[len(failures)-1].ID != Hash([]byte("run/failure-summaries-truncated")) {
		t.Fatal("missing truncation notice")
	}
	response, err := (&API{s: s}).GetFailures(context.Background(), connect.NewRequest(&pb.GetFailuresRequest{RunId: r.ID}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := protojson.Marshal(response.Msg)
	if err != nil || len(data) >= 8*1024*1024 {
		t.Fatal("failure response is unavailable", len(data), err)
	}
}
