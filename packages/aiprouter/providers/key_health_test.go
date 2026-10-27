// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestKeyHealthProviderMappings(t *testing.T) {
	cases := []struct {
		kind, body        string
		windows, balances int
	}{
		{"openrouter", `{"data":{"limit":100,"usage":500,"limit_remaining":75}}`, 1, 1},
		{"opencode-go", `{"usage":{"rolling":{"percent":25},"weekly":{"percent":30}}}`, 2, 0},
		{"deepseek", `{"balance_infos":[{"currency":"CNY","total_balance":"0.25"}]}`, 0, 1},
		{"cline", `{"data":{"limits":[{"type":"five_hour","percentUsed":10}]}}`, 1, 0},
		{"ollama", `{"included":{"session":{"remaining_percent":75},"allowance_usd":100,"balance_usd":25}}`, 1, 1},
		{"zai", `{"data":{"limits":[{"type":"TIME_LIMIT","unit":3,"number":5,"percentage":99},{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":10}]}}`, 1, 0},
		{"minimax", `{"base_resp":{"status_code":0},"model_remains":[{"model_name":"general","current_interval_remaining_percent":80}]}`, 1, 0},
		{"moonshot", `{"data":{"available_balance":0}}`, 0, 1},
		{"venice", `{"balance":10,"diem_epoch_allocated":100,"diem_epoch_used":50}`, 1, 1},
		{"synthetic", `{"rollingFiveHourLimit":10,"weeklyTokenLimit":20,"search":{"hourly":100}}`, 2, 0},
		{"deepinfra", `{"stripe_balance":-5,"spending_limit":100,"total_amount_due":20}`, 1, 1},
		{"neuralwatt", `{"subscription":{"kwh_included":10,"kwh_used":1},"balance":{"total_credits_usd":10,"credits_remaining_usd":8}}`, 1, 1},
		{"abliteration", `{"data":{"total_credits":10,"total_usage":2}}`, 1, 1},
	}
	for _, tt := range cases {
		t.Run(tt.kind, func(t *testing.T) {
			var m map[string]any
			if e := json.Unmarshal([]byte(tt.body), &m); e != nil {
				t.Fatal(e)
			}
			h := parseKeyHealth(Health{State: HealthUnknown}, tt.kind, m, "https://api.moonshot.cn/v1/users/me/balance")
			if h.State != HealthAvailable || len(h.Windows) != tt.windows || len(h.Balances) != tt.balances {
				t.Fatalf("%+v", h)
			}
			if tt.kind == "openrouter" && h.Windows[0].UsedPercent != 25 {
				t.Fatal("used lifetime instead of remaining")
			}
			if tt.kind == "moonshot" && h.Balances[0].Unit != "CNY" {
				t.Fatal("wrong currency")
			}
		})
	}
}
func TestKeyHealthDestinationAndAuthorization(t *testing.T) {
	a, _ := newMock(t)
	h, e := a.ProbeConnectionHealth(context.Background(), testScope, Connection{Provider: "custom", Wire: Chat, BaseURL: "https://openrouter.ai.evil.test/api/v1", APIKey: "secret"})
	if e != nil || h.State != HealthUnknown {
		t.Fatal(h, e)
	}
	a, _ = newMock(t, responseStep{"GET", "https://open.bigmodel.cn/api/monitor/usage/quota/limit", 200, `{"data":{"limits":[]}}`, func(t *testing.T, r *http.Request, b []byte) {
		if r.Header.Get("Authorization") != "private-key" {
			t.Fatal("CN scheme")
		}
	}})
	h, e = a.ProbeConnectionHealth(context.Background(), testScope, Connection{Provider: "custom", Wire: Chat, BaseURL: "https://open.bigmodel.cn/api/v1", APIKey: "private-key"})
	if e != nil || h.State != HealthAvailable || len(h.Windows) != 0 {
		t.Fatal(h, e)
	}
	a, _ = newMock(t, responseStep{"GET", "https://api.a6api.com/dashboard/billing/subscription", 200, `{"hard_limit_usd":10}`, nil}, responseStep{"GET", "https://api.a6api.com/api/usage/token/", 200, `{"total_granted":1000,"total_used":250,"total_available":750}`, nil})
	h, e = a.ProbeConnectionHealth(context.Background(), testScope, Connection{Provider: "custom", Wire: Chat, BaseURL: "https://api.a6api.com/v1", APIKey: "key"})
	if e != nil || len(h.Balances) != 1 || h.Balances[0].Remaining != 7.5 {
		t.Fatal(h, e)
	}
	if _, e := a6Health(Health{}, map[string]any{"hard_limit_usd": float64(10)}, map[string]any{"total_granted": float64(1000), "total_used": float64(250), "total_available": float64(800)}); e == nil {
		t.Fatal("accepted inconsistent units")
	}
}
