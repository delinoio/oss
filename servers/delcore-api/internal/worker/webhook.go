// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/delinoio/oss/servers/delcore-api/internal/accounting"
	"github.com/delinoio/oss/servers/delcore-api/internal/postgres"
	"github.com/delinoio/oss/servers/delcore-api/internal/provider"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func id() string { return uuid.Must(uuid.NewV7()).String() }
func (w *Worker) Webhook(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	raw, e := io.ReadAll(http.MaxBytesReader(rw, r.Body, w.Config.MaxPayloadBytes))
	if e != nil {
		rw.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	if !provider.VerifySignature(w.Config.PaddleWebhookSecret, r.Header.Get("Paddle-Signature"), raw, time.Now().UTC()) {
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}
	var event provider.Event
	if json.Unmarshal(raw, &event) != nil || event.ID == "" || len(event.ID) > 200 || event.OccurredAt.IsZero() || len(event.Data) == 0 {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), w.Config.Timeout())
	defer cancel()
	e = w.DB.Transaction(ctx, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, "INSERT INTO webhooks(event_id,occurred_at,payload,received_at) VALUES($1,$2,$3,now()) ON CONFLICT DO NOTHING", event.ID, event.OccurredAt, raw)
		if e != nil || tag.RowsAffected() == 0 {
			return e
		}
		_, e = tx.Exec(ctx, "INSERT INTO jobs(id,kind,dedup_key,payload,available_at,created_at,updated_at) VALUES($1,'webhook',$2,$3,now(),now(),now())", id(), "webhook:"+event.ID, map[string]string{"event_id": event.ID})
		return e
	})
	if e != nil {
		rw.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	rw.WriteHeader(http.StatusAccepted)
}
func (w *Worker) ProcessWebhook(ctx context.Context, selectedID string) error {
	return w.DB.Transaction(ctx, func(tx pgx.Tx) error {
		var eventID string
		var raw []byte
		e := tx.QueryRow(ctx, "SELECT event_id,payload FROM webhooks WHERE processed_at IS NULL AND event_id=$1 FOR UPDATE", selectedID).Scan(&eventID, &raw)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		var event provider.Event
		if e = json.Unmarshal(raw, &event); e != nil {
			return e
		}
		switch {
		case event.Type == "transaction.completed":
			var t provider.Transaction
			if e = json.Unmarshal(event.Data, &t); e != nil {
				return e
			}
			if e = w.applyTransaction(ctx, tx, t, event.OccurredAt); e != nil {
				return e
			}
		case event.Type == "transaction.payment_failed":
			var t provider.Transaction
			if e = json.Unmarshal(event.Data, &t); e != nil {
				return e
			}
			t = t.AttributeCharge(w.Config.PaddleCreditProductID)
			if t.Origin == "subscription_recurring" && t.BillingPeriod != nil {
				_, e = tx.Exec(ctx, "UPDATE tenants SET renewal_failed=true WHERE external_subscription_id=$1 AND external_customer_id=$2 AND period_end<=$3 AND NOT EXISTS(SELECT 1 FROM provider_transactions WHERE external_id=$4)", t.SubscriptionID, t.CustomerID, t.BillingPeriod.StartsAt, t.ID)
			} else if u, err := uuid.Parse(t.Custom.JobID); err == nil && u.Version() == 7 {
				_, e = tx.Exec(ctx, "UPDATE jobs SET state='failed',error_category='payment_failed',updated_at=now() WHERE id=$1 AND tenant_id::text=$2 AND state<>'done'", t.Custom.JobID, t.Custom.TenantID)
				if e == nil {
					var tag pgconn.CommandTag
					tag, e = tx.Exec(ctx, "UPDATE tenants SET auto_suspended=true WHERE id::text=$1 AND EXISTS(SELECT 1 FROM jobs WHERE id=$2 AND kind='auto_topup' AND state='failed')", t.Custom.TenantID, t.Custom.JobID)
					if e == nil && tag.RowsAffected() > 0 {
						e = notify(ctx, tx, t.Custom.TenantID, "auto_failed:"+t.Custom.JobID, "Automatic top-up failed", "Automatic top-ups are suspended. Review your payment settings.", event.OccurredAt)
					}
				}
			}
			if e != nil {
				return e
			}
		case strings.HasPrefix(event.Type, "subscription."):
			if e = w.subscriptionEvent(ctx, tx, event); e != nil {
				return e
			}
		case strings.HasPrefix(event.Type, "adjustment."):
			if e = w.adjustmentEvent(ctx, tx, event); e != nil {
				return e
			}
		}
		_, e = tx.Exec(ctx, "UPDATE webhooks SET processed_at=now() WHERE event_id=$1", eventID)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, "UPDATE jobs SET state='done',error_category='',updated_at=now() WHERE kind='webhook' AND payload->>'event_id'=$1", eventID)
		return e
	})
}
func (w *Worker) ApplyTransaction(ctx context.Context, t provider.Transaction, at time.Time) error {
	return w.DB.Transaction(ctx, func(tx pgx.Tx) error { return w.applyTransaction(ctx, tx, t, at) })
}
func (w *Worker) applyTransaction(ctx context.Context, tx pgx.Tx, t provider.Transaction, at time.Time) error {
	t = t.AttributeCharge(w.Config.PaddleCreditProductID)
	if t.Status != "completed" {
		return nil
	}
	if t.Origin == "subscription_recurring" {
		if e := w.applyRenewalSettlements(ctx, tx, t, at); e != nil {
			return e
		}
		items := t.Items[:0:0]
		for _, item := range t.Items {
			if item.Price.ProductID != w.Config.PaddleCreditProductID || item.Price.Custom.JobID == "" {
				items = append(items, item)
			}
		}
		t.Items = items
		return w.renew(ctx, tx, t, at)
	}
	if t.Custom.JobID == "" {
		return nil
	}
	u, e := uuid.Parse(t.Custom.JobID)
	if e != nil || u.Version() != 7 {
		return nil
	}
	var j job
	e = tx.QueryRow(ctx, "SELECT id::text,tenant_id::text,kind,state,external_id,payload FROM jobs WHERE id=$1", t.Custom.JobID).Scan(&j.ID, &j.Tenant, &j.Kind, &j.State, &j.ExternalID, &j.Payload)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if j.State == "done" {
		return nil
	}
	var p struct {
		Amount         string   `json:"amount"`
		MinorUnits     string   `json:"minor_units"`
		PlanID         string   `json:"plan_id"`
		PriceIDs       []string `json:"price_ids"`
		ExternalPrices []string `json:"external_price_ids"`
	}
	if e = json.Unmarshal(j.Payload, &p); e != nil {
		return e
	}
	if t.Custom.TenantID != j.Tenant || t.Currency != w.Config.Currency || (j.ExternalID != "" && j.ExternalID != t.ID && j.ExternalID != t.SubscriptionID) || (p.MinorUnits != "" && t.Details.Totals.Subtotal != p.MinorUnits) {
		_, e = tx.Exec(ctx, "UPDATE jobs SET state='failed',error_category='payment_mismatch',updated_at=now() WHERE id=$1", j.ID)
		return e
	}
	switch j.Kind {
	case "topup", "auto_topup":
		if p.Amount == "" {
			return errors.New("top-up amount missing")
		}
		_, e = tx.Exec(ctx, "INSERT INTO ledger(id,tenant_id,kind,amount,source_key,created_at) VALUES($1,$2,'credit',$3,$4,$5) ON CONFLICT(source_key) DO NOTHING", id(), j.Tenant, p.Amount, "payment:"+t.ID, at)
	case "settlement":
		_, e = tx.Exec(ctx, "INSERT INTO ledger(id,tenant_id,kind,amount,source_key,created_at) VALUES($1,$2,'settlement',$3,$4,$5) ON CONFLICT(source_key) DO NOTHING", id(), j.Tenant, p.Amount, "payment:"+t.ID, at)
	case "subscription":
		if !matchesPrices(t, p.ExternalPrices) {
			_, e = tx.Exec(ctx, "UPDATE jobs SET state='failed',error_category='payment_mismatch',updated_at=now() WHERE id=$1", j.ID)
			return e
		}
		if t.SubscriptionID == "" || t.BillingPeriod == nil || !t.BillingPeriod.EndsAt.After(t.BillingPeriod.StartsAt) {
			_, e = tx.Exec(ctx, "UPDATE jobs SET state='reconcile',external_id=$2,error_category='subscription_period_missing',available_at=now() WHERE id=$1", j.ID, t.ID)
			return e
		}
		var oldSubscription string
		var periodStart, periodEnd time.Time
		if e = tx.QueryRow(ctx, "SELECT external_subscription_id,period_start,period_end FROM tenants WHERE id=$1", j.Tenant).Scan(&oldSubscription, &periodStart, &periodEnd); e != nil {
			return e
		}
		var previous postgres.Allowances
		if oldSubscription == t.SubscriptionID && periodEnd.After(at) {
			previous, e = postgres.CurrentAllowances(ctx, tx, j.Tenant)
			if e != nil {
				return e
			}
		} else {
			periodStart = t.BillingPeriod.StartsAt
			periodEnd = t.BillingPeriod.EndsAt
		}
		_, e = tx.Exec(ctx, "UPDATE tenants SET plan_id=$2,external_customer_id=$3,external_subscription_id=$4,period_start=$5,period_end=$6 WHERE id=$1", j.Tenant, p.PlanID, t.CustomerID, t.SubscriptionID, periodStart, periodEnd)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, "INSERT INTO billing_periods SELECT t.id,$2,$3,p.postpaid FROM tenants t JOIN plans p ON p.id=t.plan_id WHERE t.id=$1 ON CONFLICT DO NOTHING", j.Tenant, periodStart, periodEnd)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, "UPDATE entitlements SET ends_at=$2 WHERE tenant_id=$1 AND ends_at IS NULL AND starts_at<$2", j.Tenant, at)
		if e != nil {
			return e
		}
		for _, price := range p.PriceIDs {
			if _, e = tx.Exec(ctx, "INSERT INTO entitlements(id,tenant_id,price_id,starts_at) VALUES($1,$2,$3,$4)", id(), j.Tenant, price, at); e != nil {
				return e
			}
		}
		if e = postgres.GrantAllowances(ctx, tx, j.Tenant, periodStart, periodEnd, at, "payment:"+t.ID, previous); e != nil {
			return e
		}
	default:
		return nil
	}
	if e != nil {
		return e
	}
	principal := p.Amount
	if principal == "" {
		principal = "0"
	}
	if _, e = tx.Exec(ctx, "INSERT INTO provider_transactions VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING", t.ID, j.Tenant, j.Kind, principal, t.Currency, at); e != nil {
		return e
	}
	_, e = tx.Exec(ctx, "UPDATE jobs SET state='done',external_id=$2,error_category='',updated_at=now() WHERE id=$1", j.ID, t.ID)
	return e
}
func (w *Worker) subscriptionEvent(ctx context.Context, tx pgx.Tx, event provider.Event) error {
	var v struct {
		ID            string `json:"id"`
		Status        string `json:"status"`
		CurrentPeriod *struct {
			Start time.Time `json:"starts_at"`
			End   time.Time `json:"ends_at"`
		} `json:"current_billing_period"`
	}
	if e := json.Unmarshal(event.Data, &v); e != nil {
		return e
	}
	var tenant string
	e := tx.QueryRow(ctx, "SELECT id::text FROM tenants WHERE external_subscription_id=$1 AND $1<>''", v.ID).Scan(&tenant)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	var previous time.Time
	e = tx.QueryRow(ctx, "SELECT occurred_at FROM provider_objects WHERE external_id=$1", v.ID).Scan(&previous)
	if e == nil && !event.OccurredAt.After(previous) {
		return nil
	}
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO provider_objects VALUES($1,$2,$3) ON CONFLICT(external_id) DO UPDATE SET occurred_at=EXCLUDED.occurred_at,state=EXCLUDED.state", v.ID, event.OccurredAt, v.Status); e != nil {
		return e
	}
	switch v.Status {
	case "past_due":
		_, e = tx.Exec(ctx, "UPDATE tenants SET renewal_failed=true WHERE id=$1", tenant)
	case "canceled":
		e = w.activateFree(ctx, tx, tenant, event.OccurredAt)

	}
	// An active subscription event alone never clears payment failure or grants a
	// paid entitlement. Only a confirmed corresponding payment can do that.
	return e
}
func (w *Worker) adjustmentEvent(ctx context.Context, tx pgx.Tx, event provider.Event) error {
	var v struct {
		ID            string `json:"id"`
		Action        string `json:"action"`
		Status        string `json:"status"`
		TransactionID string `json:"transaction_id"`
		Currency      string `json:"currency_code"`
		Totals        struct {
			Subtotal string `json:"subtotal"`
		} `json:"totals"`
	}
	if e := json.Unmarshal(event.Data, &v); e != nil {
		return e
	}
	if v.ID == "" || v.Currency != w.Config.Currency {
		return nil
	}
	var tenant, kind, principal string
	e := tx.QueryRow(ctx, "SELECT tenant_id::text,kind,amount::text FROM provider_transactions WHERE external_id=$1", v.TransactionID).Scan(&tenant, &kind, &principal)
	if errors.Is(e, pgx.ErrNoRows) {
		return errors.New("adjustment payment attribution is pending")
	}
	if e != nil {
		return e
	}
	// Reversal notifications update the original adjustment's status. Their own
	// amount is not applied again; the original approved/reversed state owns it.
	if strings.HasSuffix(v.Action, "_reverse") {
		return nil
	}
	var prior string
	var previous time.Time
	e = tx.QueryRow(ctx, "SELECT amount::text,occurred_at FROM adjustment_effects WHERE external_id=$1", v.ID).Scan(&prior, &previous)
	if e == nil && !event.OccurredAt.After(previous) {
		return nil
	}
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if prior == "" {
		prior = "0"
	}
	effect := "0"
	if v.Status == "approved" && (v.Action == "refund" || v.Action == "chargeback") {
		amount, e := accounting.Parse(v.Totals.Subtotal)
		if e != nil || amount.Cmp(accounting.Zero()) < 0 {
			return errors.New("invalid adjustment amount")
		}
		if e = tx.QueryRow(ctx, "SELECT ($1::numeric/power(10::numeric,$2))::text", amount.String(), w.Config.MinorUnits).Scan(&effect); e != nil {
			return e
		}
		var already string
		if e = tx.QueryRow(ctx, "SELECT COALESCE(sum(amount),0)::text FROM adjustment_effects WHERE transaction_id=$1 AND external_id<>$2", v.TransactionID, v.ID).Scan(&already); e != nil {
			return e
		}
		other, _ := accounting.Parse(already)
		total, _ := accounting.Parse(principal)
		reversed, _ := accounting.Parse(effect)
		if (kind == "topup" || kind == "auto_topup") && reversed.Add(other).Cmp(total) > 0 {
			return errors.New("adjustment exceeds purchased principal")
		}
	}
	if v.Action == "chargeback" || v.Action == "chargeback_warning" {
		_, e = tx.Exec(ctx, "INSERT INTO disputes(external_id,tenant_id,active,transaction_id) VALUES($1,$2,$3,$4) ON CONFLICT(external_id) DO UPDATE SET active=EXCLUDED.active", v.ID, tenant, v.Status != "reversed" && v.Status != "rejected", v.TransactionID)
		if e != nil {
			return e
		}
	}
	old, _ := accounting.Parse(prior)
	current, _ := accounting.Parse(effect)
	delta := old.Sub(current)
	if delta.Cmp(accounting.Zero()) != 0 {
		ledgerKind := "payment_refund"
		if kind == "topup" || kind == "auto_topup" {
			ledgerKind = "refund"
		}
		// Recurring and invoice refunds are payment history, not purchased credit.
		if _, e = tx.Exec(ctx, "INSERT INTO ledger(id,tenant_id,kind,amount,source_key,created_at) VALUES($1,$2,$6,$3,$4,$5) ON CONFLICT(source_key) DO NOTHING", id(), tenant, delta.String(), "adjustment:"+v.ID+":"+event.OccurredAt.Format(time.RFC3339Nano), event.OccurredAt, ledgerKind); e != nil {
			return e
		}
	}
	_, e = tx.Exec(ctx, "INSERT INTO adjustment_effects VALUES($1,$2,$3,$4) ON CONFLICT(external_id) DO UPDATE SET amount=EXCLUDED.amount,occurred_at=EXCLUDED.occurred_at", v.ID, v.TransactionID, effect, event.OccurredAt)
	return e
}

// One renewal can contain several separately reserved settlement jobs. Match
// each immutable price attribution and line subtotal before closing its debt.
func (w *Worker) applyRenewalSettlements(ctx context.Context, tx pgx.Tx, t provider.Transaction, at time.Time) error {
	for _, item := range t.Items {
		if item.Price.ProductID != w.Config.PaddleCreditProductID || item.Quantity != 1 {
			continue
		}
		jobID := item.Price.Custom.JobID
		if u, e := uuid.Parse(jobID); e != nil || u.Version() != 7 {
			continue
		}
		subtotal := ""
		for _, line := range t.Details.LineItems {
			if line.PriceID == item.Price.ID {
				subtotal = line.Totals.Subtotal
			}
		}
		var tenant, amount string
		e := tx.QueryRow(ctx, `SELECT j.tenant_id::text,j.payload->>'amount' FROM jobs j JOIN tenants t ON t.id=j.tenant_id WHERE j.id=$1 AND j.kind='settlement' AND j.state<>'done' AND j.tenant_id::text=$2 AND j.payload->>'minor_units'=$3 AND t.external_subscription_id=$4 AND t.external_customer_id=$5 AND j.payload->>'currency'=$6`, jobID, item.Price.Custom.TenantID, subtotal, t.SubscriptionID, t.CustomerID, t.Currency).Scan(&tenant, &amount)
		if errors.Is(e, pgx.ErrNoRows) {
			continue
		}
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "INSERT INTO ledger(id,tenant_id,kind,amount,source_key,created_at) VALUES($1,$2,'settlement',$3,$4,$5) ON CONFLICT(source_key) DO NOTHING", id(), tenant, amount, "settlement-job:"+jobID, at); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "UPDATE jobs SET state='done',external_id=$2,error_category='',updated_at=now() WHERE id=$1", jobID, t.ID); e != nil {
			return e
		}
	}
	return nil
}
