// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"google.golang.org/protobuf/proto"
)

func TestRoutingNativeMessagesPreferenceSurvivesSelection(t *testing.T) {
	a, sc, key := poolRoutingFixture(t, v1.PoolStrategy_POOL_STRATEGY_PRIORITY)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		d, err := a.provider(ctx, sc, id)
		if err != nil {
			t.Fatal(err)
		}
		d.Metadata.ProviderId, d.Metadata.BaseUrl = "anthropic", ""
		d.Metadata.AuthKind = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH
		d.Secret = ""
		d.Credential = &providers.Credential{Scope: sc, Provider: "anthropic", AccessToken: "fixture-" + id}
		putRoutingProvider(t, a, sc, d)
	}
	putRoutingRoute(t, a, sc, &v1.Route{Id: "r", ModelId: "model", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_PoolId{PoolId: "pool"}, ModelId: "claude-opus-5"}}})
	for _, preference := range []*bool{nil, proto.Bool(false), proto.Bool(true)} {
		pool := &v1.Pool{Id: "pool", NativeOptions: &v1.NativePoolOptions{Enabled: proto.Bool(true), NativeMessages: preference}, Members: []*v1.PoolMember{{ConnectionId: "a"}, {ConnectionId: "b"}, {ConnectionId: "c"}}}
		raw, _ := json.Marshal(pool)
		if err := a.s.PutPrivate(ctx, sc, string(poolKind), pool.Id, raw); err != nil {
			t.Fatal(err)
		}
		instance := &inferenceAdapter{s: a.s, telemetry: a.telemetry}
		p, release, err := instance.Admit(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		route, err := instance.Resolve(ctx, p, "model", inference.Generate)
		if err != nil {
			release()
			t.Fatal(err)
		}
		target := route.Targets[0]
		want := preference != nil && !*preference
		if target.ForceMessagesTranslation != want || target.Wire != inference.Anthropic || target.Auth != inference.OAuth {
			release()
			t.Fatalf("unexpected selected lane: force=%v wire=%s auth=%s", target.ForceMessagesTranslation, target.Wire, target.Auth)
		}
		credential, err := instance.Select(ctx, p, target, "")
		release()
		if err != nil || credential.AccountID == "" || credential.CredentialVersion == "" {
			t.Fatalf("selected account lost pool preference: %v", err)
		}
	}
}
