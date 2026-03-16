package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	committrackerv1 "github.com/delinoio/oss/servers/commit-tracker/gen/proto/committracker/v1"
	"github.com/delinoio/oss/servers/commit-tracker/internal/contracts"
)

// ListMetricSeries queries measurements by scope and time range.
func (s *Service) ListMetricSeries(
	ctx context.Context,
	req *connect.Request[committrackerv1.ListMetricSeriesRequest],
) (*connect.Response[committrackerv1.ListMetricSeriesResponse], error) {
	msg := req.Msg

	s.logger.Info("listing metric series",
		slog.String("event", contracts.EventListSeries),
		slog.String("repository", msg.GetRepository()),
		slog.String("branch", msg.GetBranch()),
		slog.String("metric_key", msg.GetMetricKey()),
	)

	query := `
		SELECT
			cm.metric_key,
			md.display_name,
			md.unit,
			md.value_kind,
			md.direction,
			md.warning_threshold_percent,
			md.fail_threshold_percent,
			cm.metric_value,
			cm.commit_sha,
			cm.run_id,
			cm.measured_at
		FROM commit_measurements cm
		JOIN metric_definitions md ON md.metric_key = cm.metric_key
		WHERE cm.provider = ?
		  AND cm.repository = ?
		  AND cm.branch = ?
		  AND cm.environment = ?
		  AND cm.metric_key = ?
	`
	args := []any{
		int32(msg.GetProvider()),
		msg.GetRepository(),
		msg.GetBranch(),
		msg.GetEnvironment(),
		msg.GetMetricKey(),
	}

	if msg.GetFromTime() != nil {
		query += " AND cm.measured_at >= ?"
		args = append(args, msg.GetFromTime().AsTime().UTC().Format(time.RFC3339Nano))
	}
	if msg.GetToTime() != nil {
		query += " AND cm.measured_at <= ?"
		args = append(args, msg.GetToTime().AsTime().UTC().Format(time.RFC3339Nano))
	}

	query += " ORDER BY cm.measured_at DESC"

	limit := msg.GetLimit()
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	query += fmt.Sprintf(" LIMIT %d", limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer rows.Close()

	var points []*committrackerv1.MetricSeriesPoint
	for rows.Next() {
		var (
			metricKey               string
			displayName             string
			unit                    string
			valueKind               int32
			direction               int32
			warningThresholdPercent float64
			failThresholdPercent    float64
			metricValue             float64
			commitSHA               string
			runID                   string
			measuredAt              string
		)
		if err := rows.Scan(
			&metricKey, &displayName, &unit, &valueKind, &direction,
			&warningThresholdPercent, &failThresholdPercent,
			&metricValue, &commitSHA, &runID, &measuredAt,
		); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		point := &committrackerv1.MetricSeriesPoint{
			MetricKey:               metricKey,
			DisplayName:             displayName,
			Unit:                    unit,
			ValueKind:               committrackerv1.MetricValueKind(valueKind),
			Direction:               committrackerv1.MetricDirection(direction),
			WarningThresholdPercent: warningThresholdPercent,
			FailThresholdPercent:    failThresholdPercent,
			Value:                   metricValue,
			CommitSha:               commitSHA,
			RunId:                   runID,
			MeasuredAt:              parseTimestamp(measuredAt),
		}
		points = append(points, point)
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&committrackerv1.ListMetricSeriesResponse{
		Points: points,
	}), nil
}

// GetPullRequestComparison compares metrics between base and head commits.
func (s *Service) GetPullRequestComparison(
	ctx context.Context,
	req *connect.Request[committrackerv1.GetPullRequestComparisonRequest],
) (*connect.Response[committrackerv1.GetPullRequestComparisonResponse], error) {
	msg := req.Msg

	s.logger.Info("getting pull request comparison",
		slog.String("event", contracts.EventGetComparison),
		slog.String("repository", msg.GetRepository()),
		slog.String("base_commit", msg.GetBaseCommitSha()),
		slog.String("head_commit", msg.GetHeadCommitSha()),
	)

	// Determine which metric keys to compare.
	metricKeys := msg.GetMetricKeys()
	if len(metricKeys) == 0 {
		// Find all keys that exist for either commit.
		rows, err := s.db.QueryContext(ctx, `
			SELECT DISTINCT metric_key FROM commit_measurements
			WHERE provider = ? AND repository = ? AND environment = ?
			  AND commit_sha IN (?, ?)
		`,
			int32(msg.GetProvider()),
			msg.GetRepository(),
			msg.GetEnvironment(),
			msg.GetBaseCommitSha(),
			msg.GetHeadCommitSha(),
		)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		defer rows.Close()

		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				return nil, connect.NewError(connect.CodeInternal, err)
			}
			metricKeys = append(metricKeys, key)
		}
		if err := rows.Err(); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	aggregateEval := committrackerv1.EvaluationLevel_EVALUATION_LEVEL_PASS
	var comparisons []*committrackerv1.MetricComparison

	for _, key := range metricKeys {
		comp, err := s.compareMetric(ctx, msg, key)
		if err != nil {
			return nil, err
		}
		comparisons = append(comparisons, comp)

		// Aggregate: worst evaluation wins.
		if comp.EvaluationLevel > aggregateEval {
			aggregateEval = comp.EvaluationLevel
		}
	}

	return connect.NewResponse(&committrackerv1.GetPullRequestComparisonResponse{
		Provider:            msg.GetProvider(),
		Repository:          msg.GetRepository(),
		BaseCommitSha:       msg.GetBaseCommitSha(),
		HeadCommitSha:       msg.GetHeadCommitSha(),
		Environment:         msg.GetEnvironment(),
		Comparisons:         comparisons,
		AggregateEvaluation: aggregateEval,
	}), nil
}

func (s *Service) compareMetric(
	ctx context.Context,
	msg *committrackerv1.GetPullRequestComparisonRequest,
	metricKey string,
) (*committrackerv1.MetricComparison, error) {
	var (
		displayName             string
		unit                    string
		valueKind               int32
		direction               int32
		warningThresholdPercent float64
		failThresholdPercent    float64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT display_name, unit, value_kind, direction, warning_threshold_percent, fail_threshold_percent
		FROM metric_definitions WHERE metric_key = ?
	`, metricKey).Scan(&displayName, &unit, &valueKind, &direction, &warningThresholdPercent, &failThresholdPercent)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("metric definition not found: %s", metricKey))
	}

	baseValue, hasBase := s.getCommitMetricValue(ctx, msg.GetProvider(), msg.GetRepository(), msg.GetBaseCommitSha(), msg.GetEnvironment(), metricKey)
	headValue, hasHead := s.getCommitMetricValue(ctx, msg.GetProvider(), msg.GetRepository(), msg.GetHeadCommitSha(), msg.GetEnvironment(), metricKey)

	delta := 0.0
	deltaPercent := 0.0
	if hasBase && hasHead {
		delta = headValue - baseValue
		if baseValue != 0 {
			deltaPercent = (delta / math.Abs(baseValue)) * 100
		}
	}

	evalLevel := evaluateChange(
		committrackerv1.MetricDirection(direction),
		deltaPercent,
		warningThresholdPercent,
		failThresholdPercent,
		hasBase,
		hasHead,
	)

	return &committrackerv1.MetricComparison{
		MetricKey:               metricKey,
		DisplayName:             displayName,
		Unit:                    unit,
		ValueKind:               committrackerv1.MetricValueKind(valueKind),
		Direction:               committrackerv1.MetricDirection(direction),
		WarningThresholdPercent: warningThresholdPercent,
		FailThresholdPercent:    failThresholdPercent,
		BaseValue:               baseValue,
		HeadValue:               headValue,
		Delta:                   delta,
		DeltaPercent:            deltaPercent,
		EvaluationLevel:         evalLevel,
		HasBaseValue:            hasBase,
		HasHeadValue:            hasHead,
	}, nil
}

func (s *Service) getCommitMetricValue(
	ctx context.Context,
	provider committrackerv1.GitProviderKind,
	repository, commitSHA, environment, metricKey string,
) (float64, bool) {
	var value float64
	err := s.db.QueryRowContext(ctx, `
		SELECT metric_value FROM commit_measurements
		WHERE provider = ? AND repository = ? AND commit_sha = ? AND environment = ? AND metric_key = ?
		ORDER BY measured_at DESC LIMIT 1
	`, int32(provider), repository, commitSHA, environment, metricKey).Scan(&value)
	if err != nil {
		return 0, false
	}
	return value, true
}

// evaluateChange determines the evaluation level based on metric direction and delta.
func evaluateChange(
	direction committrackerv1.MetricDirection,
	deltaPercent, warningThreshold, failThreshold float64,
	hasBase, hasHead bool,
) committrackerv1.EvaluationLevel {
	if !hasBase || !hasHead {
		return committrackerv1.EvaluationLevel_EVALUATION_LEVEL_NEUTRAL
	}

	var badDelta float64
	switch direction {
	case committrackerv1.MetricDirection_METRIC_DIRECTION_INCREASE_IS_BETTER:
		badDelta = -deltaPercent
	case committrackerv1.MetricDirection_METRIC_DIRECTION_DECREASE_IS_BETTER:
		badDelta = deltaPercent
	default:
		return committrackerv1.EvaluationLevel_EVALUATION_LEVEL_NEUTRAL
	}

	if failThreshold > 0 && badDelta >= failThreshold {
		return committrackerv1.EvaluationLevel_EVALUATION_LEVEL_FAIL
	}
	if warningThreshold > 0 && badDelta >= warningThreshold {
		return committrackerv1.EvaluationLevel_EVALUATION_LEVEL_WARN
	}
	return committrackerv1.EvaluationLevel_EVALUATION_LEVEL_PASS
}

// parseTimestamp parses an ISO 8601 timestamp string into a protobuf Timestamp.
func parseTimestamp(s string) *timestamppb.Timestamp {
	if s == "" {
		return nil
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		t, err := time.Parse(layout, s)
		if err == nil {
			return timestamppb.New(t)
		}
	}
	return nil
}
