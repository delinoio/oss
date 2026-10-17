// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/servers/delcore-api/internal/postgres"
	"github.com/jackc/pgx/v5"
	"time"
)

func (w *Worker) activateFree(ctx context.Context, tx pgx.Tx, tenant string, at time.Time) error {
	start := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	_, e := tx.Exec(ctx, "UPDATE tenants SET plan_id=(SELECT id FROM plans WHERE is_default AND free),external_subscription_id='',period_start=$2,period_end=$3 WHERE id=$1", tenant, start, end)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE entitlements SET ends_at=$2 WHERE tenant_id=$1 AND ends_at IS NULL AND starts_at<$2", tenant, at); e != nil {
		return e
	}
	rows, e := tx.Query(ctx, "SELECT DISTINCT ON(p.service_id,p.meter,p.kind) p.id::text FROM prices p JOIN tenants t ON t.plan_id=p.plan_id WHERE t.id=$1 ORDER BY p.service_id,p.meter,p.kind,p.created_at DESC,p.id DESC", tenant)
	if e != nil {
		return e
	}
	var prices []string
	for rows.Next() {
		var price string
		if e = rows.Scan(&price); e != nil {
			rows.Close()
			return e
		}
		prices = append(prices, price)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, price := range prices {
		if _, e = tx.Exec(ctx, "INSERT INTO entitlements(id,tenant_id,price_id,starts_at) VALUES($1,$2,$3,$4)", id(), tenant, price, at); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, "UPDATE jobs SET state='done',error_category='',updated_at=now() WHERE tenant_id=$1 AND kind='cancel' AND state IN ('pending','running','reconcile')", tenant); e != nil {
		return e
	}
	return postgres.GrantAllowances(ctx, tx, tenant, start, end, at, "free:"+tenant+":"+start.Format("2006-01"), nil)
}
