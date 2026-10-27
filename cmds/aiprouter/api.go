// SPDX-License-Identifier: Apache-2.0
package main

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	rpc "github.com/delinoio/oss/protos/aiprouter/v1/aiprouterv1connect"
	"net/http"
	"strings"
	"time"
)

type ConnectAPI struct {
	Server string
	HTTP   *http.Client
	Device rpc.DeviceAuthorizationServiceClient
	Client rpc.ClientServiceClient
}

func NewConnectAPI(server string) (*ConnectAPI, error) {
	if _, e := serverURL(server); e != nil {
		return nil, e
	}
	httpClient := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("RPC redirects are refused") }}
	return &ConnectAPI{Server: server, HTTP: httpClient, Device: rpc.NewDeviceAuthorizationServiceClient(httpClient, server), Client: rpc.NewClientServiceClient(httpClient, server)}, nil
}
func (a *ConnectAPI) Begin(ctx context.Context, name string) (Device, error) {
	r, e := a.Device.BeginDeviceAuthorization(ctx, connect.NewRequest(&v1.BeginDeviceAuthorizationRequest{DeviceName: name}))
	if e != nil {
		return Device{}, errors.New("device authorization unavailable")
	}
	m := r.Msg
	if m.ExpiresAt == nil || m.PollIntervalSeconds != 5 {
		return Device{}, errors.New("invalid device authorization contract")
	}
	return Device{m.AuthorizationId, m.DeviceSecret, m.UserCode, m.VerificationUrl, m.ExpiresAt.AsTime()}, nil
}
func (a *ConnectAPI) Poll(ctx context.Context, d Device) (Grant, error) {
	r, e := a.Device.PollDeviceAuthorization(ctx, connect.NewRequest(&v1.PollDeviceAuthorizationRequest{AuthorizationId: d.ID, DeviceSecret: d.Secret}))
	if e != nil {
		return Grant{}, errors.New("device poll unavailable")
	}
	g := Grant{}
	switch r.Msg.State {
	case v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_PENDING:
		g.Status = DevicePending
	case v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_APPROVED:
		g.Status = DeviceApproved
	case v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_DENIED, v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_REVOKED:
		g.Status = DeviceDenied
	case v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_EXPIRED:
		g.Status = DeviceExpired
	default:
		return g, errors.New("invalid device handoff state")
	}
	if m := r.Msg.Result; m != nil {
		g.KeyID = m.KeyId
		g.Key = m.RawKey
		g.Organization = m.OrganizationId
	}
	return g, nil
}
func (a *ConnectAPI) Confirm(ctx context.Context, d Device) error {
	_, e := a.Device.ConfirmDeviceStorage(ctx, connect.NewRequest(&v1.ConfirmDeviceStorageRequest{AuthorizationId: d.ID, DeviceSecret: d.Secret}))
	if e != nil {
		return errors.New("storage confirmation unavailable")
	}
	return nil
}
func (a *ConnectAPI) Revoke(ctx context.Context, id, key string) error {
	r := connect.NewRequest(&v1.RevokeCLIKeyRequest{KeyId: id})
	r.Header().Set("Authorization", "Bearer "+key)
	_, e := a.Client.RevokeCLIKey(ctx, r)
	if e != nil {
		return errors.New("server key revocation incomplete")
	}
	return nil
}
func (a *ConnectAPI) Models(ctx context.Context, key string) ([]ClientModel, error) {
	r := connect.NewRequest(&v1.ListClientModelsRequest{})
	r.Header().Set("Authorization", "Bearer "+key)
	response, e := a.Client.ListClientModels(ctx, r)
	if e != nil {
		return nil, errors.New("client catalog unavailable")
	}
	var models []ClientModel
	for _, m := range response.Msg.Models {
		row := ClientModel{ID: m.Id, Name: m.DisplayName, Context: int(m.GetContextLimit()), Output: int(m.GetOutputLimit()),
			FastRowAvailable: m.FastRowAvailable, SupportsReasoningEffort: m.SupportsReasoningEffort, SupportsThinkingToggle: m.SupportsThinkingToggle, DefaultThinkingEnabled: m.DefaultThinkingEnabled, SupportsReasoningSummaries: m.SupportsReasoningSummaries, MaxInputTokens: m.MaxInputTokens, SupportsReasoning: m.SupportsReasoning, SupportsTools: m.SupportsTools, ReasoningEffortsKnown: m.ReasoningEffortsKnown}
		if m.DefaultReasoningEffort != nil && *m.DefaultReasoningEffort != v1.ReasoningEffort_REASONING_EFFORT_UNSPECIFIED {
			value := strings.ToLower(strings.TrimPrefix(m.DefaultReasoningEffort.String(), "REASONING_EFFORT_"))
			row.DefaultReasoningEffort = &value
		}
		if m.ReasoningInterleaving != nil {
			var value ClientReasoningInterleaving
			switch *m.ReasoningInterleaving {
			case v1.ReasoningInterleaving_REASONING_INTERLEAVING_NONE:
				value = ClientInterleavingNone
			case v1.ReasoningInterleaving_REASONING_INTERLEAVING_REASONING_CONTENT:
				value = ClientInterleavingContent
			}
			if value != "" {
				row.ReasoningInterleaving = &value
			}
		}
		if m.ReasoningEffortsKnown != nil && *m.ReasoningEffortsKnown {
			row.Efforts = []string{}
		}
		if m.ReasoningEffortsKnown != nil && !*m.ReasoningEffortsKnown && len(m.ReasoningEfforts) > 0 {
			return nil, errors.New("inconsistent client reasoning metadata")
		}
		if row.Name == "" {
			row.Name = row.ID
		}
		for _, c := range m.Capabilities {
			switch c {
			case v1.ModelCapability_MODEL_CAPABILITY_TOOLS:
				row.Tools = true
			case v1.ModelCapability_MODEL_CAPABILITY_REASONING:
				if row.SupportsReasoning == nil {
					value := true
					row.SupportsReasoning = &value
				}
			case v1.ModelCapability_MODEL_CAPABILITY_IMAGE_INPUT:
				row.Vision = true
				row.InputModalities = append(row.InputModalities, "image")
			case v1.ModelCapability_MODEL_CAPABILITY_TEXT:
				row.InputModalities = append(row.InputModalities, "text")
			case v1.ModelCapability_MODEL_CAPABILITY_AUDIO_INPUT:
				row.InputModalities = append(row.InputModalities, "audio")
			case v1.ModelCapability_MODEL_CAPABILITY_VIDEO_INPUT:
				row.InputModalities = append(row.InputModalities, "video")
			}
		}
		for _, effort := range m.ReasoningEfforts {
			if effort != v1.ReasoningEffort_REASONING_EFFORT_UNSPECIFIED {
				row.Efforts = append(row.Efforts, strings.ToLower(strings.TrimPrefix(effort.String(), "REASONING_EFFORT_")))
			}
		}
		if row.SupportsTools != nil {
			row.Tools = *row.SupportsTools
		}
		models = append(models, row)
	}
	return models, nil
}
