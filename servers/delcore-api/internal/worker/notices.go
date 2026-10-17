// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"errors"
	"github.com/delinoio/oss/servers/delcore-api/internal/postgres"
	"github.com/jackc/pgx/v5"
	"strconv"
	"time"
)

func (w *Worker) notices(ctx context.Context, tx pgx.Tx, now time.Time) error {
	// An expired confirmed period blocks paid use like Authorize does, but it is
	// not a confirmed payment failure, so it has its own notice and wording.
	rows, e := tx.Query(ctx, `SELECT t.id::text,t.operator_block,t.renewal_failed,
 NOT t.renewal_failed AND `+postgres.PeriodExpiredSQL("t", "p", "$1")+`,
 EXISTS(SELECT 1 FROM disputes d WHERE d.tenant_id=t.id AND active),
 COALESCE((SELECT sum(amount) FROM ledger WHERE tenant_id=t.id AND kind IN ('credit','usage','refund','correction')),0)<0,
 (NOT p.postpaid AND COALESCE((SELECT sum(amount) FROM ledger WHERE tenant_id=t.id AND kind IN ('credit','usage','refund','correction')),0)=0),t.period_start,t.period_end
 FROM tenants t JOIN plans p ON p.id=t.plan_id WHERE t.deleted_at IS NULL`, now)
	if e != nil {
		return e
	}
	type state struct {
		tenant                                                 string
		operator, renewal, expired, dispute, balance, depleted bool
		start, end                                             time.Time
	}
	var states []state
	for rows.Next() {
		var v state
		if e = rows.Scan(&v.tenant, &v.operator, &v.renewal, &v.expired, &v.dispute, &v.balance, &v.depleted, &v.start, &v.end); e != nil {
			rows.Close()
			return e
		}
		states = append(states, v)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, v := range states {
		if v.depleted {
			rows, e := tx.Query(ctx, "SELECT DISTINCT p.service_id::text FROM entitlements en JOIN prices p ON p.id=en.price_id WHERE en.tenant_id=$1 AND en.starts_at<=$2 AND (en.ends_at IS NULL OR en.ends_at>$2)", v.tenant, now)
			if e != nil {
				return e
			}
			var services []string
			for rows.Next() {
				var service string
				if e = rows.Scan(&service); e != nil {
					rows.Close()
					return e
				}
				services = append(services, service)
			}
			rows.Close()
			if e = rows.Err(); e != nil {
				return e
			}
			for _, service := range services {
				remaining, e := postgres.HasAllowance(ctx, tx, v.tenant, service, v.start)
				if e != nil {
					return e
				}
				if !remaining {
					v.balance = true
					break
				}
			}
		}

		// An expiry notice is keyed by the paid period it reports, so a renewed period
		// that expires again before maintenance observed the renewal still notifies.
		expiredReason := "period_expired:" + v.end.UTC().Format("20060102T150405.000000Z")
		for _, r := range []struct {
			family, reason, text string
			active               bool
		}{
			{"operator", "operator", "An operator has blocked new paid use.", v.operator},
			{"renewal", "renewal", "A renewal payment failed. Complete payment recovery before starting new paid use.", v.renewal},
			{"period_expired", expiredReason, "Your paid period ended and no renewal has been confirmed yet. New paid use resumes after the renewal payment is confirmed.", v.expired},
			{"dispute", "dispute", "A payment dispute is active. New paid use is blocked.", v.dispute},
			{"balance", "balance", "Your credit balance is insufficient. Add funds to recover new paid use.", v.balance},
		} {
			keep := ""
			if r.active {
				keep = r.reason
			}
			if e = w.supersedeNotices(ctx, tx, v.tenant, r.family, keep, now); e != nil {
				return e
			}
			if !r.active {
				continue
			}
			var generation int
			e = tx.QueryRow(ctx, `INSERT INTO billing_notices VALUES($1,$2,true,1) ON CONFLICT(tenant_id,reason) DO UPDATE SET active=true,generation=billing_notices.generation+1 WHERE NOT billing_notices.active RETURNING generation`, v.tenant, r.reason).Scan(&generation)
			if errors.Is(e, pgx.ErrNoRows) {
				continue
			}
			if e != nil {
				return e
			}
			if e = notify(ctx, tx, v.tenant, "notice:"+v.tenant+":"+r.reason+":"+strconv.Itoa(generation), "Paid use blocked", r.text, now); e != nil {
				return e
			}
		}
	}
	return nil
}

// supersedeNotices deactivates the active notices of one family except keep, and
// ends their undelivered emails. A pending or retrying email still describes a
// condition that no longer holds, so it must not reach the tenant. Running and sent
// emails are left alone.
func (w *Worker) supersedeNotices(ctx context.Context, tx pgx.Tx, tenant, family, keep string, now time.Time) error {
	rows, e := tx.Query(ctx, "UPDATE billing_notices SET active=false WHERE tenant_id=$1 AND active AND (reason=$2 OR starts_with(reason,$3)) AND reason<>$4 RETURNING reason", tenant, family, family+":", keep)
	if e != nil {
		return e
	}
	var reasons []string
	for rows.Next() {
		var reason string
		if e = rows.Scan(&reason); e != nil {
			rows.Close()
			return e
		}
		reasons = append(reasons, reason)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, reason := range reasons {
		tag, e := tx.Exec(ctx, "UPDATE jobs SET state='done',error_category='notice_superseded',updated_at=$3 WHERE tenant_id=$1 AND kind='email' AND state='pending' AND starts_with(dedup_key,$2)", tenant, "notice:"+tenant+":"+reason+":", now)
		if e != nil {
			return e
		}
		if w.Log != nil {
			w.Log.InfoContext(ctx, "billing notice superseded", "tenant_id", tenant, "family", family, "canceled_emails", tag.RowsAffected())
		}
	}
	return nil
}
