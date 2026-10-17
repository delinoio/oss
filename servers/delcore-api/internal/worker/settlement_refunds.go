// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/delinoio/oss/servers/delcore-api/internal/accounting"
	"github.com/delinoio/oss/servers/delcore-api/internal/provider"
	"github.com/jackc/pgx/v5"
)

func recordSettlementLine(ctx context.Context, tx pgx.Tx, transaction, item, job, amount, minor string) error {
	_, e := tx.Exec(ctx, `INSERT INTO settlement_payment_lines VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, transaction, item, job, amount, minor)
	return e
}

// Only a refund of a recorded usage invoice line changes usage receivables.
// Fixed subscription refunds remain payment history and cannot erase usage debt.
func (w *Worker) applySettlementRefund(ctx context.Context, tx pgx.Tx, event provider.Event, tenant, adjustment, transaction, status string) error {
	var payload struct {
		Items []struct {
			ItemID string `json:"item_id"`
			Totals struct {
				Subtotal string `json:"subtotal"`
			} `json:"totals"`
		} `json:"items"`
	}
	if e := json.Unmarshal(event.Data, &payload); e != nil {
		return e
	}
	amounts := map[string]string{}
	for _, item := range payload.Items {
		if _, exists := amounts[item.ItemID]; exists {
			return errors.New("duplicate adjustment line")
		}
		amounts[item.ItemID] = item.Totals.Subtotal
	}
	rows, e := tx.Query(ctx, `SELECT p.item_id,p.job_id::text,p.amount::text,p.billed_minor::text,COALESCE(r.billed_minor,0)::text,COALESCE((SELECT sum(o.billed_minor) FROM settlement_refund_effects o WHERE o.transaction_id=p.transaction_id AND o.item_id=p.item_id AND o.adjustment_id<>$2),0)::text FROM settlement_payment_lines p LEFT JOIN settlement_refund_effects r ON r.transaction_id=p.transaction_id AND r.item_id=p.item_id AND r.adjustment_id=$2 WHERE p.transaction_id=$1`, transaction, adjustment)
	if e != nil {
		return e
	}
	type line struct{ item, job, principal, paid, prior, other string }
	var lines []line
	for rows.Next() {
		var l line
		if e = rows.Scan(&l.item, &l.job, &l.principal, &l.paid, &l.prior, &l.other); e != nil {
			rows.Close()
			return e
		}
		lines = append(lines, l)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	if len(lines) == 0 {
		var unmapped bool
		if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM jobs WHERE external_id=$1 AND kind='settlement' AND state='done')", transaction).Scan(&unmapped); e != nil {
			return e
		}
		if unmapped {
			return errors.New("usage refund payment attribution missing")
		}
	}
	if status == "approved" && len(lines) > 0 && len(payload.Items) == 0 {
		return errors.New("usage refund line attribution missing")
	}
	for _, l := range lines {
		current := "0"
		if status == "approved" {
			if value, ok := amounts[l.item]; ok {
				current = value
			}
		}
		amount, err := accounting.Parse(current)
		if err != nil || amount.Cmp(accounting.Zero()) < 0 || !amount.IsMinorExact(0) {
			return errors.New("invalid usage refund subtotal")
		}
		var valid bool
		if e = tx.QueryRow(ctx, "SELECT $1::numeric+$2::numeric<=$3::numeric", current, l.other, l.paid).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return errors.New("usage refund exceeds paid line")
		}
		if current == l.prior {
			continue
		}
		var delta, minor string
		// The final refund includes the original ledger's sub-minor residual. Partial
		// refunds use exact currency units; no proportional division is introduced.
		if e = tx.QueryRow(ctx, `SELECT ((CASE WHEN $1::numeric+$3::numeric=$4::numeric THEN $5::numeric ELSE ($1::numeric+$3::numeric)/power(10::numeric,$6) END)-(CASE WHEN $2::numeric+$3::numeric=$4::numeric THEN $5::numeric ELSE ($2::numeric+$3::numeric)/power(10::numeric,$6) END))::text,($1::numeric-$2::numeric)::text`, l.prior, current, l.other, l.paid, l.principal, w.Config.MinorUnits).Scan(&delta, &minor); e != nil {
			return e
		}
		source := "usage-refund:" + adjustment + ":" + l.item + ":" + event.OccurredAt.Format(time.RFC3339Nano)
		if _, e = tx.Exec(ctx, "INSERT INTO ledger(id,tenant_id,kind,amount,source_key,created_at) VALUES($1,$2,'settlement',$3,$4,$5)", id(), tenant, delta, source, event.OccurredAt); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO settlements(id,tenant_id,period_start,period_end,service_id,meter,amount,source_key,job_id,created_at,billed_minor) VALUES($1,$2,$3,$3,NULL,'',$4,$5,$6,$3,$7)`, id(), tenant, event.OccurredAt, delta, source, l.job, minor); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO settlement_refund_effects VALUES($1,$2,$3,$4) ON CONFLICT(adjustment_id,item_id) DO UPDATE SET billed_minor=EXCLUDED.billed_minor`, adjustment, transaction, l.item, current); e != nil {
			return e
		}
	}
	return nil
}
