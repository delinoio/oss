// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/delinoio/oss/packages/tairouter/core"
	"time"
)

type RequestOutcome string

const (
	OutcomeUnknown       RequestOutcome = "unknown"
	OutcomeSucceeded     RequestOutcome = "succeeded"
	OutcomeFailed        RequestOutcome = "failed"
	OutcomeCanceled      RequestOutcome = "canceled"
	OutcomeUpstreamError RequestOutcome = "upstream_error"
	OutcomeGatewayError  RequestOutcome = "gateway_error"
	OutcomeRateLimited   RequestOutcome = "rate_limited"
)

type AuditOutcome string

const (
	AuditUnknown   AuditOutcome = "unknown"
	AuditSucceeded AuditOutcome = "succeeded"
	AuditFailed    AuditOutcome = "failed"
	AuditDenied    AuditOutcome = "denied"
)

var ErrRangeUnavailable = errors.New("exact partial-day history is outside retained request metadata")

type JobHealth struct {
	LastSuccess, LastFailure *time.Time
	ConsecutiveFailures      int64
}
type HealthStatus struct{ Cleanup, UsageRecording JobHealth }

func (s *Store) OperationalHealth(ctx context.Context, i core.Identity) (out HealthStatus, e error) {
	if !s.IsOperator(i) {
		return out, ErrForbidden
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		rows, e := tx.QueryContext(ctx, "SELECT job,last_success,last_failure,consecutive_failures FROM operational_health")
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var job string
			var j JobHealth
			if e = rows.Scan(&job, &j.LastSuccess, &j.LastFailure, &j.ConsecutiveFailures); e != nil {
				return e
			}
			if job == "cleanup" {
				out.Cleanup = j
			}
			if job == "usage" {
				out.UsageRecording = j
			}
		}
		return rows.Err()
	})
	return
}
func (s *Store) recordHealth(ctx context.Context, job string, success bool) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "INSERT INTO operational_health(job,last_success,last_failure,consecutive_failures) VALUES($1,CASE WHEN $2 THEN clock_timestamp() END,CASE WHEN NOT $2 THEN clock_timestamp() END,CASE WHEN $2 THEN 0 ELSE 1 END) ON CONFLICT(job) DO UPDATE SET last_success=CASE WHEN $2 THEN clock_timestamp() ELSE operational_health.last_success END,last_failure=CASE WHEN NOT $2 THEN clock_timestamp() ELSE operational_health.last_failure END,consecutive_failures=CASE WHEN $2 THEN 0 ELSE operational_health.consecutive_failures+1 END", job, success)
		return e
	})
}
func (s *Store) RecordUsageFailure(ctx context.Context) error {
	return s.recordHealth(ctx, "usage", false)
}
func (s *Store) RecordUsage(ctx context.Context, l Lease, u Usage) error {
	e := s.recordUsage(ctx, l, u)
	if e != nil {
		if h := s.recordHealth(ctx, "usage", false); h != nil {
			return errors.Join(e, h)
		}
		return e
	}
	return s.recordHealth(ctx, "usage", true)
}
func (s *Store) Cleanup(ctx context.Context) error {
	e := s.cleanup(ctx)
	h := s.recordHealth(ctx, "cleanup", e == nil)
	return errors.Join(e, h)
}

func (s *Store) RecordOperationUsage(ctx context.Context, l Lease, operationID string, u Usage) error {
	if operationID == "" || len(operationID) > 512 {
		return ErrConflict
	}
	e := s.recordUsageReceipt(ctx, l, operationID, u)
	if e != nil {
		return errors.Join(e, s.recordHealth(ctx, "usage", false))
	}
	return s.recordHealth(ctx, "usage", true)
}
