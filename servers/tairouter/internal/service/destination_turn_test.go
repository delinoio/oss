// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/providers"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

func setTurnDestinations(t *testing.T, f *providerAcceptanceFixture, rules []store.AllowedDestination) {
	t.Helper()
	ctx := context.Background()
	settings, err := f.s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.Destinations = nil
	settings.DestinationRules = rules
	_, err = f.s.UpdateSettingsCAS(ctx, core.Identity{Issuer: "https://identity.test", Subject: "provider-owner"}, settings.Revision, settings)
	if err != nil {
		t.Fatal(err)
	}
}

func turnSocket(t *testing.T, ctx context.Context, base, key string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/v1/responses", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + key}}})
	if err != nil {
		if resp != nil {
			t.Fatalf("socket dial: %v (HTTP %d)", err, resp.StatusCode)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func sendTurn(t *testing.T, ctx context.Context, conn *websocket.Conn, input string) string {
	t.Helper()
	request, _ := json.Marshal(map[string]any{"type": "response.create", "model": "fixture-model", "input": input})
	if err := conn.Write(ctx, websocket.MessageText, request); err != nil {
		t.Fatal(err)
	}
	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var event struct {
			Type  string `json:"type"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "error" {
			return event.Error.Code
		}
		if event.Type == "response.completed" {
			return ""
		}
	}
}

func waitTurnRelease(t *testing.T, ctx context.Context, f *providerAcceptanceFixture, count int) {
	t.Helper()
	for {
		var released int
		if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM "`+f.schema+`".admissions WHERE released`).Scan(&released); err != nil {
			t.Fatal(err)
		}
		if released >= count {
			return
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("turn admission was not released")
		}
	}
}

func waitSocketTurnReady(t *testing.T, ctx context.Context, conn *websocket.Conn) {
	t.Helper()
	request := []byte(`{"type":"response.create","model":"fixture-model","generate":false}`)
	for {
		if err := conn.Write(ctx, websocket.MessageText, request); err != nil {
			t.Fatal(err)
		}
		for {
			_, payload, err := conn.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var event struct {
				Type  string `json:"type"`
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(payload, &event); err != nil {
				t.Fatal(err)
			}
			if event.Type == "response.completed" {
				return
			}
			if event.Type == "error" {
				if event.Error.Code != "response_in_progress" {
					t.Fatalf("turn readiness: %s", event.Error.Code)
				}
				break
			}
		}
		select {
		case <-time.After(time.Millisecond):
		case <-ctx.Done():
			t.Fatal("completed worker was not consumed")
		}
	}
}

func TestDestinationPolicyRefreshesAcrossResponsesSocketTurns(t *testing.T) {
	for _, change := range []string{"removed", "narrowed", "unchanged"} {
		t.Run(change, func(t *testing.T) {
			var sends atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"id\":\"chat_fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n"+
					"data: {\"id\":\"chat_fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n")
			}))
			defer upstream.Close()
			f := newProviderAcceptance(t, "custom", "fixture-model", func(http.ResponseWriter, *http.Request) {})
			f.srv.inferenceDestinations = nil
			wire := v1.ProviderWire_PROVIDER_WIRE_CHAT
			f.data.Metadata.BaseUrl = upstream.URL + "/v1"
			f.data.Metadata.Configuration = &v1.ProviderConfiguration{Wire: &wire}
			f.data.Models = &providers.ModelCatalog{State: providers.ValidationAccepted, Models: []providers.Model{{ID: "fixture-model"}}}
			f.data.ModelsDiscoveredAt = time.Now()
			f.save()
			putRoutingRoute(t, &inferenceAdapter{s: f.s}, f.scope, &v1.Route{Id: "destination-turn", OrganizationId: f.scope.OrganizationID, ModelId: "fixture-model", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: f.data.Metadata.Id}, ModelId: "fixture-model"}}})
			approved := []store.AllowedDestination{{Origin: upstream.URL, AllowedCIDRs: []string{"127.0.0.0/8"}}}
			setTurnDestinations(t, f, approved)
			service := httptest.NewServer(f.srv.handler())
			defer service.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			conn := turnSocket(t, ctx, service.URL, f.key)
			if code := sendTurn(t, ctx, conn, "first"); code != "" {
				t.Fatalf("first turn: %s", code)
			}
			waitTurnRelease(t, ctx, f, 1)
			waitSocketTurnReady(t, ctx, conn)
			switch change {
			case "removed":
				setTurnDestinations(t, f, nil)
			case "narrowed":
				setTurnDestinations(t, f, []store.AllowedDestination{{Origin: upstream.URL, AllowedCIDRs: []string{"10.0.0.0/8"}}})
			}
			code := sendTurn(t, ctx, conn, "second")
			if change == "unchanged" {
				if code != "" || sends.Load() != 2 {
					t.Fatalf("unchanged approval: error=%q sends=%d", code, sends.Load())
				}
				return
			}
			if code != inference.ErrDestination.Code || sends.Load() != 1 {
				t.Fatalf("stale destination authorization: error=%q sends=%d", code, sends.Load())
			}
			fresh := turnSocket(t, ctx, service.URL, f.key)
			if code := sendTurn(t, ctx, fresh, "fresh"); code != inference.ErrDestination.Code || sends.Load() != 1 {
				t.Fatalf("fresh socket: error=%q sends=%d", code, sends.Load())
			}
			response := f.call("/v1/responses", `{"model":"fixture-model","input":"fresh"}`, nil)
			if response.Code != http.StatusForbidden || sends.Load() != 1 {
				t.Fatalf("fresh HTTP: status=%d sends=%d", response.Code, sends.Load())
			}
		})
	}
}

func TestDestinationTurnSnapshotKeepsTransportOverrides(t *testing.T) {
	f := newProviderAcceptance(t, "custom", "fixture-model", func(http.ResponseWriter, *http.Request) {})
	custom := f.srv.inferenceDestinations
	custom.ApprovedOrigins = []string{"http://fixture-override.test:3210"}
	setTurnDestinations(t, f, []store.AllowedDestination{{Origin: "http://127.0.0.1:8080", AllowedCIDRs: []string{"127.0.0.0/8"}}})
	a := &inferenceAdapter{s: f.s, telemetry: f.srv.telemetry, destinationOverrides: custom, turnPolicyEnabled: true}
	p, release, err := a.Admit(context.Background(), f.key)
	if err != nil {
		t.Fatal(err)
	}
	first, active, err := a.TurnDestinationPolicy(p)
	if err != nil || !active || first.Resolver == nil || first.DialContext == nil || first.TLSConfig == nil || len(first.ApprovedOrigins) != 2 || len(first.Rules) != 1 {
		t.Fatalf("missing first turn policy or transport overrides: active=%t err=%v", active, err)
	}
	setTurnDestinations(t, f, []store.AllowedDestination{{Origin: "http://127.0.0.1:8080", AllowedCIDRs: []string{"10.0.0.0/8"}}})
	if first.Rules[0].AllowedCIDRs[0] != "127.0.0.0/8" {
		t.Fatal("active turn policy changed with settings")
	}
	release()
	p, release, err = a.Admit(context.Background(), f.key)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	second, active, err := a.TurnDestinationPolicy(p)
	if err != nil || !active || second.Rules[0].AllowedCIDRs[0] != "10.0.0.0/8" || second.ApprovedOrigins[0] != "http://fixture-override.test:3210" {
		t.Fatalf("new turn policy not refreshed: active=%t err=%v", active, err)
	}
}

func TestDestinationPublicHTTPSRuleRefreshesAcrossTurns(t *testing.T) {
	var sends atomic.Int32
	f := newProviderAcceptance(t, "custom", "fixture-model", func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chat_fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"+
			"data: {\"id\":\"chat_fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n")
	})
	wire := v1.ProviderWire_PROVIDER_WIRE_CHAT
	f.data.Metadata.Configuration = &v1.ProviderConfiguration{Wire: &wire}
	f.data.Models = &providers.ModelCatalog{State: providers.ValidationAccepted, Models: []providers.Model{{ID: "fixture-model"}}}
	f.data.ModelsDiscoveredAt = time.Now()
	f.save()
	putRoutingRoute(t, &inferenceAdapter{s: f.s}, f.scope, &v1.Route{Id: "public-destination-turn", OrganizationId: f.scope.OrganizationID, ModelId: "fixture-model", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: f.data.Metadata.Id}, ModelId: "fixture-model"}}})
	setTurnDestinations(t, f, []store.AllowedDestination{{Origin: "https://provider.example", AllowedCIDRs: []string{"93.184.216.0/24"}}})
	service := httptest.NewServer(f.srv.handler())
	defer service.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn := turnSocket(t, ctx, service.URL, f.key)
	if code := sendTurn(t, ctx, conn, "first"); code != "" {
		t.Fatalf("first HTTPS turn: %s", code)
	}
	waitTurnRelease(t, ctx, f, 1)
	waitSocketTurnReady(t, ctx, conn)
	setTurnDestinations(t, f, []store.AllowedDestination{{Origin: "https://provider.example", AllowedCIDRs: []string{"10.0.0.0/8"}}})
	if code := sendTurn(t, ctx, conn, "second"); code != inference.ErrDestination.Code || sends.Load() != 1 {
		t.Fatalf("public HTTPS escaped current rule: error=%q sends=%d", code, sends.Load())
	}
}
