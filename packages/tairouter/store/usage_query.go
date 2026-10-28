// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/delinoio/oss/packages/tairouter/core"
	"time"
)

type UsageFilter struct {
	UserID, Model, Provider, After string
	Outcome                        RequestOutcome
	Start, End                     *time.Time
	Limit                          int
}
type DailyUsage struct {
	Date          time.Time
	UserID, Model string
	Aggregate
}
type pageCursor struct {
	Time  time.Time `json:"t"`
	ID    string    `json:"i"`
	User  string    `json:"u,omitempty"`
	Model string    `json:"m,omitempty"`
}

func validRange(start, end *time.Time) error {
	if start != nil && end != nil && !start.Before(*end) {
		return ErrConflict
	}
	return nil
}
func (s *Store) cursor(raw, context string) (c pageCursor, e error) {
	if raw == "" {
		return
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return c, ErrConflict
	}
	p, e := s.keys.Decrypt(b, context)
	if e != nil {
		return c, ErrConflict
	}
	if e = json.Unmarshal(p, &c); e != nil {
		return c, ErrConflict
	}
	return
}
func (s *Store) makeCursor(c pageCursor, context string) (string, error) {
	p, e := json.Marshal(c)
	if e != nil {
		return "", e
	}
	b, e := s.keys.Encrypt(p, context)
	if e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func cursorContext(kind string, scope any, filter any) string {
	b, _ := json.Marshal([]any{scope, filter})
	return kind + "/" + hex.EncodeToString(core.Hash(string(b)))
}
func (s *Store) usageGuard(ctx context.Context, tx *sql.Tx, sc core.Scope, f UsageFilter) error {
	if e := validRange(f.Start, f.End); e != nil {
		return e
	}
	r, e := s.guard(ctx, tx, sc)
	if e != nil {
		return e
	}
	if f.UserID != sc.UserID && !elevated(r) {
		return ErrForbidden
	}
	return nil
}
func (s *Store) QueryUsage(ctx context.Context, sc core.Scope, f UsageFilter) (out []Usage, next string, e error) {
	if f.Limit < 1 || f.Limit > 1000 {
		f.Limit = 100
	}
	binding := f
	binding.After = ""
	aad := cursorContext("usage-page", sc, binding)
	c, e := s.cursor(f.After, aad)
	if e != nil {
		return nil, "", e
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.usageGuard(ctx, tx, sc, f); e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, `SELECT id,org_id,COALESCE(user_id,''),model,provider,error_code,input_tokens,output_tokens,cost::text,price,provenance,created_at,outcome,price_known FROM usage WHERE org_id=$1 AND ($2='' OR user_id=$2) AND ($3='' OR model=$3) AND ($4::timestamptz IS NULL OR created_at >= $4) AND ($5::timestamptz IS NULL OR created_at < $5) AND ($6='' OR provider=$6) AND ($7='' OR outcome=$7) AND ($8='' OR (created_at,id)<($9,$8)) AND expires_at>clock_timestamp() ORDER BY created_at DESC,id DESC LIMIT $10`, sc.OrganizationID, f.UserID, f.Model, f.Start, f.End, f.Provider, f.Outcome, c.ID, c.Time, f.Limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var u Usage
			if e = rows.Scan(&u.ID, &u.OrganizationID, &u.UserID, &u.Model, &u.Provider, &u.ErrorCode, &u.InputTokens, &u.OutputTokens, &u.Cost, &u.Price, &u.Provenance, &u.CreatedAt, &u.Outcome, &u.PriceKnown); e != nil {
				return e
			}
			out = append(out, u)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out) > f.Limit {
			out = out[:f.Limit]
			last := out[len(out)-1]
			next, e = s.makeCursor(pageCursor{Time: last.CreatedAt, ID: last.ID}, aad)
		}
		return e
	})
	return
}

const sums = `COALESCE(sum(requests),0),COALESCE(sum(input_tokens),0),COALESCE(sum(output_tokens),0),COALESCE(sum(cost),0)::text,COALESCE(sum(missing_usage),0),COALESCE(sum(missing_cost),0),COALESCE(sum(missing_price),0),COALESCE(sum(failed_requests),0),COALESCE(sum(known_input),0),COALESCE(sum(known_output),0),COALESCE(sum(known_cost),0)`

func scanAggregate(row interface{ Scan(...any) error }, a *Aggregate) error {
	return row.Scan(&a.Requests, &a.InputTokens, &a.OutputTokens, &a.Cost, &a.MissingUsage, &a.MissingCost, &a.MissingPrice, &a.FailedRequests, &a.KnownInput, &a.KnownOutput, &a.KnownCost)
}

// Complete UTC days use long-lived daily aggregates. Only partial boundary days
// use retained request detail; fabricating old partial-day totals is forbidden.
func usageSource(ctx context.Context, tx *sql.Tx, org string, f UsageFilter) (string, []any, error) {
	if e := validRange(f.Start, f.End); e != nil {
		return "", nil, e
	}
	var floor time.Time
	if e := tx.QueryRowContext(ctx, "SELECT GREATEST(usage_history_floor,clock_timestamp()-usage_days*interval '1 day') FROM settings").Scan(&floor); e != nil {
		return "", nil, e
	}
	for n, t := range []*time.Time{f.Start, f.End} {
		if t == nil {
			continue
		}
		utc := t.UTC()
		day := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
		if utc.Equal(day) {
			continue
		}
		needed := utc
		if n == 1 {
			needed = day
			if f.Start != nil && f.Start.After(needed) {
				needed = *f.Start
			}
		}
		if needed.Before(floor) {
			return "", nil, ErrRangeUnavailable
		}
	}

	args := []any{org, f.UserID, f.Model, f.Start, f.End, f.Provider, f.Outcome}
	q := `WITH records AS (
 SELECT day,COALESCE(user_id,'') AS user_id,model,requests,input_tokens,output_tokens,cost,missing_usage,missing_cost,missing_price,failed_requests,known_input,known_output,known_cost FROM daily_usage WHERE ($1='' OR org_id=$1) AND ($2='' OR user_id=$2) AND ($3='' OR model=$3) AND ($4::timestamptz IS NULL OR day::timestamp AT TIME ZONE 'UTC'>=$4) AND ($5::timestamptz IS NULL OR (day+1)::timestamp AT TIME ZONE 'UTC'<=$5) AND ($6='' OR provider=$6) AND ($7='' OR outcome=$7) AND expires_at>clock_timestamp()
 UNION ALL
 SELECT (created_at AT TIME ZONE 'UTC')::date,COALESCE(user_id,''),model,1,COALESCE(input_tokens,0),COALESCE(output_tokens,0),COALESCE(cost,0),CASE WHEN input_tokens IS NULL OR output_tokens IS NULL THEN 1 ELSE 0 END,CASE WHEN cost IS NULL THEN 1 ELSE 0 END,CASE WHEN price_known THEN 0 ELSE 1 END,CASE WHEN outcome IN ('failed','upstream_error','gateway_error','rate_limited') THEN 1 ELSE 0 END,CASE WHEN input_tokens IS NULL THEN 0 ELSE 1 END,CASE WHEN output_tokens IS NULL THEN 0 ELSE 1 END,CASE WHEN cost IS NULL THEN 0 ELSE 1 END FROM usage WHERE ($1='' OR org_id=$1) AND ($2='' OR user_id=$2) AND ($3='' OR model=$3) AND ($4::timestamptz IS NULL OR created_at>=$4) AND ($5::timestamptz IS NULL OR created_at<$5) AND ($6='' OR provider=$6) AND ($7='' OR outcome=$7) AND expires_at>clock_timestamp() AND (($4::timestamptz IS NOT NULL AND date_trunc('day',created_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'<$4) OR ($5::timestamptz IS NOT NULL AND (date_trunc('day',created_at AT TIME ZONE 'UTC')+interval '1 day') AT TIME ZONE 'UTC'>$5))
 ) `
	return q, args, nil
}
func (s *Store) QueryAggregate(ctx context.Context, sc core.Scope, f UsageFilter) (a Aggregate, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.usageGuard(ctx, tx, sc, f); e != nil {
			return e
		}
		q, args, e := usageSource(ctx, tx, sc.OrganizationID, f)
		if e != nil {
			return e
		}
		return scanAggregate(tx.QueryRowContext(ctx, q+"SELECT "+sums+" FROM records", args...), &a)
	})
	return
}
func (s *Store) QueryInstanceAggregate(ctx context.Context, i core.Identity, f UsageFilter) (a Aggregate, e error) {
	if !s.IsOperator(i) || f.UserID != "" || f.Model != "" || f.Provider != "" || f.Outcome != "" {
		return a, ErrForbidden
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		q, args, e := usageSource(ctx, tx, "", f)
		if e != nil {
			return e
		}
		return scanAggregate(tx.QueryRowContext(ctx, q+"SELECT "+sums+" FROM records", args...), &a)
	})
	return
}
func (s *Store) QueryDailyUsagePage(ctx context.Context, sc core.Scope, f UsageFilter) (out []DailyUsage, next string, e error) {
	if f.Limit < 1 || f.Limit > 1000 {
		f.Limit = 100
	}
	binding := f
	binding.After = ""
	aad := cursorContext("daily-page", sc, binding)
	c, e := s.cursor(f.After, aad)
	if e != nil {
		return nil, "", e
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.usageGuard(ctx, tx, sc, f); e != nil {
			return e
		}
		q, args, e := usageSource(ctx, tx, sc.OrganizationID, f)
		if e != nil {
			return e
		}
		args = append(args, c.ID, c.Time, c.User, c.Model, f.Limit+1)
		rows, e := tx.QueryContext(ctx, q+"SELECT day,user_id,model,"+sums+" FROM records WHERE ($8='' OR day<$9::date OR (day=$9::date AND (user_id,model)>($10,$11))) GROUP BY day,user_id,model ORDER BY day DESC,user_id,model LIMIT $12", args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var d DailyUsage
			if e = rows.Scan(&d.Date, &d.UserID, &d.Model, &d.Requests, &d.InputTokens, &d.OutputTokens, &d.Cost, &d.MissingUsage, &d.MissingCost, &d.MissingPrice, &d.FailedRequests, &d.KnownInput, &d.KnownOutput, &d.KnownCost); e != nil {
				return e
			}
			out = append(out, d)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out) > f.Limit {
			out = out[:f.Limit]
			last := out[len(out)-1]
			next, e = s.makeCursor(pageCursor{Time: last.Date, ID: "daily", User: last.UserID, Model: last.Model}, aad)
		}
		return e
	})
	return
}
func (s *Store) QueryDailyUsage(ctx context.Context, sc core.Scope, f UsageFilter) (out []DailyUsage, e error) {
	for {
		rows, next, err := s.QueryDailyUsagePage(ctx, sc, f)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if next == "" {
			return out, nil
		}
		f.After = next
	}
}
