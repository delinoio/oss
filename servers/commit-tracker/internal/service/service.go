package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	committrackerv1 "github.com/delinoio/oss/servers/commit-tracker/gen/proto/committracker/v1"
	"github.com/delinoio/oss/servers/commit-tracker/internal/contracts"
	"github.com/delinoio/oss/servers/commit-tracker/internal/logging"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	defaultSeriesLimit = 50
	maxSeriesLimit     = 500
	migrationVersion1  = 1
)

type Config struct {
	DatabaseURL   string
	AuthToken     string
	GitHubToken   string
	GitHubAPIBase string
	HTTPClient    *http.Client
}

type Service struct {
	db            *sql.DB
	logger        *logging.Logger
	authToken     string
	githubToken   string
	githubAPIBase string
	httpClient    *http.Client
}

type metricSnapshot struct {
	key                     string
	displayName             string
	unit                    string
	valueKind               committrackerv1.MetricValueKind
	direction               committrackerv1.MetricDirection
	warningThresholdPercent float64
	failThresholdPercent    float64
	value                   float64
}

type githubAPIError struct {
	statusCode int
	body       string
	method     string
	path       string
}

func (e *githubAPIError) Error() string {
	return fmt.Sprintf("github api %s %s failed (%d): %s", e.method, e.path, e.statusCode, e.body)
}

func New(ctx context.Context, cfg Config, logger *logging.Logger) (*Service, error) {
	if logger == nil {
		logger = logging.New()
	}

	databaseURL := strings.TrimSpace(cfg.DatabaseURL)
	if databaseURL == "" {
		return nil, errors.New("database url is required")
	}
	authToken := strings.TrimSpace(cfg.AuthToken)
	if authToken == "" {
		return nil, errors.New("auth token is required")
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres database: %w", err)
	}

	db.SetMaxIdleConns(4)
	db.SetMaxOpenConns(8)
	db.SetConnMaxIdleTime(2 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres database: %w", err)
	}

	svc := &Service{
		db:            db,
		logger:        logger,
		authToken:     authToken,
		githubToken:   strings.TrimSpace(cfg.GitHubToken),
		githubAPIBase: strings.TrimRight(defaultGitHubAPIBase(cfg.GitHubAPIBase), "/"),
		httpClient:    cfg.HTTPClient,
	}

	if svc.httpClient == nil {
		svc.httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	if err := svc.applyMigrations(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}

	return svc, nil
}

func defaultGitHubAPIBase(configured string) string {
	trimmed := strings.TrimSpace(configured)
	if trimmed == "" {
		return "https://api.github.com"
	}
	if strings.Contains(trimmed, "://") {
		return trimmed
	}
	return "https://" + trimmed
}

func (s *Service) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Service) applyMigrations(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return err
	}

	if err := s.applyMigration(ctx, migrationVersion1, []string{
		`CREATE TABLE IF NOT EXISTS metric_definitions (
			metric_key TEXT PRIMARY KEY,
			display_name TEXT NOT NULL,
			unit TEXT NOT NULL,
			value_kind SMALLINT NOT NULL,
			direction SMALLINT NOT NULL,
			warning_threshold_percent DOUBLE PRECISION NOT NULL,
			fail_threshold_percent DOUBLE PRECISION NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS commit_measurements (
			id BIGSERIAL PRIMARY KEY,
			provider SMALLINT NOT NULL,
			repository TEXT NOT NULL,
			branch TEXT NOT NULL,
			commit_sha TEXT NOT NULL,
			run_id TEXT NOT NULL,
			environment TEXT NOT NULL,
			metric_key TEXT NOT NULL REFERENCES metric_definitions(metric_key),
			metric_value DOUBLE PRECISION NOT NULL,
			measured_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(provider, repository, branch, commit_sha, run_id, environment, metric_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_commit_measurements_series
			ON commit_measurements(provider, repository, branch, environment, metric_key, measured_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_commit_measurements_commit
			ON commit_measurements(provider, repository, commit_sha, environment, metric_key, measured_at DESC)`,
		`CREATE TABLE IF NOT EXISTS pull_request_reports (
			id BIGSERIAL PRIMARY KEY,
			provider SMALLINT NOT NULL,
			repository TEXT NOT NULL,
			pull_request BIGINT NOT NULL,
			base_commit_sha TEXT NOT NULL,
			head_commit_sha TEXT NOT NULL,
			environment TEXT NOT NULL,
			aggregate_evaluation SMALLINT NOT NULL,
			markdown TEXT NOT NULL,
			comment_url TEXT,
			status_url TEXT,
			published_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_pull_request_reports_key
			ON pull_request_reports(provider, repository, pull_request, head_commit_sha, published_at DESC)`,
	}); err != nil {
		return err
	}

	return nil
}

func (s *Service) applyMigration(ctx context.Context, version int64, statements []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	claimResult, err := tx.ExecContext(
		ctx,
		`INSERT INTO schema_migrations(version) VALUES ($1) ON CONFLICT(version) DO NOTHING`,
		version,
	)
	if err != nil {
		return err
	}
	claimedRows, err := claimResult.RowsAffected()
	if err != nil {
		return err
	}
	if claimedRows == 0 {
		return tx.Commit()
	}

	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Service) UpsertCommitMetrics(ctx context.Context, req *connect.Request[committrackerv1.UpsertCommitMetricsRequest]) (*connect.Response[committrackerv1.UpsertCommitMetricsResponse], error) {
	if _, err := s.authorize(req.Header()); err != nil {
		s.logDenied(contracts.OperationUpsertCommitMetrics, req.Msg, err)
		return nil, err
	}
	if err := validateUpsertRequest(req.Msg); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	measuredAt := time.Now().UTC()
	if req.Msg.GetMeasuredAt() != nil {
		measuredAt = req.Msg.GetMeasuredAt().AsTime().UTC()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.logFailure(contracts.OperationUpsertCommitMetrics, req.Msg, "", 0, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	for _, metric := range req.Msg.GetMetrics() {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO metric_definitions(
				metric_key,
				display_name,
				unit,
				value_kind,
				direction,
				warning_threshold_percent,
				fail_threshold_percent,
				updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
			ON CONFLICT(metric_key)
			DO UPDATE SET
				display_name = EXCLUDED.display_name,
				unit = EXCLUDED.unit,
				value_kind = EXCLUDED.value_kind,
				direction = EXCLUDED.direction,
				warning_threshold_percent = EXCLUDED.warning_threshold_percent,
				fail_threshold_percent = EXCLUDED.fail_threshold_percent,
				updated_at = NOW()`,
			metric.GetMetricKey(),
			metric.GetDisplayName(),
			metric.GetUnit(),
			int32(metric.GetValueKind()),
			int32(metric.GetDirection()),
			metric.GetWarningThresholdPercent(),
			metric.GetFailThresholdPercent(),
		); err != nil {
			s.logFailure(contracts.OperationUpsertCommitMetrics, req.Msg, metric.GetMetricKey(), 0, err)
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO commit_measurements(
				provider,
				repository,
				branch,
				commit_sha,
				run_id,
				environment,
				metric_key,
				metric_value,
				measured_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT(provider, repository, branch, commit_sha, run_id, environment, metric_key)
			DO UPDATE SET
				metric_value = EXCLUDED.metric_value,
				measured_at = EXCLUDED.measured_at,
				branch = EXCLUDED.branch`,
			int32(req.Msg.GetProvider()),
			req.Msg.GetRepository(),
			req.Msg.GetBranch(),
			req.Msg.GetCommitSha(),
			req.Msg.GetRunId(),
			req.Msg.GetEnvironment(),
			metric.GetMetricKey(),
			metric.GetValue(),
			measuredAt,
		); err != nil {
			s.logFailure(contracts.OperationUpsertCommitMetrics, req.Msg, metric.GetMetricKey(), 0, err)
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		s.logger.Event(map[string]any{
			"operation":         contracts.OperationUpsertCommitMetrics,
			"result":            contracts.OperationResultSuccess,
			"provider":          req.Msg.GetProvider().String(),
			"repository":        req.Msg.GetRepository(),
			"pull_request":      0,
			"commit":            req.Msg.GetCommitSha(),
			"run_id":            req.Msg.GetRunId(),
			"metric_key":        metric.GetMetricKey(),
			"evaluation_level":  "",
			"delta_percent":     0,
			"warning_threshold": metric.GetWarningThresholdPercent(),
			"fail_threshold":    metric.GetFailThresholdPercent(),
		})
	}

	if err := tx.Commit(); err != nil {
		s.logFailure(contracts.OperationUpsertCommitMetrics, req.Msg, "", 0, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&committrackerv1.UpsertCommitMetricsResponse{
		UpsertedCount: int32(len(req.Msg.GetMetrics())),
	}), nil
}

func (s *Service) ListMetricSeries(ctx context.Context, req *connect.Request[committrackerv1.ListMetricSeriesRequest]) (*connect.Response[committrackerv1.ListMetricSeriesResponse], error) {
	if _, err := s.authorize(req.Header()); err != nil {
		s.logDenied(contracts.OperationListMetricSeries, req.Msg, err)
		return nil, err
	}
	if err := validateSeriesRequest(req.Msg); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	limit := req.Msg.GetLimit()
	if limit <= 0 {
		limit = defaultSeriesLimit
	}
	if limit > maxSeriesLimit {
		limit = maxSeriesLimit
	}

	query := strings.Builder{}
	query.WriteString(`
		SELECT
			cm.metric_key,
			md.display_name,
			md.unit,
			md.value_kind,
			md.direction,
			md.warning_threshold_percent,
			md.fail_threshold_percent,
			cm.commit_sha,
			cm.run_id,
			cm.metric_value,
			cm.measured_at
		FROM commit_measurements cm
		JOIN metric_definitions md ON md.metric_key = cm.metric_key
		WHERE cm.provider = $1
			AND cm.repository = $2
			AND cm.environment = $3
	`)

	args := []any{int32(req.Msg.GetProvider()), req.Msg.GetRepository(), req.Msg.GetEnvironment()}
	index := 4

	if branch := strings.TrimSpace(req.Msg.GetBranch()); branch != "" {
		query.WriteString(" AND cm.branch = $" + strconv.Itoa(index))
		args = append(args, branch)
		index++
	}
	if metricKey := strings.TrimSpace(req.Msg.GetMetricKey()); metricKey != "" {
		query.WriteString(" AND cm.metric_key = $" + strconv.Itoa(index))
		args = append(args, metricKey)
		index++
	}
	if req.Msg.GetFromTime() != nil {
		query.WriteString(" AND cm.measured_at >= $" + strconv.Itoa(index))
		args = append(args, req.Msg.GetFromTime().AsTime().UTC())
		index++
	}
	if req.Msg.GetToTime() != nil {
		query.WriteString(" AND cm.measured_at <= $" + strconv.Itoa(index))
		args = append(args, req.Msg.GetToTime().AsTime().UTC())
		index++
	}

	query.WriteString(" ORDER BY cm.measured_at DESC LIMIT $" + strconv.Itoa(index))
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		s.logFailure(contracts.OperationListMetricSeries, req.Msg, "", 0, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer rows.Close()

	points := make([]*committrackerv1.MetricSeriesPoint, 0, limit)
	for rows.Next() {
		var (
			metricKey               string
			displayName             string
			unit                    string
			valueKind               int32
			direction               int32
			warningThresholdPercent float64
			failThresholdPercent    float64
			commitSHA               string
			runID                   string
			value                   float64
			measuredAt              time.Time
		)
		if err := rows.Scan(
			&metricKey,
			&displayName,
			&unit,
			&valueKind,
			&direction,
			&warningThresholdPercent,
			&failThresholdPercent,
			&commitSHA,
			&runID,
			&value,
			&measuredAt,
		); err != nil {
			s.logFailure(contracts.OperationListMetricSeries, req.Msg, "", 0, err)
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		points = append(points, &committrackerv1.MetricSeriesPoint{
			MetricKey:               metricKey,
			DisplayName:             displayName,
			Unit:                    unit,
			ValueKind:               committrackerv1.MetricValueKind(valueKind),
			Direction:               committrackerv1.MetricDirection(direction),
			WarningThresholdPercent: warningThresholdPercent,
			FailThresholdPercent:    failThresholdPercent,
			CommitSha:               commitSHA,
			RunId:                   runID,
			Value:                   value,
			MeasuredAt:              timestamppb.New(measuredAt.UTC()),
		})
	}
	if err := rows.Err(); err != nil {
		s.logFailure(contracts.OperationListMetricSeries, req.Msg, "", 0, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	s.logger.Event(map[string]any{
		"operation":          contracts.OperationListMetricSeries,
		"result":             contracts.OperationResultSuccess,
		"provider":           req.Msg.GetProvider().String(),
		"repository":         req.Msg.GetRepository(),
		"pull_request":       0,
		"commit":             "",
		"run_id":             "",
		"metric_key":         req.Msg.GetMetricKey(),
		"evaluation_level":   "",
		"delta_percent":      0,
		"series_point_count": len(points),
	})

	return connect.NewResponse(&committrackerv1.ListMetricSeriesResponse{Points: points}), nil
}

func (s *Service) GetPullRequestComparison(ctx context.Context, req *connect.Request[committrackerv1.GetPullRequestComparisonRequest]) (*connect.Response[committrackerv1.GetPullRequestComparisonResponse], error) {
	if _, err := s.authorize(req.Header()); err != nil {
		s.logDenied(contracts.OperationGetPullRequestCompare, req.Msg, err)
		return nil, err
	}

	comparison, err := s.buildPullRequestComparison(ctx, req.Msg)
	if err != nil {
		s.logFailure(contracts.OperationGetPullRequestCompare, req.Msg, "", 0, err)
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	for _, item := range comparison.GetComparisons() {
		s.logger.Event(map[string]any{
			"operation":        contracts.OperationGetPullRequestCompare,
			"result":           contracts.OperationResultSuccess,
			"provider":         comparison.GetProvider().String(),
			"repository":       comparison.GetRepository(),
			"pull_request":     0,
			"commit":           comparison.GetHeadCommitSha(),
			"run_id":           "",
			"metric_key":       item.GetMetricKey(),
			"evaluation_level": item.GetEvaluationLevel().String(),
			"delta_percent":    item.GetDeltaPercent(),
		})
	}

	return connect.NewResponse(comparison), nil
}

func (s *Service) PublishPullRequestReport(ctx context.Context, req *connect.Request[committrackerv1.PublishPullRequestReportRequest]) (*connect.Response[committrackerv1.PublishPullRequestReportResponse], error) {
	if _, err := s.authorize(req.Header()); err != nil {
		s.logDenied(contracts.OperationPublishPullRequestInfo, req.Msg, err)
		return nil, err
	}
	if req.Msg.GetProvider() != committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("provider integration is only live for github in phase 1"))
	}
	if req.Msg.GetPullRequest() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("pull_request must be greater than zero"))
	}

	comparison, err := s.buildPullRequestComparison(ctx, &committrackerv1.GetPullRequestComparisonRequest{
		Provider:      req.Msg.GetProvider(),
		Repository:    req.Msg.GetRepository(),
		BaseCommitSha: req.Msg.GetBaseCommitSha(),
		HeadCommitSha: req.Msg.GetHeadCommitSha(),
		Environment:   req.Msg.GetEnvironment(),
		MetricKeys:    req.Msg.GetMetricKeys(),
	})
	if err != nil {
		s.logFailure(contracts.OperationPublishPullRequestInfo, req.Msg, "", req.Msg.GetPullRequest(), err)
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	markdown := buildMarkdownReport(comparison.GetComparisons(), comparison.GetAggregateEvaluation(), comparison.GetBaseCommitSha(), comparison.GetHeadCommitSha())
	owner, repo, err := splitRepository(comparison.GetRepository())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	commentURL, err := s.publishGitHubComment(ctx, owner, repo, req.Msg.GetPullRequest(), markdown)
	if err != nil {
		return nil, mapGitHubError(err)
	}

	statusURL, err := s.publishGitHubStatus(ctx, owner, repo, comparison.GetHeadCommitSha(), comparison.GetAggregateEvaluation())
	if err != nil {
		return nil, mapGitHubError(err)
	}

	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO pull_request_reports(
			provider,
			repository,
			pull_request,
			base_commit_sha,
			head_commit_sha,
			environment,
			aggregate_evaluation,
			markdown,
			comment_url,
			status_url
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		int32(comparison.GetProvider()),
		comparison.GetRepository(),
		req.Msg.GetPullRequest(),
		comparison.GetBaseCommitSha(),
		comparison.GetHeadCommitSha(),
		comparison.GetEnvironment(),
		int32(comparison.GetAggregateEvaluation()),
		markdown,
		commentURL,
		statusURL,
	); err != nil {
		s.logFailure(contracts.OperationPublishPullRequestInfo, req.Msg, "", req.Msg.GetPullRequest(), err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	for _, item := range comparison.GetComparisons() {
		s.logger.Event(map[string]any{
			"operation":        contracts.OperationPublishPullRequestInfo,
			"result":           contracts.OperationResultSuccess,
			"provider":         comparison.GetProvider().String(),
			"repository":       comparison.GetRepository(),
			"pull_request":     req.Msg.GetPullRequest(),
			"commit":           comparison.GetHeadCommitSha(),
			"run_id":           "",
			"metric_key":       item.GetMetricKey(),
			"evaluation_level": item.GetEvaluationLevel().String(),
			"delta_percent":    item.GetDeltaPercent(),
		})
	}

	return connect.NewResponse(&committrackerv1.PublishPullRequestReportResponse{
		AggregateEvaluation: comparison.GetAggregateEvaluation(),
		Markdown:            markdown,
		CommentUrl:          commentURL,
		StatusUrl:           statusURL,
	}), nil
}

func (s *Service) buildPullRequestComparison(ctx context.Context, req *committrackerv1.GetPullRequestComparisonRequest) (*committrackerv1.GetPullRequestComparisonResponse, error) {
	if err := validateComparisonRequest(req); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	metricKeys := uniqueSortedMetricKeys(req.GetMetricKeys())
	baseMetrics, err := s.loadCommitMetrics(ctx, req.GetProvider(), req.GetRepository(), req.GetEnvironment(), req.GetBaseCommitSha(), metricKeys)
	if err != nil {
		return nil, err
	}
	headMetrics, err := s.loadCommitMetrics(ctx, req.GetProvider(), req.GetRepository(), req.GetEnvironment(), req.GetHeadCommitSha(), metricKeys)
	if err != nil {
		return nil, err
	}

	comparisonKeys := metricKeys
	if len(comparisonKeys) == 0 {
		comparisonKeys = mergeMetricKeys(baseMetrics, headMetrics)
	}

	comparisons := make([]*committrackerv1.MetricComparison, 0, len(comparisonKeys))
	aggregate := committrackerv1.EvaluationLevel_EVALUATION_LEVEL_NEUTRAL

	for _, key := range comparisonKeys {
		base, hasBase := baseMetrics[key]
		head, hasHead := headMetrics[key]

		seed := head
		if !hasHead {
			seed = base
		}

		delta, deltaPercent := computeDelta(base.value, head.value, hasBase, hasHead)
		evaluation := evaluateComparison(seed.direction, seed.warningThresholdPercent, seed.failThresholdPercent, delta, deltaPercent, hasBase, hasHead)
		if evaluationRank(evaluation) > evaluationRank(aggregate) {
			aggregate = evaluation
		}

		comparisons = append(comparisons, &committrackerv1.MetricComparison{
			MetricKey:               key,
			DisplayName:             seed.displayName,
			Unit:                    seed.unit,
			ValueKind:               seed.valueKind,
			Direction:               seed.direction,
			WarningThresholdPercent: seed.warningThresholdPercent,
			FailThresholdPercent:    seed.failThresholdPercent,
			BaseValue:               base.value,
			HeadValue:               head.value,
			Delta:                   delta,
			DeltaPercent:            deltaPercent,
			EvaluationLevel:         evaluation,
			HasBaseValue:            hasBase,
			HasHeadValue:            hasHead,
		})
	}

	return &committrackerv1.GetPullRequestComparisonResponse{
		Provider:            req.GetProvider(),
		Repository:          req.GetRepository(),
		BaseCommitSha:       req.GetBaseCommitSha(),
		HeadCommitSha:       req.GetHeadCommitSha(),
		Environment:         req.GetEnvironment(),
		Comparisons:         comparisons,
		AggregateEvaluation: aggregate,
	}, nil
}

func (s *Service) loadCommitMetrics(
	ctx context.Context,
	provider committrackerv1.GitProviderKind,
	repository string,
	environment string,
	commitSHA string,
	metricKeys []string,
) (map[string]metricSnapshot, error) {
	query := strings.Builder{}
	query.WriteString(`
		SELECT DISTINCT ON (cm.metric_key)
			cm.metric_key,
			md.display_name,
			md.unit,
			md.value_kind,
			md.direction,
			md.warning_threshold_percent,
			md.fail_threshold_percent,
			cm.metric_value
		FROM commit_measurements cm
		JOIN metric_definitions md ON md.metric_key = cm.metric_key
		WHERE cm.provider = $1
			AND cm.repository = $2
			AND cm.environment = $3
			AND cm.commit_sha = $4
	`)

	args := []any{int32(provider), repository, environment, commitSHA}
	index := 5
	if len(metricKeys) > 0 {
		query.WriteString(" AND cm.metric_key IN (")
		for i, key := range metricKeys {
			if i > 0 {
				query.WriteString(",")
			}
			query.WriteString("$" + strconv.Itoa(index))
			args = append(args, key)
			index++
		}
		query.WriteString(")")
	}
	query.WriteString(" ORDER BY cm.metric_key, cm.measured_at DESC, cm.id DESC")

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer rows.Close()

	out := make(map[string]metricSnapshot)
	for rows.Next() {
		var (
			key                     string
			displayName             string
			unit                    string
			valueKind               int32
			direction               int32
			warningThresholdPercent float64
			failThresholdPercent    float64
			value                   float64
		)
		if err := rows.Scan(
			&key,
			&displayName,
			&unit,
			&valueKind,
			&direction,
			&warningThresholdPercent,
			&failThresholdPercent,
			&value,
		); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		out[key] = metricSnapshot{
			key:                     key,
			displayName:             displayName,
			unit:                    unit,
			valueKind:               committrackerv1.MetricValueKind(valueKind),
			direction:               committrackerv1.MetricDirection(direction),
			warningThresholdPercent: warningThresholdPercent,
			failThresholdPercent:    failThresholdPercent,
			value:                   value,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return out, nil
}

func (s *Service) authorize(headers http.Header) (string, error) {
	authorization := strings.TrimSpace(headers.Get("Authorization"))
	if authorization == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("authorization header is required"))
	}
	if !strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("authorization must use bearer token"))
	}

	token := strings.TrimSpace(authorization[len("Bearer "):])
	if token == "" || token != s.authToken {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("invalid bearer token"))
	}

	subject := strings.TrimSpace(headers.Get("X-Commit-Tracker-Subject"))
	if subject == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("X-Commit-Tracker-Subject header is required"))
	}

	return subject, nil
}

func validateUpsertRequest(request *committrackerv1.UpsertCommitMetricsRequest) error {
	if request == nil {
		return errors.New("request is required")
	}
	if err := validateProvider(request.GetProvider()); err != nil {
		return err
	}
	if strings.TrimSpace(request.GetRepository()) == "" {
		return errors.New("repository is required")
	}
	if strings.TrimSpace(request.GetBranch()) == "" {
		return errors.New("branch is required")
	}
	if strings.TrimSpace(request.GetCommitSha()) == "" {
		return errors.New("commit_sha is required")
	}
	if strings.TrimSpace(request.GetRunId()) == "" {
		return errors.New("run_id is required")
	}
	if strings.TrimSpace(request.GetEnvironment()) == "" {
		return errors.New("environment is required")
	}
	if len(request.GetMetrics()) == 0 {
		return errors.New("at least one metric is required")
	}

	for _, metric := range request.GetMetrics() {
		if strings.TrimSpace(metric.GetMetricKey()) == "" {
			return errors.New("metric_key is required")
		}
		if strings.TrimSpace(metric.GetDisplayName()) == "" {
			return fmt.Errorf("display_name is required for metric %s", metric.GetMetricKey())
		}
		if strings.TrimSpace(metric.GetUnit()) == "" {
			return fmt.Errorf("unit is required for metric %s", metric.GetMetricKey())
		}
		if metric.GetValueKind() == committrackerv1.MetricValueKind_METRIC_VALUE_KIND_UNSPECIFIED {
			return fmt.Errorf("value_kind is required for metric %s", metric.GetMetricKey())
		}
		if metric.GetDirection() == committrackerv1.MetricDirection_METRIC_DIRECTION_UNSPECIFIED {
			return fmt.Errorf("direction is required for metric %s", metric.GetMetricKey())
		}
		if metric.GetFailThresholdPercent() < metric.GetWarningThresholdPercent() {
			return fmt.Errorf("fail threshold must be >= warning threshold for metric %s", metric.GetMetricKey())
		}
		if math.IsNaN(metric.GetValue()) || math.IsInf(metric.GetValue(), 0) {
			return fmt.Errorf("value must be finite for metric %s", metric.GetMetricKey())
		}
	}

	return nil
}

func validateSeriesRequest(request *committrackerv1.ListMetricSeriesRequest) error {
	if request == nil {
		return errors.New("request is required")
	}
	if err := validateProvider(request.GetProvider()); err != nil {
		return err
	}
	if strings.TrimSpace(request.GetRepository()) == "" {
		return errors.New("repository is required")
	}
	if strings.TrimSpace(request.GetEnvironment()) == "" {
		return errors.New("environment is required")
	}
	return nil
}

func validateComparisonRequest(request *committrackerv1.GetPullRequestComparisonRequest) error {
	if request == nil {
		return errors.New("request is required")
	}
	if err := validateProvider(request.GetProvider()); err != nil {
		return err
	}
	if strings.TrimSpace(request.GetRepository()) == "" {
		return errors.New("repository is required")
	}
	if strings.TrimSpace(request.GetBaseCommitSha()) == "" {
		return errors.New("base_commit_sha is required")
	}
	if strings.TrimSpace(request.GetHeadCommitSha()) == "" {
		return errors.New("head_commit_sha is required")
	}
	if strings.TrimSpace(request.GetEnvironment()) == "" {
		return errors.New("environment is required")
	}
	return nil
}

func validateProvider(provider committrackerv1.GitProviderKind) error {
	switch provider {
	case committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB,
		committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITLAB,
		committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_BITBUCKET:
		return nil
	case committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_UNSPECIFIED:
		return errors.New("provider is required")
	default:
		return fmt.Errorf("unsupported provider: %d", int32(provider))
	}
}

func uniqueSortedMetricKeys(metricKeys []string) []string {
	set := make(map[string]struct{}, len(metricKeys))
	out := make([]string, 0, len(metricKeys))
	for _, key := range metricKeys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if _, exists := set[trimmed]; exists {
			continue
		}
		set[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func mergeMetricKeys(base map[string]metricSnapshot, head map[string]metricSnapshot) []string {
	set := make(map[string]struct{}, len(base)+len(head))
	keys := make([]string, 0, len(base)+len(head))
	for key := range base {
		set[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range head {
		if _, exists := set[key]; exists {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func computeDelta(baseValue float64, headValue float64, hasBase bool, hasHead bool) (float64, float64) {
	if !hasBase || !hasHead {
		return 0, 0
	}
	delta := headValue - baseValue
	if baseValue == 0 {
		if headValue == 0 {
			return delta, 0
		}
		return delta, 100
	}
	return delta, (delta / baseValue) * 100
}

func evaluateComparison(
	direction committrackerv1.MetricDirection,
	warnThreshold float64,
	failThreshold float64,
	delta float64,
	deltaPercent float64,
	hasBase bool,
	hasHead bool,
) committrackerv1.EvaluationLevel {
	if !hasBase || !hasHead {
		return committrackerv1.EvaluationLevel_EVALUATION_LEVEL_NEUTRAL
	}

	degradePercent := 0.0
	switch direction {
	case committrackerv1.MetricDirection_METRIC_DIRECTION_INCREASE_IS_BETTER:
		if delta >= 0 {
			return committrackerv1.EvaluationLevel_EVALUATION_LEVEL_PASS
		}
		degradePercent = math.Abs(deltaPercent)
	case committrackerv1.MetricDirection_METRIC_DIRECTION_DECREASE_IS_BETTER:
		if delta <= 0 {
			return committrackerv1.EvaluationLevel_EVALUATION_LEVEL_PASS
		}
		degradePercent = math.Abs(deltaPercent)
	default:
		return committrackerv1.EvaluationLevel_EVALUATION_LEVEL_NEUTRAL
	}

	if degradePercent >= failThreshold {
		return committrackerv1.EvaluationLevel_EVALUATION_LEVEL_FAIL
	}
	if degradePercent >= warnThreshold {
		return committrackerv1.EvaluationLevel_EVALUATION_LEVEL_WARN
	}
	return committrackerv1.EvaluationLevel_EVALUATION_LEVEL_PASS
}

func evaluationRank(level committrackerv1.EvaluationLevel) int {
	switch level {
	case committrackerv1.EvaluationLevel_EVALUATION_LEVEL_NEUTRAL:
		return 0
	case committrackerv1.EvaluationLevel_EVALUATION_LEVEL_PASS:
		return 1
	case committrackerv1.EvaluationLevel_EVALUATION_LEVEL_WARN:
		return 2
	case committrackerv1.EvaluationLevel_EVALUATION_LEVEL_FAIL:
		return 3
	default:
		return 0
	}
}

func evaluationLabel(level committrackerv1.EvaluationLevel) string {
	switch level {
	case committrackerv1.EvaluationLevel_EVALUATION_LEVEL_PASS:
		return "pass"
	case committrackerv1.EvaluationLevel_EVALUATION_LEVEL_WARN:
		return "warn"
	case committrackerv1.EvaluationLevel_EVALUATION_LEVEL_FAIL:
		return "fail"
	case committrackerv1.EvaluationLevel_EVALUATION_LEVEL_NEUTRAL:
		return "neutral"
	default:
		return "unspecified"
	}
}

func buildMarkdownReport(
	comparisons []*committrackerv1.MetricComparison,
	aggregate committrackerv1.EvaluationLevel,
	baseCommit string,
	headCommit string,
) string {
	builder := strings.Builder{}
	builder.WriteString("## Commit Tracker Report\n\n")
	builder.WriteString("- Base: `" + baseCommit + "`\n")
	builder.WriteString("- Head: `" + headCommit + "`\n")
	builder.WriteString("- Aggregate: **" + strings.ToUpper(evaluationLabel(aggregate)) + "**\n\n")

	builder.WriteString("| metric | base | head | delta | delta% | verdict |\n")
	builder.WriteString("| --- | ---: | ---: | ---: | ---: | --- |\n")

	for _, comparison := range comparisons {
		base := "-"
		if comparison.GetHasBaseValue() {
			base = fmt.Sprintf("%.3f", comparison.GetBaseValue())
		}
		head := "-"
		if comparison.GetHasHeadValue() {
			head = fmt.Sprintf("%.3f", comparison.GetHeadValue())
		}
		builder.WriteString(fmt.Sprintf(
			"| %s | %s | %s | %.3f | %.2f%% | %s |\n",
			comparison.GetMetricKey(),
			base,
			head,
			comparison.GetDelta(),
			comparison.GetDeltaPercent(),
			strings.ToUpper(evaluationLabel(comparison.GetEvaluationLevel())),
		))
	}

	return builder.String()
}

func splitRepository(repository string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(repository), "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", errors.New("repository must be formatted as owner/repo")
	}
	return parts[0], parts[1], nil
}

type githubCommentResponse struct {
	HTMLURL string `json:"html_url"`
	URL     string `json:"url"`
}

type githubStatusResponse struct {
	URL string `json:"url"`
}

func (s *Service) publishGitHubComment(ctx context.Context, owner string, repo string, pullRequest int64, markdown string) (string, error) {
	if strings.TrimSpace(s.githubToken) == "" {
		return "", errors.New("COMMIT_TRACKER_GITHUB_TOKEN is required for report publish")
	}

	var response githubCommentResponse
	if err := s.callGitHubAPI(
		ctx,
		http.MethodPost,
		fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, pullRequest),
		map[string]any{"body": markdown},
		&response,
	); err != nil {
		return "", err
	}

	if response.HTMLURL != "" {
		return response.HTMLURL, nil
	}
	return response.URL, nil
}

func (s *Service) publishGitHubStatus(ctx context.Context, owner string, repo string, headCommitSHA string, aggregate committrackerv1.EvaluationLevel) (string, error) {
	if strings.TrimSpace(s.githubToken) == "" {
		return "", errors.New("COMMIT_TRACKER_GITHUB_TOKEN is required for status publish")
	}

	state := "pending"
	description := "Commit tracker neutral result"
	switch aggregate {
	case committrackerv1.EvaluationLevel_EVALUATION_LEVEL_PASS:
		state = "success"
		description = "Commit tracker checks passed"
	case committrackerv1.EvaluationLevel_EVALUATION_LEVEL_WARN:
		state = "error"
		description = "Commit tracker reported warnings"
	case committrackerv1.EvaluationLevel_EVALUATION_LEVEL_FAIL:
		state = "failure"
		description = "Commit tracker reported failures"
	}

	var response githubStatusResponse
	if err := s.callGitHubAPI(
		ctx,
		http.MethodPost,
		fmt.Sprintf("/repos/%s/%s/statuses/%s", owner, repo, headCommitSHA),
		map[string]any{
			"state":       state,
			"description": description,
			"context":     "commit-tracker",
		},
		&response,
	); err != nil {
		return "", err
	}

	return response.URL, nil
}

func (s *Service) callGitHubAPI(ctx context.Context, method string, path string, payload any, out any) error {
	var requestBody []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		requestBody = encoded
	}

	request, err := http.NewRequestWithContext(
		ctx,
		method,
		s.githubAPIBase+path,
		bytes.NewReader(requestBody),
	)
	if err != nil {
		return err
	}

	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+s.githubToken)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := s.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	body, err := ioReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode >= 300 {
		return &githubAPIError{statusCode: response.StatusCode, body: string(body), method: method, path: path}
	}

	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return err
		}
	}

	return nil
}

func mapGitHubError(err error) error {
	var apiErr *githubAPIError
	if !errors.As(err, &apiErr) {
		if strings.Contains(err.Error(), "COMMIT_TRACKER_GITHUB_TOKEN") {
			return connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewError(connect.CodeUnavailable, err)
	}

	switch apiErr.statusCode {
	case http.StatusUnauthorized:
		return connect.NewError(connect.CodeUnauthenticated, apiErr)
	case http.StatusForbidden:
		return connect.NewError(connect.CodePermissionDenied, apiErr)
	case http.StatusNotFound:
		return connect.NewError(connect.CodeNotFound, apiErr)
	default:
		return connect.NewError(connect.CodeUnavailable, apiErr)
	}
}

func (s *Service) logFailure(operation contracts.Operation, request any, metricKey string, pullRequest int64, err error) {
	provider := ""
	repository := ""
	commit := ""
	runID := ""
	evaluation := ""
	deltaPercent := float64(0)

	switch typed := request.(type) {
	case *committrackerv1.UpsertCommitMetricsRequest:
		provider = typed.GetProvider().String()
		repository = typed.GetRepository()
		commit = typed.GetCommitSha()
		runID = typed.GetRunId()
	case *committrackerv1.ListMetricSeriesRequest:
		provider = typed.GetProvider().String()
		repository = typed.GetRepository()
	case *committrackerv1.GetPullRequestComparisonRequest:
		provider = typed.GetProvider().String()
		repository = typed.GetRepository()
		commit = typed.GetHeadCommitSha()
	case *committrackerv1.PublishPullRequestReportRequest:
		provider = typed.GetProvider().String()
		repository = typed.GetRepository()
		commit = typed.GetHeadCommitSha()
		pullRequest = typed.GetPullRequest()
	}

	s.logger.Event(map[string]any{
		"operation":        operation,
		"result":           contracts.OperationResultFailure,
		"provider":         provider,
		"repository":       repository,
		"pull_request":     pullRequest,
		"commit":           commit,
		"run_id":           runID,
		"metric_key":       metricKey,
		"evaluation_level": evaluation,
		"delta_percent":    deltaPercent,
		"error":            err.Error(),
	})
}

func (s *Service) logDenied(operation contracts.Operation, request any, err error) {
	provider := ""
	repository := ""
	commit := ""
	runID := ""
	pullRequest := int64(0)

	switch typed := request.(type) {
	case *committrackerv1.UpsertCommitMetricsRequest:
		provider = typed.GetProvider().String()
		repository = typed.GetRepository()
		commit = typed.GetCommitSha()
		runID = typed.GetRunId()
	case *committrackerv1.ListMetricSeriesRequest:
		provider = typed.GetProvider().String()
		repository = typed.GetRepository()
	case *committrackerv1.GetPullRequestComparisonRequest:
		provider = typed.GetProvider().String()
		repository = typed.GetRepository()
		commit = typed.GetHeadCommitSha()
	case *committrackerv1.PublishPullRequestReportRequest:
		provider = typed.GetProvider().String()
		repository = typed.GetRepository()
		commit = typed.GetHeadCommitSha()
		pullRequest = typed.GetPullRequest()
	}

	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}

	s.logger.Event(map[string]any{
		"operation":        operation,
		"result":           contracts.OperationResultDenied,
		"provider":         provider,
		"repository":       repository,
		"pull_request":     pullRequest,
		"commit":           commit,
		"run_id":           runID,
		"metric_key":       "",
		"evaluation_level": "",
		"delta_percent":    0,
		"error":            errorMessage,
	})
}

func ioReadAll(body io.Reader) ([]byte, error) {
	buffer := bytes.Buffer{}
	if _, err := buffer.ReadFrom(body); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
