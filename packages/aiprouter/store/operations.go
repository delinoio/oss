// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"time"
)

type AllowedDestination = core.DestinationRule

type Settings struct {
	DestinationRules                                      []AllowedDestination
	Revision                                              int64
	RPM, Concurrency, UsageDays, AggregateDays, AuditDays int
	Destinations                                          []string
	Prices                                                json.RawMessage
}

func (s *Store) Settings(ctx context.Context) (v Settings, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error { var err error; v, err = readSettings(ctx, tx, false); return err })
	return
}
func (s *Store) IsOperator(i core.Identity) bool { return s.operators[i] }
func (s *Store) UpdateSettings(ctx context.Context, i core.Identity, v Settings) error {
	if !s.IsOperator(i) {
		return ErrForbidden
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := readSettings(ctx, tx, true); e != nil {
			return e
		}
		_, e := s.writeSettings(ctx, tx, i, v)
		return e
	})
}

type Usage struct {
	Outcome                                                RequestOutcome
	PriceKnown                                             bool
	ID, OrganizationID, UserID, Model, Provider, ErrorCode string
	InputTokens, OutputTokens                              *int64
	Cost                                                   *string
	Price                                                  json.RawMessage
	Provenance                                             string
	CreatedAt                                              time.Time
}
type Aggregate struct {
	MissingPrice, FailedRequests, KnownInput, KnownOutput, KnownCost int64
	Requests, InputTokens, OutputTokens, MissingUsage, MissingCost   int64
	Cost                                                             string
}

// RecordUsage freezes supplied price provenance and cost. The lease ID is the
// idempotency key, so replaying completion cannot double-count daily totals.
func (s *Store) recordUsage(ctx context.Context, l Lease, u Usage) error {
	return s.recordUsageReceipt(ctx, l, "", u)
}
func (s *Store) recordUsageReceipt(ctx context.Context, l Lease, operationID string, u Usage) error {
	if u.InputTokens != nil && *u.InputTokens < 0 || u.OutputTokens != nil && *u.OutputTokens < 0 {
		return ErrConflict
	}
	if u.Outcome == "" {
		u.Outcome = OutcomeUnknown
	}
	switch u.Outcome {
	case OutcomeUnknown, OutcomeSucceeded, OutcomeFailed, OutcomeCanceled, OutcomeUpstreamError, OutcomeGatewayError, OutcomeRateLimited:
	default:
		return ErrConflict
	}
	if len(u.Price) == 0 {
		u.Price = json.RawMessage(`null`)
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		sc := l.Principal.Scope
		if e := s.lock(ctx, tx, sc.OrganizationID); e != nil {
			return e
		}
		var ok bool
		e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM admissions a JOIN users u ON u.id=a.user_id WHERE a.id=$1 AND a.org_id=$2 AND a.user_id=$3 AND a.key_id=$4 AND a.generation=$5)", l.ID, sc.OrganizationID, sc.UserID, l.Principal.KeyID, sc.Generation).Scan(&ok)
		if e != nil {
			return e
		}
		if !ok {
			return ErrForbidden
		}
		usageID := l.ID
		if operationID == "" {
			reserved, e := tx.ExecContext(ctx, "UPDATE admissions SET usage_recorded=true WHERE id=$1 AND NOT usage_recorded", l.ID)
			if e != nil {
				return e
			}
			changed, _ := reserved.RowsAffected()
			if changed == 0 {
				return nil
			}
		} else {
			usageID = core.ID()
			res, e := tx.ExecContext(ctx, "INSERT INTO operation_receipts(admission_id,operation_id,usage_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", l.ID, operationID, usageID)
			if e != nil {
				return e
			}
			changed, _ := res.RowsAffected()
			if changed == 0 {
				return nil
			}
		}
		res, e := tx.ExecContext(ctx, `INSERT INTO usage(id,org_id,user_id,model,provider,error_code,input_tokens,output_tokens,cost,price,provenance,outcome,price_known,expires_at) SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,clock_timestamp()+usage_days*interval '1 day' FROM settings ON CONFLICT(id) DO NOTHING`, usageID, sc.OrganizationID, sc.UserID, u.Model, u.Provider, u.ErrorCode, u.InputTokens, u.OutputTokens, u.Cost, u.Price, u.Provenance, u.Outcome, u.PriceKnown)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return nil
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO daily_usage(id,org_id,user_id,day,model,provider,outcome,requests,input_tokens,output_tokens,cost,missing_usage,missing_cost,missing_price,failed_requests,known_input,known_output,known_cost,expires_at)
 SELECT $1,$2,$3,(clock_timestamp() AT TIME ZONE 'UTC')::date,$4,$8,$9,1,COALESCE($5::bigint,0),COALESCE($6::bigint,0),COALESCE($7::numeric,0),CASE WHEN $5::bigint IS NULL OR $6::bigint IS NULL THEN 1 ELSE 0 END,CASE WHEN $7::numeric IS NULL THEN 1 ELSE 0 END,CASE WHEN $10 THEN 0 ELSE 1 END,CASE WHEN $9 IN ('failed','upstream_error','gateway_error','rate_limited') THEN 1 ELSE 0 END,CASE WHEN $5::bigint IS NULL THEN 0 ELSE 1 END,CASE WHEN $6::bigint IS NULL THEN 0 ELSE 1 END,CASE WHEN $7::numeric IS NULL THEN 0 ELSE 1 END,clock_timestamp()+aggregate_days*interval '1 day' FROM settings
 ON CONFLICT(org_id,user_id,day,model,provider,outcome) DO UPDATE SET requests=daily_usage.requests+1,input_tokens=daily_usage.input_tokens+excluded.input_tokens,output_tokens=daily_usage.output_tokens+excluded.output_tokens,cost=daily_usage.cost+excluded.cost,missing_usage=daily_usage.missing_usage+excluded.missing_usage,missing_cost=daily_usage.missing_cost+excluded.missing_cost,missing_price=daily_usage.missing_price+excluded.missing_price,failed_requests=daily_usage.failed_requests+excluded.failed_requests,known_input=daily_usage.known_input+excluded.known_input,known_output=daily_usage.known_output+excluded.known_output,known_cost=daily_usage.known_cost+excluded.known_cost`, core.ID(), sc.OrganizationID, sc.UserID, u.Model, u.InputTokens, u.OutputTokens, u.Cost, u.Provider, u.Outcome, u.PriceKnown)
		return e
	})
}
func (s *Store) ListUsage(ctx context.Context, sc core.Scope, user string, limit int) (out []Usage, e error) {
	out, _, e = s.QueryUsage(ctx, sc, UsageFilter{UserID: user, Limit: limit})
	return
}
func (s *Store) AggregateUsage(ctx context.Context, sc core.Scope, user string) (Aggregate, error) {
	return s.QueryAggregate(ctx, sc, UsageFilter{UserID: user})
}
func (s *Store) InstanceAggregate(ctx context.Context, i core.Identity) (Aggregate, error) {
	return s.QueryInstanceAggregate(ctx, i, UsageFilter{})
}

type Audit struct {
	Outcome                                       AuditOutcome
	ID, OrganizationID, ActorID, Action, TargetID string
	CreatedAt                                     time.Time
}

func (s *Store) ListAudits(ctx context.Context, sc core.Scope) (out []Audit, e error) {
	out, _, e = s.QueryAudits(ctx, sc, AuditFilter{Limit: 1000})
	return
}
func (s *Store) InstanceAudits(ctx context.Context, i core.Identity) (out []Audit, e error) {
	out, _, e = s.QueryInstanceAudits(ctx, i, AuditFilter{Limit: 1000})
	return
}
func (s *Store) cleanup(ctx context.Context) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		// Use the same gate as request and device mutations before deleting rows.
		rows, e := tx.QueryContext(ctx, "SELECT id FROM organizations ORDER BY id")
		if e != nil {
			return e
		}
		var ids []string
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			if e = s.lock(ctx, tx, id); e != nil {
				return e
			}
		}

		queries := []string{
			"UPDATE settings SET usage_history_floor=GREATEST(usage_history_floor,clock_timestamp()-usage_days*interval '1 day')",
			"DELETE FROM " + s.temp + ".provider_flows WHERE expires_at<=clock_timestamp()",
			"DELETE FROM " + s.temp + ".provider_states WHERE expires_at<=clock_timestamp()",
			"DELETE FROM " + s.temp + ".native_quotas WHERE expires_at<=clock_timestamp()",
			"DELETE FROM " + s.temp + ".native_affinities WHERE expires_at<=clock_timestamp()",
			"DELETE FROM " + s.temp + ".compaction_handoffs WHERE expires_at<=clock_timestamp()",
			"DELETE FROM " + s.temp + ".vertex_projects WHERE (value IS NULL AND claim_until<=clock_timestamp()) OR (expires_at<=clock_timestamp() AND claim_hash IS NULL)",
			"DELETE FROM native_recovery_buckets WHERE bucket_start<=clock_timestamp()-interval '10 seconds'",
			"DELETE FROM quota_collection_claims WHERE claim_until<=clock_timestamp() AND next_at<=clock_timestamp()-interval '1 day'",
			"DELETE FROM native_health_permits WHERE created_at<=clock_timestamp()-interval '1 day' AND (status IN ('finished','released') OR NOT probe)",
			"DELETE FROM native_health WHERE updated_at<=clock_timestamp()-interval '32 days' AND (state->>'ProbeID'='' OR (lane='transient' AND (state->>'ProbeExpiresAt')::timestamptz<=clock_timestamp()))",
			"DELETE FROM selection_affinity WHERE expires_at<=clock_timestamp()",
			"DELETE FROM account_cooldowns WHERE until_at<=clock_timestamp()",
			"DELETE FROM account_soft_denials WHERE until_at<=clock_timestamp()",
			"UPDATE api_keys SET revoked=true WHERE NOT handed_off AND handoff_deadline<=clock_timestamp()",
			// Keep only safe terminal metadata for a bounded browser status window.
			"UPDATE " + s.temp + ".devices SET value=NULL WHERE expires_at<=clock_timestamp()",
			"DELETE FROM " + s.temp + ".devices WHERE expires_at<=clock_timestamp()-interval '24 hours'",
			"DELETE FROM " + s.temp + ".states WHERE expires_at<=clock_timestamp()",
			"DELETE FROM " + s.temp + ".login_states WHERE expires_at<=clock_timestamp()",
			"DELETE FROM sessions WHERE created_at<=clock_timestamp()-interval '7 days' OR last_seen<=clock_timestamp()-interval '24 hours'",
			"UPDATE invitations SET status='failed',delivery_state='failed',claim=NULL WHERE status='pending' AND delivery_state<>'sent' AND claim IS NOT NULL AND attempts>=3 AND claim_until<=clock_timestamp()",
			"DELETE FROM " + s.temp + ".mail_tokens WHERE invitation_id IN(SELECT id FROM invitations WHERE status<>'pending' OR expires_at<=clock_timestamp())",
			// Keep terminal metadata briefly for authenticated recipient inspection; raw mail tokens are already removed.
			"DELETE FROM invitations WHERE expires_at<=clock_timestamp()-interval '24 hours'",
			"DELETE FROM transfers WHERE expires_at<=clock_timestamp()",
			"DELETE FROM usage WHERE expires_at<=clock_timestamp()",
			"DELETE FROM daily_usage WHERE expires_at<=clock_timestamp()",
			"DELETE FROM audits WHERE expires_at<=clock_timestamp()",
			"DELETE FROM mail_attempts WHERE attempted_at<=clock_timestamp()-interval '1 hour'",
			// Retain admission IDs beyond usage TTL so duplicate completion cannot recreate expired usage.
			"DELETE FROM admissions WHERE released AND created_at<=clock_timestamp()-interval '731 days'",
			"DELETE FROM artifacts WHERE expires_at<=clock_timestamp() OR (state='pending' AND upload_deadline<=clock_timestamp())",
		}
		for _, q := range queries {
			if _, e := tx.ExecContext(ctx, q); e != nil {
				return e
			}
		}
		return nil
	})
}

// InvalidateAfterRestore must run offline, before any listeners or workers start.
func (s *Store) InvalidateAfterRestore(ctx context.Context) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{"UPDATE api_keys SET revoked=true", "DELETE FROM sessions", "UPDATE invitations SET status='canceled',claim=NULL", "DELETE FROM transfers", "DELETE FROM admissions", "DELETE FROM " + s.temp + ".devices", "DELETE FROM " + s.temp + ".mail_tokens", "DELETE FROM " + s.temp + ".login_states", "DELETE FROM " + s.temp + ".states", "DELETE FROM " + s.temp + ".provider_flows", "DELETE FROM " + s.temp + ".provider_states", "DELETE FROM " + s.temp + ".native_quotas", "DELETE FROM " + s.temp + ".native_affinities", "DELETE FROM " + s.temp + ".vertex_projects", "DELETE FROM " + s.temp + ".compaction_handoffs", "UPDATE private_refresh SET state='uncertain'"} {
			if _, e := tx.ExecContext(ctx, q); e != nil {
				return e
			}
		}
		return nil
	})
}
