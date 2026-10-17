// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/delinoio/oss/protos/gen/go/delcore/v1"
	"github.com/delinoio/oss/servers/delcore-api/internal/accounting"
)

func (s *Server) PutService(ctx context.Context, r *connect.Request[pb.PutServiceRequest]) (*connect.Response[pb.PutServiceResponse], error) {
	o := &pb.PutServiceResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.operator(q.Reason); e != nil {
			return e
		}
		v := q.Service
		if v == nil {
			return invalid("service required")
		}
		if e := nonempty(v.Name, 200); e != nil {
			return e
		}
		if e := nonempty(v.ClientId, 200); e != nil {
			return e
		}
		if len(v.Capabilities) == 0 || len(v.Capabilities) > 100 || len(v.Scopes) > 10 {
			return invalid("invalid service permissions")
		}
		for _, cap := range v.Capabilities {
			if e := nonempty(cap, 100); e != nil {
				return e
			}
		}
		for _, scope := range v.Scopes {
			if scope != "authorize" && scope != "usage:write" && scope != "usage:read" {
				return invalid("unknown service scope")
			}
		}
		if v.Id == "" {
			v.Id = ID()
		} else if !canonical(v.Id) {
			return invalid("invalid service ID")
		}
		_, e := c.tx.Exec(c.ctx, `INSERT INTO services VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,client_id=EXCLUDED.client_id,capabilities=EXCLUDED.capabilities,scopes=EXCLUDED.scopes`, v.Id, v.Name, v.ClientId, v.Capabilities, v.Scopes)
		if e != nil {
			return e
		}
		o.Service = v
		return c.audit("", "catalog.service", q.Reason)
	})
}
func (s *Server) PutPlan(ctx context.Context, r *connect.Request[pb.PutPlanRequest]) (*connect.Response[pb.PutPlanResponse], error) {
	o := &pb.PutPlanResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.operator(q.Reason); e != nil {
			return e
		}
		v := q.Plan
		if v == nil {
			return invalid("plan required")
		}
		if e := nonempty(v.Name, 200); e != nil {
			return e
		}
		if v.Rank < 0 || v.DefaultFree && !v.Free {
			return invalid("invalid plan")
		}
		if v.Id == "" {
			v.Id = ID()
		} else if !canonical(v.Id) {
			return invalid("invalid plan ID")
		}
		if v.DefaultFree {
			if _, e := c.tx.Exec(c.ctx, "UPDATE plans SET is_default=false WHERE is_default"); e != nil {
				return e
			}
		}
		tag, e := c.tx.Exec(c.ctx, `INSERT INTO plans VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,is_default=EXCLUDED.is_default WHERE plans.rank=EXCLUDED.rank AND plans.free=EXCLUDED.free AND plans.postpaid=EXCLUDED.postpaid`, v.Id, v.Name, v.Rank, v.Free, v.DefaultFree, v.Postpaid)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return precondition("plan billing policy is immutable; create a new plan")
		}
		o.Plan = v
		return c.audit("", "catalog.plan", q.Reason)
	})
}
func (s *Server) CreatePrice(ctx context.Context, r *connect.Request[pb.CreatePriceRequest]) (*connect.Response[pb.CreatePriceResponse], error) {
	o := &pb.CreatePriceResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.operator(q.Reason); e != nil {
			return e
		}
		v := q.Price
		if v == nil || ((!canonical(v.PlanId) || v.AddOnId != "") && (!canonical(v.AddOnId) || v.PlanId != "")) || !canonical(v.ServiceId) || v.Currency != c.s.Config.Currency {
			return invalid("invalid price references or currency")
		}
		for _, n := range []string{v.UnitPrice, v.Included} {
			d, e := accounting.Parse(n)
			if e != nil || len(n) > 64 || d.Cmp(accounting.Zero()) < 0 {
				return invalid("price and allowance must be nonnegative decimals")
			}
		}
		if e := nonempty(v.Meter, 100); e != nil {
			return e
		}
		v.Id = ID()
		var planID, addonID any
		if v.PlanId != "" {
			planID = v.PlanId
		}
		if v.AddOnId != "" {
			addonID = v.AddOnId
		}
		if v.Kind == pb.PriceKind_PRICE_KIND_UNSPECIFIED {
			v.Kind = pb.PriceKind_PRICE_KIND_USAGE
		}
		if v.Kind != pb.PriceKind_PRICE_KIND_USAGE && v.Kind != pb.PriceKind_PRICE_KIND_RECURRING {
			return invalid("unknown price kind")
		}
		if v.Kind == pb.PriceKind_PRICE_KIND_RECURRING && v.ExternalPriceId == "" {
			return invalid("recurring price requires Paddle mapping")
		}
		if v.Kind == pb.PriceKind_PRICE_KIND_USAGE {
			var exists bool
			if e := c.tx.QueryRow(c.ctx, "SELECT EXISTS(SELECT 1 FROM meters WHERE service_id=$1 AND name=$2)", v.ServiceId, v.Meter).Scan(&exists); e != nil {
				return e
			}
			if !exists {
				return invalid("unregistered meter")
			}
		}
		_, e := c.tx.Exec(c.ctx, `INSERT INTO prices(id,plan_id,service_id,meter,unit_price,included,currency,external_price_id,created_at,addon_id,kind) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, v.Id, planID, v.ServiceId, v.Meter, v.UnitPrice, v.Included, v.Currency, v.ExternalPriceId, c.now, addonID, int32(v.Kind))
		if e != nil {
			return e
		}
		o.Price = v
		return c.audit("", "catalog.price", q.Reason)
	})
}
func (s *Server) GetCatalog(ctx context.Context, r *connect.Request[pb.GetCatalogRequest]) (*connect.Response[pb.GetCatalogResponse], error) {
	o := &pb.GetCatalogResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		n, after, e := page(r.Msg.Page)
		if e != nil {
			return e
		}
		rows, e := c.tx.Query(c.ctx, "SELECT id::text,name,client_id,capabilities,scopes FROM services WHERE id>$1 ORDER BY id LIMIT $2", after, n+1)
		if e != nil {
			return e
		}
		for rows.Next() {
			v := &pb.Service{}
			if e = rows.Scan(&v.Id, &v.Name, &v.ClientId, &v.Capabilities, &v.Scopes); e != nil {
				rows.Close()
				return e
			}
			if !c.identity.Operator {
				v.ClientId = ""
				v.Scopes = nil
			}
			o.Services = append(o.Services, v)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		rows, e = c.tx.Query(c.ctx, "SELECT id::text,name,rank,free,is_default,postpaid FROM plans WHERE id>$1 ORDER BY id LIMIT $2", after, n+1)
		if e != nil {
			return e
		}
		for rows.Next() {
			v := &pb.Plan{}
			if e = rows.Scan(&v.Id, &v.Name, &v.Rank, &v.Free, &v.DefaultFree, &v.Postpaid); e != nil {
				rows.Close()
				return e
			}
			o.Plans = append(o.Plans, v)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		rows, e = c.tx.Query(c.ctx, "SELECT id::text,COALESCE(plan_id::text,''),service_id::text,meter,unit_price::text,included::text,currency,external_price_id,COALESCE(addon_id::text,''),kind FROM prices WHERE id>$1 ORDER BY id LIMIT $2", after, n+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			v := &pb.Price{}
			if e = rows.Scan(&v.Id, &v.PlanId, &v.ServiceId, &v.Meter, &v.UnitPrice, &v.Included, &v.Currency, &v.ExternalPriceId, &v.AddOnId, &v.Kind); e != nil {
				return e
			}
			o.Prices = append(o.Prices, v)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		rows, e = c.tx.Query(c.ctx, "SELECT id::text,service_id::text,name FROM addons WHERE id>$1 ORDER BY id LIMIT $2", after, n+1)
		if e != nil {
			return e
		}
		for rows.Next() {
			v := &pb.AddOn{}
			if e = rows.Scan(&v.Id, &v.ServiceId, &v.Name); e != nil {
				rows.Close()
				return e
			}
			o.AddOns = append(o.AddOns, v)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		// One cursor advances all catalog collections only to their minimum full boundary.
		boundary := ""
		if len(o.Services) > n {
			boundary = o.Services[n-1].Id
		}
		if len(o.Plans) > n && (boundary == "" || o.Plans[n-1].Id < boundary) {
			boundary = o.Plans[n-1].Id
		}
		if len(o.Prices) > n && (boundary == "" || o.Prices[n-1].Id < boundary) {
			boundary = o.Prices[n-1].Id
		}
		if len(o.AddOns) > n && (boundary == "" || o.AddOns[n-1].Id < boundary) {
			boundary = o.AddOns[n-1].Id
		}
		if boundary != "" {
			for len(o.AddOns) > 0 && o.AddOns[len(o.AddOns)-1].Id > boundary {
				o.AddOns = o.AddOns[:len(o.AddOns)-1]
			}
			for len(o.Services) > 0 && o.Services[len(o.Services)-1].Id > boundary {
				o.Services = o.Services[:len(o.Services)-1]
			}
			for len(o.Plans) > 0 && o.Plans[len(o.Plans)-1].Id > boundary {
				o.Plans = o.Plans[:len(o.Plans)-1]
			}
			for len(o.Prices) > 0 && o.Prices[len(o.Prices)-1].Id > boundary {
				o.Prices = o.Prices[:len(o.Prices)-1]
			}
			o.NextAfterId = boundary
		}
		// Meters travel with their service; service registration bounds each set.
		for _, service := range o.Services {
			rows, e = c.tx.Query(c.ctx, "SELECT service_id::text,name FROM meters WHERE service_id=$1 ORDER BY name", service.Id)
			if e != nil {
				return e
			}
			for rows.Next() {
				v := &pb.Meter{}
				if e = rows.Scan(&v.ServiceId, &v.Name); e != nil {
					rows.Close()
					return e
				}
				o.Meters = append(o.Meters, v)
			}
			rows.Close()
			if e = rows.Err(); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Server) SetGroup(ctx context.Context, r *connect.Request[pb.SetGroupRequest]) (*connect.Response[pb.SetGroupResponse], error) {
	o := &pb.SetGroupResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.require(q.TenantId, pb.Role_ROLE_ADMIN); e != nil {
			return e
		}
		t, e := c.tenant(q.TenantId)
		if e != nil {
			return e
		}
		if t.Personal || t.Deleting {
			return precondition("groups require an active organization")
		}
		g := q.Group
		if g == nil {
			return invalid("group required")
		}
		if e = nonempty(g.Name, 200); e != nil {
			return e
		}
		if len(g.UserIds) > 1000 || len(g.Permissions) > 1000 {
			return invalid("group is too large")
		}
		if g.Id == "" {
			g.Id = ID()
			_, e = c.tx.Exec(c.ctx, "INSERT INTO groups VALUES($1,$2,$3)", g.Id, q.TenantId, g.Name)
		} else {
			if !canonical(g.Id) {
				return invalid("invalid group ID")
			}
			tag, err := c.tx.Exec(c.ctx, "UPDATE groups SET name=$3 WHERE id=$1 AND tenant_id=$2", g.Id, q.TenantId, g.Name)
			e = err
			if e == nil && tag.RowsAffected() != 1 {
				return forbidden()
			}
		}
		if e != nil {
			return e
		}
		if _, e = c.tx.Exec(c.ctx, "DELETE FROM group_members WHERE group_id=$1", g.Id); e != nil {
			return e
		}
		if _, e = c.tx.Exec(c.ctx, "DELETE FROM group_permissions WHERE group_id=$1", g.Id); e != nil {
			return e
		}
		for _, user := range g.UserIds {
			if !canonical(user) {
				return invalid("invalid member ID")
			}
			if _, e = c.tx.Exec(c.ctx, "INSERT INTO group_members VALUES($1,$2,$3)", q.TenantId, g.Id, user); e != nil {
				return invalid("group contains a non-member or duplicate")
			}
		}
		for _, p := range g.Permissions {
			if p == nil || !canonical(p.ServiceId) {
				return invalid("invalid permission")
			}
			var ok bool
			if e = c.tx.QueryRow(c.ctx, "SELECT $2=ANY(capabilities) FROM services WHERE id=$1", p.ServiceId, p.Capability).Scan(&ok); e != nil || !ok {
				return invalid("unregistered capability")
			}
			if _, e = c.tx.Exec(c.ctx, "INSERT INTO group_permissions VALUES($1,$2,$3)", g.Id, p.ServiceId, p.Capability); e != nil {
				return invalid("duplicate permission")
			}
		}
		o.Group = g
		return c.audit(q.TenantId, "group.set", "")
	})
}
func (s *Server) ListGroups(ctx context.Context, r *connect.Request[pb.ListGroupsRequest]) (*connect.Response[pb.ListGroupsResponse], error) {
	o := &pb.ListGroupsResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		if e := c.member(r.Msg.TenantId); e != nil {
			return e
		}
		n, after, e := page(r.Msg.Page)
		if e != nil {
			return e
		}
		rows, e := c.tx.Query(c.ctx, "SELECT id::text,name FROM groups WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3", r.Msg.TenantId, after, n+1)
		if e != nil {
			return e
		}
		for rows.Next() {
			g := &pb.Group{}
			if e = rows.Scan(&g.Id, &g.Name); e != nil {
				rows.Close()
				return e
			}
			o.Groups = append(o.Groups, g)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		if len(o.Groups) > n {
			o.Groups = o.Groups[:n]
			o.NextAfterId = o.Groups[n-1].Id
		}
		for _, g := range o.Groups {
			rows, e = c.tx.Query(c.ctx, "SELECT user_id::text FROM group_members WHERE group_id=$1 ORDER BY user_id", g.Id)
			if e != nil {
				return e
			}
			for rows.Next() {
				var u string
				if e = rows.Scan(&u); e != nil {
					rows.Close()
					return e
				}
				g.UserIds = append(g.UserIds, u)
			}
			rows.Close()
			if e = rows.Err(); e != nil {
				return e
			}
			rows, e = c.tx.Query(c.ctx, "SELECT service_id::text,capability FROM group_permissions WHERE group_id=$1 ORDER BY service_id,capability", g.Id)
			if e != nil {
				return e
			}
			for rows.Next() {
				p := &pb.Permission{}
				if e = rows.Scan(&p.ServiceId, &p.Capability); e != nil {
					rows.Close()
					return e
				}
				g.Permissions = append(g.Permissions, p)
			}
			rows.Close()
			if e = rows.Err(); e != nil {
				return e
			}
		}
		return nil
	})
}
