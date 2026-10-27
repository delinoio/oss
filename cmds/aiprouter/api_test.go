// SPDX-License-Identifier: Apache-2.0
package main

import (
	"connectrpc.com/connect"
	"context"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	rpc "github.com/delinoio/oss/protos/aiprouter/v1/aiprouterv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type deviceServer struct {
	rpc.UnimplementedDeviceAuthorizationServiceHandler
	secret    string
	confirmed bool
}

func (s *deviceServer) BeginDeviceAuthorization(ctx context.Context, r *connect.Request[v1.BeginDeviceAuthorizationRequest]) (*connect.Response[v1.BeginDeviceAuthorizationResponse], error) {
	return connect.NewResponse(&v1.BeginDeviceAuthorizationResponse{AuthorizationId: "id", DeviceSecret: s.secret, UserCode: "CODE", VerificationUrl: "https://router.test/device", ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)), PollIntervalSeconds: 5}), nil
}
func (s *deviceServer) PollDeviceAuthorization(ctx context.Context, r *connect.Request[v1.PollDeviceAuthorizationRequest]) (*connect.Response[v1.PollDeviceAuthorizationResponse], error) {
	if r.Msg.DeviceSecret != s.secret || r.Msg.AuthorizationId != "id" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	return connect.NewResponse(&v1.PollDeviceAuthorizationResponse{State: v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_APPROVED, Result: &v1.DeviceKeyResult{KeyId: "key-id", RawKey: "RAW_SECRET", OrganizationId: "org"}}), nil
}
func (s *deviceServer) ConfirmDeviceStorage(ctx context.Context, r *connect.Request[v1.ConfirmDeviceStorageRequest]) (*connect.Response[v1.ConfirmDeviceStorageResponse], error) {
	s.confirmed = r.Msg.DeviceSecret == s.secret
	return connect.NewResponse(&v1.ConfirmDeviceStorageResponse{}), nil
}

type clientServer struct {
	rpc.UnimplementedClientServiceHandler
	revoked bool
	models  []*v1.Model
}

func (s *clientServer) RevokeCLIKey(ctx context.Context, r *connect.Request[v1.RevokeCLIKeyRequest]) (*connect.Response[v1.RevokeCLIKeyResponse], error) {
	if r.Header().Get("Authorization") != "Bearer RAW_SECRET" || r.Msg.KeyId != "key-id" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	s.revoked = true
	return connect.NewResponse(&v1.RevokeCLIKeyResponse{}), nil
}
func (s *clientServer) ListClientModels(ctx context.Context, r *connect.Request[v1.ListClientModelsRequest]) (*connect.Response[v1.ListClientModelsResponse], error) {
	if r.Header().Get("Authorization") != "Bearer RAW_SECRET" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	if s.models != nil {
		return connect.NewResponse(&v1.ListClientModelsResponse{Models: s.models}), nil
	}
	return connect.NewResponse(&v1.ListClientModelsResponse{Models: []*v1.Model{{Id: "v/m", Capabilities: []v1.ModelCapability{v1.ModelCapability_MODEL_CAPABILITY_IMAGE_INPUT}}}}), nil
}
func TestGeneratedConnectTransport(t *testing.T) {
	device := &deviceServer{secret: "DEVICE_SECRET"}
	client := &clientServer{}
	mux := http.NewServeMux()
	p, h := rpc.NewDeviceAuthorizationServiceHandler(device)
	mux.Handle(p, h)
	p, h = rpc.NewClientServiceHandler(client)
	mux.Handle(p, h)
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	a := &ConnectAPI{Device: rpc.NewDeviceAuthorizationServiceClient(server.Client(), server.URL), Client: rpc.NewClientServiceClient(server.Client(), server.URL)}
	ctx := context.Background()
	d, e := a.Begin(ctx, "laptop")
	if e != nil {
		t.Fatal(e)
	}
	g, e := a.Poll(ctx, d)
	if e != nil || g.Status != DeviceApproved || g.Organization != "org" {
		t.Fatal(e)
	}
	if e = a.Confirm(ctx, d); e != nil || !device.confirmed {
		t.Fatal(e)
	}
	models, e := a.Models(ctx, g.Key)
	if e != nil || len(models) != 1 || !models[0].Vision {
		t.Fatal(e)
	}
	if e = a.Revoke(ctx, g.KeyID, g.Key); e != nil || !client.revoked {
		t.Fatal(e)
	}
}
func TestAPIRedactsRemoteErrors(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		_, _ = io.WriteString(w, `{"code":"internal","message":"SECRET provider diagnostic"}`)
	}))
	defer srv.Close()
	a := &ConnectAPI{Device: rpc.NewDeviceAuthorizationServiceClient(srv.Client(), srv.URL)}
	_, e := a.Begin(context.Background(), "laptop")
	if e == nil || strings.Contains(e.Error(), "SECRET") {
		t.Fatal("remote diagnostics leaked")
	}
}
