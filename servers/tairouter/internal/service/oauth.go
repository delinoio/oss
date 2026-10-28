// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/providers"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

// Both the verifier and the presentation state live in encrypted temporary storage.
// Only the explicitly safe presentation fields cross the management boundary.
type providerOAuthFlow struct {
	Flow       providers.Flow
	Challenge  providers.Challenge
	Connection providerData
}

func providerRPCError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, providers.ErrPending), errors.Is(err, store.ErrBusy):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("provider authorization is pending"))
	case errors.Is(err, providers.ErrReauthenticate), errors.Is(err, store.ErrReauthentication):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("provider requires a new login"))
	case errors.Is(err, providers.ErrFlow), errors.Is(err, providers.ErrConnection), errors.Is(err, providers.ErrUnsupported):
		return invalid("invalid or unsupported provider authorization")
	case errors.Is(err, providers.ErrScope):
		return rpcError(store.ErrForbidden)
	}
	var authError *providers.AuthError
	if errors.As(err, &authError) || errors.Is(err, providers.ErrNetwork) || errors.Is(err, providers.ErrResponse) {
		return connect.NewError(connect.CodeUnavailable, errors.New("provider authorization failed; cancel and begin a new login"))
	}
	return rpcError(err)
}

func oauthOptions(q *v1.BeginProviderOAuthRequest) (providers.StartOptions, error) {
	o := providers.StartOptions{RedirectURI: q.GetRedirectUri(), LoginProvider: q.GetLoginProvider(), SSORegion: q.GetSsoRegion(), APIRegion: q.GetApiRegion(), StartURL: q.GetStartUrl(), BaseURL: q.GetBaseUrl(), AuthBaseURL: q.GetAuthBaseUrl(), PortalURL: q.GetPortalUrl(), RegisterURL: q.GetRegisterUrl(), ClientID: q.GetClientId()}
	switch q.GetMethod() {
	case v1.ProviderOAuthMethod_PROVIDER_O_AUTH_METHOD_UNSPECIFIED:
	case v1.ProviderOAuthMethod_PROVIDER_O_AUTH_METHOD_PKCE:
		o.Method = providers.PKCE
	case v1.ProviderOAuthMethod_PROVIDER_O_AUTH_METHOD_DEVICE:
		o.Method = providers.Device
	case v1.ProviderOAuthMethod_PROVIDER_O_AUTH_METHOD_BROWSER_POLL:
		o.Method = providers.BrowserPoll
	case v1.ProviderOAuthMethod_PROVIDER_O_AUTH_METHOD_EXPLICIT_TOKEN:
		o.Method = providers.ExplicitToken
	case v1.ProviderOAuthMethod_PROVIDER_O_AUTH_METHOD_NATIVE_CALLBACK:
		o.Method = providers.NativeCallback
	default:
		return o, invalid("invalid provider authorization method")
	}
	return o, nil
}
func flowMessage(id string, f providerOAuthFlow) *v1.ProviderOAuth {
	out := &v1.ProviderOAuth{Id: id, State: v1.OAuthState_O_AUTH_STATE_PENDING, AuthorizationUrl: f.Challenge.URL, ExpiresAt: timestamp(f.Flow.ExpiresAt), CallbackState: &f.Challenge.State}
	if f.Challenge.UserCode != "" {
		out.UserCode = &f.Challenge.UserCode
	}
	if f.Flow.Method == providers.Device || f.Flow.Method == providers.BrowserPoll {
		seconds := uint32(f.Flow.Interval / time.Second)
		out.PollIntervalSeconds = &seconds
	}
	return out
}
func (m *management) BeginProviderOAuth(ctx context.Context, r *connect.Request[v1.BeginProviderOAuthRequest]) (*connect.Response[v1.BeginProviderOAuthResponse], error) {
	sc, err := m.scope(ctx, r.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	q := r.Msg
	if strings.TrimSpace(q.Name) == "" || len(q.Name) > 200 {
		return nil, invalid("provider name is required")
	}
	preset, ok := providers.Lookup(q.ProviderId)
	if !ok || preset.Excluded || len(providers.AuthMethods(q.ProviderId)) == 0 {
		return nil, invalid("provider authorization is unsupported")
	}
	options, err := oauthOptions(q)
	if err != nil {
		return nil, err
	}
	d := providerData{Metadata: &v1.ProviderConnection{Id: core.ID(), OrganizationId: sc.OrganizationID, ProviderId: q.ProviderId, Name: q.Name, BaseUrl: preset.BaseURL, AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH, State: v1.ConnectionState_CONNECTION_STATE_READY, CreatedAt: timestamp(time.Now())}}
	version := ""
	if q.GetConnectionId() != "" {
		raw, v, e := m.store.GetPrivateVersionForManagement(ctx, sc, string(providerKind), q.GetConnectionId())
		if e != nil {
			return nil, providerRPCError(e)
		}
		if json.Unmarshal(raw, &d) != nil || d.Metadata == nil || d.Metadata.ProviderId != q.ProviderId {
			return nil, invalid("connection provider does not match")
		}
		d.Metadata.Name = q.Name
		d.Metadata.AuthKind = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH
		d.Metadata.State = v1.ConnectionState_CONNECTION_STATE_READY
		d.Secret = ""
		d.Credential = nil
		d.Models = nil
		d.CodexCatalog = nil
		d.ModelsDiscoveredAt = time.Time{}
		version = v
	}
	if options.BaseURL != "" {
		d.Metadata.BaseUrl = options.BaseURL
	}
	executor := m.oauthExecutor(sc, "", "")
	challenge, flow, err := providers.NewAuth(executor).Start(ctx, sc, q.ProviderId, options)
	if err != nil {
		return nil, providerRPCError(err)
	}
	f := providerOAuthFlow{Flow: flow, Challenge: challenge, Connection: d}
	raw, err := json.Marshal(f)
	if err != nil {
		return nil, providerRPCError(err)
	}
	id := core.ID()
	if err = m.store.SaveProviderFlowBound(ctx, sc, id, string(providerKind), d.Metadata.Id, version, raw, flow.ExpiresAt); err != nil {
		return nil, providerRPCError(err)
	}
	return connect.NewResponse(&v1.BeginProviderOAuthResponse{Authorization: flowMessage(id, f)}), nil
}
func (m *management) oauthExecutor(sc core.Scope, id, claim string) *providerExecutor {
	e := &providerExecutor{store: m.store, scope: sc, test: m.providerExecutor}
	e.gate = func(ctx context.Context, start func(context.Context) error) error {
		if id != "" {
			return m.store.WithProviderFlowDispatch(ctx, sc, id, claim, start)
		}
		return m.store.WithScopeDispatch(ctx, sc, start)
	}
	return e
}
func (m *management) GetProviderOAuth(ctx context.Context, r *connect.Request[v1.GetProviderOAuthRequest]) (*connect.Response[v1.GetProviderOAuthResponse], error) {
	sc, err := m.scope(ctx, r.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	id := r.Msg.AuthorizationId
	receipt, err := m.store.ProviderFlowReceipt(ctx, sc, id)
	if err != nil {
		return nil, providerRPCError(err)
	}
	out := &v1.ProviderOAuth{Id: id, ExpiresAt: timestamp(receipt.ExpiresAt)}
	switch receipt.Status {
	case "completed":
		out.State = v1.OAuthState_O_AUTH_STATE_COMPLETED
		out.ConnectionId = receipt.ProviderID
	case "canceled":
		out.State = v1.OAuthState_O_AUTH_STATE_CANCELED
	case "expired":
		out.State = v1.OAuthState_O_AUTH_STATE_EXPIRED
	case "claimed":
		out.State = v1.OAuthState_O_AUTH_STATE_PENDING
	default:
		raw, e := m.store.GetProviderFlow(ctx, sc, id)
		if e != nil {
			return nil, providerRPCError(e)
		}
		var f providerOAuthFlow
		if json.Unmarshal(raw, &f) != nil {
			return nil, providerRPCError(providers.ErrFlow)
		}
		out = flowMessage(id, f)
		if f.Flow.Method == providers.Device || f.Flow.Method == providers.BrowserPoll {
			d, e := m.exchangeProviderFlow(ctx, sc, id, "", "")
			if errors.Is(e, providers.ErrPending) || errors.Is(e, store.ErrBusy) {
				if updated, readErr := m.store.GetProviderFlow(ctx, sc, id); readErr == nil {
					var pending providerOAuthFlow
					if json.Unmarshal(updated, &pending) == nil {
						out = flowMessage(id, pending)
					}
				}
				return connect.NewResponse(&v1.GetProviderOAuthResponse{Authorization: out}), nil
			}
			if e != nil {
				return nil, providerRPCError(e)
			}
			out = &v1.ProviderOAuth{Id: id, State: v1.OAuthState_O_AUTH_STATE_COMPLETED, ConnectionId: d.Metadata.Id, ExpiresAt: timestamp(receipt.ExpiresAt)}
		}
	}
	return connect.NewResponse(&v1.GetProviderOAuthResponse{Authorization: out}), nil
}
func (m *management) exchangeProviderFlow(ctx context.Context, sc core.Scope, id, code, state string) (providerData, error) {
	raw, claim, err := m.store.ClaimProviderFlow(ctx, sc, id)
	if err != nil {
		return providerData{}, err
	}
	var f providerOAuthFlow
	if json.Unmarshal(raw, &f) != nil || f.Connection.Metadata == nil || f.Flow.Scope != sc {
		return providerData{}, providers.ErrFlow
	}
	auth := providers.NewAuth(m.oauthExecutor(sc, id, claim))
	var credential providers.Credential
	if f.Flow.Method == providers.Device || f.Flow.Method == providers.BrowserPoll {
		credential, err = auth.Poll(ctx, sc, &f.Flow)
	} else {
		credential, err = auth.Complete(ctx, sc, f.Flow, code, state)
	}
	if errors.Is(err, providers.ErrPending) {
		raw, marshalErr := json.Marshal(f)
		if marshalErr != nil {
			return providerData{}, marshalErr
		}
		if releaseErr := m.store.ReleaseProviderFlow(ctx, sc, id, claim, raw); releaseErr != nil {
			return providerData{}, releaseErr
		}
	}
	if err != nil {
		return providerData{}, err
	}
	f.Connection.Credential = &credential
	if credential.APIBaseURL != "" {
		f.Connection.Metadata.BaseUrl = credential.APIBaseURL
	}
	if err = providers.ValidateConnection(providerConnection(f.Connection)); err != nil {
		return providerData{}, err
	}
	raw, err = json.Marshal(f.Connection)
	if err != nil {
		return providerData{}, err
	}
	if err = m.store.CompleteProviderFlow(ctx, sc, id, claim, f.Connection.Metadata.Id, raw); err != nil {
		return providerData{}, err
	}
	// Publish account-specific discovery through its own durable claim. A provider
	// without discovery support can still connect; absence never grants entitlement.
	if discovered, discoveryErr := m.discoverProviderModels(ctx, sc, f.Connection.Metadata.Id); discoveryErr == nil {
		return discovered, nil
	}
	return f.Connection, nil
}
func (m *management) CompleteProviderOAuth(ctx context.Context, r *connect.Request[v1.CompleteProviderOAuthRequest]) (*connect.Response[v1.CompleteProviderOAuthResponse], error) {
	sc, err := m.scope(ctx, r.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	d, err := m.exchangeProviderFlow(ctx, sc, r.Msg.AuthorizationId, r.Msg.Code, r.Msg.State)
	if err != nil {
		return nil, providerRPCError(err)
	}
	return connect.NewResponse(&v1.CompleteProviderOAuthResponse{Connection: d.Metadata}), nil
}
func (m *management) CancelProviderOAuth(ctx context.Context, r *connect.Request[v1.CancelProviderOAuthRequest]) (*connect.Response[v1.CancelProviderOAuthResponse], error) {
	sc, err := m.scope(ctx, r.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	if err = m.store.CancelProviderFlow(ctx, sc, r.Msg.AuthorizationId); err != nil {
		return nil, providerRPCError(err)
	}
	return connect.NewResponse(&v1.CancelProviderOAuthResponse{}), nil
}
