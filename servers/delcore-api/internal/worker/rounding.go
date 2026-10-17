// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/servers/delcore-api/internal/accounting"
	"github.com/delinoio/oss/servers/delcore-api/internal/postgres"
	"github.com/jackc/pgx/v5"
	"time"
)

// Round the full meter charge before final payment allocation, including amounts
// already consumed from purchased credits. Never round individual usage events.
func (w *Worker) roundUsage(ctx context.Context, tx pgx.Tx, now time.Time) error {
	rows, e := tx.Query(ctx, `SELECT DISTINCT u.tenant_id::text,u.service_id::text,u.meter,u.period_start,u.period_end,
 COALESCE((SELECT b.postpaid FROM billing_periods b WHERE b.tenant_id=u.tenant_id AND b.starts_at=u.period_start),p.postpaid)
 FROM usage_events u JOIN tenants t ON t.id=u.tenant_id JOIN prices pr ON pr.id=u.price_id LEFT JOIN plans p ON p.id=pr.plan_id
 WHERE u.period_end<=$1 OR t.deleting_at<$2`, now, now.Add(-time.Duration(w.Config.ReportingGraceSeconds)*time.Second))
	if e != nil {
		return e
	}
	type period struct {
		tenant, service, meter string
		start, end             time.Time
		postpaid               bool
	}
	var periods []period
	for rows.Next() {
		var p period
		if e = rows.Scan(&p.tenant, &p.service, &p.meter, &p.start, &p.end, &p.postpaid); e != nil {
			rows.Close()
			return e
		}
		periods = append(periods, p)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, p := range periods {
		raw, _, e := postgres.RatePeriod(ctx, tx, p.tenant, p.service, p.meter, p.start)
		if e != nil {
			return e
		}
		units, e := raw.MinorUnits(w.Config.MinorUnits)
		if e != nil {
			return e
		}
		total := accounting.FromMinor(units, w.Config.MinorUnits)
		var chargedText, unpaidText, balanceText string
		e = tx.QueryRow(ctx, `SELECT (-COALESCE(sum(amount),0))::text,greatest(-COALESCE(sum(amount) FILTER(WHERE kind='postpaid'),0),0)::text FROM ledger WHERE tenant_id=$1 AND service_id=$2 AND meter=$3 AND period_start=$4 AND kind IN ('usage','postpaid','correction')`, p.tenant, p.service, p.meter, p.start).Scan(&chargedText, &unpaidText)
		if e != nil {
			return e
		}
		charged, _ := accounting.Parse(chargedText)
		delta := total.Sub(charged)
		if delta.Cmp(accounting.Zero()) == 0 {
			continue
		}
		appendEntry := func(kind string, amount accounting.Decimal) error {
			entry := id()
			_, e := tx.Exec(ctx, `INSERT INTO ledger(id,tenant_id,service_id,meter,kind,amount,period_start,period_end,source_key,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, entry, p.tenant, p.service, p.meter, kind, amount.String(), p.start, p.end, "currency_rounding:"+entry, now)
			return e
		}
		if delta.Cmp(accounting.Zero()) < 0 {
			unpaid, _ := accounting.Parse(unpaidText)
			refund := delta.Neg()
			if refund.Cmp(unpaid) > 0 {
				refund = unpaid
			}
			if refund.Cmp(accounting.Zero()) > 0 {
				if e = appendEntry("postpaid", refund); e != nil {
					return e
				}
				delta = delta.Add(refund)
			}
		} else if p.postpaid {
			if e = tx.QueryRow(ctx, `SELECT greatest(COALESCE(sum(amount),0),0)::text FROM ledger WHERE tenant_id=$1 AND kind IN ('credit','usage','refund','correction')`, p.tenant).Scan(&balanceText); e != nil {
				return e
			}
			balance, _ := accounting.Parse(balanceText)
			if delta.Cmp(balance) > 0 {
				unpaid := delta.Sub(balance)
				if e = appendEntry("postpaid", unpaid.Neg()); e != nil {
					return e
				}
				delta = balance
			}
		}
		if delta.Cmp(accounting.Zero()) != 0 {
			if e = appendEntry("correction", delta.Neg()); e != nil {
				return e
			}
		}
	}
	return nil
}
