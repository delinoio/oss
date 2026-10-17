// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/servers/delcore-api/internal/provider"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (w *Worker) reconcile(ctx context.Context, j job) error {
	// A crash can occur before finish persists the response ID. Subscription
	// mutations already have an authoritative target on the tenant.
	if j.ExternalID == "" && (j.Kind == "cancel" || j.Kind == "subscription") {
		if e := w.DB.Pool.QueryRow(ctx, "SELECT external_subscription_id FROM tenants WHERE id=$1", j.Tenant).Scan(&j.ExternalID); e != nil {
			return e
		}
	}
	if j.ExternalID == "" {
		// Search provider state once before requiring operator review. Absence
		// from a bounded page is never proof that a charge did not happen.
		var customer string
		if e := w.DB.Pool.QueryRow(ctx, "SELECT external_customer_id FROM tenants WHERE id=$1", j.Tenant).Scan(&customer); e != nil {
			return e
		}
		path := "/transactions?per_page=200&order_by=created_at[DESC]"
		if customer != "" {
			path += "&customer_id=" + url.QueryEscape(customer)
		}
		raw, e := w.Provider.Request(ctx, http.MethodGet, path, nil)
		if e != nil {
			return w.retry(ctx, j, e, true)
		}
		var transactions []provider.Transaction
		if json.Unmarshal(raw, &transactions) != nil {
			return w.retry(ctx, j, provider.ErrAmbiguous, true)
		}
		for _, t := range transactions {
			t = t.AttributeCharge(w.Config.PaddleCreditProductID)
			if t.Custom.JobID != j.ID || t.Custom.TenantID != j.Tenant {
				continue
			}
			if t.Status == "completed" {
				return w.ApplyTransaction(ctx, t, time.Now().UTC())
			}
			return w.finish(ctx, j, "reconcile", "awaiting_payment", t.ID)
		}
		return w.finish(ctx, j, "failed", "unknown_payment_outcome", "")
	}
	if strings.HasPrefix(j.ExternalID, "sub_") {
		if j.Kind == "cancel" {
			raw, e := w.Provider.Request(ctx, http.MethodGet, "/subscriptions/"+url.PathEscape(j.ExternalID), nil)
			if e != nil {
				return w.retry(ctx, j, e, true)
			}
			var v struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(raw, &v) != nil {
				return w.retry(ctx, j, provider.ErrAmbiguous, true)
			}
			if v.Status == "canceled" {
				event := provider.Event{Type: "subscription.canceled", OccurredAt: time.Now().UTC(), Data: raw}
				if e = w.DB.Transaction(ctx, func(tx pgx.Tx) error { return w.subscriptionEvent(ctx, tx, event) }); e != nil {
					return e
				}
				return w.finish(ctx, j, "done", "", j.ExternalID)
			}
			return w.finish(ctx, j, "reconcile", "awaiting_renewal", j.ExternalID)
		}
		if j.Kind == "subscription" {
			var p struct {
				Timing         string   `json:"timing"`
				ExternalPrices []string `json:"external_price_ids"`
			}
			if json.Unmarshal(j.Payload, &p) != nil {
				return w.finish(ctx, j, "failed", "invalid_payload", j.ExternalID)
			}
			if p.Timing == "do_not_bill" {
				raw, e := w.Provider.Request(ctx, http.MethodGet, "/subscriptions/"+url.PathEscape(j.ExternalID), nil)
				if e != nil {
					return w.retry(ctx, j, e, true)
				}
				var state provider.Transaction
				if json.Unmarshal(raw, &state) != nil || state.ID != j.ExternalID {
					return w.retry(ctx, j, provider.ErrAmbiguous, true)
				}
				if !matchesPrices(state, p.ExternalPrices) {
					return w.finish(ctx, j, "failed", "subscription_mismatch", j.ExternalID)
				}
				// Accepted no-charge changes become entitlements only on a paid renewal.
				return w.finish(ctx, j, "reconcile", "awaiting_renewal", j.ExternalID)
			}
		}
		raw, e := w.Provider.Request(ctx, http.MethodGet, "/transactions?subscription_id="+url.QueryEscape(j.ExternalID)+"&per_page=200&order_by=created_at[DESC]", nil)
		if e != nil {
			return w.retry(ctx, j, e, true)
		}
		var transactions []provider.Transaction
		if json.Unmarshal(raw, &transactions) != nil {
			return w.retry(ctx, j, provider.ErrAmbiguous, true)
		}
		for _, t := range transactions {
			t = t.AttributeCharge(w.Config.PaddleCreditProductID)
			matches := t.Custom.JobID == j.ID
			for _, item := range t.Items {
				if item.Price.Custom.JobID == j.ID && item.Price.ProductID == w.Config.PaddleCreditProductID {
					matches = true
				}
			}
			if matches && t.Status == "completed" {
				return w.ApplyTransaction(ctx, t, time.Now().UTC())
			}
		}
		return w.finish(ctx, j, "reconcile", "awaiting_payment", j.ExternalID)
	}
	raw, e := w.Provider.Request(ctx, http.MethodGet, "/transactions/"+url.PathEscape(j.ExternalID), nil)
	if e != nil {
		return w.retry(ctx, j, e, true)
	}
	var transaction provider.Transaction
	if json.Unmarshal(raw, &transaction) != nil {
		return w.retry(ctx, j, provider.ErrAmbiguous, true)
	}
	if transaction.Status == "completed" {
		return w.ApplyTransaction(ctx, transaction, time.Now().UTC())
	}
	if transaction.Status == "canceled" || transaction.Status == "past_due" {
		return w.finish(ctx, j, "failed", "payment_failed", j.ExternalID)
	}
	return w.finish(ctx, j, "reconcile", "awaiting_payment", j.ExternalID)
}
