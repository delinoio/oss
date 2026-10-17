// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/delinoio/oss/servers/delcore-api/internal/config"
	"github.com/delinoio/oss/servers/delcore-api/internal/postgres"
	"github.com/delinoio/oss/servers/delcore-api/internal/provider"
	"github.com/jackc/pgx/v5"
)

type Worker struct {
	DB       *postgres.Store
	Provider *provider.Client
	Config   config.Config
	Log      *slog.Logger
}
type job struct {
	ID, Tenant, Kind, State, ExternalID string
	Attempts                            int
	Payload                             json.RawMessage
}

func (w *Worker) Recover(ctx context.Context) error {
	_, e := w.DB.Pool.Exec(ctx, `UPDATE jobs SET state=CASE WHEN kind IN ('email','webhook') THEN 'pending' ELSE 'reconcile' END,available_at=now(),updated_at=now() WHERE state='running'`)
	return e
}
func (w *Worker) Run(ctx context.Context) error {
	if e := w.Recover(ctx); e != nil {
		return e
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if e := w.Step(ctx); e != nil {
				w.Log.ErrorContext(ctx, "background step failed", "category", "database")
			}
		}
	}
}
func (w *Worker) Step(ctx context.Context) error {
	if e := w.Maintain(ctx, time.Now().UTC()); e != nil {
		return e
	}
	j := job{}
	e := w.DB.Transaction(ctx, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `SELECT id::text,COALESCE(tenant_id::text,''),kind,state,attempts,external_id,payload FROM jobs WHERE state IN ('pending','reconcile') AND available_at<=now() ORDER BY available_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&j.ID, &j.Tenant, &j.Kind, &j.State, &j.Attempts, &j.ExternalID, &j.Payload)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, "UPDATE jobs SET state='running',attempts=attempts+1,updated_at=now() WHERE id=$1", j.ID)
		return e
	})
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, w.Config.Timeout())
	defer cancel()
	if j.Kind == "webhook" {
		var p struct {
			EventID string `json:"event_id"`
		}
		if json.Unmarshal(j.Payload, &p) != nil {
			return w.finish(ctx, j, "failed", "invalid_payload", "")
		}
		if e = w.ProcessWebhook(ctx, p.EventID); e != nil {
			return w.retry(ctx, j, provider.ErrTransient, false)
		}
		return w.finish(ctx, j, "done", "", "")
	}
	if j.Kind == "email" {
		var p struct{ To, Subject, Text string }
		if json.Unmarshal(j.Payload, &p) != nil {
			return w.finish(ctx, j, "failed", "invalid_payload", "")
		}
		e = w.Provider.SendEmail(ctx, p.To, p.Subject, p.Text)
		if e == nil {
			return w.finish(ctx, j, "done", "", "")
		}
		return w.retry(ctx, j, e, false)
	}
	if j.State == "reconcile" {
		return w.reconcile(ctx, j)
	}
	var p struct {
		Amount         string   `json:"amount"`
		MinorUnits     string   `json:"minor_units"`
		Currency       string   `json:"currency"`
		ReturnURL      string   `json:"return_url"`
		ExternalPrices []string `json:"external_price_ids"`
		Timing         string   `json:"timing"`
	}
	if json.Unmarshal(j.Payload, &p) != nil {
		return w.finish(ctx, j, "failed", "invalid_payload", "")
	}
	var customer, subscription string
	if e = w.DB.Pool.QueryRow(ctx, "SELECT external_customer_id,external_subscription_id FROM tenants WHERE id=$1", j.Tenant).Scan(&customer, &subscription); e != nil {
		return e
	}
	body := map[string]any{"custom_data": map[string]string{"delcore_job_id": j.ID, "delcore_tenant_id": j.Tenant}}
	path := "/transactions"
	method := http.MethodPost
	switch j.Kind {
	case "topup", "auto_topup", "settlement":
		body["items"] = []any{map[string]any{"quantity": 1, "price": map[string]any{"description": "Delcore usage credits", "product_id": w.Config.PaddleCreditProductID, "unit_price": map[string]string{"amount": p.MinorUnits, "currency_code": w.Config.Currency}}}}
		if customer != "" {
			body["customer_id"] = customer
		}
		body["collection_mode"] = "automatic"
		if j.Kind != "topup" {
			if subscription == "" {
				return w.finish(ctx, j, "failed", "payment_method_required", "")
			}
			// Paddle cannot modify subscriptions within thirty minutes of renewal.
			// Keep the reserved settlement durable until a confirmed cycle is eligible.
			var end time.Time
			var failed bool
			if e = w.DB.Pool.QueryRow(ctx, "SELECT period_end,renewal_failed FROM tenants WHERE id=$1", j.Tenant).Scan(&end, &failed); e != nil {
				return e
			}
			if failed || !end.After(time.Now().Add(30*time.Minute)) {
				return w.finish(ctx, j, "pending", "provider_billing_window", "")
			}
			path = "/subscriptions/" + url.PathEscape(subscription) + "/charge"
			delete(body, "customer_id")
			delete(body, "collection_mode")
			body["effective_from"] = "immediately"
			body["on_payment_failure"] = "prevent_change"
		} else {
			body["checkout"] = map[string]string{"url": p.ReturnURL}
		}
	case "subscription":
		items := []any{}
		for _, price := range p.ExternalPrices {
			items = append(items, map[string]any{"price_id": price, "quantity": 1})
		}
		body["items"] = items
		if subscription != "" {
			method = http.MethodPatch
			path = "/subscriptions/" + url.PathEscape(subscription)
			body["proration_billing_mode"] = p.Timing
			body["on_payment_failure"] = "prevent_change"
		} else {
			body["collection_mode"] = "automatic"
			body["checkout"] = map[string]string{"url": p.ReturnURL}
			if customer != "" {
				body["customer_id"] = customer
			}
		}
	case "cancel":
		if subscription == "" {
			return w.finish(ctx, j, "failed", "subscription_required", "")
		}
		path = "/subscriptions/" + url.PathEscape(subscription) + "/cancel"
		body = map[string]any{"effective_from": "next_billing_period"}
	default:
		return w.finish(ctx, j, "failed", "unknown_job_kind", "")
	}
	raw, e := w.Provider.Request(ctx, method, path, body)
	if e != nil {
		return w.retry(ctx, j, e, false)
	}
	var result provider.Transaction
	if json.Unmarshal(raw, &result) != nil || result.ID == "" {
		return w.retry(ctx, j, provider.ErrAmbiguous, false)
	}
	// Subscription responses are not payment confirmation. Webhooks and provider
	// reconciliation own activation, including zero-due changes and cancellations.
	if j.Kind == "cancel" || j.Kind == "subscription" && subscription != "" {
		return w.finish(ctx, j, "reconcile", "awaiting_provider_event", subscription)
	}
	if result.Checkout.URL != "" {
		u, err := url.Parse(result.Checkout.URL)
		if err != nil || u.Scheme != "https" || u.User != nil {
			return w.retry(ctx, j, provider.ErrAmbiguous, false)
		}
		if _, e = w.DB.Pool.Exec(ctx, "UPDATE jobs SET checkout_url=$2 WHERE id=$1", j.ID, result.Checkout.URL); e != nil {
			return e
		}
	}
	if e = w.finish(ctx, j, "reconcile", "", result.ID); e != nil {
		return e
	}
	if result.Status == "completed" {
		return w.ApplyTransaction(ctx, result, time.Now().UTC())
	}
	return nil
}
func (w *Worker) finish(ctx context.Context, j job, state, category, external string) error {
	// Persist an ambiguous outcome even after the provider request times out or
	// shutdown cancels the worker. This bounded write prevents stranded running jobs.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.Config.Timeout())
	defer cancel()
	return w.DB.Transaction(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "UPDATE jobs SET state=$2,error_category=$3,external_id=CASE WHEN $4='' THEN external_id ELSE $4 END,available_at=$5,updated_at=now() WHERE id=$1 AND state='running'", j.ID, state, category, external, time.Now().Add(time.Duration(w.Config.RetrySeconds)*time.Second))
		if e != nil {
			return e
		}
		if j.Kind == "auto_topup" && state == "failed" {
			_, e = tx.Exec(ctx, "UPDATE tenants SET auto_suspended=true WHERE id=$1", j.Tenant)
			if e == nil {
				e = notify(ctx, tx, j.Tenant, "auto_failed:"+j.ID, "Automatic top-up failed", "Automatic top-ups are suspended. Review your payment settings.", time.Now().UTC())
			}
		}
		return e
	})
}
func (w *Worker) retry(ctx context.Context, j job, e error, reconcile bool) error {
	state := "pending"
	category := "provider_transient"
	if reconcile || errors.Is(e, provider.ErrAmbiguous) {
		state = "reconcile"
		category = "payment_ambiguous"
	}
	if errors.Is(e, provider.ErrPermanent) || j.Attempts+1 >= w.Config.RetryLimit {
		state = "failed"
		category = "retry_exhausted"
		if errors.Is(e, provider.ErrPermanent) {
			category = "provider_rejected"
		}
	}
	w.Log.WarnContext(ctx, "provider work deferred", "kind", j.Kind, "state", state, "category", category)
	return w.finish(ctx, j, state, category, j.ExternalID)
}
