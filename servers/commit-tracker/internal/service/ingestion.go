package service

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"

	committrackerv1 "github.com/delinoio/oss/servers/commit-tracker/gen/proto/committracker/v1"
	"github.com/delinoio/oss/servers/commit-tracker/internal/contracts"
)

// UpsertCommitMetrics upserts metric definitions and inserts commit measurements.
func (s *Service) UpsertCommitMetrics(
	ctx context.Context,
	req *connect.Request[committrackerv1.UpsertCommitMetricsRequest],
) (*connect.Response[committrackerv1.UpsertCommitMetricsResponse], error) {
	msg := req.Msg

	s.logger.Info("upserting commit metrics",
		slog.String("event", contracts.EventUpsertMetrics),
		slog.String("repository", msg.GetRepository()),
		slog.String("commit_sha", msg.GetCommitSha()),
		slog.Int("metric_count", len(msg.GetMetrics())),
	)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer tx.Rollback()

	var measuredAt string
	if msg.GetMeasuredAt() != nil {
		measuredAt = msg.GetMeasuredAt().AsTime().UTC().Format("2006-01-02T15:04:05.000Z")
	}

	upsertedCount := int32(0)

	for _, m := range msg.GetMetrics() {
		// Upsert the metric definition.
		_, err := tx.ExecContext(ctx, `
			INSERT INTO metric_definitions (metric_key, display_name, unit, value_kind, direction, warning_threshold_percent, fail_threshold_percent, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
			ON CONFLICT(metric_key) DO UPDATE SET
				display_name = excluded.display_name,
				unit = excluded.unit,
				value_kind = excluded.value_kind,
				direction = excluded.direction,
				warning_threshold_percent = excluded.warning_threshold_percent,
				fail_threshold_percent = excluded.fail_threshold_percent,
				updated_at = excluded.updated_at
		`,
			m.GetMetricKey(),
			m.GetDisplayName(),
			m.GetUnit(),
			int32(m.GetValueKind()),
			int32(m.GetDirection()),
			m.GetWarningThresholdPercent(),
			m.GetFailThresholdPercent(),
		)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		// Insert the measurement.
		_, err = tx.ExecContext(ctx, `
			INSERT INTO commit_measurements (provider, repository, branch, commit_sha, run_id, environment, metric_key, metric_value, measured_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(provider, repository, commit_sha, run_id, environment, metric_key) DO UPDATE SET
				metric_value = excluded.metric_value,
				measured_at = excluded.measured_at
		`,
			int32(msg.GetProvider()),
			msg.GetRepository(),
			msg.GetBranch(),
			msg.GetCommitSha(),
			msg.GetRunId(),
			msg.GetEnvironment(),
			m.GetMetricKey(),
			m.GetValue(),
			measuredAt,
		)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		upsertedCount++
	}

	if err := tx.Commit(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&committrackerv1.UpsertCommitMetricsResponse{
		UpsertedCount: upsertedCount,
	}), nil
}
