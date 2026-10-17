// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"github.com/delinoio/oss/servers/delcore-api/internal/accounting"
	"time"
)

func (c *call) ratePeriod(tenant, service, meter string, start time.Time) (accounting.Decimal, accounting.Decimal, error) {
	rows, e := c.tx.Query(c.ctx, `WITH RECURSIVE chain AS (
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
