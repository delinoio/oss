// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestPrivateProfileBinding(t *testing.T) {
	c := Credential{Scope: testScope, Provider: "anthropic", AccessToken: "first", AccountID: "account"}
	a, _ := newMock(t, responseStep{"GET", "https://api.anthropic.com/api/oauth/profile", 200, `{"account":{"uuid":"account","email":"test@example.test"},"organization":{"rate_limit_tier":"default_claude_max"}}`, nil})
	p, err := a.Profile(context.Background(), testScope, c)
	if err != nil || p.Plan != "max" || !p.Matches(c) {
		t.Fatal(p, err)
	}
	c.AccessToken = "rotated"
	if p.Matches(c) {
		t.Fatal("proof survives rotation")
	}
	a, _ = newMock(t, responseStep{"GET", "https://api.anthropic.com/api/oauth/profile", 200, `{"account":{"uuid":"different"}}`, nil})
	if _, err := a.Profile(context.Background(), testScope, c); !errors.Is(err, ErrReauthenticate) {
		t.Fatal(err)
	}
	c.Provider = "google-antigravity"
	a, _ = newMock(t, responseStep{"GET", "https://www.googleapis.com/oauth2/v2/userinfo", 200, `{"id":"account","email":"test@example.test"}`, nil})
	if p, err := a.Profile(context.Background(), testScope, c); err != nil || p.AccountID != "account" {
		t.Fatal(p, err)
	}
}
func TestProviderHealth(t *testing.T) {
	cases := []struct {
		provider, endpoint, body string
		windows, balances        int
	}{
		{"anthropic", "https://api.anthropic.com/api/oauth/usage", `{"five_hour":{"utilization":42,"resets_at":"2030-01-01T00:00:00Z"},"seven_day_opus":{"utilization":75}}`, 2, 0},
		{"openai", "https://chatgpt.com/backend-api/wham/usage", `{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000,"reset_at":1900000000}}}`, 1, 0},
		{"dsh-account", "https://platform.deepseek.com/api/v0/users/get_user_summary", `{"code":0,"data":{"biz_code":0,"biz_data":{"user_summary":{"total_costs":0}}}}`, 0, 0},
		{"workbuddy-ai", "https://www.workbuddy.ai/billing/meter/get-user-resource-summary", `{"code":0,"data":{"Packages":[{"CycleTotalCapacity":20,"CycleUsedCapacity":4,"CycleFrozenCapacity":2}]}}`, 0, 1},
	}
	for _, tt := range cases {
		t.Run(tt.provider, func(t *testing.T) {
			method := "GET"
			if tt.provider == "workbuddy-ai" {
				method = "POST"
			}
			a, _ := newMock(t, responseStep{method, tt.endpoint, 200, tt.body, nil})
			c := Credential{Scope: testScope, Provider: tt.provider, AccessToken: "token", Metadata: map[string]string{"uid": "user", "domain": "www.workbuddy.ai"}}
			h, e := a.ProbeHealth(context.Background(), testScope, c)
			if e != nil || h.State != HealthAvailable || len(h.Windows) != tt.windows || len(h.Balances) != tt.balances {
				t.Fatalf("%+v %v", h, e)
			}
		})
	}
	t.Run("kiro overage", func(t *testing.T) {
		for _, overage := range []string{"ENABLED", "DISABLED"} {
			c := Credential{Scope: testScope, Provider: "kiro", AccessToken: "token", Metadata: map[string]string{"clientId": "client", "apiRegion": "eu-central-1"}}
			endpoint, _, _, _ := kiroManagement(c, true)
			a, _ := newMock(t, responseStep{"POST", endpoint, 200, `{"usageBreakdownList":[{"resourceType":"STORAGE","currentUsage":1,"usageLimit":1},{"resourceType":"CREDIT","currentUsageWithPrecision":10.5,"usageLimitWithPrecision":10}],"overageConfiguration":{"overageStatus":"` + overage + `"}}`, func(t *testing.T, r *http.Request, b []byte) {
				if r.Header.Get("X-Amz-Target") != "AmazonCodeWhispererService.GetUsageLimits" {
					t.Fatal("wrong target")
				}
			}})
			h, e := a.ProbeHealth(context.Background(), testScope, c)
			if e != nil || h.Exhausted == nil || *h.Exhausted != (overage == "DISABLED") {
				t.Fatal(h, e)
			}
		}
	})
	t.Run("unknown and rejection", func(t *testing.T) {
		for _, status := range []int{200, 401, 403} {
			a, _ := newMock(t, responseStep{"GET", "https://api.anthropic.com/api/oauth/usage", status, `{}`, nil})
			c := Credential{Scope: testScope, Provider: "anthropic", AccessToken: "token"}
			h, e := a.ProbeHealth(context.Background(), testScope, c)
			want := HealthUnknown
			if status != 200 {
				want = HealthRejected
			}
			if e != nil || h.State != want || len(h.Windows) != 0 {
				t.Fatal(h, e)
			}
		}
	})
}
func TestAdditionalAccountHealth(t *testing.T) {
	cases := []struct{ provider, method, url, body string }{
		{"cursor", "POST", "https://api2.cursor.sh/aiserver.v1.DashboardService/GetCurrentPeriodUsage", `{"planUsage":{"limit":100,"remaining":20,"autoPercentUsed":10},"billingCycleEnd":"2030-01-01T00:00:00Z"}`},
		{"xai", "GET", "https://cli-chat-proxy.grok.com/v1/billing?format=credits", `{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","end":"2030-01-01T00:00:00Z"}}}`},
		{"meta-muse", "POST", "https://api.meta.ai/muse-code/key", `{"api_key":"DO_NOT_REPLACE","subs_usage":{"window":{"used_percent":12,"window_duration_mins":600},"weekly":{"used_percent":30}}}`},
		{"kimi", "GET", "https://api.kimi.com/coding/v1/usages", `{"data":{"usage":{"limit":100,"remaining":50},"limits":[{"window":{"duration":5,"timeUnit":"HOUR"},"detail":{"limit":100,"used":25}}]}}`},
	}
	for _, tt := range cases {
		t.Run(tt.provider, func(t *testing.T) {
			a, _ := newMock(t, responseStep{tt.method, tt.url, 200, tt.body, func(t *testing.T, r *http.Request, b []byte) {
				if tt.provider == "meta-muse" && (string(b) != "{}" || r.Header.Get("Authorization") != "Bearer account-token") {
					t.Fatal("must not onboard or use inference key")
				}
			}})
			c := Credential{Scope: testScope, Provider: tt.provider, AccessToken: "inference-token", AccountID: "user", Metadata: map[string]string{"oauthAccessToken": "account-token"}}
			h, e := a.ProbeHealth(context.Background(), testScope, c)
			if e != nil || h.State != HealthAvailable || len(h.Windows) == 0 {
				t.Fatal(h, e)
			}
			if tt.provider == "meta-muse" && (h.Windows[0].Kind == "five-hour" || c.AccessToken != "inference-token") {
				t.Fatal("mislabeled duration or changed key")
			}
		})
	}
	t.Run("command-code org scope", func(t *testing.T) {
		a, _ := newMock(t, responseStep{"GET", "https://api.commandcode.ai/alpha/whoami", 200, `{"data":{"org":{"id":"personal team"}}}`, nil}, responseStep{"GET", "https://api.commandcode.ai/alpha/billing/credits?orgId=personal+team", 200, `{"data":{"windowLimits":{"fiveHour":{"cap":100,"used":25}}}}`, nil})
		h, e := a.ProbeHealth(context.Background(), testScope, Credential{Scope: testScope, Provider: "command-code", AccessToken: "token"})
		if e != nil || len(h.Windows) != 1 {
			t.Fatal(h, e)
		}
	})
	t.Run("muse expired account token leaves inference health unknown", func(t *testing.T) {
		a, _ := newMock(t, responseStep{"POST", "https://api.meta.ai/muse-code/key", 401, `{}`, nil})
		h, e := a.ProbeHealth(context.Background(), testScope, Credential{Scope: testScope, Provider: "meta-muse", AccessToken: "key", Metadata: map[string]string{"oauthAccessToken": "expired"}})
		if e != nil || h.State != HealthUnknown {
			t.Fatal(h, e)
		}
	})
	t.Run("devin credit and dated quota", func(t *testing.T) {
		plan := protoNumber(35, 1)
		status := append(protoNumber(6, 40), protoNumber(8, 60)...)
		status = append(status, protoNumber(14, 90)...)
		status = append(status, protoNumber(17, 1900000000)...)
		body := append(protoBytes(1, protoBytes(13, status)), protoBytes(2, plan)...)
		a, _ := newMock(t, responseStep{"POST", "https://server.codeium.com/exa.seat_management_pb.SeatManagementService/GetUserStatus", 200, string(body), nil})
		h, e := a.ProbeHealth(context.Background(), testScope, Credential{Scope: testScope, Provider: "devin", AccessToken: "token"})
		if e != nil || len(h.Windows) != 2 || h.Windows[0].UsedPercent != 10 || h.Windows[1].UsedPercent != 40 {
			t.Fatal(h, e)
		}
	})
}
func TestGoogleHealthRecoveryAndModelFallback(t *testing.T) {
	c := Credential{Scope: testScope, Provider: "google-antigravity", AccessToken: "token", ProjectID: "project"}
	base := "https://daily-cloudcode-pa.googleapis.com/v1internal:"
	a, _ := newMock(t, responseStep{"POST", base + "retrieveUserQuotaSummary", 403, `{}`, nil}, responseStep{"POST", base + "retrieveUserQuotaSummary", 200, `{"groups":[{"displayName":"Gemini","buckets":[{"bucketId":"argon-five-hour","remaining":{"remainingFraction":0.25}},{"window":"weekly","remainingPercentage":0.5}]}]}`, func(t *testing.T, r *http.Request, b []byte) {
		if r.Header.Get("User-Agent") != "antigravity/1.0" {
			t.Fatal("missing compatibility recovery")
		}
	}})
	h, e := a.ProbeHealth(context.Background(), testScope, c)
	if e != nil || len(h.Windows) != 2 || h.Windows[0].Model != "gemini-4-argon" || h.Windows[0].UsedPercent != 75 {
		t.Fatal(h, e)
	}
	a, _ = newMock(t, responseStep{"POST", base + "retrieveUserQuotaSummary", 200, `{}`, nil}, responseStep{"POST", base + "fetchAvailableModels", 200, `{"models":{"gemini-model":{"quotaInfo":{"remainingFraction":0.5}},"gemini-4-argon":{"quotaInfo":{"remainingFraction":0.75}}}}`, nil})
	h, e = a.ProbeHealth(context.Background(), testScope, c)
	if e != nil || len(h.Windows) != 3 {
		t.Fatal(h, e)
	}
	a, _ = newMock(t, responseStep{"POST", base + "retrieveUserQuotaSummary", 401, `{}`, nil})
	h, e = a.ProbeHealth(context.Background(), testScope, c)
	if e != nil || h.State != HealthUnknown {
		t.Fatal("quota denial must not retire inference", h, e)
	}
}
func TestWorkBuddyTeamAndCreditExpiry(t *testing.T) {
	c := Credential{Scope: testScope, Provider: "workbuddy-ai", AccessToken: "token", Metadata: map[string]string{"uid": "user", "domain": "www.workbuddy.ai", "enterpriseId": "team"}}
	base := "https://www.workbuddy.ai/billing/meter/"
	a, _ := newMock(t, responseStep{"POST", base + "get-user-resource-summary", 200, `{"code":0,"data":{"Packages":[]}}`, nil}, responseStep{"POST", base + "get-enterprise-user-usage", 200, `{"code":0,"data":{"limitNum":100,"credit":25}}`, nil})
	h, e := a.ProbeHealth(context.Background(), testScope, c)
	if e != nil || len(h.Balances) != 1 || h.Balances[0].Remaining != 75 {
		t.Fatal(h, e)
	}
	a, _ = newMock(t, responseStep{"POST", base + "get-user-resource-summary", 200, `{"code":0,"data":{"Packages":[{"PackageCode":"plan","CycleTotalCapacity":100,"CycleUsedCapacity":20}]}}`, nil}, responseStep{"POST", base + "get-user-resource-paid-packages", 200, `{"code":0,"data":{"Accounts":[{"PackageCode":"plan","CycleCapacityRemainPrecise":20,"ExpiredTime":"2030-01-01 00:00:00"}],"TotalCount":1}}`, nil}, responseStep{"POST", base + "get-user-resource-free-packages", 200, `{"code":0,"data":{"Accounts":[]}}`, nil})
	h, e = a.ProbeHealth(context.Background(), testScope, c)
	if e != nil || len(h.Balances) != 1 || h.Balances[0].ExpiringAmount != 20 || h.Balances[0].ExpiresAt.IsZero() {
		t.Fatal(h, e)
	}
}
func TestHealthScopeAndCancellationBeforeSend(t *testing.T) {
	a, _ := newMock(t)
	c := Credential{Scope: testScope, Provider: "anthropic", AccessToken: "token"}
	other := testScope
	other.Generation++
	if _, e := a.ProbeHealth(context.Background(), other, c); !errors.Is(e, ErrScope) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := a.ProbeHealth(ctx, testScope, c); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := a.Profile(ctx, testScope, c); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestProfileEnrichmentDoesNotMutateInput(t *testing.T) {
	c := Credential{Scope: testScope, Provider: "anthropic", AccessToken: "token", Metadata: map[string]string{"untouched": "value"}}
	a, _ := newMock(t, responseStep{"GET", "https://api.anthropic.com/api/oauth/profile", 200, `{"account":{"uuid":"account"},"organization":{"rate_limit_tier":"default_claude_max"}}`, nil})
	fresh, p, e := a.EnrichCredential(context.Background(), testScope, c)
	if e != nil || fresh.AccountID != "account" || fresh.Metadata["profilePlan"] != "max" || c.Metadata["profilePlan"] != "" {
		t.Fatal(fresh, p, e)
	}
	other := fresh
	other.Scope.Generation++
	if p.Matches(other) {
		t.Fatal("cross-scope proof accepted")
	}
}
