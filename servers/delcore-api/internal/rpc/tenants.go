// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/delcore/v1"
)

func (c *call) tenant(id string) (*pb.Tenant, error) {
	t := &pb.Tenant{}
	e := c.tx.QueryRow(c.ctx, `SELECT t.id::text,t.name,t.personal,t.owner_id::text,t.plan_id::text,
 (t.operator_block OR t.renewal_failed OR EXISTS(SELECT 1 FROM disputes d WHERE d.tenant_id=t.id AND active)),
 t.deleting_at IS NOT NULL,COALESCE((SELECT sum(amount)::text FROM ledger WHERE tenant_id=t.id AND kind IN ('credit','usage','refund','correction')),'0')
 FROM tenants t WHERE t.id=$1 AND deleted_at IS NULL`, id).Scan(&t.Id, &t.Name, &t.Personal, &t.OwnerId, &t.PlanId, &t.Blocked, &t.Deleting, &t.CreditBalance)
	return t, e
}
func (c *call) createTenant(name string, personal bool) (*pb.Tenant, error) {
	if c.user == "" {
		return nil, forbidden()
	}
	if e := nonempty(name, 200); e != nil {
		return nil, e
	}
	var plan string
	if e := c.tx.QueryRow(c.ctx, "SELECT id::text FROM plans WHERE is_default AND free").Scan(&plan); e != nil {
		return nil, precondition("default free plan is not configured")
	}
	id := ID()
	start := time.Date(c.now.Year(), c.now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if _, e := c.tx.Exec(c.ctx, `INSERT INTO tenants(id,name,personal,owner_id,plan_id,period_start,period_end) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, name, personal, c.user, plan, start, start.AddDate(0, 1, 0)); e != nil {
		return nil, e
	}
	if _, e := c.tx.Exec(c.ctx, "INSERT INTO members VALUES($1,$2,ARRAY[1])", id, c.user); e != nil {
		return nil, e
	}
	rows, e := c.tx.Query(c.ctx, `SELECT DISTINCT ON(service_id,meter) id::text FROM prices WHERE plan_id=$1 ORDER BY service_id,meter,created_at DESC,id DESC`, plan)
	if e != nil {
		return nil, e
	}
	var priceIDs []string
	for rows.Next() {
		var price string
		if e = rows.Scan(&price); e != nil {
			rows.Close()
			return nil, e
		}
		priceIDs = append(priceIDs, price)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return nil, e
	}
	for _, price := range priceIDs {
		if _, e = c.tx.Exec(c.ctx, "INSERT INTO entitlements(id,tenant_id,price_id,starts_at) VALUES($1,$2,$3,$4)", ID(), id, price, c.now); e != nil {
			return nil, e
		}
	}
	if e := c.audit(id, "tenant.create", ""); e != nil {
		return nil, e
	}
	return c.tenant(id)
}
func (s *Server) GetCurrentUser(ctx context.Context, r *connect.Request[pb.GetCurrentUserRequest]) (*connect.Response[pb.GetCurrentUserResponse], error) {
	o := &pb.GetCurrentUserResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		if c.user == "" {
			return forbidden()
		}
		o.UserId = c.user
		var exists bool
		if e := c.tx.QueryRow(c.ctx, "SELECT EXISTS(SELECT 1 FROM tenants WHERE personal AND owner_id=$1 AND deleted_at IS NULL)", c.user).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			if _, e := c.createTenant("Personal", true); e != nil {
				return e
			}
		}
		rows, e := c.tx.Query(c.ctx, "SELECT tenant_id::text FROM members WHERE user_id=$1 ORDER BY tenant_id LIMIT 100", c.user)
		if e != nil {
			return e
		}
		var ids []string
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		for _, id := range ids {
			t, e := c.tenant(id)
			if e != nil {
				return e
			}
			o.Tenants = append(o.Tenants, t)
		}
		return nil
	})
}
func (s *Server) CreateTenant(ctx context.Context, r *connect.Request[pb.CreateTenantRequest]) (*connect.Response[pb.CreateTenantResponse], error) {
	o := &pb.CreateTenantResponse{}
	return run(s, ctx, r, o, func(c *call) error { t, e := c.createTenant(r.Msg.Name, false); o.Tenant = t; return e })
}
func (s *Server) GetTenant(ctx context.Context, r *connect.Request[pb.GetTenantRequest]) (*connect.Response[pb.GetTenantResponse], error) {
	o := &pb.GetTenantResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		if e := c.member(r.Msg.TenantId); e != nil {
			return e
		}
		t, e := c.tenant(r.Msg.TenantId)
		o.Tenant = t
		return e
	})
}
func (s *Server) ListMembers(ctx context.Context, r *connect.Request[pb.ListMembersRequest]) (*connect.Response[pb.ListMembersResponse], error) {
	o := &pb.ListMembersResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		if e := c.require(r.Msg.TenantId, pb.Role_ROLE_ADMIN); e != nil {
			return e
		}
		n, after, e := page(r.Msg.Page)
		if e != nil {
			return e
		}
		rows, e := c.tx.Query(c.ctx, "SELECT user_id::text,roles FROM members WHERE tenant_id=$1 AND user_id>$2 ORDER BY user_id LIMIT $3", r.Msg.TenantId, after, n+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			m := &pb.Member{}
			var roles []int32
			if e = rows.Scan(&m.UserId, &roles); e != nil {
				return e
			}
			for _, role := range roles {
				m.Roles = append(m.Roles, pb.Role(role))
			}
			o.Members = append(o.Members, m)
		}
		if len(o.Members) > n {
			o.Members = o.Members[:n]
			o.NextAfterId = o.Members[n-1].UserId
		}
		return rows.Err()
	})
}
func (c *call) grant(tenant string, roles []pb.Role) error {
	existing, e := c.roles(tenant)
	if e != nil {
		return e
	}
	if !has(existing, pb.Role_ROLE_OWNER, pb.Role_ROLE_ADMIN) {
		return forbidden()
	}
	if len(roles) == 0 || len(roles) > 3 {
		return invalid("one to three non-owner roles required")
	}
	seen := map[pb.Role]bool{}
	for _, role := range roles {
		if role < pb.Role_ROLE_ADMIN || role > pb.Role_ROLE_MEMBER || seen[role] {
			return invalid("invalid or repeated role")
		}
		seen[role] = true
		if role != pb.Role_ROLE_MEMBER && !has(existing, pb.Role_ROLE_OWNER) {
			return forbidden()
		}
	}
	return nil
}
func roleInts(roles []pb.Role) []int32 {
	out := make([]int32, len(roles))
	for i, r := range roles {
		out[i] = int32(r)
	}
	return out
}
func (s *Server) SetMemberRoles(ctx context.Context, r *connect.Request[pb.SetMemberRolesRequest]) (*connect.Response[pb.SetMemberRolesResponse], error) {
	o := &pb.SetMemberRolesResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.grant(q.TenantId, q.Roles); e != nil {
			return e
		}
		var current []int32
		if !canonical(q.UserId) {
			return invalid("invalid user ID")
		}
		if e := c.tx.QueryRow(c.ctx, "SELECT roles FROM members WHERE tenant_id=$1 AND user_id=$2", q.TenantId, q.UserId).Scan(&current); e != nil {
			return forbidden()
		}
		mine, e := c.roles(q.TenantId)
		if e != nil {
			return e
		}
		if has(current, pb.Role_ROLE_OWNER) || (!has(mine, pb.Role_ROLE_OWNER) && has(current, pb.Role_ROLE_ADMIN, pb.Role_ROLE_BILLING)) {
			return forbidden()
		}
		if _, e = c.tx.Exec(c.ctx, "UPDATE members SET roles=$3 WHERE tenant_id=$1 AND user_id=$2", q.TenantId, q.UserId, roleInts(q.Roles)); e != nil {
			return e
		}
		return c.audit(q.TenantId, "member.roles", "")
	})
}
func (s *Server) RemoveMember(ctx context.Context, r *connect.Request[pb.RemoveMemberRequest]) (*connect.Response[pb.RemoveMemberResponse], error) {
	o := &pb.RemoveMemberResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		mine, e := c.roles(q.TenantId)
		if e != nil {
			return e
		}
		if !canonical(q.UserId) {
			return invalid("invalid user ID")
		}
		var roles []int32
		if e = c.tx.QueryRow(c.ctx, "SELECT roles FROM members WHERE tenant_id=$1 AND user_id=$2", q.TenantId, q.UserId).Scan(&roles); e != nil {
			return forbidden()
		}
		if has(roles, pb.Role_ROLE_OWNER) {
			return precondition("transfer ownership before leaving")
		}
		if q.UserId != c.user && !has(mine, pb.Role_ROLE_OWNER) {
			if !has(mine, pb.Role_ROLE_ADMIN) || has(roles, pb.Role_ROLE_ADMIN, pb.Role_ROLE_BILLING) {
				return forbidden()
			}
		}
		if _, e = c.tx.Exec(c.ctx, "DELETE FROM members WHERE tenant_id=$1 AND user_id=$2", q.TenantId, q.UserId); e != nil {
			return e
		}
		return c.audit(q.TenantId, "member.remove", "")
	})
}
func tokenHash(t string) string { h := sha256.Sum256([]byte(t)); return hex.EncodeToString(h[:]) }
func (s *Server) CreateInvitation(ctx context.Context, r *connect.Request[pb.CreateInvitationRequest]) (*connect.Response[pb.CreateInvitationResponse], error) {
	o := &pb.CreateInvitationResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.grant(q.TenantId, q.Roles); e != nil {
			return e
		}
		t, e := c.tenant(q.TenantId)
		if e != nil {
			return e
		}
		if t.Personal || t.Deleting {
			return precondition("tenant cannot accept invitations")
		}
		addr, e := mail.ParseAddress(q.Email)
		if e != nil || addr.Address != q.Email || !c.s.Config.AllowsReturn(q.ReturnUrl) {
			return invalid("invalid recipient or return URL")
		}
		b := make([]byte, 32)
		if _, e = rand.Read(b); e != nil {
			return e
		}
		token := hex.EncodeToString(b)
		o.InvitationId = ID()
		if _, e = c.tx.Exec(c.ctx, "INSERT INTO invitations(id,tenant_id,email,token_hash,roles,expires_at) VALUES($1,$2,$3,$4,$5,$6)", o.InvitationId, q.TenantId, strings.ToLower(q.Email), tokenHash(token), roleInts(q.Roles), c.now.Add(time.Duration(c.s.Config.InvitationSeconds)*time.Second)); e != nil {
			return e
		}
		u, _ := url.Parse(q.ReturnUrl)
		v := u.Query()
		v.Set("invitation_token", token)
		u.RawQuery = v.Encode()
		_, e = c.job(q.TenantId, "email", "invitation:"+o.InvitationId, map[string]any{"to": q.Email, "subject": "Organization invitation", "text": "Accept your organization invitation: " + u.String()})
		if e != nil {
			return e
		}
		return c.audit(q.TenantId, "invitation.create", "")
	})
}
func (s *Server) AcceptInvitation(ctx context.Context, r *connect.Request[pb.AcceptInvitationRequest]) (*connect.Response[pb.AcceptInvitationResponse], error) {
	o := &pb.AcceptInvitationResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		if c.user == "" || len(r.Msg.Token) != 64 {
			return forbidden()
		}
		var id string
		var roles []int32
		e := c.tx.QueryRow(c.ctx, `SELECT i.id::text,i.tenant_id::text,i.roles FROM invitations i JOIN tenants t ON t.id=i.tenant_id WHERE token_hash=$1 AND email=$2 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at>$3 AND t.deleting_at IS NULL AND NOT t.personal`, tokenHash(r.Msg.Token), c.identity.Email, c.now).Scan(&id, &o.TenantId, &roles)
		if e != nil {
			return forbidden()
		}
		if _, e = c.tx.Exec(c.ctx, "INSERT INTO members VALUES($1,$2,$3) ON CONFLICT DO NOTHING", o.TenantId, c.user, roles); e != nil {
			return e
		}
		if _, e = c.tx.Exec(c.ctx, "UPDATE invitations SET accepted_at=$2 WHERE id=$1", id, c.now); e != nil {
			return e
		}
		return c.audit(o.TenantId, "invitation.accept", "")
	})
}
func (s *Server) RevokeInvitation(ctx context.Context, r *connect.Request[pb.RevokeInvitationRequest]) (*connect.Response[pb.RevokeInvitationResponse], error) {
	o := &pb.RevokeInvitationResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.require(q.TenantId, pb.Role_ROLE_ADMIN); e != nil {
			return e
		}
		if !canonical(q.InvitationId) {
			return invalid("invalid invitation ID")
		}
		_, e := c.tx.Exec(c.ctx, "UPDATE invitations SET revoked_at=$3 WHERE id=$1 AND tenant_id=$2 AND accepted_at IS NULL", q.InvitationId, q.TenantId, c.now)
		if e != nil {
			return e
		}
		return c.audit(q.TenantId, "invitation.revoke", "")
	})
}
func (s *Server) TransferOwnership(ctx context.Context, r *connect.Request[pb.TransferOwnershipRequest]) (*connect.Response[pb.TransferOwnershipResponse], error) {
	o := &pb.TransferOwnershipResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.require(q.TenantId); e != nil {
			return e
		}
		t, e := c.tenant(q.TenantId)
		if e != nil {
			return e
		}
		if t.Personal || t.Deleting || q.RecipientId == c.user || !canonical(q.RecipientId) {
			return precondition("invalid ownership recipient")
		}
		var exists bool
		if e = c.tx.QueryRow(c.ctx, "SELECT EXISTS(SELECT 1 FROM members WHERE tenant_id=$1 AND user_id=$2)", q.TenantId, q.RecipientId).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return forbidden()
		}
		o.TransferId = ID()
		_, e = c.tx.Exec(c.ctx, "INSERT INTO ownership_transfers(id,tenant_id,sender_id,recipient_id,expires_at) VALUES($1,$2,$3,$4,$5)", o.TransferId, q.TenantId, c.user, q.RecipientId, c.now.Add(time.Duration(c.s.Config.InvitationSeconds)*time.Second))
		if e != nil {
			return e
		}
		return c.audit(q.TenantId, "ownership.request", "")
	})
}
func (s *Server) AcceptOwnership(ctx context.Context, r *connect.Request[pb.AcceptOwnershipRequest]) (*connect.Response[pb.AcceptOwnershipResponse], error) {
	o := &pb.AcceptOwnershipResponse{}
	return run(s, ctx, r, o, func(c *call) error {
		q := r.Msg
		if e := c.member(q.TenantId); e != nil {
			return e
		}
		if !canonical(q.TransferId) {
			return invalid("invalid transfer ID")
		}
		var sender string
		e := c.tx.QueryRow(c.ctx, `SELECT x.sender_id::text FROM ownership_transfers x JOIN tenants t ON t.id=x.tenant_id WHERE x.id=$1 AND x.tenant_id=$2 AND x.recipient_id=$3 AND x.accepted_at IS NULL AND x.expires_at>$4 AND t.owner_id=x.sender_id AND t.deleting_at IS NULL`, q.TransferId, q.TenantId, c.user, c.now).Scan(&sender)
		if e != nil {
			return precondition("ownership transfer is no longer valid")
		}
		for _, step := range []struct {
			sql  string
			args []any
		}{{"UPDATE members SET roles=array_remove(roles,1) || ARRAY[4] WHERE tenant_id=$1 AND user_id=$2", []any{q.TenantId, sender}}, {"UPDATE members SET roles=ARRAY[1] WHERE tenant_id=$1 AND user_id=$2", []any{q.TenantId, c.user}}, {"UPDATE tenants SET owner_id=$2 WHERE id=$1", []any{q.TenantId, c.user}}, {"UPDATE ownership_transfers SET accepted_at=$2 WHERE id=$1", []any{q.TransferId, c.now}}} {
			if _, e = c.tx.Exec(c.ctx, step.sql, step.args...); e != nil {
				return e
			}
		}
		return c.audit(q.TenantId, "ownership.accept", "")
	})
}
