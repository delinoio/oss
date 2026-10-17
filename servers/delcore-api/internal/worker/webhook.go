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

	"github.com/delinoio/oss/servers/delcore-api/internal/provider"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
		_, e := tx.Exec(ctx, "INSERT INTO webhooks(event_id,occurred_at,payload,received_at) VALUES($1,$2,$3,now()) ON CONFLICT DO NOTHING", event.ID, event.OccurredAt, raw)
		return e
	})
	if e != nil {
		rw.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	rw.WriteHeader(http.StatusAccepted)
}
func (w *Worker) ProcessWebhook(ctx context.Context) error {
	return w.DB.Transaction(ctx, func(tx pgx.Tx) error {
		var eventID string
		var raw []byte
		e := tx.QueryRow(ctx, "SELECT event_id,payload FROM webhooks WHERE processed_at IS NULL ORDER BY received_at,event_id LIMIT 1 FOR UPDATE").Scan(&eventID, &raw)
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
		return e
	})
}
func (w *Worker) ApplyTransaction(ctx context.Context, t provider.Transaction, at time.Time) error {
	return w.DB.Transaction(ctx, func(tx pgx.Tx) error { return w.applyTransaction(ctx, tx, t, at) })
}
func (w *Worker) applyTransaction(ctx context.Context, tx pgx.Tx, t provider.Transaction, at time.Time) error {
	if t.Status != "completed" || t.Custom.JobID == "" {
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
		Amount     string   `json:"amount"`
		MinorUnits string   `json:"minor_units"`
		PlanID     string   `json:"plan_id"`
		PriceIDs   []string `json:"price_ids"`
	}
	if e = json.Unmarshal(j.Payload, &p); e != nil {
		return e
	}
	if t.Custom.TenantID != j.Tenant || t.Currency != w.Config.Currency || (j.ExternalID != "" && j.ExternalID != t.ID) || (p.MinorUnits != "" && t.Details.Totals.Subtotal != p.MinorUnits) {
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
		if t.SubscriptionID == "" || t.BillingPeriod == nil || !t.BillingPeriod.EndsAt.After(t.BillingPeriod.StartsAt) {
			_, e = tx.Exec(ctx, "UPDATE jobs SET state='reconcile',external_id=$2,error_category='subscription_period_missing',available_at=now() WHERE id=$1", j.ID, t.ID)
			return e
		}
		_, e = tx.Exec(ctx, "UPDATE tenants SET plan_id=$2,external_customer_id=$3,external_subscription_id=$4,period_start=$5,period_end=$6,renewal_failed=false WHERE id=$1", j.Tenant, p.PlanID, t.CustomerID, t.SubscriptionID, t.BillingPeriod.StartsAt, t.BillingPeriod.EndsAt)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, "INSERT INTO billing_periods SELECT id,$2,$3,p.postpaid FROM tenants t JOIN plans p ON p.id=t.plan_id WHERE t.id=$1 ON CONFLICT DO NOTHING", j.Tenant, t.BillingPeriod.StartsAt, t.BillingPeriod.EndsAt)
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
	default:
		return nil
	}
	if e != nil {
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
		_, e = tx.Exec(ctx, "UPDATE tenants SET plan_id=(SELECT id FROM plans WHERE is_default AND free),external_subscription_id='' WHERE id=$1", tenant)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, "UPDATE entitlements SET ends_at=$2 WHERE tenant_id=$1 AND ends_at IS NULL AND starts_at<$2", tenant, event.OccurredAt)
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
		Totals        struct {
			Subtotal string `json:"subtotal"`
		} `json:"totals"`
	}
	if e := json.Unmarshal(event.Data, &v); e != nil {
		return e
	}
	var tenant string
	var amount string
	e := tx.QueryRow(ctx, "SELECT tenant_id::text,amount::text FROM ledger WHERE source_key=$1 AND kind='credit'", "payment:"+v.TransactionID).Scan(&tenant, &amount)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if v.Action == "chargeback" || v.Action == "chargeback_warning" {
		_, e = tx.Exec(ctx, "INSERT INTO disputes VALUES($1,$2,true) ON CONFLICT(external_id) DO UPDATE SET active=true", v.ID, tenant)
		if e != nil {
			return e
		}
	}
	if v.Status == "approved" && (v.Action == "refund" || v.Action == "chargeback") {
		if v.Totals.Subtotal == "" {
			return errors.New("adjustment amount missing")
		}
		_, e = tx.Exec(ctx, `INSERT INTO ledger(id,tenant_id,kind,amount,source_key,created_at) VALUES($1,$2,'refund',-$3::numeric/power(10::numeric,$4),$5,$6) ON CONFLICT(source_key) DO NOTHING`, id(), tenant, v.Totals.Subtotal, w.Config.MinorUnits, "adjustment:"+v.ID, event.OccurredAt)
	}
	return e
}
