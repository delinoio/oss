// SPDX-License-Identifier: Apache-2.0
package service

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *management) GetHostedSettings(ctx context.Context, r *connect.Request[v1.GetHostedSettingsRequest]) (*connect.Response[v1.GetHostedSettingsResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	out := &v1.HostedSettings{}
	data, e := m.store.GetPrivate(ctx, sc, "hosted-helpers", "settings")
	if errors.Is(e, store.ErrNotFound) {
		return connect.NewResponse(&v1.GetHostedSettingsResponse{Settings: out}), nil
	}
	if e != nil {
		return nil, rpcError(e)
	}
	if e = protojson.Unmarshal(data, out); e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&v1.GetHostedSettingsResponse{Settings: out}), nil
}
func (m *management) UpdateHostedSettings(ctx context.Context, r *connect.Request[v1.UpdateHostedSettingsRequest]) (*connect.Response[v1.UpdateHostedSettingsResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	if r.Msg.Settings == nil {
		return nil, invalid("hosted settings required")
	}
	if e = validateHostedSettings(ctx, m.store, sc, r.Msg.Settings); e != nil {
		return nil, rpcError(e)
	}
	data, e := protojson.Marshal(r.Msg.Settings)
	if e != nil {
		return nil, invalid("invalid hosted settings")
	}
	if e = m.store.PutPrivate(ctx, sc, "hosted-helpers", "settings", data); e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&v1.UpdateHostedSettingsResponse{Settings: r.Msg.Settings}), nil
}
