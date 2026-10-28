// SPDX-License-Identifier: Apache-2.0
package service

import (
	"github.com/delinoio/oss/packages/aiprouter/providers"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"math"
	"testing"
	"time"
)

func TestNormalQuotaCollectionProjection(t *testing.T) {
	now := time.Now()
	reset := now.Add(time.Hour)
	health := providers.Health{State: providers.HealthAvailable, ObservedAt: now, Windows: []providers.QuotaWindow{
		{Kind: "five-hour", UsedPercent: 20, ResetAt: reset},
		{Kind: "weekly", UsedPercent: 80},
		{Kind: "monthly", UsedPercent: 45},
		{Kind: "credits", UsedPercent: 99},
		{Kind: "spending-cap", UsedPercent: 99},
		{Kind: "five-hour", UsedPercent: math.NaN()},
		{Kind: "weekly", UsedPercent: 101},
		{Kind: "weekly", Model: "unrelated-model", UsedPercent: 88},
	}, Balances: []providers.Balance{{Unit: "USD", Remaining: 0}}}
	q := normalQuotaCollectionSnapshot("custom", health)
	if len(q.Windows) != 3 || q.Windows[0].Window != "5h" || !q.Windows[0].ObservedAt.Equal(now) || !q.Windows[0].ResetAt.Equal(reset) {
		t.Fatalf("invalid projection: %+v", q)
	}
	health.State = providers.HealthUnknown
	if len(normalQuotaCollectionSnapshot("custom", health).Windows) != 0 {
		t.Fatal("unknown health published")
	}
}

func TestNormalQuotaCollectionAntigravityFamilies(t *testing.T) {
	h := providers.Health{State: providers.HealthAvailable, ObservedAt: time.Now(), Windows: []providers.QuotaWindow{
		{Kind: "five-hour", Model: "gemini", UsedPercent: 25},
		{Kind: "weekly", Model: "claude", UsedPercent: 95},
		{Kind: "account-family", Model: "claude", UsedPercent: 35},
		{Kind: "five-hour", Model: "gemini-4-argon", UsedPercent: 100},
		{Kind: "model-observation", Model: "gemini-pro", UsedPercent: 100},
		{Kind: "monthly", Model: "gemini", UsedPercent: 100},
	}}
	q := normalQuotaCollectionSnapshot("google-antigravity", h)
	if len(q.Windows) != 2 || q.Windows[0].Family != "gem" || q.Windows[1].Family != "cla" {
		t.Fatalf("invalid family projection: %+v", q)
	}
	tiers, _ := resolveJEVQuotaTiers(nil)
	if s := jevQuotaFromWindows(q.Windows, "gemini-pro", time.Now(), tiers); s.UsedPercent == nil || *s.UsedPercent != 25 {
		t.Fatalf("family leak: %+v", s)
	}
	if s := jevQuotaFromWindows(q.Windows, "gemini-4-argon", time.Now(), tiers); s.Tier != jevQuotaUnknown {
		t.Fatalf("argon gained family evidence: %+v", s)
	}
}

func TestNormalQuotaCollectionBudget(t *testing.T) {
	for _, tt := range []struct {
		base string
		want int
	}{
		{"https://opencode.ai/zen/go/v1", 1}, {"https://api.cline.bot/api/v1", 1},
		{"https://ollama.com/v1", 2}, {"https://api.z.ai/api/coding/paas/v4", 1},
		{"https://api.synthetic.new/openai/v1", 1}, {"https://api.kimi.com/coding/v1", 1},
		{"https://api.commandcode.ai/provider/v1", 4},
		{"https://api.minimax.io/v1", 0}, {"https://openrouter.ai/api/v1", 0},
		{"https://api.deepseek.com/v1", 0}, {"https://api.z.ai.attacker.invalid/api/v1", 0},
	} {
		t.Run(tt.base, func(t *testing.T) {
			d := providerData{Metadata: &v1.ProviderConnection{ProviderId: "custom", BaseUrl: tt.base, AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: "fixture"}
			if got := normalQuotaCollectionBudget(d); got != tt.want {
				t.Fatalf("budget=%d want%d", got, tt.want)
			}
		})
	}
	for _, id := range []string{"openai", "anthropic", "anthropic2", "google-antigravity"} {
		d := providerData{Metadata: &v1.ProviderConnection{ProviderId: id, AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH, State: v1.ConnectionState_CONNECTION_STATE_READY}, Credential: &providers.Credential{ProjectID: "fixture"}}
		want := 0
		if id == "google-antigravity" {
			want = 3
		}
		if got := normalQuotaCollectionBudget(d); got != want {
			t.Fatalf("%s budget=%d", id, got)
		}
	}
}
