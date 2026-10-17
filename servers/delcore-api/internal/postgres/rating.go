// SPDX-License-Identifier: Apache-2.0
package postgres

import (
	"context"
	"github.com/delinoio/oss/servers/delcore-api/internal/accounting"
	"github.com/jackc/pgx/v5"
	"time"
)

func RatePeriod(ctx context.Context, tx pgx.Tx, tenant, service, meter string, start time.Time) (accounting.Decimal, accounting.Decimal, error) {
	rows, e := tx.Query(ctx, `WITH RECURSIVE chain AS (
 SELECT id root,id,quantity,price_id,occurred_at FROM usage_events WHERE tenant_id=$1 AND service_id=$2 AND meter=$3 AND period_start=$4 AND correction_of IS NULL
 UNION ALL SELECT c.root,u.id,u.quantity,c.price_id,c.occurred_at FROM chain c JOIN usage_events u ON u.correction_of=c.id
 ), actions AS (
 SELECT c.root id,c.occurred_at at,false is_grant,c.quantity amount,p.unit_price rate FROM chain c JOIN prices p ON p.id=c.price_id WHERE NOT EXISTS(SELECT 1 FROM usage_events n WHERE n.correction_of=c.id)
 UNION ALL SELECT id,applied_at,true,amount,0::numeric FROM allowance_grants WHERE tenant_id=$1 AND service_id=$2 AND meter=$3 AND period_start=$4
 ) SELECT is_grant,amount::text,rate::text FROM actions ORDER BY at,is_grant DESC,id`, tenant, service, meter, start)
	if e != nil {
		return accounting.Zero(), accounting.Zero(), e
	}
	defer rows.Close()
	remaining, total := accounting.Zero(), accounting.Zero()
	for rows.Next() {
		var grant bool
		var amountText, rateText string
		if e = rows.Scan(&grant, &amountText, &rateText); e != nil {
			return total, remaining, e
		}
		amount, e := accounting.Parse(amountText)
		if e != nil {
			return total, remaining, e
		}
		if grant {
			remaining = remaining.Add(amount)
			continue
		}
		included := amount
		if included.Cmp(remaining) > 0 {
			included = remaining
		}
		remaining = remaining.Sub(included)
		rate, e := accounting.Parse(rateText)
		if e != nil {
			return total, remaining, e
		}
		total = total.Add(amount.Sub(included).Mul(rate))
	}
	return total, remaining, rows.Err()
}

// HasAllowance matches the service-level paid authorization rule: any current
// meter with remaining included units permits use of that service.
func HasAllowance(ctx context.Context, tx pgx.Tx, tenant, service string, start time.Time) (bool, error) {
	rows, e := tx.Query(ctx, "SELECT DISTINCT p.meter FROM entitlements en JOIN prices p ON p.id=en.price_id WHERE en.tenant_id=$1 AND p.service_id=$2 AND p.kind=1 AND en.ends_at IS NULL", tenant, service)
	if e != nil {
		return false, e
	}
	var meters []string
	for rows.Next() {
		var meter string
		if e = rows.Scan(&meter); e != nil {
			rows.Close()
			return false, e
		}
		meters = append(meters, meter)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return false, e
	}
	for _, meter := range meters {
		_, remaining, e := RatePeriod(ctx, tx, tenant, service, meter, start)
		if e != nil {
			return false, e
		}
		if remaining.Cmp(accounting.Zero()) > 0 {
			return true, nil
		}
	}
	return false, nil
}
