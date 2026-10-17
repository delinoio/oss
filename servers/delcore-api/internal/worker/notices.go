// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"strconv"
	"time"
)

func (w *Worker) notices(ctx context.Context, tx pgx.Tx, now time.Time) error {
	rows, e := tx.Query(ctx, `SELECT t.id::text,t.operator_block,t.renewal_failed,
 EXISTS(SELECT 1 FROM disputes d WHERE d.tenant_id=t.id AND active),
 COALESCE((SELECT sum(amount) FROM ledger WHERE tenant_id=t.id AND kind IN ('credit','usage','refund','correction')),0)<0
 FROM tenants t WHERE deleted_at IS NULL`)
	if e != nil {
		return e
	}
	type state struct {
		tenant                              string
		operator, renewal, dispute, balance bool
	}
	var states []state
	for rows.Next() {
		var v state
		if e = rows.Scan(&v.tenant, &v.operator, &v.renewal, &v.dispute, &v.balance); e != nil {
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
		for _, r := range []struct {
			name, text string
			active     bool
		}{{"operator", "An operator has blocked new paid use.", v.operator}, {"renewal", "A renewal payment failed. Complete payment recovery before starting new paid use.", v.renewal}, {"dispute", "A payment dispute is active. New paid use is blocked.", v.dispute}, {"balance", "Your credit balance is insufficient. Add funds to recover new paid use.", v.balance}} {
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
