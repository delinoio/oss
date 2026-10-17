// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/servers/delcore-api/internal/postgres"
	"github.com/delinoio/oss/servers/delcore-api/internal/provider"
	"github.com/jackc/pgx/v5"
	"time"
)

func matchesPrices(t provider.Transaction, expected []string) bool {
	if len(expected) == 0 || len(t.Items) != len(expected) {
		return false
	}
	counts := map[string]int{}
	for _, p := range expected {
		counts[p]++
	}
	for _, item := range t.Items {
		if item.Quantity != 1 || counts[item.Price.ID] == 0 {
			return false
		}
		counts[item.Price.ID]--
	}
	return true
}
func (w *Worker) renew(ctx context.Context, tx pgx.Tx, t provider.Transaction, at time.Time) error {
	if t.SubscriptionID == "" || t.BillingPeriod == nil || !t.BillingPeriod.EndsAt.After(t.BillingPeriod.StartsAt) || t.Currency != w.Config.Currency {
		return nil
	}
	var tenant, customer string
	var previousEnd time.Time
	e := tx.QueryRow(ctx, "SELECT id::text,external_customer_id,period_end FROM tenants WHERE external_subscription_id=$1 AND deleted_at IS NULL", t.SubscriptionID).Scan(&tenant, &customer, &previousEnd)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if customer != t.CustomerID || t.BillingPeriod.StartsAt.Before(previousEnd) {
		return nil
	}
	var changeID string
	var payload []byte
	e = tx.QueryRow(ctx, "SELECT id::text,payload FROM jobs WHERE tenant_id=$1 AND kind='subscription' AND state IN ('reconcile','running') AND payload->>'timing'='do_not_bill' ORDER BY created_at LIMIT 1", tenant).Scan(&changeID, &payload)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if changeID != "" {
		var p struct {
			PlanID         string   `json:"plan_id"`
			PriceIDs       []string `json:"price_ids"`
			ExternalPrices []string `json:"external_price_ids"`
		}
		if e = json.Unmarshal(payload, &p); e != nil {
			return e
		}
		if !matchesPrices(t, p.ExternalPrices) {
			_, e = tx.Exec(ctx, "UPDATE jobs SET state='failed',error_category='payment_mismatch' WHERE id=$1", changeID)
			return e
		}
		if _, e = tx.Exec(ctx, "UPDATE entitlements SET ends_at=$2 WHERE tenant_id=$1 AND ends_at IS NULL AND starts_at<$2", tenant, t.BillingPeriod.StartsAt); e != nil {
			return e
		}
		for _, price := range p.PriceIDs {
			if _, e = tx.Exec(ctx, "INSERT INTO entitlements(id,tenant_id,price_id,starts_at) VALUES($1,$2,$3,$4)", id(), tenant, price, t.BillingPeriod.StartsAt); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(ctx, "UPDATE tenants SET plan_id=$2 WHERE id=$1", tenant, p.PlanID); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "UPDATE jobs SET state='done',external_id=$2,error_category='',updated_at=now() WHERE id=$1", changeID, t.ID); e != nil {
			return e
		}
	} else {
		rows, e := tx.Query(ctx, "SELECT p.external_price_id FROM entitlements en JOIN prices p ON p.id=en.price_id WHERE en.tenant_id=$1 AND en.ends_at IS NULL AND p.kind=2 ORDER BY p.id", tenant)
		if e != nil {
			return e
		}
		var expected []string
		for rows.Next() {
			var p string
			if e = rows.Scan(&p); e != nil {
				rows.Close()
				return e
			}
			expected = append(expected, p)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		if !matchesPrices(t, expected) {
			return nil
		}
	}
	if _, e = tx.Exec(ctx, "INSERT INTO provider_transactions VALUES($1,$2,'subscription',0,$3,$4) ON CONFLICT DO NOTHING", t.ID, tenant, t.Currency, at); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO provider_objects VALUES($1,$2,'active') ON CONFLICT(external_id) DO UPDATE SET occurred_at=EXCLUDED.occurred_at,state='active' WHERE provider_objects.occurred_at<EXCLUDED.occurred_at", t.SubscriptionID, at); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE tenants SET period_start=$2,period_end=$3,renewal_failed=false WHERE id=$1", tenant, t.BillingPeriod.StartsAt, t.BillingPeriod.EndsAt); e != nil {
		return e
	}
	if e = postgres.GrantAllowances(ctx, tx, tenant, t.BillingPeriod.StartsAt, t.BillingPeriod.EndsAt, t.BillingPeriod.StartsAt, "renewal:"+t.ID, nil); e != nil {
		return e
	}
	_, e = tx.Exec(ctx, "INSERT INTO billing_periods SELECT t.id,$2,$3,p.postpaid FROM tenants t JOIN plans p ON p.id=t.plan_id WHERE t.id=$1 ON CONFLICT DO NOTHING", tenant, t.BillingPeriod.StartsAt, t.BillingPeriod.EndsAt)
	return e
}
