// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/servers/delcore-api/internal/accounting"
	"github.com/delinoio/oss/servers/delcore-api/internal/postgres"
	"github.com/jackc/pgx/v5"
)

func (w *Worker) Maintain(ctx context.Context, now time.Time) error {
	return w.DB.Transaction(ctx, func(tx pgx.Tx) error {
		if e := w.freePlanChanges(ctx, tx, now); e != nil {
			return e
		}
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		// Free plans use calendar months even when their tenant was created mid-month.
		if _, e := tx.Exec(ctx, "UPDATE tenants t SET period_start=$1,period_end=$2 FROM plans p WHERE t.plan_id=p.id AND p.free AND t.external_subscription_id='' AND t.period_end<=$3", start, start.AddDate(0, 1, 0), now); e != nil {
			return e
		}
		freeRows, e := tx.Query(ctx, "SELECT t.id::text FROM tenants t JOIN plans p ON p.id=t.plan_id WHERE p.free AND t.external_subscription_id='' AND t.deleted_at IS NULL")
		if e != nil {
			return e
		}
		var freeTenants []string
		for freeRows.Next() {
			var tenant string
			if e = freeRows.Scan(&tenant); e != nil {
				freeRows.Close()
				return e
			}
			freeTenants = append(freeTenants, tenant)
		}
		freeRows.Close()
		if e = freeRows.Err(); e != nil {
			return e
		}
		for _, tenant := range freeTenants {
			if e = postgres.GrantAllowances(ctx, tx, tenant, start, start.AddDate(0, 1, 0), start, "free:"+tenant+":"+start.Format("2006-01"), nil); e != nil {
				return e
			}
		}
		rows, e := tx.Query(ctx, `SELECT t.id::text,t.auto_amount::text FROM tenants t WHERE t.auto_amount IS NOT NULL AND t.external_subscription_id<>'' AND t.period_end>now() AND NOT t.auto_suspended AND t.deleting_at IS NULL AND t.deleted_at IS NULL AND NOT t.operator_block AND NOT t.renewal_failed
   AND NOT EXISTS(SELECT 1 FROM disputes WHERE tenant_id=t.id AND active)
   AND COALESCE((SELECT sum(amount) FROM ledger WHERE tenant_id=t.id AND kind IN ('credit','usage','refund','correction')),0)<t.auto_threshold
   AND NOT EXISTS(SELECT 1 FROM jobs WHERE tenant_id=t.id AND kind='auto_topup' AND state IN ('pending','running','reconcile'))
   AND COALESCE((SELECT sum((payload->>'amount')::numeric) FROM jobs WHERE tenant_id=t.id AND kind='auto_topup' AND created_at >= $1 AND (state<>'failed' OR error_category NOT IN ('provider_rejected','payment_failed','payment_method_required'))),0)+t.auto_amount<=t.auto_cap`, start)
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
			if _, e = tx.Exec(ctx, `INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,available_at,created_at,updated_at) VALUES($1,$2,'auto_topup',$1::uuid::text,$3,$4,$4,$4)`, jobID, a.tenant, payload, now); e != nil {
				return e
			}
		}
		if e = w.roundUsage(ctx, tx, now); e != nil {
			return e
		}
		if e = w.settle(ctx, tx, now); e != nil {
			return e
		}
		// Final deletion is held by every independent debt, dispute and refund gate.
		rows, e = tx.Query(ctx, `SELECT t.id::text,t.owner_id::text,t.personal FROM tenants t WHERE t.deleting_at<$1 AND t.deleted_at IS NULL
   AND NOT EXISTS(SELECT 1 FROM disputes WHERE tenant_id=t.id AND active)
   AND COALESCE((SELECT sum(amount) FROM ledger WHERE tenant_id=t.id AND kind IN ('credit','usage','refund','correction')),0)=0
   AND COALESCE((SELECT sum(amount) FROM ledger WHERE tenant_id=t.id AND kind IN ('postpaid','settlement')),0)=0
   AND NOT EXISTS(SELECT 1 FROM jobs WHERE tenant_id=t.id AND kind<>'email' AND state<>'done' AND NOT (kind IN ('topup','auto_topup') AND state='failed' AND error_category IN ('provider_rejected','payment_failed','payment_method_required')))
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
			if _, e = tx.Exec(ctx, "DELETE FROM ownership_transfers WHERE tenant_id=$1", v.tenant); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "UPDATE jobs SET payload='{}'::jsonb,state='done',error_category='recipient_deleted' WHERE tenant_id=$1 AND kind='email'", v.tenant); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "UPDATE groups SET name=id::text WHERE tenant_id=$1", v.tenant); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "UPDATE audit SET actor_id='deleted',reason='' WHERE tenant_id=$1", v.tenant); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "UPDATE requests SET response=''::bytea WHERE tenant_id=$1", v.tenant); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "UPDATE jobs SET payload='{}'::jsonb,checkout_url='' WHERE tenant_id=$1 AND state='done'", v.tenant); e != nil {
				return e
			}

			if _, e = tx.Exec(ctx, "DELETE FROM group_members WHERE tenant_id=$1", v.tenant); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "DELETE FROM members WHERE tenant_id=$1", v.tenant); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "UPDATE invitations SET email='',token_hash=id::text,revoked_at=COALESCE(revoked_at,$2) WHERE tenant_id=$1", v.tenant, now); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "UPDATE tenants SET name='Deleted tenant',deleted_at=$2,external_customer_id='',owner_id=NULL WHERE id=$1", v.tenant, now); e != nil {
				return e
			}
			if v.personal {
				if _, e = tx.Exec(ctx, "UPDATE users SET email='',subject_hash='deleted:'||id::text WHERE id=$1 AND withdrawing_at IS NOT NULL", v.user); e != nil {
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
		if e = w.notices(ctx, tx, now); e != nil {
			return e
		}
		return w.retain(ctx, tx, now)
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

func (w *Worker) freePlanChanges(ctx context.Context, tx pgx.Tx, now time.Time) error {
	rows, e := tx.Query(ctx, `SELECT j.id::text,j.tenant_id::text,j.payload,j.available_at FROM jobs j JOIN tenants t ON t.id=j.tenant_id WHERE j.kind='subscription' AND j.payload->>'timing'='free_next_period' AND j.state IN ('pending','reconcile') AND j.available_at<=$1 AND t.external_subscription_id='' AND t.deleted_at IS NULL`, now)
	if e != nil {
		return e
	}
	type change struct {
		id, tenant string
		payload    []byte
		at         time.Time
	}
	var changes []change
	for rows.Next() {
		var c change
		if e = rows.Scan(&c.id, &c.tenant, &c.payload, &c.at); e != nil {
			rows.Close()
			return e
		}
		changes = append(changes, c)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, c := range changes {
		var p struct {
			Plan   string   `json:"plan_id"`
			Prices []string `json:"price_ids"`
		}
		if e = json.Unmarshal(c.payload, &p); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "UPDATE tenants SET plan_id=$2 WHERE id=$1", c.tenant, p.Plan); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "UPDATE entitlements SET ends_at=$2 WHERE tenant_id=$1 AND ends_at IS NULL AND starts_at<$2", c.tenant, c.at); e != nil {
			return e
		}
		for _, price := range p.Prices {
			if _, e = tx.Exec(ctx, "INSERT INTO entitlements(id,tenant_id,price_id,starts_at) VALUES($1,$2,$3,$4)", id(), c.tenant, price, c.at); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(ctx, "UPDATE jobs SET state='done',updated_at=$2 WHERE id=$1", c.id, now); e != nil {
			return e
		}
	}
	return nil
}
