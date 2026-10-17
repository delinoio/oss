// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/servers/delcore-api/internal/accounting"
	"github.com/jackc/pgx/v5"
)

func (w *Worker) Maintain(ctx context.Context, now time.Time) error {
	return w.DB.Transaction(ctx, func(tx pgx.Tx) error {
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		// Free plans use calendar months even when their tenant was created mid-month.
		if _, e := tx.Exec(ctx, "UPDATE tenants t SET period_start=$1,period_end=$2 FROM plans p WHERE t.plan_id=p.id AND p.free AND t.period_end<=$3", start, start.AddDate(0, 1, 0), now); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `SELECT t.id::text,t.auto_amount::text FROM tenants t WHERE t.auto_amount IS NOT NULL AND NOT t.auto_suspended AND t.deleting_at IS NULL AND t.deleted_at IS NULL AND NOT t.operator_block AND NOT t.renewal_failed
   AND NOT EXISTS(SELECT 1 FROM disputes WHERE tenant_id=t.id AND active)
   AND COALESCE((SELECT sum(amount) FROM ledger WHERE tenant_id=t.id AND kind IN ('credit','usage','refund','correction')),0)<t.auto_threshold
   AND NOT EXISTS(SELECT 1 FROM jobs WHERE tenant_id=t.id AND kind='auto_topup' AND state IN ('pending','running','reconcile'))
   AND COALESCE((SELECT sum((payload->>'amount')::numeric) FROM jobs WHERE tenant_id=t.id AND kind='auto_topup' AND created_at >= $1 AND state<>'failed'),0)+t.auto_amount<=t.auto_cap`, start)
		if e != nil {
			return e
		}
		type auto struct{ tenant, amount string }
		var autos []auto
		for rows.Next() {
			var v auto
			if e = rows.Scan(&v.tenant, &v.amount); e != nil {
				rows.Close()
				return e
			}
			autos = append(autos, v)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		for _, a := range autos {
			amount, e := accounting.Parse(a.amount)
			if e != nil {
				return e
			}
			units, e := amount.MinorUnits(w.Config.MinorUnits)
			if e != nil {
				return e
			}
			jobID := id()
			payload := map[string]string{"amount": amount.String(), "minor_units": units.String(), "currency": w.Config.Currency}
			if _, e = tx.Exec(ctx, `INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,available_at,created_at,updated_at) VALUES($1,$2,'auto_topup',$1,$3,$4,$4,$4)`, jobID, a.tenant, payload, now); e != nil {
				return e
			}
		}
		// Settlement records claim an exact source total transactionally. Late reports
		// produce a later delta rather than reopening an already submitted charge.
		rows, e = tx.Query(ctx, `SELECT l.tenant_id::text,l.service_id::text,l.meter,l.period_start,l.period_end,
   (-sum(l.amount)-COALESCE((SELECT sum(s.amount) FROM settlements s WHERE s.tenant_id=l.tenant_id AND s.service_id=l.service_id AND s.meter=l.meter AND s.period_start=l.period_start),0))::text
   FROM ledger l WHERE l.kind='postpaid' AND l.period_end<=$1 GROUP BY l.tenant_id,l.service_id,l.meter,l.period_start,l.period_end`, now)
		if e != nil {
			return e
		}
		type settlement struct {
			tenant, service, meter, amount string
			start, end                     time.Time
		}
		var pending []settlement
		for rows.Next() {
			var v settlement
			if e = rows.Scan(&v.tenant, &v.service, &v.meter, &v.start, &v.end, &v.amount); e != nil {
				rows.Close()
				return e
			}
			pending = append(pending, v)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		for _, v := range pending {
			amount, e := accounting.Parse(v.amount)
			if e != nil {
				return e
			}
			units, e := amount.MinorUnits(w.Config.MinorUnits)
			if e != nil {
				return e
			}
			if units.Sign() <= 0 {
				continue
			}
			jobID := id()
			payload := map[string]string{"amount": amount.String(), "minor_units": units.String(), "currency": w.Config.Currency}
			if _, e = tx.Exec(ctx, `INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,available_at,created_at,updated_at) VALUES($1,$2,'settlement',$1,$3,$4,$4,$4)`, jobID, v.tenant, payload, now); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "INSERT INTO settlements VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)", id(), v.tenant, v.start, v.end, v.service, v.meter, amount.String(), jobID, jobID, now); e != nil {
				return e
			}
		}
		// Final deletion is held by every independent debt, dispute and refund gate.
		rows, e = tx.Query(ctx, `SELECT t.id::text,t.owner_id::text,t.personal FROM tenants t WHERE t.deleting_at<$1 AND t.deleted_at IS NULL
   AND NOT EXISTS(SELECT 1 FROM disputes WHERE tenant_id=t.id AND active)
   AND COALESCE((SELECT sum(amount) FROM ledger WHERE tenant_id=t.id AND kind IN ('credit','usage','refund','correction')),0)=0
   AND COALESCE((SELECT sum(amount) FROM ledger WHERE tenant_id=t.id AND kind IN ('postpaid','settlement')),0)=0
   AND NOT EXISTS(SELECT 1 FROM jobs WHERE tenant_id=t.id AND kind<>'email' AND state<>'done')
   AND t.external_subscription_id=''`, now.Add(-time.Duration(w.Config.ReportingGraceSeconds+w.Config.DeletionGraceSeconds)*time.Second))
		if e != nil {
			return e
		}
		type deletion struct {
			tenant, user string
			personal     bool
		}
		var deletes []deletion
		for rows.Next() {
			var v deletion
			if e = rows.Scan(&v.tenant, &v.user, &v.personal); e != nil {
				rows.Close()
				return e
			}
			deletes = append(deletes, v)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		for _, v := range deletes {
			if _, e = tx.Exec(ctx, "DELETE FROM group_members WHERE tenant_id=$1", v.tenant); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "DELETE FROM members WHERE tenant_id=$1", v.tenant); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "UPDATE invitations SET email='',token_hash=id::text,revoked_at=COALESCE(revoked_at,$2) WHERE tenant_id=$1", v.tenant, now); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "UPDATE tenants SET name='Deleted tenant',deleted_at=$2,external_customer_id='' WHERE id=$1", v.tenant, now); e != nil {
				return e
			}
			if v.personal {
				if _, e = tx.Exec(ctx, "UPDATE users SET email='' WHERE id=$1 AND withdrawing_at IS NOT NULL", v.user); e != nil {
					return e
				}
			}
		}
		// Strip provider payloads after their retention interval while retaining event
		// IDs as replay tombstones. Pending events and unsettled jobs retain evidence.
		if _, e = tx.Exec(ctx, "UPDATE webhooks SET payload='{}'::jsonb WHERE processed_at IS NOT NULL AND received_at<$1 AND payload<>'{}'::jsonb", now.Add(-time.Duration(w.Config.WebhookRetentionSeconds)*time.Second)); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "UPDATE jobs SET payload='{}'::jsonb WHERE kind='email' AND state='done' AND updated_at<$1", now.Add(-time.Duration(w.Config.InvitationSeconds)*time.Second)); e != nil {
			return e
		}
		return nil
	})
}

// Notify queues one message per current billing recipient. Caller-owned keys
// deduplicate a transition without placing recipient addresses in telemetry.
func notify(ctx context.Context, tx pgx.Tx, tenant, key, subject, text string, now time.Time) error {
	rows, e := tx.Query(ctx, "SELECT u.id::text,u.email FROM members m JOIN users u ON u.id=m.user_id WHERE tenant_id=$1 AND (1=ANY(roles) OR 3=ANY(roles)) AND u.email<>''", tenant)
	if e != nil {
		return e
	}
	type recipient struct{ id, email string }
	var recipients []recipient
	for rows.Next() {
		var r recipient
		if e = rows.Scan(&r.id, &r.email); e != nil {
			rows.Close()
			return e
		}
		recipients = append(recipients, r)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, r := range recipients {
		payload, _ := json.Marshal(map[string]string{"to": r.email, "subject": subject, "text": text})
		if _, e = tx.Exec(ctx, "INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,available_at,created_at,updated_at) VALUES($1,$2,'email',$3,$4,$5,$5,$5) ON CONFLICT(dedup_key) DO NOTHING", id(), tenant, key+":"+r.id, payload, now); e != nil {
			return e
		}
	}
	return nil
}
