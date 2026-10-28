// SPDX-License-Identifier: Apache-2.0
package service

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

func organization(o store.Organization) *v1.Organization {
	return &v1.Organization{Id: o.ID, Name: o.Name, Role: role(o.Role)}
}
func (m *management) GetSession(ctx context.Context, r *connect.Request[v1.GetSessionRequest]) (*connect.Response[v1.GetSessionResponse], error) {
	i := identity(ctx)
	return connect.NewResponse(&v1.GetSessionResponse{UserId: i.UserID, Email: i.Email, IsInstanceOperator: m.auth.IsOperator(i)}), nil
}
func (m *management) Logout(ctx context.Context, r *connect.Request[v1.LogoutRequest]) (*connect.Response[v1.LogoutResponse], error) {
	raw, _ := ctx.Value(sessionKey{}).(string)
	e := m.store.Logout(ctx, raw)
	return connect.NewResponse(&v1.LogoutResponse{}), rpcError(e)
}
func (m *management) DeleteAccount(ctx context.Context, r *connect.Request[v1.DeleteAccountRequest]) (*connect.Response[v1.DeleteAccountResponse], error) {
	e := m.store.DeleteAccount(ctx, identity(ctx).UserID)
	return connect.NewResponse(&v1.DeleteAccountResponse{}), rpcError(e)
}
func (m *management) CreateOrganization(ctx context.Context, r *connect.Request[v1.CreateOrganizationRequest]) (*connect.Response[v1.CreateOrganizationResponse], error) {
	if r.Msg.Name == "" || len(r.Msg.Name) > 200 {
		return nil, invalid("name must contain 1 to 200 bytes")
	}
	o, e := m.store.CreateOrganization(ctx, identity(ctx).UserID, r.Msg.Name)
	return connect.NewResponse(&v1.CreateOrganizationResponse{Organization: organization(o)}), rpcError(e)
}
func (m *management) ListOrganizations(ctx context.Context, r *connect.Request[v1.ListOrganizationsRequest]) (*connect.Response[v1.ListOrganizationsResponse], error) {
	list, e := m.store.ListOrganizations(ctx, identity(ctx).UserID)
	if e != nil {
		return nil, rpcError(e)
	}
	out := &v1.ListOrganizationsResponse{}
	for _, o := range list {
		out.Organizations = append(out.Organizations, organization(o))
	}
	return connect.NewResponse(out), nil
}
func (m *management) GetOrganization(ctx context.Context, r *connect.Request[v1.GetOrganizationRequest]) (*connect.Response[v1.GetOrganizationResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	o, e := m.store.GetOrganization(ctx, sc)
	return connect.NewResponse(&v1.GetOrganizationResponse{Organization: organization(o)}), rpcError(e)
}
func (m *management) UpdateOrganization(ctx context.Context, r *connect.Request[v1.UpdateOrganizationRequest]) (*connect.Response[v1.UpdateOrganizationResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	if r.Msg.Name == "" || len(r.Msg.Name) > 200 {
		return nil, invalid("name must contain 1 to 200 bytes")
	}
	o, e := m.store.UpdateOrganization(ctx, sc, r.Msg.Name)
	return connect.NewResponse(&v1.UpdateOrganizationResponse{Organization: organization(o)}), rpcError(e)
}
func (m *management) DeleteOrganization(ctx context.Context, r *connect.Request[v1.DeleteOrganizationRequest]) (*connect.Response[v1.DeleteOrganizationResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&v1.DeleteOrganizationResponse{}), rpcError(m.store.DeleteOrganization(ctx, sc))
}
func (m *management) LeaveOrganization(ctx context.Context, r *connect.Request[v1.LeaveOrganizationRequest]) (*connect.Response[v1.LeaveOrganizationResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&v1.LeaveOrganizationResponse{}), rpcError(m.store.RemoveMember(ctx, sc, sc.UserID))
}
func (m *management) ListMembers(ctx context.Context, r *connect.Request[v1.ListMembersRequest]) (*connect.Response[v1.ListMembersResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	members, e := m.store.ListMembers(ctx, sc)
	if e != nil {
		return nil, rpcError(e)
	}
	out := &v1.ListMembersResponse{}
	for _, u := range members {
		out.Members = append(out.Members, &v1.Member{UserId: u.UserID, Email: u.Email, Role: role(u.Role)})
	}
	return connect.NewResponse(out), nil
}
func (m *management) SetMemberRole(ctx context.Context, r *connect.Request[v1.SetMemberRoleRequest]) (*connect.Response[v1.SetMemberRoleResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	var target core.Role
	switch r.Msg.Role {
	case v1.Role_ROLE_ADMINISTRATOR:
		target = core.Administrator
	case v1.Role_ROLE_MEMBER:
		target = core.Member
	default:
		return nil, invalid("role must be member or administrator")
	}
	e = m.store.ChangeRole(ctx, sc, r.Msg.UserId, target)
	return connect.NewResponse(&v1.SetMemberRoleResponse{Member: &v1.Member{UserId: r.Msg.UserId, Role: r.Msg.Role}}), rpcError(e)
}
func (m *management) RemoveMember(ctx context.Context, r *connect.Request[v1.RemoveMemberRequest]) (*connect.Response[v1.RemoveMemberResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&v1.RemoveMemberResponse{}), rpcError(m.store.RemoveMember(ctx, sc, r.Msg.UserId))
}
