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
 (NOT p.postpaid AND COALESCE((SELECT sum(amount) FROM ledger WHERE tenant_id=t.id AND kind IN ('credit','usage','refund','correction')),0)=0),t.period_start
 FROM tenants t JOIN plans p ON p.id=t.plan_id WHERE t.deleted_at IS NULL`, now)
	if e != nil {
		return e
	}
	type state struct {
		tenant                                                 string
		operator, renewal, expired, dispute, balance, depleted bool
		start                                                  time.Time
	}
	var states []state
	for rows.Next() {
		var v state
		if e = rows.Scan(&v.tenant, &v.operator, &v.renewal, &v.expired, &v.dispute, &v.balance, &v.depleted, &v.start); e != nil {
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

		for _, r := range []struct {
			name, text string
			active     bool
		}{{"operator", "An operator has blocked new paid use.", v.operator}, {"renewal", "A renewal payment failed. Complete payment recovery before starting new paid use.", v.renewal}, {"period_expired", "Your paid period ended and no renewal has been confirmed yet. New paid use resumes after the renewal payment is confirmed.", v.expired}, {"dispute", "A payment dispute is active. New paid use is blocked.", v.dispute}, {"balance", "Your credit balance is insufficient. Add funds to recover new paid use.", v.balance}} {
			if !r.active {
				if _, e = tx.Exec(ctx, "UPDATE billing_notices SET active=false WHERE tenant_id=$1 AND reason=$2", v.tenant, r.name); e != nil {
					return e
				}
				continue
			}
			var generation int
			e = tx.QueryRow(ctx, `INSERT INTO billing_notices VALUES($1,$2,true,1) ON CONFLICT(tenant_id,reason) DO UPDATE SET active=true,generation=billing_notices.generation+1 WHERE NOT billing_notices.active RETURNING generation`, v.tenant, r.name).Scan(&generation)
			if errors.Is(e, pgx.ErrNoRows) {
				continue
			}
			if e != nil {
				return e
			}
			if e = notify(ctx, tx, v.tenant, "notice:"+v.tenant+":"+r.name+":"+strconv.Itoa(generation), "Paid use blocked", r.text, now); e != nil {
				return e
			}
		}
	}
	return nil
}
