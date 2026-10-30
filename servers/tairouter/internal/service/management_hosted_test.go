// SPDX-License-Identifier: Apache-2.0
package service

import (
	"connectrpc.com/connect"
	"context"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	rpc "github.com/delinoio/oss/protos/tairouter/v1/tairouterv1connect"
	sessionauth "github.com/delinoio/oss/servers/tairouter/internal/auth"
	serverconfig "github.com/delinoio/oss/servers/tairouter/internal/config"
	observability "github.com/delinoio/oss/servers/tairouter/internal/observability"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHostedSettingsRemainPersonal(t *testing.T) {
	s := serverStore(t)
	ctx := context.Background()
	owner, ownerSession := testIdentity(t, s, "helper-owner")
	member, memberSession := testIdentity(t, s, "helper-member")
	org, e := s.CreateOrganization(ctx, owner.ID, "helpers")
	if e != nil {
		t.Fatal(e)
	}
	sc, _, e := s.Membership(ctx, org.ID, owner.ID)
	if e != nil {
		t.Fatal(e)
	}
	invite, e := s.Invite(ctx, sc, member.Email)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcceptInviteID(ctx, member.ID, invite.ID); e != nil {
		t.Fatal(e)
	}
	cfg := serverconfig.Config{PublicURL: "https://router.test"}
	srv := &server{config: cfg, store: s, auth: sessionauth.New(cfg, persistentAuth{s: s}), telemetry: observability.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))}
	h := httptest.NewServer(srv.handler())
	defer h.Close()
	client := func(cookie string) rpc.HostedSettingsServiceClient {
		return rpc.NewHostedSettingsServiceClient(&http.Client{Transport: sessionTransport{origin: cfg.PublicURL, cookie: cookie}}, h.URL)
	}
	ownerClient, memberClient := client(ownerSession), client(memberSession)
	for _, c := range []rpc.HostedSettingsServiceClient{ownerClient, memberClient} {
		got, e := c.GetHostedSettings(ctx, connect.NewRequest(&v1.GetHostedSettingsRequest{OrganizationId: org.ID}))
		if e != nil || got.Msg.Settings.Search != nil {
			t.Fatalf("missing config: %+v %v", got, e)
		}
	}
	disabled := false
	limit := uint32(2)
	settings := &v1.HostedSettings{Search: &v1.HostedSearchSettings{Enabled: &disabled, MaxSearchesPerTurn: &limit}}
	if _, e = memberClient.UpdateHostedSettings(ctx, connect.NewRequest(&v1.UpdateHostedSettingsRequest{OrganizationId: org.ID, Settings: settings})); e != nil {
		t.Fatal(e)
	}
	got, e := memberClient.GetHostedSettings(ctx, connect.NewRequest(&v1.GetHostedSettingsRequest{OrganizationId: org.ID}))
	if e != nil || got.Msg.Settings.GetSearch().GetMaxSearchesPerTurn() != 2 {
		t.Fatalf("round trip: %+v %v", got, e)
	}
	got, e = ownerClient.GetHostedSettings(ctx, connect.NewRequest(&v1.GetHostedSettingsRequest{OrganizationId: org.ID}))
	if e != nil || got.Msg.Settings.Search != nil {
		t.Fatalf("admin read another user's config: %+v %v", got, e)
	}
	enabled := true
	if _, e = memberClient.UpdateHostedSettings(ctx, connect.NewRequest(&v1.UpdateHostedSettingsRequest{OrganizationId: org.ID, Settings: &v1.HostedSettings{Search: &v1.HostedSearchSettings{Enabled: &enabled}}})); connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatalf("borrowed parent credentials: %v", e)
	}
	if e = s.RemoveMember(ctx, sc, member.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = memberClient.GetHostedSettings(ctx, connect.NewRequest(&v1.GetHostedSettingsRequest{OrganizationId: org.ID})); e == nil {
		t.Fatal("removed member read config")
	}
}
