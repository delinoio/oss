package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"

	committrackerv1 "github.com/delinoio/oss/servers/commit-tracker/gen/proto/committracker/v1"
	committrackerv1connect "github.com/delinoio/oss/servers/commit-tracker/gen/proto/committracker/v1/committrackerv1connect"
	"github.com/delinoio/oss/servers/commit-tracker/internal/logging"
)

func TestUpsertCommitMetricsIdempotentPath(t *testing.T) {
	svc, mock := newMockService(t, "", nil)

	upsertRequest := &committrackerv1.UpsertCommitMetricsRequest{
		Provider:    committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB,
		Repository:  "acme/repo",
		Branch:      "main",
		CommitSha:   "head-sha",
		RunId:       "run-1",
		Environment: "ci",
		Metrics: []*committrackerv1.MetricDatum{
			newMetric("binary-size", committrackerv1.MetricDirection_METRIC_DIRECTION_DECREASE_IS_BETTER, 5, 10, 120),
		},
	}

	for i := 0; i < 2; i++ {
		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO metric_definitions(")).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO commit_measurements(")).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		request := connect.NewRequest(upsertRequest)
		setAuthHeaders(request)
		response, err := svc.UpsertCommitMetrics(context.Background(), request)
		if err != nil {
			t.Fatalf("upsert failed on iteration %d: %v", i, err)
		}
		if response.Msg.GetUpsertedCount() != 1 {
			t.Fatalf("expected upsert count 1, got=%d", response.Msg.GetUpsertedCount())
		}
	}

	assertMockExpectations(t, mock)
}

func TestUpsertCommitMetricsConnectHandlerPath(t *testing.T) {
	svc, mock := newMockService(t, "", nil)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO metric_definitions(")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO commit_measurements(")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	path, handler := committrackerv1connect.NewMetricIngestionServiceHandler(svc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := committrackerv1connect.NewMetricIngestionServiceClient(server.Client(), server.URL)
	request := connect.NewRequest(&committrackerv1.UpsertCommitMetricsRequest{
		Provider:    committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB,
		Repository:  "acme/repo",
		Branch:      "main",
		CommitSha:   "head-sha",
		RunId:       "run-1",
		Environment: "ci",
		Metrics: []*committrackerv1.MetricDatum{
			newMetric("binary-size", committrackerv1.MetricDirection_METRIC_DIRECTION_DECREASE_IS_BETTER, 5, 10, 120),
		},
	})
	setAuthHeaders(request)

	response, err := client.UpsertCommitMetrics(context.Background(), request)
	if err != nil {
		t.Fatalf("connect handler path failed: %v", err)
	}
	if response.Msg.GetUpsertedCount() != 1 {
		t.Fatalf("expected upserted count 1, got=%d", response.Msg.GetUpsertedCount())
	}

	assertMockExpectations(t, mock)
}

func TestUpsertCommitMetricsRejectsUnknownProvider(t *testing.T) {
	svc, mock := newMockService(t, "", nil)

	request := connect.NewRequest(&committrackerv1.UpsertCommitMetricsRequest{
		Provider:    committrackerv1.GitProviderKind(99),
		Repository:  "acme/repo",
		Branch:      "main",
		CommitSha:   "head-sha",
		RunId:       "run-1",
		Environment: "ci",
		Metrics: []*committrackerv1.MetricDatum{
			newMetric("binary-size", committrackerv1.MetricDirection_METRIC_DIRECTION_DECREASE_IS_BETTER, 5, 10, 120),
		},
	})
	setAuthHeaders(request)

	_, err := svc.UpsertCommitMetrics(context.Background(), request)
	if err == nil {
		t.Fatal("expected invalid argument error")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect error, got=%v", err)
	}
	if connectErr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("expected invalid argument, got=%s", connectErr.Code())
	}

	assertMockExpectations(t, mock)
}

func TestListMetricSeriesDeniedAuthLogsStructuredEvent(t *testing.T) {
	logBuffer := &bytes.Buffer{}
	svc, mock := newMockServiceWithLogger(t, "", nil, logging.NewWithWriter(logBuffer))

	secretToken := "wrong-secret-token"
	secretSubject := "secret-subject-value"

	request := connect.NewRequest(&committrackerv1.ListMetricSeriesRequest{
		Provider:    committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB,
		Repository:  "acme/repo",
		Environment: "ci",
	})
	request.Header().Set("Authorization", "Bearer "+secretToken)
	request.Header().Set("X-Commit-Tracker-Subject", secretSubject)

	_, err := svc.ListMetricSeries(context.Background(), request)
	if err == nil {
		t.Fatal("expected unauthenticated error")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect error, got=%v", err)
	}
	if connectErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("expected unauthenticated, got=%s", connectErr.Code())
	}

	logOutput := logBuffer.String()
	if !strings.Contains(logOutput, `"result":"denied"`) {
		t.Fatalf("expected denied result in logs, got=%s", logOutput)
	}
	if !strings.Contains(logOutput, `"operation":"list-metric-series"`) {
		t.Fatalf("expected operation in logs, got=%s", logOutput)
	}
	if !strings.Contains(logOutput, `"provider":"GIT_PROVIDER_KIND_GITHUB"`) {
		t.Fatalf("expected provider in logs, got=%s", logOutput)
	}
	if !strings.Contains(logOutput, `"repository":"acme/repo"`) {
		t.Fatalf("expected repository in logs, got=%s", logOutput)
	}
	if strings.Contains(logOutput, secretToken) {
		t.Fatalf("log output must not contain bearer token, got=%s", logOutput)
	}
	if strings.Contains(logOutput, secretSubject) {
		t.Fatalf("log output must not contain subject, got=%s", logOutput)
	}

	assertMockExpectations(t, mock)
}

func TestGetPullRequestComparisonNeutralWhenBaseMissing(t *testing.T) {
	svc, mock := newMockService(t, "", nil)

	headRows := sqlmock.NewRows([]string{
		"metric_key",
		"display_name",
		"unit",
		"value_kind",
		"direction",
		"warning_threshold_percent",
		"fail_threshold_percent",
		"metric_value",
	}).AddRow(
		"cpu-ms",
		"CPU Time",
		"ms",
		int32(committrackerv1.MetricValueKind_METRIC_VALUE_KIND_UNIT_NUMBER),
		int32(committrackerv1.MetricDirection_METRIC_DIRECTION_DECREASE_IS_BETTER),
		5.0,
		10.0,
		80.0,
	)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT ON (cm.metric_key)")).
		WithArgs(int32(committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB), "acme/repo", "ci", "base-sha", "cpu-ms").
		WillReturnRows(sqlmock.NewRows([]string{
			"metric_key",
			"display_name",
			"unit",
			"value_kind",
			"direction",
			"warning_threshold_percent",
			"fail_threshold_percent",
			"metric_value",
		}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT ON (cm.metric_key)")).
		WithArgs(int32(committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB), "acme/repo", "ci", "head-sha", "cpu-ms").
		WillReturnRows(headRows)

	request := connect.NewRequest(&committrackerv1.GetPullRequestComparisonRequest{
		Provider:      committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB,
		Repository:    "acme/repo",
		BaseCommitSha: "base-sha",
		HeadCommitSha: "head-sha",
		Environment:   "ci",
		MetricKeys:    []string{"cpu-ms"},
	})
	setAuthHeaders(request)

	response, err := svc.GetPullRequestComparison(context.Background(), request)
	if err != nil {
		t.Fatalf("comparison failed: %v", err)
	}
	if len(response.Msg.GetComparisons()) != 1 {
		t.Fatalf("expected one comparison, got=%d", len(response.Msg.GetComparisons()))
	}
	item := response.Msg.GetComparisons()[0]
	if item.GetEvaluationLevel() != committrackerv1.EvaluationLevel_EVALUATION_LEVEL_NEUTRAL {
		t.Fatalf("expected neutral, got=%s", item.GetEvaluationLevel().String())
	}
	if item.GetHasBaseValue() {
		t.Fatal("expected missing base")
	}

	assertMockExpectations(t, mock)
}

func TestGetPullRequestComparisonDirectionThresholds(t *testing.T) {
	svc, mock := newMockService(t, "", nil)

	baseRows := sqlmock.NewRows([]string{
		"metric_key",
		"display_name",
		"unit",
		"value_kind",
		"direction",
		"warning_threshold_percent",
		"fail_threshold_percent",
		"metric_value",
	}).
		AddRow("throughput", "Throughput", "ops/s", int32(committrackerv1.MetricValueKind_METRIC_VALUE_KIND_UNIT_NUMBER), int32(committrackerv1.MetricDirection_METRIC_DIRECTION_INCREASE_IS_BETTER), 5.0, 10.0, 100.0).
		AddRow("memory", "Memory", "bytes", int32(committrackerv1.MetricValueKind_METRIC_VALUE_KIND_UNIT_NUMBER), int32(committrackerv1.MetricDirection_METRIC_DIRECTION_DECREASE_IS_BETTER), 5.0, 10.0, 100.0)

	headRows := sqlmock.NewRows([]string{
		"metric_key",
		"display_name",
		"unit",
		"value_kind",
		"direction",
		"warning_threshold_percent",
		"fail_threshold_percent",
		"metric_value",
	}).
		AddRow("throughput", "Throughput", "ops/s", int32(committrackerv1.MetricValueKind_METRIC_VALUE_KIND_UNIT_NUMBER), int32(committrackerv1.MetricDirection_METRIC_DIRECTION_INCREASE_IS_BETTER), 5.0, 10.0, 85.0).
		AddRow("memory", "Memory", "bytes", int32(committrackerv1.MetricValueKind_METRIC_VALUE_KIND_UNIT_NUMBER), int32(committrackerv1.MetricDirection_METRIC_DIRECTION_DECREASE_IS_BETTER), 5.0, 10.0, 90.0)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT ON (cm.metric_key)")).
		WithArgs(int32(committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB), "acme/repo", "ci", "base-th", "memory", "throughput").
		WillReturnRows(baseRows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT ON (cm.metric_key)")).
		WithArgs(int32(committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB), "acme/repo", "ci", "head-th", "memory", "throughput").
		WillReturnRows(headRows)

	request := connect.NewRequest(&committrackerv1.GetPullRequestComparisonRequest{
		Provider:      committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB,
		Repository:    "acme/repo",
		BaseCommitSha: "base-th",
		HeadCommitSha: "head-th",
		Environment:   "ci",
		MetricKeys:    []string{"throughput", "memory"},
	})
	setAuthHeaders(request)

	response, err := svc.GetPullRequestComparison(context.Background(), request)
	if err != nil {
		t.Fatalf("comparison failed: %v", err)
	}

	levels := map[string]committrackerv1.EvaluationLevel{}
	for _, item := range response.Msg.GetComparisons() {
		levels[item.GetMetricKey()] = item.GetEvaluationLevel()
	}
	if levels["throughput"] != committrackerv1.EvaluationLevel_EVALUATION_LEVEL_FAIL {
		t.Fatalf("expected throughput fail, got=%s", levels["throughput"].String())
	}
	if levels["memory"] != committrackerv1.EvaluationLevel_EVALUATION_LEVEL_PASS {
		t.Fatalf("expected memory pass, got=%s", levels["memory"].String())
	}
	if response.Msg.GetAggregateEvaluation() != committrackerv1.EvaluationLevel_EVALUATION_LEVEL_FAIL {
		t.Fatalf("expected aggregate fail, got=%s", response.Msg.GetAggregateEvaluation().String())
	}

	assertMockExpectations(t, mock)
}

func TestComputeDeltaBaseZeroDeterministic(t *testing.T) {
	delta, deltaPercent := computeDelta(0, 10, true, true)
	if delta != 10 {
		t.Fatalf("expected delta 10, got=%f", delta)
	}
	if deltaPercent != 100 {
		t.Fatalf("expected delta percent 100, got=%f", deltaPercent)
	}

	level := evaluateComparison(
		committrackerv1.MetricDirection_METRIC_DIRECTION_DECREASE_IS_BETTER,
		10,
		50,
		delta,
		deltaPercent,
		true,
		true,
	)
	if level != committrackerv1.EvaluationLevel_EVALUATION_LEVEL_FAIL {
		t.Fatalf("expected fail, got=%s", level.String())
	}
}

func TestPublishPullRequestReportGitHubSuccess(t *testing.T) {
	github := newGitHubServer(t, false)
	svc, mock := newMockService(t, github.URL, github.Client())

	baseRows := sqlmock.NewRows([]string{
		"metric_key",
		"display_name",
		"unit",
		"value_kind",
		"direction",
		"warning_threshold_percent",
		"fail_threshold_percent",
		"metric_value",
	}).AddRow("binary-size", "Binary Size", "bytes", int32(committrackerv1.MetricValueKind_METRIC_VALUE_KIND_UNIT_NUMBER), int32(committrackerv1.MetricDirection_METRIC_DIRECTION_DECREASE_IS_BETTER), 5.0, 10.0, 100.0)
	headRows := sqlmock.NewRows([]string{
		"metric_key",
		"display_name",
		"unit",
		"value_kind",
		"direction",
		"warning_threshold_percent",
		"fail_threshold_percent",
		"metric_value",
	}).AddRow("binary-size", "Binary Size", "bytes", int32(committrackerv1.MetricValueKind_METRIC_VALUE_KIND_UNIT_NUMBER), int32(committrackerv1.MetricDirection_METRIC_DIRECTION_DECREASE_IS_BETTER), 5.0, 10.0, 120.0)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT ON (cm.metric_key)")).
		WithArgs(int32(committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB), "acme/repo", "ci", "base-pr").
		WillReturnRows(baseRows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT ON (cm.metric_key)")).
		WithArgs(int32(committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB), "acme/repo", "ci", "head-pr").
		WillReturnRows(headRows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO pull_request_reports(")).
		WithArgs(
			int32(committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB),
			"acme/repo",
			int64(23),
			"base-pr",
			"head-pr",
			"ci",
			int32(committrackerv1.EvaluationLevel_EVALUATION_LEVEL_FAIL),
			sqlmock.AnyArg(),
			"https://github.example/comment/1",
			"https://github.example/status/1",
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	request := connect.NewRequest(&committrackerv1.PublishPullRequestReportRequest{
		Provider:      committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB,
		Repository:    "acme/repo",
		PullRequest:   23,
		BaseCommitSha: "base-pr",
		HeadCommitSha: "head-pr",
		Environment:   "ci",
	})
	setAuthHeaders(request)

	response, err := svc.PublishPullRequestReport(context.Background(), request)
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if response.Msg.GetCommentUrl() == "" || response.Msg.GetStatusUrl() == "" {
		t.Fatal("expected comment and status urls")
	}

	assertMockExpectations(t, mock)
}

func TestPublishPullRequestReportGitHubAuthFailureMapped(t *testing.T) {
	github := newGitHubServer(t, true)
	svc, mock := newMockService(t, github.URL, github.Client())

	baseRows := sqlmock.NewRows([]string{
		"metric_key",
		"display_name",
		"unit",
		"value_kind",
		"direction",
		"warning_threshold_percent",
		"fail_threshold_percent",
		"metric_value",
	}).AddRow("binary-size", "Binary Size", "bytes", int32(committrackerv1.MetricValueKind_METRIC_VALUE_KIND_UNIT_NUMBER), int32(committrackerv1.MetricDirection_METRIC_DIRECTION_DECREASE_IS_BETTER), 5.0, 10.0, 100.0)
	headRows := sqlmock.NewRows([]string{
		"metric_key",
		"display_name",
		"unit",
		"value_kind",
		"direction",
		"warning_threshold_percent",
		"fail_threshold_percent",
		"metric_value",
	}).AddRow("binary-size", "Binary Size", "bytes", int32(committrackerv1.MetricValueKind_METRIC_VALUE_KIND_UNIT_NUMBER), int32(committrackerv1.MetricDirection_METRIC_DIRECTION_DECREASE_IS_BETTER), 5.0, 10.0, 120.0)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT ON (cm.metric_key)")).
		WithArgs(int32(committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB), "acme/repo", "ci", "base-pr").
		WillReturnRows(baseRows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT ON (cm.metric_key)")).
		WithArgs(int32(committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB), "acme/repo", "ci", "head-pr").
		WillReturnRows(headRows)

	request := connect.NewRequest(&committrackerv1.PublishPullRequestReportRequest{
		Provider:      committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB,
		Repository:    "acme/repo",
		PullRequest:   23,
		BaseCommitSha: "base-pr",
		HeadCommitSha: "head-pr",
		Environment:   "ci",
	})
	setAuthHeaders(request)

	_, err := svc.PublishPullRequestReport(context.Background(), request)
	if err == nil {
		t.Fatal("expected publish failure")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect error, got=%v", err)
	}
	if connectErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("expected unauthenticated code, got=%s", connectErr.Code())
	}

	assertMockExpectations(t, mock)
}

func TestPublishPullRequestReportUnsupportedProvider(t *testing.T) {
	svc, mock := newMockService(t, "", nil)
	request := connect.NewRequest(&committrackerv1.PublishPullRequestReportRequest{
		Provider:      committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITLAB,
		Repository:    "acme/repo",
		PullRequest:   9,
		BaseCommitSha: "base",
		HeadCommitSha: "head",
		Environment:   "ci",
	})
	setAuthHeaders(request)

	_, err := svc.PublishPullRequestReport(context.Background(), request)
	if err == nil {
		t.Fatal("expected failed precondition error")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect error, got=%v", err)
	}
	if connectErr.Code() != connect.CodeFailedPrecondition {
		t.Fatalf("expected failed precondition, got=%s", connectErr.Code())
	}

	assertMockExpectations(t, mock)
}

func TestPublishPullRequestReportRejectsUnknownProvider(t *testing.T) {
	svc, mock := newMockService(t, "", nil)
	request := connect.NewRequest(&committrackerv1.PublishPullRequestReportRequest{
		Provider:      committrackerv1.GitProviderKind(99),
		Repository:    "acme/repo",
		PullRequest:   9,
		BaseCommitSha: "base",
		HeadCommitSha: "head",
		Environment:   "ci",
	})
	setAuthHeaders(request)

	_, err := svc.PublishPullRequestReport(context.Background(), request)
	if err == nil {
		t.Fatal("expected invalid argument error")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect error, got=%v", err)
	}
	if connectErr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("expected invalid argument, got=%s", connectErr.Code())
	}

	assertMockExpectations(t, mock)
}

func newMockService(t *testing.T, githubAPIBase string, httpClient *http.Client) (*Service, sqlmock.Sqlmock) {
	return newMockServiceWithLogger(t, githubAPIBase, httpClient, nil)
}

func newMockServiceWithLogger(t *testing.T, githubAPIBase string, httpClient *http.Client, logger *logging.Logger) (*Service, sqlmock.Sqlmock) {
	t.Helper()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if httpClient == nil {
		httpClient = &http.Client{}
	}
	if logger == nil {
		logger = logging.NewWithWriter(io.Discard)
	}
	service := &Service{
		db:            db,
		logger:        logger,
		authToken:     "ct-token",
		githubToken:   "gh-token",
		githubAPIBase: githubAPIBase,
		httpClient:    httpClient,
	}

	return service, mock
}

func assertMockExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func newMetric(
	metricKey string,
	direction committrackerv1.MetricDirection,
	warningThreshold float64,
	failThreshold float64,
	value float64,
) *committrackerv1.MetricDatum {
	return &committrackerv1.MetricDatum{
		MetricKey:               metricKey,
		DisplayName:             metricKey,
		Unit:                    "ms",
		ValueKind:               committrackerv1.MetricValueKind_METRIC_VALUE_KIND_UNIT_NUMBER,
		Direction:               direction,
		WarningThresholdPercent: warningThreshold,
		FailThresholdPercent:    failThreshold,
		Value:                   value,
	}
}

func setAuthHeaders[T any](request *connect.Request[T]) {
	request.Header().Set("Authorization", "Bearer ct-token")
	request.Header().Set("X-Commit-Tracker-Subject", "ci-bot")
}

func newGitHubServer(t *testing.T, unauthorized bool) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/repo/issues/23/comments", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if unauthorized {
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"message":"bad credentials"}`))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"html_url":"https://github.example/comment/1"}`))
	})
	mux.HandleFunc("/repos/acme/repo/statuses/head-pr", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if unauthorized {
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"message":"bad credentials"}`))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"url":"https://github.example/status/1"}`))
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}
