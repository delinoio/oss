// SPDX-License-Identifier: Apache-2.0
package postgres

import (
	"context"
	"github.com/delinoio/oss/servers/delcore-api/internal/accounting"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

type Allowances map[string]accounting.Decimal

func CurrentAllowances(ctx context.Context, tx pgx.Tx, tenant string) (Allowances, error) {
	rows, e := tx.Query(ctx, "SELECT p.service_id::text,p.meter,sum(p.included)::text FROM entitlements en JOIN prices p ON p.id=en.price_id WHERE en.tenant_id=$1 AND en.ends_at IS NULL AND p.kind=1 GROUP BY p.service_id,p.meter", tenant)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := Allowances{}
	for rows.Next() {
		var service, meter, amount string
		if e = rows.Scan(&service, &meter, &amount); e != nil {
			return nil, e
		}
		d, e := accounting.Parse(amount)
		if e != nil {
			return nil, e
		}
		out[service+"\x00"+meter] = d
	}
	return out, rows.Err()
}
func GrantAllowances(ctx context.Context, tx pgx.Tx, tenant string, start, end, at time.Time, source string, previous Allowances) error {
	current, e := CurrentAllowances(ctx, tx, tenant)
	if e != nil {
		return e
	}
	for key, amount := range current {
		if previous != nil {
			amount = amount.Sub(previous[key])
			if amount.Cmp(accounting.Zero()) <= 0 {
				continue
			}
			remaining := end.Sub(at).Microseconds()
			duration := end.Sub(start).Microseconds()
			if remaining < 0 {
				remaining = 0
			}
			if remaining > duration {
				remaining = duration
			}
			amount, e = accounting.ProratedAllowance(amount, remaining, duration)
			if e != nil {
				return e
			}
		}
		parts := strings.SplitN(key, "\x00", 2)
		_, e = tx.Exec(ctx, "INSERT INTO allowance_grants VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(source_key) DO NOTHING", uuid.Must(uuid.NewV7()).String(), tenant, parts[0], parts[1], amount.String(), start, end, at, source+":"+parts[0]+":"+parts[1])
		if e != nil {
			return e
		}
	}
	return nil
}
