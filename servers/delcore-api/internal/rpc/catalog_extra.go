// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/delinoio/oss/protos/gen/go/delcore/v1"
)

func (s *Server) PutMeter(ctx context.Context, r *connect.Request[pb.PutMeterRequest]) (*connect.Response[pb.PutMeterResponse], error) {
	o := &pb.PutMeterResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.operator(q.Reason); e != nil {
			return e
		}
		m := q.Meter
		if m == nil || !canonical(m.ServiceId) {
			return invalid("invalid meter service")
		}
		if e := nonempty(m.Name, 100); e != nil {
			return e
		}
		var count int
		if e := c.tx.QueryRow(c.ctx, "SELECT count(*) FROM meters WHERE service_id=$1 AND name<>$2", m.ServiceId, m.Name).Scan(&count); e != nil {
			return e
		}
		if count >= 100 {
			return invalid("service meter limit reached")
		}
		_, e := c.tx.Exec(c.ctx, "INSERT INTO meters VALUES($1,$2) ON CONFLICT DO NOTHING", m.ServiceId, m.Name)
		if e != nil {
			return e
		}
		o.Meter = m
		return c.audit("", "catalog.meter", q.Reason)
	})
}
func (s *Server) PutAddOn(ctx context.Context, r *connect.Request[pb.PutAddOnRequest]) (*connect.Response[pb.PutAddOnResponse], error) {
	o := &pb.PutAddOnResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.operator(q.Reason); e != nil {
			return e
		}
		a := q.AddOn
		if a == nil || !canonical(a.ServiceId) {
			return invalid("invalid add-on service")
		}
		if e := nonempty(a.Name, 200); e != nil {
			return e
		}
		if a.Id == "" {
			a.Id = ID()
		} else if !canonical(a.Id) {
			return invalid("invalid add-on ID")
		}
		tag, e := c.tx.Exec(c.ctx, "INSERT INTO addons VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name WHERE addons.service_id=EXCLUDED.service_id", a.Id, a.ServiceId, a.Name)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return invalid("add-on service cannot change")
		}
		o.AddOn = a
		return c.audit("", "catalog.addon", q.Reason)
	})
}
func (s *Server) ListBillingHistory(ctx context.Context, r *connect.Request[pb.ListBillingHistoryRequest]) (*connect.Response[pb.ListBillingHistoryResponse], error) {
	o := &pb.ListBillingHistoryResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		if e := c.require(r.Msg.TenantId, pb.Role_ROLE_BILLING); e != nil {
			return e
		}
		n, after, e := page(r.Msg.Page)
		if e != nil {
			return e
		}
		rows, e := c.tx.Query(c.ctx, "SELECT id::text,kind,state,error_category,attempts,checkout_url FROM jobs WHERE tenant_id=$1 AND kind<>'email' AND id>$2 ORDER BY id LIMIT $3", r.Msg.TenantId, after, n+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		states := map[string]pb.JobState{"pending": pb.JobState_JOB_STATE_PENDING, "running": pb.JobState_JOB_STATE_RUNNING, "reconcile": pb.JobState_JOB_STATE_RECONCILE, "done": pb.JobState_JOB_STATE_DONE, "failed": pb.JobState_JOB_STATE_FAILED}
		for rows.Next() {
			j := &pb.Job{}
			var state string
			if e = rows.Scan(&j.Id, &j.Kind, &state, &j.ErrorCategory, &j.Attempts, &j.CheckoutUrl); e != nil {
				return e
			}
			j.State = states[state]
			o.Jobs = append(o.Jobs, j)
		}
		if len(o.Jobs) > n {
			o.Jobs = o.Jobs[:n]
			o.NextAfterId = o.Jobs[n-1].Id
		}
		return rows.Err()
	})
}
func (s *Server) DeleteGroup(ctx context.Context, r *connect.Request[pb.DeleteGroupRequest]) (*connect.Response[pb.DeleteGroupResponse], error) {
	o := &pb.DeleteGroupResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.require(q.TenantId, pb.Role_ROLE_ADMIN); e != nil {
			return e
		}
		if !canonical(q.GroupId) {
			return invalid("invalid group ID")
		}
		var exists bool
		if e := c.tx.QueryRow(c.ctx, "SELECT EXISTS(SELECT 1 FROM groups WHERE id=$1 AND tenant_id=$2)", q.GroupId, q.TenantId).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return forbidden()
		}
		if _, e := c.tx.Exec(c.ctx, "DELETE FROM group_members WHERE group_id=$1", q.GroupId); e != nil {
			return e
		}
		if _, e := c.tx.Exec(c.ctx, "DELETE FROM group_permissions WHERE group_id=$1", q.GroupId); e != nil {
			return e
		}
		if _, e := c.tx.Exec(c.ctx, "DELETE FROM groups WHERE id=$1", q.GroupId); e != nil {
			return e
		}
		return c.audit(q.TenantId, "group.delete", "")
	})
}
