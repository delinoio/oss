// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"google.golang.org/protobuf/proto"
)

func saveRoutingFixture(t *testing.T, a *inferenceAdapter, sc core.Scope, id, model string) providerData {
	t.Helper()
	d := providerData{Metadata: &v1.ProviderConnection{Id: id, OrganizationId: sc.OrganizationID, ProviderId: "custom", BaseUrl: "https://provider.example/v1", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: "fixture-secret"}
	putRoutingProvider(t, a, sc, d)
	r := &v1.Route{Id: "route-" + id, OrganizationId: sc.OrganizationID, ModelId: model, Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: id}, ModelId: "wire-model"}}}
	putRoutingRoute(t, a, sc, r)
	return d
}
func putRoutingProvider(t *testing.T, a *inferenceAdapter, sc core.Scope, d providerData) {
	t.Helper()
	b, e := json.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.s.PutPrivate(context.Background(), sc, string(providerKind), d.Metadata.Id, b); e != nil {
		t.Fatal(e)
	}
}
func putRoutingRoute(t *testing.T, a *inferenceAdapter, sc core.Scope, r *v1.Route) {
	t.Helper()
	b, e := proto.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.s.PutPrivate(context.Background(), sc, string(routeKind), r.Id, b); e != nil {
		t.Fatal(e)
	}
}
func admitRouting(t *testing.T, a *inferenceAdapter, key string) inference.Principal {
	t.Helper()
	p, release, e := a.Admit(context.Background(), key)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(release)
	return p
}

func TestModelViewsCurrentPrincipalAndExactMetadata(t *testing.T) {
	a, sc, key := dispatchFixture(t)
	d := saveRoutingFixture(t, a, sc, "account", "route/with~escape")
	d.Models = &providers.ModelCatalog{State: providers.ValidationAccepted, Models: []providers.Model{{ID: "wire-model", Name: "Exact model", ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningEfforts: []string{}}}}
	d.ModelsDiscoveredAt = time.Now()
	d.CodexCatalog = []json.RawMessage{json.RawMessage(`{"slug":"wire-model","display_name":"Exact model","context_window":1000000,"supported_reasoning_levels":[],"supports_websockets":false,"base_instructions":"fixture instructions"}`)}
	putRoutingProvider(t, a, sc, d)
	p := admitRouting(t, a, key)
	b, e := a.Read(context.Background(), p, inference.Catalog, nil)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "fixture-secret") || !strings.Contains(string(b), `"supports_websockets":false`) || !strings.Contains(string(b), `"supported_reasoning_levels":[]`) || !strings.Contains(string(b), `"slug":"route/with~escape"`) {
		t.Fatalf("catalog projection lost authoritative fields: %s", b)
	}
	b, e = a.Read(context.Background(), p, "models", url.Values{"ids": {"cli"}})
	if e != nil {
		t.Fatal(e)
	}
	var cli struct {
		Data []map[string]any `json:"data"`
	}
	if json.Unmarshal(b, &cli) != nil || len(cli.Data) != 1 {
		t.Fatal("invalid model view")
	}
	if cli.Data[0]["max_input_tokens"] != nil {
		t.Fatal("input limit invented from context window")
	}
	if cli.Data[0]["reasoning_efforts_known"] != true {
		t.Fatal("declared empty effort ladder lost")
	}
	if _, ok := cli.Data[0]["fast_row_available"]; ok {
		t.Fatal("fast alias advertised without authoritative route")
	}
	if !strings.Contains(string(b), "ocx-claude2-aiprouter--route~swith~tescape") {
		t.Fatalf("CLI alias not reversible: %s", b)
	}
	b, e = a.Read(context.Background(), p, "models", url.Values{"ids": {"desktop"}, "format": {"desktop-config"}})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), `"supports1m":true`) {
		t.Fatal("authoritative context omitted")
	}
	_, e = a.Read(context.Background(), p, "models", url.Values{"ids": {"desktop"}, "format": {"desktop-config"}, "client_version": {""}})
	var ie *inference.Error
	if !errors.As(e, &ie) || ie.Status != 400 {
		t.Fatalf("mixed selector accepted: %v", e)
	}
	forged := p
	forged.UserID = "another-user"
	if _, e = a.Read(context.Background(), forged, inference.Catalog, nil); !errors.Is(e, inference.ErrUnauthorized) {
		t.Fatalf("cross-user read: %v", e)
	}
	d.Metadata.State = v1.ConnectionState_CONNECTION_STATE_DISABLED
	putRoutingProvider(t, a, sc, d)
	rows, e := a.Models(context.Background(), p)
	if e != nil || len(rows) != 0 {
		t.Fatalf("paused connection exposed: %v %v", rows, e)
	}
}
func TestModelViewsNoInventedCatalogOrCapabilities(t *testing.T) {
	a, sc, key := dispatchFixture(t)
	saveRoutingFixture(t, a, sc, "account", "route")
	p := admitRouting(t, a, key)
	b, e := a.Read(context.Background(), p, "models", url.Values{"ids": {"cli"}})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "reasoning_efforts") || strings.Contains(string(b), "input_modalities") || strings.Contains(string(b), "context_window") {
		t.Fatalf("unknown capability fabricated: %s", b)
	}
	_, e = a.Read(context.Background(), p, inference.Catalog, nil)
	var ie *inference.Error
	if !errors.As(e, &ie) || ie.Code != "catalog_not_found" {
		t.Fatalf("unknown catalog=%v", e)
	}
	if e = a.s.RevokeKey(context.Background(), sc, p.KeyID); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Read(context.Background(), p, "models", nil); !errors.Is(e, inference.ErrUnauthorized) {
		t.Fatalf("revoked key read=%v", e)
	}
}
func TestModelEvidenceGatedAndUnknownVersusEmpty(t *testing.T) {
	now := time.Now()
	d := providerData{Metadata: &v1.ProviderConnection{ProviderId: "openai"}, Credential: &providers.Credential{}}
	for _, id := range []string{"gpt-daybreak-blue-latest", "gpt-6-astra-minor"} {
		if _, ok := modelEvidence(d, id, now); ok {
			t.Fatal("missing native grant admitted")
		}
	}
	if _, ok := modelEvidence(d, "gpt-6-astra", now); !ok {
		t.Fatal("flagship incorrectly gated")
	}
	d.Models = &providers.ModelCatalog{State: providers.ValidationAccepted, Models: []providers.Model{{ID: "gpt-6-astra-minor", ReasoningEfforts: []string{}}}}
	d.ModelsDiscoveredAt = now
	v, ok := modelEvidence(d, "gpt-6-astra-minor", now)
	if !ok || v.Efforts == nil || len(v.Efforts) != 0 {
		t.Fatal("explicit empty ladder lost")
	}
	d.ModelsDiscoveredAt = now.Add(-7 * time.Hour)
	if _, ok := modelEvidence(d, "gpt-6-astra-minor", now); ok {
		t.Fatal("stale grant admitted")
	}
}
func TestRouteAliasResolutionUsesCurrentRoutes(t *testing.T) {
	a, sc, key := dispatchFixture(t)
	saveRoutingFixture(t, a, sc, "account", "x/y~z")
	p := admitRouting(t, a, key)
	for _, alias := range []string{cliRouteAlias("x/y~z"), desktopRouteAlias("x/y~z")} {
		r, e := a.Resolve(context.Background(), p, alias, inference.Generate)
		if e != nil || len(r.Targets) != 1 || r.Targets[0].Model != "wire-model" {
			t.Fatalf("alias resolve: %v %v", r, e)
		}
	}
	if e := a.s.DeletePrivate(context.Background(), sc, string(routeKind), "route-account"); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Resolve(context.Background(), p, desktopRouteAlias("x/y~z"), inference.Generate); e == nil {
		t.Fatal("deleted route alias retained")
	}
}
