// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/delinoio/oss/protos/gen/go/delcore/v1"
	"github.com/delinoio/oss/servers/delcore-api/internal/accounting"
	"github.com/delinoio/oss/servers/delcore-api/internal/postgres"
	"google.golang.org/protobuf/types/known/timestamppb"
	"slices"
	"sort"
	"time"
)

func (s *Server) RequestTopUp(ctx context.Context, r *connect.Request[pb.RequestTopUpRequest]) (*connect.Response[pb.RequestTopUpResponse], error) {
	o := &pb.RequestTopUpResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.require(q.TenantId, pb.Role_ROLE_BILLING); e != nil {
			return e
		}
		d, e := accounting.Parse(q.Amount)
		if e != nil || len(q.Amount) > 64 || d.Cmp(accounting.Zero()) <= 0 || !d.IsMinorExact(c.s.Config.MinorUnits) || !c.s.Config.AllowsReturn(q.ReturnUrl) {
			return invalid("invalid top-up amount or return URL")
		}
		units, _ := d.MinorUnits(c.s.Config.MinorUnits)
		if units.Sign() <= 0 {
			return invalid("top-up amount is below the currency minor unit")
		}
		t, e := c.tenant(q.TenantId)
		if e != nil {
			return e
		}
		if t.Deleting {
			balance, err := accounting.Parse(t.CreditBalance)
			if err != nil || balance.Cmp(accounting.Zero()) >= 0 {
				return precondition("deleting tenants can only top up to recover credit debt")
			}
		}
		o.JobId, e = c.job(q.TenantId, "topup", "request:"+q.RequestKey, map[string]any{"amount": d.String(), "minor_units": units.String(), "currency": c.s.Config.Currency, "return_url": q.ReturnUrl})
		return e
	})
}
func (s *Server) SetAutoTopUp(ctx context.Context, r *connect.Request[pb.SetAutoTopUpRequest]) (*connect.Response[pb.SetAutoTopUpResponse], error) {
	o := &pb.SetAutoTopUpResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.require(q.TenantId, pb.Role_ROLE_BILLING); e != nil {
			return e
		}
		if !q.Enabled {
			if _, e := c.tx.Exec(c.ctx, "UPDATE tenants SET auto_threshold=NULL,auto_amount=NULL,auto_cap=NULL WHERE id=$1", q.TenantId); e != nil {
				return e
			}
			// Queued charges carry their amount in the job payload, so disabling the
			// setting must also end work that has never reached the provider. Running,
			// reconciling, attempted and externally identified jobs stay untouched.
			tag, e := c.tx.Exec(c.ctx, "UPDATE jobs SET state='done',error_category='auto_topup_disabled',updated_at=$2 WHERE tenant_id=$1 AND "+unattemptedAutoTopUp, q.TenantId, c.now)
			if e != nil {
				return e
			}
			c.s.Log.InfoContext(c.ctx, "automatic top-up disabled", "correlation_id", c.correlation, "tenant_id", q.TenantId, "stopped_jobs", tag.RowsAffected())
			return nil
		}
		threshold, e1 := accounting.Parse(q.Threshold)
		amount, e2 := accounting.Parse(q.Amount)
		cap, e3 := accounting.Parse(q.MonthlyCap)
		if e1 != nil || e2 != nil || e3 != nil || len(q.Threshold) > 64 || len(q.Amount) > 64 || len(q.MonthlyCap) > 64 || threshold.Cmp(accounting.Zero()) < 0 || amount.Cmp(accounting.Zero()) <= 0 || cap.Cmp(amount) < 0 || !amount.IsMinorExact(c.s.Config.MinorUnits) {
			return invalid("invalid automatic top-up settings")
		}
		tag, e := c.tx.Exec(c.ctx, "UPDATE tenants SET auto_threshold=$2,auto_amount=$3,auto_cap=$4,auto_suspended=false WHERE id=$1 AND deleting_at IS NULL AND external_subscription_id<>'' AND NOT renewal_failed AND period_end>$5 AND NOT EXISTS(SELECT 1 FROM jobs WHERE tenant_id=tenants.id AND kind='cancel' AND state IN ('pending','running','reconcile'))", q.TenantId, threshold.String(), amount.String(), cap.String(), c.now)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return precondition("automatic top-ups require an active Paddle subscription")
		}
		return c.audit(q.TenantId, "billing.auto_topup", "")
	})
}
func (s *Server) ChangeSubscription(ctx context.Context, r *connect.Request[pb.ChangeSubscriptionRequest]) (*connect.Response[pb.ChangeSubscriptionResponse], error) {
	o := &pb.ChangeSubscriptionResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.require(q.TenantId, pb.Role_ROLE_BILLING); e != nil {
			return e
		}
		if !canonical(q.PlanId) || len(q.PriceIds) == 0 || len(q.PriceIds) > 100 || !c.s.Config.AllowsReturn(q.ReturnUrl) {
			return invalid("invalid plan, prices or return URL")
		}
		t, e := c.tenant(q.TenantId)
		if e != nil {
			return e
		}
		if t.Deleting {
			return precondition("tenant is deleting")
		}
		var oldRank, newRank int
		var free bool
		if e = c.tx.QueryRow(c.ctx, "SELECT rank FROM plans WHERE id=$1", t.PlanId).Scan(&oldRank); e != nil {
			return e
		}
		if e = c.tx.QueryRow(c.ctx, "SELECT rank,free FROM plans WHERE id=$1", q.PlanId).Scan(&newRank, &free); e != nil {
			return invalid("unknown plan")
		}
		var pending bool
		if e = c.tx.QueryRow(c.ctx, "SELECT EXISTS(SELECT 1 FROM jobs WHERE tenant_id=$1 AND kind IN ('subscription','cancel') AND state IN ('pending','running','reconcile'))", q.TenantId).Scan(&pending); e != nil {
			return e
		}
		if pending {
			return precondition("a subscription change is already pending")
		}
		external := []string{}
		seen := map[string]bool{}
		newAddons := map[string]bool{}
		usageMeters := map[string]accounting.Decimal{}
		addonRecurring := map[string]int{}
		baseRecurring := 0
		for _, priceID := range q.PriceIds {
			if !canonical(priceID) || seen[priceID] {
				return invalid("invalid or repeated price")
			}
			seen[priceID] = true
			var ext, ownerPlan, addon, service, meter, rateText string
			var kind int
			if e = c.tx.QueryRow(c.ctx, "SELECT external_price_id,COALESCE(plan_id::text,''),COALESCE(addon_id::text,''),kind,service_id::text,meter,unit_price::text FROM prices WHERE id=$1", priceID).Scan(&ext, &ownerPlan, &addon, &kind, &service, &meter, &rateText); e != nil {
				return invalid("unknown price")
			}
			var current bool
			if e = c.tx.QueryRow(c.ctx, `SELECT NOT EXISTS(SELECT 1 FROM prices newer JOIN prices selected ON selected.id=$1 WHERE newer.plan_id IS NOT DISTINCT FROM selected.plan_id AND newer.addon_id IS NOT DISTINCT FROM selected.addon_id AND newer.service_id=selected.service_id AND newer.meter=selected.meter AND newer.kind=selected.kind AND (newer.created_at,newer.id)>(selected.created_at,selected.id))`, priceID).Scan(&current); e != nil {
				return e
			}
			if !current {
				return invalid("explicit changes require current price versions")
			}
			if addon == "" && ownerPlan != q.PlanId {
				return invalid("price belongs to another plan")
			}
			if addon != "" {
				newAddons[addon] = true
			}
			if kind == 1 {
				k := service + "\x00" + meter
				rate, err := accounting.Parse(rateText)
				if err != nil {
					return err
				}
				if previous, exists := usageMeters[k]; exists && previous.Cmp(rate) != 0 {
					return invalid("conflicting rates for the same service meter")
				}
				usageMeters[k] = rate
			}
			if kind == 2 {
				if ext == "" {
					return precondition("Paddle recurring price mapping required")
				}
				external = append(external, ext)
				if addon == "" {
					baseRecurring++
				} else {
					addonRecurring[addon]++
				}
			}
		}
		if !free && baseRecurring != 1 || free && baseRecurring != 0 {
			return invalid("paid base plans require one recurring base price; free base plans require none")
		}
		for addon := range newAddons {
			if addonRecurring[addon] != 1 {
				return invalid("one recurring price is required for each add-on")
			}
		}
		oldAddons := map[string]bool{}
		rows, e := c.tx.Query(c.ctx, "SELECT DISTINCT p.addon_id::text FROM entitlements en JOIN prices p ON p.id=en.price_id WHERE en.tenant_id=$1 AND en.ends_at IS NULL AND p.addon_id IS NOT NULL", q.TenantId)
		if e != nil {
			return e
		}
		for rows.Next() {
			var addon string
			if e = rows.Scan(&addon); e != nil {
				rows.Close()
				return e
			}
			oldAddons[addon] = true
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		added, removed := false, false
		for a := range newAddons {
			if !oldAddons[a] {
				added = true
			}
		}
		for a := range oldAddons {
			if !newAddons[a] {
				removed = true
			}
		}
		if added && removed || newRank > oldRank && removed || newRank < oldRank && added {
			return invalid("submit immediate additions and renewal removals as separate changes")
		}
		timing := "prorated_immediately"
		if newRank < oldRank || removed {
			timing = "do_not_bill"
		}
		// Existing paid entitlements prove the recurring items were purchased.
		// Replacing only internal usage versions creates no provider charge.
		localRefresh := false
		if q.PlanId == t.PlanId && !added && !removed && len(external) > 0 {
			var oldExternal []string
			if e = c.tx.QueryRow(c.ctx, "SELECT COALESCE(array_agg(p.external_price_id ORDER BY p.external_price_id),'{}') FROM entitlements en JOIN prices p ON p.id=en.price_id WHERE en.tenant_id=$1 AND en.ends_at IS NULL AND p.kind=2", q.TenantId).Scan(&oldExternal); e != nil {
				return e
			}
			sort.Strings(external)
			localRefresh = slices.Equal(oldExternal, external)
		}
		if free && len(external) == 0 || localRefresh {
			var subscription string
			var start, end time.Time
			if e = c.tx.QueryRow(c.ctx, "SELECT external_subscription_id,period_start,period_end FROM tenants WHERE id=$1", q.TenantId).Scan(&subscription, &start, &end); e != nil {
				return e
			}
			if subscription != "" && !localRefresh {
				return precondition("use cancellation to return to the default free plan")
			}
			if newRank < oldRank {
				o.JobId, e = c.job(q.TenantId, "subscription", "request:"+q.RequestKey, map[string]any{"plan_id": q.PlanId, "price_ids": q.PriceIds, "timing": "free_next_period"})
				if e != nil {
					return e
				}
				_, e = c.tx.Exec(c.ctx, "UPDATE jobs SET available_at=$2 WHERE id=$1", o.JobId, end)
				return e
			}
			if _, e = c.tx.Exec(c.ctx, "UPDATE tenants SET plan_id=$2 WHERE id=$1", q.TenantId, q.PlanId); e != nil {
				return e
			}
			// An explicit free-plan price-version refresh has no fixed fee to
			// collect. Preserve used allowance and add only a prorated increase.
			previous, e := postgres.CurrentAllowances(c.ctx, c.tx, q.TenantId)
			if e != nil {
				return e
			}
			if _, e = c.tx.Exec(c.ctx, "UPDATE entitlements SET ends_at=$2 WHERE tenant_id=$1 AND ends_at IS NULL AND starts_at<$2", q.TenantId, c.now); e != nil {
				return e
			}
			for _, price := range q.PriceIds {
				if _, e = c.tx.Exec(c.ctx, "INSERT INTO entitlements(id,tenant_id,price_id,starts_at) VALUES($1,$2,$3,$4)", ID(), q.TenantId, price, c.now); e != nil {
					return e
				}
			}
			if e = postgres.GrantAllowances(c.ctx, c.tx, q.TenantId, start, end, c.now, "free_refresh:"+q.RequestKey, previous); e != nil {
				return e
			}
			o.JobId, e = c.job(q.TenantId, "subscription", "request:"+q.RequestKey, map[string]any{"plan_id": q.PlanId, "price_ids": q.PriceIds, "amount": "0"})
			if e != nil {
				return e
			}
			if _, e = c.tx.Exec(c.ctx, "UPDATE jobs SET state='done' WHERE id=$1", o.JobId); e != nil {
				return e
			}
			return c.audit(q.TenantId, "subscription.free_refresh", "")
		}
		o.JobId, e = c.job(q.TenantId, "subscription", "request:"+q.RequestKey, map[string]any{"plan_id": q.PlanId, "price_ids": q.PriceIds, "external_price_ids": external, "timing": timing, "return_url": q.ReturnUrl})
		return e
	})
}
func (s *Server) CancelSubscription(ctx context.Context, r *connect.Request[pb.CancelSubscriptionRequest]) (*connect.Response[pb.CancelSubscriptionResponse], error) {
	o := &pb.CancelSubscriptionResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		if e := c.require(r.Msg.TenantId, pb.Role_ROLE_BILLING); e != nil {
			return e
		}
		var subscription string
		var pending bool
		if e := c.tx.QueryRow(c.ctx, "SELECT external_subscription_id,EXISTS(SELECT 1 FROM jobs WHERE tenant_id=t.id AND kind IN ('subscription','cancel') AND state IN ('pending','running','reconcile')) FROM tenants t WHERE id=$1", r.Msg.TenantId).Scan(&subscription, &pending); e != nil {
			return e
		}
		if subscription == "" || pending {
			return precondition("an active subscription without a pending change is required")
		}
		if _, e := c.tx.Exec(c.ctx, "UPDATE tenants SET auto_suspended=true WHERE id=$1", r.Msg.TenantId); e != nil {
			return e
		}
		if _, e := c.tx.Exec(c.ctx, "UPDATE jobs SET state='done',error_category='subscription_canceled',updated_at=$2 WHERE tenant_id=$1 AND "+unattemptedAutoTopUp, r.Msg.TenantId, c.now); e != nil {
			return e
		}
		id, e := c.job(r.Msg.TenantId, "cancel", "request:"+r.Msg.RequestKey, map[string]any{"effective_from": "next_billing_period"})
		o.JobId = id
		return e
	})
}
func (s *Server) DeleteTenant(ctx context.Context, r *connect.Request[pb.DeleteTenantRequest]) (*connect.Response[pb.DeleteTenantResponse], error) {
	o := &pb.DeleteTenantResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		if e := c.require(r.Msg.TenantId); e != nil {
			return e
		}
		if e := c.stopPendingSubscriptions(r.Msg.TenantId, ""); e != nil {
			return e
		}
		_, e := c.tx.Exec(c.ctx, "UPDATE tenants SET deleting_at=COALESCE(deleting_at,$2),auto_suspended=true WHERE id=$1", r.Msg.TenantId, c.now)
		if e != nil {
			return e
		}
		var subscription string
		if e = c.tx.QueryRow(c.ctx, "SELECT external_subscription_id FROM tenants WHERE id=$1", r.Msg.TenantId).Scan(&subscription); e != nil {
			return e
		}
		if subscription != "" {
			var pending bool
			if e = c.tx.QueryRow(c.ctx, "SELECT EXISTS(SELECT 1 FROM jobs WHERE tenant_id=$1 AND kind='cancel' AND state IN ('pending','running','reconcile'))", r.Msg.TenantId).Scan(&pending); e != nil {
				return e
			}
			if !pending {
				if _, e = c.job(r.Msg.TenantId, "cancel", "deletion_cancel:"+r.Msg.TenantId, map[string]string{"effective_from": "next_billing_period"}); e != nil {
					return e
				}
			}
		}
		return c.audit(r.Msg.TenantId, "tenant.delete", "")
	})
}
func (s *Server) Withdraw(ctx context.Context, r *connect.Request[pb.WithdrawRequest]) (*connect.Response[pb.WithdrawResponse], error) {
	o := &pb.WithdrawResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		if c.user == "" {
			return forbidden()
		}
		var owns bool
		if e := c.tx.QueryRow(c.ctx, "SELECT EXISTS(SELECT 1 FROM tenants WHERE owner_id=$1 AND NOT personal AND deleted_at IS NULL)", c.user).Scan(&owns); e != nil {
			return e
		}
		if owns {
			return precondition("transfer or delete owned organizations first")
		}
		if e := c.stopPendingSubscriptions("", c.user); e != nil {
			return e
		}
		if _, e := c.tx.Exec(c.ctx, "UPDATE users SET withdrawing_at=$2 WHERE id=$1", c.user, c.now); e != nil {
			return e
		}
		if _, e := c.tx.Exec(c.ctx, "UPDATE tenants SET deleting_at=COALESCE(deleting_at,$2),auto_suspended=true WHERE owner_id=$1 AND personal", c.user, c.now); e != nil {
			return e
		}
		rows, e := c.tx.Query(c.ctx, "SELECT id::text FROM tenants WHERE owner_id=$1 AND personal AND external_subscription_id<>'' AND NOT EXISTS(SELECT 1 FROM jobs WHERE tenant_id=tenants.id AND kind='cancel' AND state IN ('pending','running','reconcile'))", c.user)
		if e != nil {
			return e
		}
		var tenants []string
		for rows.Next() {
			var tenant string
			if e = rows.Scan(&tenant); e != nil {
				rows.Close()
				return e
			}
			tenants = append(tenants, tenant)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		for _, tenant := range tenants {
			if _, e = c.job(tenant, "cancel", "deletion_cancel:"+tenant, map[string]string{"effective_from": "next_billing_period"}); e != nil {
				return e
			}
		}
		if _, e := c.tx.Exec(c.ctx, "DELETE FROM members WHERE user_id=$1 AND tenant_id IN (SELECT id FROM tenants WHERE NOT personal)", c.user); e != nil {
			return e
		}
		return c.audit("", "user.withdraw", "")
	})
}
func (s *Server) SetTenantBlocked(ctx context.Context, r *connect.Request[pb.SetTenantBlockedRequest]) (*connect.Response[pb.SetTenantBlockedResponse], error) {
	o := &pb.SetTenantBlockedResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.operator(q.Reason); e != nil {
			return e
		}
		if !canonical(q.TenantId) {
			return invalid("invalid tenant ID")
		}
		tag, e := c.tx.Exec(c.ctx, "UPDATE tenants SET operator_block=$2 WHERE id=$1 AND deleted_at IS NULL", q.TenantId, q.Blocked)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return precondition("tenant unavailable")
		}
		return c.audit(q.TenantId, "tenant.block", q.Reason)
	})
}
func (s *Server) ListJobs(ctx context.Context, r *connect.Request[pb.ListJobsRequest]) (*connect.Response[pb.ListJobsResponse], error) {
	o := &pb.ListJobsResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		if !c.identity.Operator {
			return forbidden()
		}
		n, after, e := page(r.Msg.Page)
		if e != nil {
			return e
		}
		rows, e := c.tx.Query(c.ctx, "SELECT id::text,kind,state,error_category,attempts FROM jobs WHERE id>$1 ORDER BY id LIMIT $2", after, n+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		states := map[string]pb.JobState{"pending": pb.JobState_JOB_STATE_PENDING, "running": pb.JobState_JOB_STATE_RUNNING, "reconcile": pb.JobState_JOB_STATE_RECONCILE, "done": pb.JobState_JOB_STATE_DONE, "failed": pb.JobState_JOB_STATE_FAILED}
		for rows.Next() {
			v := &pb.Job{}
			var state string
			if e = rows.Scan(&v.Id, &v.Kind, &state, &v.ErrorCategory, &v.Attempts); e != nil {
				return e
			}
			v.State = states[state]
			o.Jobs = append(o.Jobs, v)
		}
		if len(o.Jobs) > n {
			o.Jobs = o.Jobs[:n]
			o.NextAfterId = o.Jobs[n-1].Id
		}
		return rows.Err()
	})
}
func (s *Server) RetryJob(ctx context.Context, r *connect.Request[pb.RetryJobRequest]) (*connect.Response[pb.RetryJobResponse], error) {
	o := &pb.RetryJobResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.operator(q.Reason); e != nil {
			return e
		}
		if !canonical(q.JobId) {
			return invalid("invalid job ID")
		}
		// A payment with an unknown result always returns to reconciliation, never to
		// a blind POST retry, even when an operator explicitly reprocesses the job.
		tag, e := c.tx.Exec(c.ctx, `UPDATE jobs SET state=CASE WHEN kind IN ('email','webhook') OR error_category='provider_rejected' THEN 'pending' ELSE 'reconcile' END,attempts=0,available_at=$2,updated_at=$2 WHERE id=$1 AND state='failed'`, q.JobId, c.now)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return precondition("job is not failed")
		}
		return c.audit("", "job.retry", q.Reason)
	})
}
func (s *Server) ListAudit(ctx context.Context, r *connect.Request[pb.ListAuditRequest]) (*connect.Response[pb.ListAuditResponse], error) {
	o := &pb.ListAuditResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		if !c.identity.Operator {
			return forbidden()
		}
		n, after, e := page(r.Msg.Page)
		if e != nil {
			return e
		}
		rows, e := c.tx.Query(c.ctx, "SELECT id::text,actor_id,action,reason,created_at FROM audit WHERE id>$1 ORDER BY id LIMIT $2", after, n+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			v := &pb.AuditEntry{}
			var at time.Time
			if e = rows.Scan(&v.Id, &v.ActorId, &v.Action, &v.Reason, &at); e != nil {
				return e
			}
			v.CreatedAt = timestamppb.New(at)
			o.Entries = append(o.Entries, v)
		}
		if len(o.Entries) > n {
			o.Entries = o.Entries[:n]
			o.NextAfterId = o.Entries[n-1].Id
		}
		return rows.Err()
	})
}

// unattemptedAutoTopUp selects automatic top-up jobs that never reached the
// provider: queued with no attempts and no external identity. A deferral inside
// the Paddle billing window returns the job to pending with its attempt count
// restored, so that category still marks unattempted work. An operator retry of a
// provider-rejected charge also resets the attempt count, but it keeps the
// provider_rejected category (the worker keeps it through a billing-window
// deferral), which marks a charge that was attempted before and must continue.
const unattemptedAutoTopUp = "kind='auto_topup' AND state='pending' AND attempts=0 AND external_id='' AND error_category IN ('','provider_billing_window')"

// A pending job with no attempts has never reached the provider, and a job the
// provider definitively rejected without creating anything has nothing left to
// reconcile. Both are stopped. All other incomplete subscription intents must be
// reconciled before deletion can start. A stopped rejected job keeps its
// provider_rejected category as failure evidence (including a rejected job that an
// operator already moved back to pending) and becomes done, so an operator retry
// cannot revive it. This runs under the same transaction lock as worker claims and
// payment events.
func (c *call) stopPendingSubscriptions(tenant, owner string) error {
	var uncertain bool
	if e := c.tx.QueryRow(c.ctx, `SELECT EXISTS(SELECT 1 FROM jobs j JOIN tenants t ON t.id=j.tenant_id WHERE (t.id::text=$1 OR (t.owner_id::text=$2 AND t.personal AND t.deleted_at IS NULL)) AND j.kind='subscription' AND j.state<>'done' AND NOT (j.state='pending' AND j.attempts=0 AND j.external_id='') AND NOT (j.state='failed' AND j.error_category='provider_rejected' AND j.external_id=''))`, tenant, owner).Scan(&uncertain); e != nil {
		return e
	}
	if uncertain {
		return precondition("reconcile the pending subscription before requesting deletion")
	}
	tag, e := c.tx.Exec(c.ctx, `UPDATE jobs j SET state='done',error_category=CASE WHEN j.error_category='provider_rejected' THEN j.error_category ELSE 'deletion_requested' END,updated_at=$3 FROM tenants t WHERE t.id=j.tenant_id AND (t.id::text=$1 OR (t.owner_id::text=$2 AND t.personal AND t.deleted_at IS NULL)) AND j.kind='subscription' AND j.external_id='' AND ((j.state='pending' AND j.attempts=0) OR (j.state='failed' AND j.error_category='provider_rejected'))`, tenant, owner, c.now)
	if e != nil {
		return e
	}
	if tag.RowsAffected() > 0 {
		c.s.Log.InfoContext(c.ctx, "subscription intents stopped for deletion", "correlation_id", c.correlation, "stopped_jobs", tag.RowsAffected())
	}
	return nil
}
