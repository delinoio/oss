package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	committrackerv1 "github.com/delinoio/oss/servers/commit-tracker/gen/proto/committracker/v1"
	committrackerv1connect "github.com/delinoio/oss/servers/commit-tracker/gen/proto/committracker/v1/committrackerv1connect"
)

func TestExecuteIngestRequiresInput(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"ingest", "--server", "http://127.0.0.1:8091", "--token", "token-1"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2, got=%d", code)
	}
	if !strings.Contains(stderr.String(), "ingest requires --input") {
		t.Fatalf("expected missing input error, got=%s", stderr.String())
	}
}

func TestExecuteIngestInvalidJSON(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	inputPath := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(inputPath, []byte("{invalid-json"), 0o600); err != nil {
		t.Fatalf("write input file: %v", err)
	}

	code := execute([]string{"ingest", "--server", "http://127.0.0.1:8091", "--token", "token-1", "--input", inputPath}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2 for invalid payload, got=%d", code)
	}
	if !strings.Contains(stderr.String(), "parse input JSON") {
		t.Fatalf("expected JSON parse error, got=%s", stderr.String())
	}
}

func TestExecuteIngestSuccess(t *testing.T) {
	fake := &fakeMetricIngestionService{}
	server := newIngestionTestServer(t, fake)

	payload := `{
	  "provider": "github",
	  "repository": "acme/repo",
	  "branch": "main",
	  "commitSha": "abc123",
	  "runId": "run-001",
	  "environment": "ci",
	  "metrics": [
	    {
	      "metricKey": "binary-size",
	      "displayName": "Binary Size",
	      "unit": "bytes",
	      "valueKind": "unit-number",
	      "direction": "decrease-is-better",
	      "warningThresholdPercent": 5,
	      "failThresholdPercent": 10,
	      "value": 1234
	    }
	  ]
	}`

	inputPath := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(inputPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{
		"ingest",
		"--server", server,
		"--token", "token-1",
		"--subject", "ci-bot",
		"--input", inputPath,
	}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected success code 0, got=%d stderr=%s", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), `"upsertedCount":1`) {
		t.Fatalf("expected upsertedCount in output, got=%s", stdout.String())
	}

	if fake.lastRequest == nil {
		t.Fatal("expected ingestion request to be received")
	}
	if fake.lastRequest.GetRepository() != "acme/repo" {
		t.Fatalf("unexpected repository: %s", fake.lastRequest.GetRepository())
	}
	if fake.lastRequest.GetCommitSha() != "abc123" {
		t.Fatalf("unexpected commit sha: %s", fake.lastRequest.GetCommitSha())
	}
	if fake.lastAuthorization != "Bearer token-1" {
		t.Fatalf("unexpected authorization header: %s", fake.lastAuthorization)
	}
	if fake.lastSubject != "ci-bot" {
		t.Fatalf("unexpected subject header: %s", fake.lastSubject)
	}
}

type fakeMetricIngestionService struct {
	lastRequest       *committrackerv1.UpsertCommitMetricsRequest
	lastAuthorization string
	lastSubject       string
}

func (s *fakeMetricIngestionService) UpsertCommitMetrics(
	_ context.Context,
	request *connect.Request[committrackerv1.UpsertCommitMetricsRequest],
) (*connect.Response[committrackerv1.UpsertCommitMetricsResponse], error) {
	s.lastRequest = request.Msg
	s.lastAuthorization = request.Header().Get("Authorization")
	s.lastSubject = request.Header().Get("X-Commit-Tracker-Subject")
	return connect.NewResponse(&committrackerv1.UpsertCommitMetricsResponse{UpsertedCount: int32(len(request.Msg.GetMetrics()))}), nil
}

func newIngestionTestServer(t *testing.T, service committrackerv1connect.MetricIngestionServiceHandler) string {
	t.Helper()
	path, handler := committrackerv1connect.NewMetricIngestionServiceHandler(service)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL
}
