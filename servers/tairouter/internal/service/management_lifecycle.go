// SPDX-License-Identifier: Apache-2.0
package service

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	"net/mail"
	"time"
)

func invitation(i store.Invitation) *v1.Invitation {
	state := v1.LifecycleState_LIFECYCLE_STATE_PENDING
	delivery := v1.DeliveryState_DELIVERY_STATE_QUEUED
	switch i.DeliveryState {
	case "sent":
		delivery = v1.DeliveryState_DELIVERY_STATE_SENT
	case "retry_pending":
		delivery = v1.DeliveryState_DELIVERY_STATE_RETRY_PENDING
	case "failed":
		delivery = v1.DeliveryState_DELIVERY_STATE_FAILED
	}
	switch i.Status {
	case "expired":
		state = v1.LifecycleState_LIFECYCLE_STATE_EXPIRED
	case "superseded":
		state = v1.LifecycleState_LIFECYCLE_STATE_SUPERSEDED
	case "accepted":
		state = v1.LifecycleState_LIFECYCLE_STATE_ACCEPTED
	case "canceled":
		state = v1.LifecycleState_LIFECYCLE_STATE_CANCELED
	case "failed":
		state = v1.LifecycleState_LIFECYCLE_STATE_FAILED
		delivery = v1.DeliveryState_DELIVERY_STATE_FAILED
	}
	return &v1.Invitation{OrganizationName: i.OrganizationName, Role: v1.Role_ROLE_MEMBER, CreatedAt: timestamp(i.CreatedAt), NextAttemptAt: timestamp(i.NextAttemptAt), ResendAvailableAt: timestamp(i.CreatedAt.Add(time.Minute)), Id: i.ID, OrganizationId: i.OrganizationID, Email: i.Email, State: state, DeliveryState: delivery, ExpiresAt: timestamp(i.ExpiresAt), DeliveryAttempts: uint32(i.Attempts)}
}
func (m *management) CreateInvitation(ctx context.Context, r *connect.Request[v1.CreateInvitationRequest]) (*connect.Response[v1.CreateInvitationResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	address, e := mail.ParseAddress(r.Msg.Email)
	if e != nil || address.Address != r.Msg.Email {
		return nil, invalid("email address required")
	}
	i, e := m.store.Invite(ctx, sc, r.Msg.Email)
	return connect.NewResponse(&v1.CreateInvitationResponse{Invitation: invitation(i)}), rpcError(e)
}
func (m *management) ListInvitations(ctx context.Context, r *connect.Request[v1.ListInvitationsRequest]) (*connect.Response[v1.ListInvitationsResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	list, e := m.store.ListInvitations(ctx, sc)
	if e != nil {
		return nil, rpcError(e)
	}
	out := &v1.ListInvitationsResponse{}
	for _, i := range list {
		out.Invitations = append(out.Invitations, invitation(i))
	}
	return connect.NewResponse(out), nil
}
func (m *management) ResendInvitation(ctx context.Context, r *connect.Request[v1.ResendInvitationRequest]) (*connect.Response[v1.ResendInvitationResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	i, e := m.store.ResendInvitation(ctx, sc, r.Msg.InvitationId)
	return connect.NewResponse(&v1.ResendInvitationResponse{Invitation: invitation(i)}), rpcError(e)
}
func (m *management) CancelInvitation(ctx context.Context, r *connect.Request[v1.CancelInvitationRequest]) (*connect.Response[v1.CancelInvitationResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&v1.CancelInvitationResponse{}), rpcError(m.store.CancelInvite(ctx, sc, r.Msg.InvitationId))
}
func (m *management) AcceptInvitation(ctx context.Context, r *connect.Request[v1.AcceptInvitationRequest]) (*connect.Response[v1.AcceptInvitationResponse], error) {
	if (r.Msg.Token == "") == (r.Msg.GetInvitationId() == "") {
		return nil, invalid("exactly one invitation selector required")
	}
	var sc core.Scope
	var e error
	if r.Msg.GetInvitationId() != "" {
		sc, e = m.store.AcceptInviteID(ctx, identity(ctx).UserID, r.Msg.GetInvitationId())
	} else {
		sc, e = m.store.AcceptInvite(ctx, identity(ctx).UserID, r.Msg.Token)
	}
	if e != nil {
		return nil, invitationError(e)
	}
	o, e := m.store.GetOrganization(ctx, sc)
	return connect.NewResponse(&v1.AcceptInvitationResponse{Organization: organization(o)}), rpcError(e)
}
func transfer(t store.Transfer) *v1.OwnershipTransfer {
	return &v1.OwnershipTransfer{Id: t.ID, OrganizationId: t.OrganizationID, FromUserId: t.FromUserID, ToUserId: t.ToUserID, ExpiresAt: timestamp(t.ExpiresAt), State: v1.LifecycleState_LIFECYCLE_STATE_PENDING}
}
func (m *management) GetOwnershipTransfer(ctx context.Context, r *connect.Request[v1.GetOwnershipTransferRequest]) (*connect.Response[v1.GetOwnershipTransferResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	t, e := m.store.GetTransfer(ctx, sc)
	return connect.NewResponse(&v1.GetOwnershipTransferResponse{Transfer: transfer(t)}), rpcError(e)
}
func (m *management) NominateOwner(ctx context.Context, r *connect.Request[v1.NominateOwnerRequest]) (*connect.Response[v1.NominateOwnerResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	if e = m.store.RequestTransfer(ctx, sc, r.Msg.UserId); e != nil {
		return nil, rpcError(e)
	}
	t, e := m.store.GetTransfer(ctx, sc)
	return connect.NewResponse(&v1.NominateOwnerResponse{Transfer: transfer(t)}), rpcError(e)
}
func (m *management) AcceptOwnership(ctx context.Context, r *connect.Request[v1.AcceptOwnershipRequest]) (*connect.Response[v1.AcceptOwnershipResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	if e = m.store.AcceptTransferID(ctx, sc, r.Msg.TransferId); e != nil {
		return nil, rpcError(e)
	}
	o, e := m.store.GetOrganization(ctx, sc)
	return connect.NewResponse(&v1.AcceptOwnershipResponse{Organization: organization(o)}), rpcError(e)
}
func (m *management) CancelOwnershipTransfer(ctx context.Context, r *connect.Request[v1.CancelOwnershipTransferRequest]) (*connect.Response[v1.CancelOwnershipTransferResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&v1.CancelOwnershipTransferResponse{}), rpcError(m.store.CancelTransferID(ctx, sc, r.Msg.TransferId))
}

func invitationError(err error) error {
	if errors.Is(err, store.ErrInvitationEmailMismatch) {
		e := connect.NewError(connect.CodePermissionDenied, errors.New("invitation email mismatch"))
		d, _ := connect.NewErrorDetail(&v1.ErrorDetail{Reason: v1.ErrorReason_ERROR_REASON_INVITATION_EMAIL_MISMATCH})
		e.AddDetail(d)
		return e
	}
	return rpcError(err)
}
func (m *management) GetInvitation(ctx context.Context, r *connect.Request[v1.GetInvitationRequest]) (*connect.Response[v1.GetInvitationResponse], error) {
	if r.Msg.Token == "" {
		return nil, invalid("invitation token required")
	}
	i, e := m.store.GetInvitationForUser(ctx, identity(ctx).UserID, r.Msg.Token)
	if e != nil {
		return nil, invitationError(e)
	}
	return connect.NewResponse(&v1.GetInvitationResponse{Invitation: invitation(i)}), nil
}
func (m *management) ListMyInvitations(ctx context.Context, r *connect.Request[v1.ListMyInvitationsRequest]) (*connect.Response[v1.ListMyInvitationsResponse], error) {
	list, next, e := m.store.ListMyInvitations(ctx, identity(ctx).UserID, int(r.Msg.Page.GetPageSize()), r.Msg.Page.GetPageToken())
	if e != nil {
		return nil, rpcError(e)
	}
	out := &v1.ListMyInvitationsResponse{Page: &v1.PageResponse{NextPageToken: next}}
	for _, i := range list {
		out.Invitations = append(out.Invitations, invitation(i))
	}
	return connect.NewResponse(out), nil
}
