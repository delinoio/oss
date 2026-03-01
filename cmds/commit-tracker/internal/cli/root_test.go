package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

func TestExecuteIngestFlagUsageDoesNotLeakEnvToken(t *testing.T) {
	t.Setenv("COMMIT_TRACKER_TOKEN", "super-secret-token")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"ingest", "--unknown-flag"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2, got=%d", code)
	}
	if strings.Contains(stderr.String(), "super-secret-token") {
		t.Fatalf("stderr should not include COMMIT_TRACKER_TOKEN, got=%s", stderr.String())
	}
}

func TestExecuteIngestSubjectFallsBackToResolvedTokenAfterParse(t *testing.T) {
	t.Setenv("COMMIT_TRACKER_TOKEN", "env-token")
	t.Setenv("COMMIT_TRACKER_SUBJECT", "")

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
		"--token", "cli-token",
		"--input", inputPath,
	}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected success code 0, got=%d stderr=%s", code, stderr.String())
	}

	if fake.lastAuthorization != "Bearer cli-token" {
		t.Fatalf("unexpected authorization header: %s", fake.lastAuthorization)
	}
	if fake.lastSubject != "cli-token" {
		t.Fatalf("expected subject fallback to parsed token, got=%s", fake.lastSubject)
	}
}

func TestExecuteReportRequiresPullRequestContext(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "acme/repo")
	t.Setenv("GITHUB_SHA", "head-commit")
	t.Setenv("GITHUB_EVENT_PATH", "")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"report", "--server", "http://127.0.0.1:8091", "--token", "token-1"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2, got=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "pull request is required") {
		t.Fatalf("expected pull request validation error, got=%s", stderr.String())
	}
}

func TestExecuteReportInvalidEventJSON(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "acme/repo")
	t.Setenv("GITHUB_SHA", "head-commit")

	eventPath := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(eventPath, []byte("{invalid-json"), 0o600); err != nil {
		t.Fatalf("write event payload: %v", err)
	}
	t.Setenv("GITHUB_EVENT_PATH", eventPath)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"report", "--server", "http://127.0.0.1:8091", "--token", "token-1"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2, got=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "parse GITHUB_EVENT_PATH JSON") {
		t.Fatalf("expected event parse error, got=%s", stderr.String())
	}
}

func TestExecuteReportSuccessUsesEnvAndEventFallback(t *testing.T) {
	fake := &fakeProviderReportService{response: &committrackerv1.PublishPullRequestReportResponse{
		AggregateEvaluation: committrackerv1.EvaluationLevel_EVALUATION_LEVEL_WARN,
		CommentUrl:          "https://example.com/comment",
		StatusUrl:           "https://example.com/status",
	}}
	server := newReportTestServer(t, fake)

	t.Setenv("GITHUB_REPOSITORY", "acme/repo")
	t.Setenv("GITHUB_SHA", "merge-commit-sha")
	t.Setenv("GITHUB_EVENT_PATH", writePullRequestEvent(t, 42, "base-commit", "pr-head-commit"))

	outputPath := filepath.Join(t.TempDir(), "github-output.txt")
	t.Setenv("GITHUB_OUTPUT", outputPath)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{
		"report",
		"--server", server,
		"--token", "token-1",
		"--subject", "ci-bot",
		"--metric-key", "binary-size",
	}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected success code 0, got=%d stderr=%s", code, stderr.String())
	}

	if fake.lastRequest == nil {
		t.Fatal("expected report request to be received")
	}
	if fake.lastRequest.GetProvider() != committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB {
		t.Fatalf("unexpected provider: %s", fake.lastRequest.GetProvider().String())
	}
	if fake.lastRequest.GetRepository() != "acme/repo" {
		t.Fatalf("unexpected repository: %s", fake.lastRequest.GetRepository())
	}
	if fake.lastRequest.GetPullRequest() != 42 {
		t.Fatalf("unexpected pull request: %d", fake.lastRequest.GetPullRequest())
	}
	if fake.lastRequest.GetBaseCommitSha() != "base-commit" {
		t.Fatalf("unexpected base commit: %s", fake.lastRequest.GetBaseCommitSha())
	}
	if fake.lastRequest.GetHeadCommitSha() != "pr-head-commit" {
		t.Fatalf("unexpected head commit: %s", fake.lastRequest.GetHeadCommitSha())
	}
	if fake.lastRequest.GetEnvironment() != "ci" {
		t.Fatalf("unexpected environment: %s", fake.lastRequest.GetEnvironment())
	}
	if len(fake.lastRequest.GetMetricKeys()) != 1 || fake.lastRequest.GetMetricKeys()[0] != "binary-size" {
		t.Fatalf("unexpected metric keys: %#v", fake.lastRequest.GetMetricKeys())
	}
	if fake.lastAuthorization != "Bearer token-1" {
		t.Fatalf("unexpected authorization header: %s", fake.lastAuthorization)
	}
	if fake.lastSubject != "ci-bot" {
		t.Fatalf("unexpected subject header: %s", fake.lastSubject)
	}

	if !strings.Contains(stdout.String(), `"aggregateEvaluation":"EVALUATION_LEVEL_WARN"`) {
		t.Fatalf("expected aggregate evaluation in output, got=%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"commentUrl":"https://example.com/comment"`) {
		t.Fatalf("expected comment URL in output, got=%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"statusUrl":"https://example.com/status"`) {
		t.Fatalf("expected status URL in output, got=%s", stdout.String())
	}

	outputData, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read github output file: %v", err)
	}
	outputString := string(outputData)
	if !strings.Contains(outputString, "aggregate_evaluation=EVALUATION_LEVEL_WARN") {
		t.Fatalf("expected aggregate_evaluation in github output, got=%s", outputString)
	}
	if !strings.Contains(outputString, "pull_request=42") {
		t.Fatalf("expected pull_request in github output, got=%s", outputString)
	}
	if !strings.Contains(outputString, "base_commit_sha=base-commit") {
		t.Fatalf("expected base commit in github output, got=%s", outputString)
	}
	if !strings.Contains(outputString, "head_commit_sha=pr-head-commit") {
		t.Fatalf("expected head commit in github output, got=%s", outputString)
	}
}

func TestExecuteReportFlagsOverrideEventAndEnv(t *testing.T) {
	fake := &fakeProviderReportService{response: &committrackerv1.PublishPullRequestReportResponse{
		AggregateEvaluation: committrackerv1.EvaluationLevel_EVALUATION_LEVEL_PASS,
	}}
	server := newReportTestServer(t, fake)

	t.Setenv("GITHUB_REPOSITORY", "env/repo")
	t.Setenv("GITHUB_SHA", "env-head")
	t.Setenv("GITHUB_EVENT_PATH", writePullRequestEvent(t, 77, "env-base", "event-head"))

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{
		"report",
		"--server", server,
		"--token", "token-1",
		"--provider", "github",
		"--repository", "flag/repo",
		"--pull-request", "7",
		"--base-commit", "flag-base",
		"--head-commit", "flag-head",
		"--environment", "staging",
		"--metric-key", "binary-size",
		"--metric-key", "p95-latency",
		"--fail-on", "never",
	}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected success code 0, got=%d stderr=%s", code, stderr.String())
	}

	if fake.lastRequest == nil {
		t.Fatal("expected report request")
	}
	if fake.lastRequest.GetRepository() != "flag/repo" {
		t.Fatalf("expected flag repository, got=%s", fake.lastRequest.GetRepository())
	}
	if fake.lastRequest.GetPullRequest() != 7 {
		t.Fatalf("expected flag pull request, got=%d", fake.lastRequest.GetPullRequest())
	}
	if fake.lastRequest.GetBaseCommitSha() != "flag-base" {
		t.Fatalf("expected flag base commit, got=%s", fake.lastRequest.GetBaseCommitSha())
	}
	if fake.lastRequest.GetHeadCommitSha() != "flag-head" {
		t.Fatalf("expected flag head commit, got=%s", fake.lastRequest.GetHeadCommitSha())
	}
	if fake.lastRequest.GetEnvironment() != "staging" {
		t.Fatalf("expected flag environment, got=%s", fake.lastRequest.GetEnvironment())
	}
	if got := strings.Join(fake.lastRequest.GetMetricKeys(), ","); got != "binary-size,p95-latency" {
		t.Fatalf("unexpected metric keys order: %s", got)
	}
}

func TestExecuteReportFailOnWarnReturnsExitOne(t *testing.T) {
	fake := &fakeProviderReportService{response: &committrackerv1.PublishPullRequestReportResponse{
		AggregateEvaluation: committrackerv1.EvaluationLevel_EVALUATION_LEVEL_WARN,
	}}
	server := newReportTestServer(t, fake)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{
		"report",
		"--server", server,
		"--token", "token-1",
		"--repository", "acme/repo",
		"--pull-request", "5",
		"--base-commit", "base",
		"--head-commit", "head",
		"--fail-on", "warn",
	}, stdout, stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1 for warn threshold, got=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "triggered failure threshold") {
		t.Fatalf("expected fail-on warning message, got=%s", stderr.String())
	}
}

func TestExecuteReportHeadCommitFallsBackToGitHubSHAWhenEventHeadMissing(t *testing.T) {
	fake := &fakeProviderReportService{response: &committrackerv1.PublishPullRequestReportResponse{
		AggregateEvaluation: committrackerv1.EvaluationLevel_EVALUATION_LEVEL_PASS,
	}}
	server := newReportTestServer(t, fake)

	t.Setenv("GITHUB_REPOSITORY", "acme/repo")
	t.Setenv("GITHUB_SHA", "fallback-head")

	eventPath := filepath.Join(t.TempDir(), "event.json")
	eventPayload := `{"pull_request":{"number":15,"base":{"sha":"base-commit"}}}`
	if err := os.WriteFile(eventPath, []byte(eventPayload), 0o600); err != nil {
		t.Fatalf("write event payload: %v", err)
	}
	t.Setenv("GITHUB_EVENT_PATH", eventPath)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{
		"report",
		"--server", server,
		"--token", "token-1",
	}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected success code 0, got=%d stderr=%s", code, stderr.String())
	}

	if fake.lastRequest == nil {
		t.Fatal("expected report request")
	}
	if fake.lastRequest.GetHeadCommitSha() != "fallback-head" {
		t.Fatalf("expected GITHUB_SHA fallback, got=%s", fake.lastRequest.GetHeadCommitSha())
	}
}

func TestExecuteReportFailOnNeverReturnsZeroForFailEvaluation(t *testing.T) {
	fake := &fakeProviderReportService{response: &committrackerv1.PublishPullRequestReportResponse{
		AggregateEvaluation: committrackerv1.EvaluationLevel_EVALUATION_LEVEL_FAIL,
	}}
	server := newReportTestServer(t, fake)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{
		"report",
		"--server", server,
		"--token", "token-1",
		"--repository", "acme/repo",
		"--pull-request", "5",
		"--base-commit", "base",
		"--head-commit", "head",
		"--fail-on", "never",
	}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0 for fail-on=never, got=%d stderr=%s", code, stderr.String())
	}
}

func TestExecuteReportRPCFailureReturnsOne(t *testing.T) {
	fake := &fakeProviderReportService{err: connect.NewError(connect.CodeInternal, errors.New("boom"))}
	server := newReportTestServer(t, fake)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{
		"report",
		"--server", server,
		"--token", "token-1",
		"--repository", "acme/repo",
		"--pull-request", "5",
		"--base-commit", "base",
		"--head-commit", "head",
	}, stdout, stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1 for rpc error, got=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "report failed: internal: boom") {
		t.Fatalf("expected rendered rpc error, got=%s", stderr.String())
	}
}

func TestExecuteReportWritesExplicitGitHubOutputPath(t *testing.T) {
	fake := &fakeProviderReportService{response: &committrackerv1.PublishPullRequestReportResponse{
		AggregateEvaluation: committrackerv1.EvaluationLevel_EVALUATION_LEVEL_PASS,
	}}
	server := newReportTestServer(t, fake)

	envOutputPath := filepath.Join(t.TempDir(), "env-output.txt")
	explicitOutputPath := filepath.Join(t.TempDir(), "explicit-output.txt")
	t.Setenv("GITHUB_OUTPUT", envOutputPath)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{
		"report",
		"--server", server,
		"--token", "token-1",
		"--repository", "acme/repo",
		"--pull-request", "5",
		"--base-commit", "base",
		"--head-commit", "head",
		"--github-output", explicitOutputPath,
	}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected success code 0, got=%d stderr=%s", code, stderr.String())
	}

	explicitData, err := os.ReadFile(explicitOutputPath)
	if err != nil {
		t.Fatalf("read explicit github output file: %v", err)
	}
	if !strings.Contains(string(explicitData), "aggregate_evaluation=EVALUATION_LEVEL_PASS") {
		t.Fatalf("expected explicit github output content, got=%s", string(explicitData))
	}
	if _, err := os.Stat(envOutputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected env output path to remain unused, err=%v", err)
	}
}

func TestExecuteReportInvalidFailOnValue(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{
		"report",
		"--server", "http://127.0.0.1:8091",
		"--token", "token-1",
		"--fail-on", "invalid",
	}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2 for invalid fail-on, got=%d", code)
	}
	if !strings.Contains(stderr.String(), "invalid --fail-on value") {
		t.Fatalf("expected invalid fail-on error, got=%s", stderr.String())
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

type fakeProviderReportService struct {
	lastRequest       *committrackerv1.PublishPullRequestReportRequest
	lastAuthorization string
	lastSubject       string
	response          *committrackerv1.PublishPullRequestReportResponse
	err               error
}

func (s *fakeProviderReportService) PublishPullRequestReport(
	_ context.Context,
	request *connect.Request[committrackerv1.PublishPullRequestReportRequest],
) (*connect.Response[committrackerv1.PublishPullRequestReportResponse], error) {
	s.lastRequest = request.Msg
	s.lastAuthorization = request.Header().Get("Authorization")
	s.lastSubject = request.Header().Get("X-Commit-Tracker-Subject")
	if s.err != nil {
		return nil, s.err
	}
	if s.response == nil {
		s.response = &committrackerv1.PublishPullRequestReportResponse{}
	}
	return connect.NewResponse(s.response), nil
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

func newReportTestServer(t *testing.T, service committrackerv1connect.ProviderReportServiceHandler) string {
	t.Helper()
	path, handler := committrackerv1connect.NewProviderReportServiceHandler(service)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL
}

func writePullRequestEvent(t *testing.T, pullRequest int64, baseCommitSHA string, headCommitSHA string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "event.json")
	payload := fmt.Sprintf(
		`{"pull_request":{"number":%d,"base":{"sha":"%s"},"head":{"sha":"%s"}}}`,
		pullRequest,
		baseCommitSHA,
		headCommitSHA,
	)
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write event payload: %v", err)
	}
	return path
}
