// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/servers/delcore-api/internal/accounting"
	"github.com/jackc/pgx/v5"
	"math/big"
	"time"
)

func (w *Worker) settle(ctx context.Context, tx pgx.Tx, now time.Time) error {
	rows, e := tx.Query(ctx, `SELECT l.tenant_id::text,l.service_id::text,l.meter,l.period_start,l.period_end,
 (-sum(l.amount))::text,
 COALESCE((SELECT sum(s.amount) FROM settlements s WHERE s.tenant_id=l.tenant_id AND s.service_id=l.service_id AND s.meter=l.meter AND s.period_start=l.period_start),0)::text,
 COALESCE((SELECT sum(s.billed_minor) FROM settlements s WHERE s.tenant_id=l.tenant_id AND s.service_id=l.service_id AND s.meter=l.meter AND s.period_start=l.period_start),0)::text
 FROM ledger l JOIN tenants t ON t.id=l.tenant_id WHERE l.kind='postpaid'
 AND (l.period_end<=$1 OR t.deleting_at < $2)
 GROUP BY l.tenant_id,l.service_id,l.meter,l.period_start,l.period_end`, now, now.Add(-time.Duration(w.Config.ReportingGraceSeconds)*time.Second))
	if e != nil {
		return e
	}
	type line struct {
		service, meter string
		start, end     time.Time
		amount         accounting.Decimal
		units          *big.Int
	}
	type batch struct {
		amount accounting.Decimal
		units  *big.Int
		lines  []line
	}
	batches := map[string]*batch{}
	for rows.Next() {
		var tenant, service, meter, totalText, claimedText, billedText string
		var start, end time.Time
		if e = rows.Scan(&tenant, &service, &meter, &start, &end, &totalText, &claimedText, &billedText); e != nil {
			rows.Close()
			return e
		}
		total, e := accounting.Parse(totalText)
		if e != nil {
			rows.Close()
			return e
		}
		claimed, e := accounting.Parse(claimedText)
		if e != nil {
			rows.Close()
			return e
		}
		amount := total.Sub(claimed)
		if amount.Cmp(accounting.Zero()) == 0 {
			continue
		}
		cumulative, e := total.MinorUnits(w.Config.MinorUnits)
		if e != nil {
			rows.Close()
			return e
		}
		billed, ok := new(big.Int).SetString(billedText, 10)
		if !ok {
			rows.Close()
			return accounting.ErrDecimal
		}
		units := new(big.Int).Sub(cumulative, billed)
		b := batches[tenant]
		if b == nil {
			b = &batch{amount: accounting.Zero(), units: new(big.Int)}
			batches[tenant] = b
		}
		b.amount = b.amount.Add(amount)
		b.units.Add(b.units, units)
		b.lines = append(b.lines, line{service, meter, start, end, amount, units})
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for tenant, b := range batches {
		// Meter totals have already been rounded in roundUsage. Purchased credit
		// allocations may be fractional while other periods remain open. Carry
		// that fraction across invoices by rounding cumulative tenant receivables.
		var cumulativeText, paidText string
		if e = tx.QueryRow(ctx, `SELECT (-COALESCE(sum(l.amount),0))::text,COALESCE((SELECT sum(billed_minor) FROM settlements WHERE tenant_id=$1),0)::text FROM ledger l JOIN tenants t ON t.id=l.tenant_id WHERE l.tenant_id=$1 AND l.kind='postpaid' AND (l.period_end<=$2 OR t.deleting_at<$3)`, tenant, now, now.Add(-time.Duration(w.Config.ReportingGraceSeconds)*time.Second)).Scan(&cumulativeText, &paidText); e != nil {
			return e
		}
		cumulative, e := accounting.Parse(cumulativeText)
		if e != nil {
			return e
		}
		units, e := cumulative.MinorUnits(w.Config.MinorUnits)
		if e != nil {
			return e
		}
		paid, ok := new(big.Int).SetString(paidText, 10)
		if !ok {
			return accounting.ErrDecimal
		}
		units.Sub(units, paid)
		if len(b.lines) > 0 {
			b.lines[0].units.Add(b.lines[0].units, new(big.Int).Sub(units, b.units))
		}
		b.units = units
		// A net credit remains an adjustment for the next bill. Do not request a
		// provider refund or discard its original service and period attribution.
		if b.units.Sign() < 0 {
			continue
		}
		jobID := id()
		state := "pending"
		if b.units.Sign() == 0 {
			state = "done"
		}
		payload := map[string]string{"amount": b.amount.String(), "minor_units": b.units.String(), "currency": w.Config.Currency}
		if _, e = tx.Exec(ctx, "INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,state,available_at,created_at,updated_at) VALUES($1,$2,'settlement',$1::uuid::text,$3,$4,$5,$5,$5)", jobID, tenant, payload, state, now); e != nil {
			return e
		}
		for _, l := range b.lines {
			if _, e = tx.Exec(ctx, "INSERT INTO settlements(id,tenant_id,period_start,period_end,service_id,meter,amount,source_key,job_id,created_at,billed_minor) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)", id(), tenant, l.start, l.end, l.service, l.meter, l.amount.String(), jobID+":"+l.service+":"+l.meter+":"+l.start.Format(time.RFC3339Nano), jobID, now, l.units.String()); e != nil {
				return e
			}
		}
		if state == "done" {
			if _, e = tx.Exec(ctx, "INSERT INTO ledger(id,tenant_id,kind,amount,source_key,created_at) VALUES($1,$2,'settlement',$3,$4,$5)", id(), tenant, b.amount.String(), "rounding:"+jobID, now); e != nil {
				return e
			}
		}
	}
	return nil
}
