// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"testing"
)

func TestRoutingHostedBudgetUsesCurrentOwnerSettings(t *testing.T) {
	a, sc, key := dispatchFixture(t)
	saveRoutingFixture(t, a, sc, "account", "model")
	p := admitRouting(t, a, key)
	ctx := context.Background()
	route, err := a.Resolve(ctx, p, "model", inference.Generate)
	if err != nil {
		t.Fatal(err)
	}
	baseline := route.MaxSends
	enabled := true
	searches := uint32(3)
	settings := &v1.HostedSettings{Search: &v1.HostedSearchSettings{Enabled: &enabled, MaxSearchesPerTurn: &searches, Target: &v1.HostedHelperTarget{Selection: &v1.HostedHelperTarget_ConnectionId{ConnectionId: "account"}}}}
	raw, err := protojson.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.s.PutPrivate(ctx, sc, "hosted-helpers", "settings", raw); err != nil {
		t.Fatal(err)
	}
	route, err = a.Resolve(ctx, p, "model", inference.Generate)
	if err != nil {
		t.Fatal(err)
	}
	if route.MaxSends != baseline*4+3 {
		t.Fatalf("hosted allowance=%d want=%d", route.MaxSends, baseline*4+3)
	}
	disabled := false
	settings.Search.Enabled = &disabled
	raw, _ = protojson.Marshal(settings)
	if err = a.s.PutPrivate(ctx, sc, "hosted-helpers", "settings", raw); err != nil {
		t.Fatal(err)
	}
	route, err = a.Resolve(ctx, p, "model", inference.Generate)
	if err != nil {
		t.Fatal(err)
	}
	if route.MaxSends != baseline {
		t.Fatal("disabled helper retained extra exchanges")
	}
}

func TestRoutingRefreshExchangeBounds(t *testing.T) {
	for provider, want := range map[string]int{"google-antigravity": 10, "zed": 10, "github-copilot": 12, "google-vertex": 6, "xai": 5, "openai": 4} {
		if got := baseLaneBudget(provider, inference.OAuth, 1); got != want {
			t.Fatalf("%s budget=%d want=%d", provider, got, want)
		}
	}
}
