// SPDX-License-Identifier: Apache-2.0
package service

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	sessionauth "github.com/delinoio/oss/servers/tairouter/internal/auth"
	"time"
)

func keyMessage(k store.Key, org string) *v1.InferenceKey {
	state := v1.KeyState_KEY_STATE_ACTIVE
	if k.Revoked {
		state = v1.KeyState_KEY_STATE_REVOKED
	} else if k.ExpiresAt != nil && !k.ExpiresAt.After(time.Now()) {
		state = v1.KeyState_KEY_STATE_EXPIRED
	}
	out := &v1.InferenceKey{CreatedAt: timestamp(k.CreatedAt), Prefix: k.Prefix, Id: k.ID, OrganizationId: org, Name: k.Name, State: state, Kind: v1.KeyKind_KEY_KIND_PERSONAL}
	if k.Device {
		out.Kind = v1.KeyKind_KEY_KIND_CLI
	}
	if k.LastUsedAt != nil {
		out.LastUsedAt = timestamp(*k.LastUsedAt)
	}
	if k.ExpiresAt != nil {
		out.ExpiresAt = timestamp(*k.ExpiresAt)
	}
	return out
}
func (m *management) CreateKey(ctx context.Context, r *connect.Request[v1.CreateKeyRequest]) (*connect.Response[v1.CreateKeyResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	expires, e := expiry(r.Msg.ExpiresAt)
	if e != nil {
		return nil, e
	}
	if r.Msg.Name == "" || len(r.Msg.Name) > 200 {
		return nil, invalid("key name must contain 1 to 200 bytes")
	}
	k, raw, e := m.store.IssueKey(ctx, sc, r.Msg.Name, expires)
	return connect.NewResponse(&v1.CreateKeyResponse{Key: keyMessage(k, sc.OrganizationID), RawKey: raw}), rpcError(e)
}
func (m *management) ListKeys(ctx context.Context, r *connect.Request[v1.ListKeysRequest]) (*connect.Response[v1.ListKeysResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	keys, e := m.store.ListKeys(ctx, sc)
	if e != nil {
		return nil, rpcError(e)
	}
	out := &v1.ListKeysResponse{}
	for _, k := range keys {
		out.Keys = append(out.Keys, keyMessage(k, sc.OrganizationID))
	}
	return connect.NewResponse(out), nil
}
func (m *management) RevokeKey(ctx context.Context, r *connect.Request[v1.RevokeKeyRequest]) (*connect.Response[v1.RevokeKeyResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&v1.RevokeKeyResponse{}), rpcError(m.store.RevokeKey(ctx, sc, r.Msg.KeyId))
}
func (m *management) BeginDeviceAuthorization(ctx context.Context, r *connect.Request[v1.BeginDeviceAuthorizationRequest]) (*connect.Response[v1.BeginDeviceAuthorizationResponse], error) {
	if r.Msg.DeviceName == "" || len(r.Msg.DeviceName) > 200 {
		return nil, invalid("device name must contain 1 to 200 bytes")
	}
	d, e := m.store.StartDevice(ctx, r.Msg.DeviceName, m.auth.PublicURL())
	return connect.NewResponse(&v1.BeginDeviceAuthorizationResponse{AuthorizationId: d.ID, DeviceSecret: d.Secret, UserCode: d.Code, VerificationUrl: m.auth.PublicURL() + "/device", ExpiresAt: timestamp(d.ExpiresAt), PollIntervalSeconds: 5}), rpcError(e)
}
func (m *management) PollDeviceAuthorization(ctx context.Context, r *connect.Request[v1.PollDeviceAuthorizationRequest]) (*connect.Response[v1.PollDeviceAuthorizationResponse], error) {
	d, e := m.store.PollDevice(ctx, r.Msg.AuthorizationId, r.Msg.DeviceSecret)
	out := &v1.PollDeviceAuthorizationResponse{}
	switch {
	case errors.Is(e, store.ErrPending):
		out.State = v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_PENDING
	case errors.Is(e, store.ErrExpired):
		out.State = v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_EXPIRED
	case d.Status == "denied" && (e == nil || errors.Is(e, store.ErrForbidden)):
		out.State = v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_DENIED
	case e != nil:
		return nil, deviceError(e)
	case d.Status == "revoked":
		out.State = v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_REVOKED
	case d.Status == "confirmed":
		out.State = v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_DELIVERED
	case d.Status == "approved":
		out.State = v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_APPROVED
		out.Result = &v1.DeviceKeyResult{KeyId: d.KeyID, RawKey: d.RawKey, OrganizationId: d.OrganizationID, RevocationUrl: m.auth.PublicURL() + "/keys"}
	default:
		return nil, deviceError(store.ErrForbidden)
	}
	return connect.NewResponse(out), nil
}
func deviceError(e error) error {
	if errors.Is(e, store.ErrNotFound) || errors.Is(e, store.ErrForbidden) {
		return rpcError(sessionauth.ErrAuthentication)
	}
	return rpcError(e)
}
func (m *management) ConfirmDeviceStorage(ctx context.Context, r *connect.Request[v1.ConfirmDeviceStorageRequest]) (*connect.Response[v1.ConfirmDeviceStorageResponse], error) {
	e := m.store.ConfirmDeviceStorage(ctx, r.Msg.AuthorizationId, r.Msg.DeviceSecret)
	return connect.NewResponse(&v1.ConfirmDeviceStorageResponse{}), deviceError(e)
}
func (m *management) RevokeDeviceKey(ctx context.Context, r *connect.Request[v1.RevokeDeviceKeyRequest]) (*connect.Response[v1.RevokeDeviceKeyResponse], error) {
	return connect.NewResponse(&v1.RevokeDeviceKeyResponse{}), deviceError(m.store.AbortDevice(ctx, r.Msg.AuthorizationId, r.Msg.DeviceSecret))
}
func deviceMessage(d store.Device) *v1.DeviceAuthorization {
	state := v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_PENDING
	switch d.State {
	case store.DeviceApproved:
		state = v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_APPROVED
	case store.DeviceDelivered:
		state = v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_DELIVERED
	case store.DeviceDenied:
		state = v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_DENIED
	case store.DeviceExpired:
		state = v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_EXPIRED
	case store.DeviceRevoked:
		state = v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_REVOKED
	}
	out := &v1.DeviceAuthorization{AuthorizationId: d.ID, DeviceName: d.Name, ServerAddress: d.Server, ExpiresAt: timestamp(d.ExpiresAt), State: state, OrganizationId: d.OrganizationID}
	if d.KeyExpiresAt != nil {
		out.KeyExpiresAt = timestamp(*d.KeyExpiresAt)
	}
	return out
}

func (m *management) GetDeviceAuthorization(ctx context.Context, r *connect.Request[v1.GetDeviceAuthorizationRequest]) (*connect.Response[v1.GetDeviceAuthorizationResponse], error) {
	d, e := m.store.InspectDeviceForUser(ctx, identity(ctx).UserID, r.Msg.UserCode)
	return connect.NewResponse(&v1.GetDeviceAuthorizationResponse{Authorization: deviceMessage(d)}), rpcError(e)
}
func (m *management) ApproveDeviceAuthorization(ctx context.Context, r *connect.Request[v1.ApproveDeviceAuthorizationRequest]) (*connect.Response[v1.ApproveDeviceAuthorizationResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	expires, e := expiry(r.Msg.KeyExpiresAt)
	if e != nil {
		return nil, e
	}
	d, e := m.store.InspectDeviceForUser(ctx, identity(ctx).UserID, r.Msg.UserCode)
	if e != nil {
		return nil, rpcError(e)
	}
	_, e = m.store.ApproveDevice(ctx, sc, r.Msg.UserCode, expires)
	out := deviceMessage(d)
	out.OrganizationId = sc.OrganizationID
	out.KeyExpiresAt = r.Msg.KeyExpiresAt
	out.State = v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_APPROVED
	return connect.NewResponse(&v1.ApproveDeviceAuthorizationResponse{Authorization: out}), rpcError(e)
}
func (m *management) DenyDeviceAuthorization(ctx context.Context, r *connect.Request[v1.DenyDeviceAuthorizationRequest]) (*connect.Response[v1.DenyDeviceAuthorizationResponse], error) {
	return connect.NewResponse(&v1.DenyDeviceAuthorizationResponse{}), rpcError(m.store.DenyDeviceForUser(ctx, identity(ctx).UserID, r.Msg.UserCode))
}
func (m *management) RevokeCLIKey(ctx context.Context, r *connect.Request[v1.RevokeCLIKeyRequest]) (*connect.Response[v1.RevokeCLIKeyResponse], error) {
	raw, _ := ctx.Value(bearerKey{}).(string)
	return connect.NewResponse(&v1.RevokeCLIKeyResponse{}), deviceError(m.store.RevokeOwnKey(ctx, raw, r.Msg.KeyId))
}
func (m *management) ListClientModels(ctx context.Context, r *connect.Request[v1.ListClientModelsRequest]) (*connect.Response[v1.ListClientModelsResponse], error) {
	raw, _ := ctx.Value(bearerKey{}).(string)
	p, e := m.store.AuthenticateKey(ctx, raw)
	if e != nil {
		return nil, deviceError(e)
	}
	models, e := m.inference.clientModels(ctx, p.Scope)
	if e != nil {
		return nil, rpcError(e)
	}
	if _, e = m.store.AuthenticateKey(ctx, raw); e != nil {
		return nil, deviceError(e)
	}
	return connect.NewResponse(&v1.ListClientModelsResponse{Models: models}), nil
}
