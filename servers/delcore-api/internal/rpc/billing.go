// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/delinoio/oss/protos/gen/go/delcore/v1"
	"github.com/delinoio/oss/servers/delcore-api/internal/accounting"
	"google.golang.org/protobuf/types/known/timestamppb"
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
		if e != nil || d.Cmp(accounting.Zero()) <= 0 || !c.s.Config.AllowsReturn(q.ReturnUrl) {
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
			return precondition("tenant is deleting")
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
			_, e := c.tx.Exec(c.ctx, "UPDATE tenants SET auto_threshold=NULL,auto_amount=NULL,auto_cap=NULL WHERE id=$1", q.TenantId)
			return e
		}
		threshold, e1 := accounting.Parse(q.Threshold)
		amount, e2 := accounting.Parse(q.Amount)
		cap, e3 := accounting.Parse(q.MonthlyCap)
		if e1 != nil || e2 != nil || e3 != nil || threshold.Cmp(accounting.Zero()) < 0 || amount.Cmp(accounting.Zero()) <= 0 || cap.Cmp(amount) < 0 {
			return invalid("invalid automatic top-up settings")
		}
		_, e := c.tx.Exec(c.ctx, "UPDATE tenants SET auto_threshold=$2,auto_amount=$3,auto_cap=$4,auto_suspended=false WHERE id=$1 AND deleting_at IS NULL", q.TenantId, threshold.String(), amount.String(), cap.String())
		if e != nil {
			return e
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
		external := []string{}
		seen := map[string]bool{}
		for _, id := range q.PriceIds {
			if !canonical(id) || seen[id] {
				return invalid("invalid or repeated price")
			}
			seen[id] = true
			var ext string
			if e = c.tx.QueryRow(c.ctx, "SELECT external_price_id FROM prices WHERE id=$1 AND plan_id=$2", id, q.PlanId).Scan(&ext); e != nil {
				return invalid("price does not belong to plan")
			}
			if !free && ext == "" {
				return precondition("Paddle price mapping is required")
			}
			external = append(external, ext)
		}
		timing := "prorated_immediately"
		if newRank < oldRank {
			timing = "next_billing_period"
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
		_, e := c.tx.Exec(c.ctx, "UPDATE tenants SET deleting_at=COALESCE(deleting_at,$2),auto_suspended=true WHERE id=$1", r.Msg.TenantId, c.now)
		if e != nil {
			return e
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
		if _, e := c.tx.Exec(c.ctx, "UPDATE users SET withdrawing_at=$2 WHERE id=$1", c.user, c.now); e != nil {
			return e
		}
		if _, e := c.tx.Exec(c.ctx, "UPDATE tenants SET deleting_at=COALESCE(deleting_at,$2),auto_suspended=true WHERE owner_id=$1 AND personal", c.user, c.now); e != nil {
			return e
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
		tag, e := c.tx.Exec(c.ctx, `UPDATE jobs SET state=CASE WHEN kind='email' THEN 'pending' ELSE 'reconcile' END,attempts=0,available_at=$2,updated_at=$2 WHERE id=$1 AND state='failed'`, q.JobId, c.now)
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
