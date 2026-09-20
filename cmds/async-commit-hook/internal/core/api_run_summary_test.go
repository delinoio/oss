package core

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestRunListPagesOmitLargeCheckGraphsAndPreserveDetail(t *testing.T) {
	const count = 4000
	const pageGraphSize = 256
	command := "exit 0 # " + strings.Repeat("padding ", 24)
	configuration := func(size int) string {
		var config strings.Builder
		config.WriteString("version=1\n")
		for i := 0; i < size; i++ {
			fmt.Fprintf(&config, "[checks.check%04d]\ncommand=%q\n", i, command)
		}
		if config.Len() >= maxProjectConfigBytes {
			t.Fatal("fixture exceeds accepted configuration limit")
		}
		return config.String()
	}
	s, repo := fixture(t, configuration(pageGraphSize))
	plan, err := s.Plan(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	expectedCounts := map[string]uint32{}
	var smallBytes, largeBytes int
	for attempt := 0; attempt < 51; attempt++ {
		if attempt == 50 {
			// Keep one real 4,000-check graph for detail coverage. Repeating it
			// 51 times made SQLite fixture setup dominate the Windows CI budget.
			// Smaller graphs with longer commands retain a >4 MiB expanded page.
			if err := os.WriteFile(filepath.Join(repo, ProjectFile), []byte(configuration(count)), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"add", "."}, {"-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "large graph"}} {
				if _, err := Git(context.Background(), repo, args...); err != nil {
					t.Fatal(err)
				}
			}
			plan, err = s.Plan(context.Background(), repo, "")
			if err != nil {
				t.Fatal(err)
			}
		}
		plan.State = Failed
		plan.Diagnostics = []Diagnostic{{Code: "fixture", Message: "detail only"}}
		plan.Checks[0].Diagnostics = []Diagnostic{{Code: "fixture-check", Message: "check detail"}}
		plan.ID = ID()
		for i := range plan.Checks {
			plan.Checks[i].ID = ID()
		}
		if _, err := s.Store.InsertRun(&plan, ""); err != nil {
			t.Fatal(err)
		}
		expectedCounts[plan.ID] = uint32(len(plan.Checks))
		if attempt == 0 || attempt == 50 {
			b, err := protojson.Marshal((&API{s: s}).run(plan, true))
			if err != nil {
				t.Fatal(err)
			}
			if attempt == 0 {
				smallBytes = len(b)
			} else {
				largeBytes = len(b)
			}
		}
	}
	if 49*smallBytes+largeBytes <= 4*1024*1024 {
		t.Fatal("fixture no longer exercises oversized expanded list responses")
	}
	code, err := s.PairingCode()
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Pair(code, "large-page-test")
	if err != nil {
		t.Fatal(err)
	}
	request := func(method string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", "http://127.0.0.1:46309/async_commit_hook.v1.LocalService/"+method, bytes.NewReader(body))
		req.Header.Set("Origin", "https://ach.delino.io")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("%s transport failed: %d %s", method, response.Code, response.Body.String())
		}
		return response
	}
	for _, inbox := range []bool{false, true} {
		payload, err := protojson.Marshal(&pb.ListRunsRequest{RepositoryId: plan.RepositoryID, Inbox: inbox})
		if err != nil {
			t.Fatal(err)
		}
		response := request("ListRuns", payload)
		if response.Body.Len() > 1024*1024 {
			t.Fatalf("list exceeds bounded summary allowance: %d", response.Body.Len())
		}
		var page pb.ListRunsResponse
		if err := protojson.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Runs) != 50 || page.NextCursor == "" {
			t.Fatalf("wrong first page: %d", len(page.Runs))
		}
		for _, run := range page.Runs {
			if run.CheckCount == nil || run.GetCheckCount() != expectedCounts[run.Id] || len(run.Checks) != 0 || len(run.Diagnostics) != 0 {
				t.Fatal("unbounded or incorrect summary", run.Id)
			}
		}
		payload, err = protojson.Marshal(&pb.ListRunsRequest{RepositoryId: plan.RepositoryID, Inbox: inbox, Cursor: page.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
		next := request("ListRuns", payload)
		var last pb.ListRunsResponse
		if err := protojson.Unmarshal(next.Body.Bytes(), &last); err != nil {
			t.Fatal(err)
		}
		if len(last.Runs) != 1 || last.NextCursor != "" {
			t.Fatal("pagination lost final run")
		}
		t.Logf("inbox=%t: expanded page %d bytes; summary page %d bytes", inbox, 49*smallBytes+largeBytes, response.Body.Len())
	}
	payload, err := protojson.Marshal(&pb.GetRunRequest{RunId: plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	response := request("GetRun", payload)
	var detail pb.GetRunResponse
	if err := protojson.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Run.GetCheckCount() != count || len(detail.Run.Checks) != count || len(detail.Run.Diagnostics) != 1 || detail.Run.Checks[0].Command != command || len(detail.Run.Checks[0].Diagnostics) != 1 {
		t.Fatal("detail lost complete checks or diagnostics")
	}
}
