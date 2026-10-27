// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

type quotaCollectionExecutor func(context.Context, core.Scope, *http.Request) (*http.Response, error)

func (f quotaCollectionExecutor) Do(c context.Context, s core.Scope, r *http.Request) (*http.Response, error) {
	return f(c, s, r)
}

func TestNormalQuotaCollectionDurableEvidence(t *testing.T) {
	for _, mode := range []string{"key", "antigravity", "fallback", "transient", "rejected", "budget", "rotated", "late-rotation"} {
		t.Run(mode, func(t *testing.T) {
			a, sc, key := dispatchFixture(t)
			ctx := context.Background()
			d := providerData{Metadata: &v1.ProviderConnection{Id: "quota-account", OrganizationId: sc.OrganizationID, ProviderId: "custom", BaseUrl: "https://opencode.ai/zen/go/v1", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: "fixture"}
			if mode == "antigravity" || mode == "fallback" {
				d.Metadata.ProviderId = "google-antigravity"
				d.Metadata.BaseUrl = ""
				d.Metadata.AuthKind = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH
				d.Credential = &providers.Credential{Scope: sc, Provider: "google-antigravity", AccessToken: "fixture", ProjectID: "fixture-project"}
			}
			putRoutingProvider(t, a, sc, d)
			p := admitRouting(t, a, key)
			_, version, e := a.providerVersion(ctx, sc, d.Metadata.Id)
			if e != nil {
				t.Fatal(e)
			}
			c := inference.Credential{AccountID: d.Metadata.Id, CredentialVersion: version, Secret: "fixture"}
			oldTime := time.Now().Add(-time.Minute)
			if e := a.writeNativeQuota(ctx, p, c.AccountID, version, nativeQuotaSnapshot{Windows: []nativeQuotaWindow{{Window: "weekly", Percent: 11, ObservedAt: oldTime}}}); e != nil {
				t.Fatal(e)
			}
			var sends atomic.Int32
			budgeted := inference.WithPhysicalSendBudget(ctx, func(c context.Context, start func(context.Context) error) error {
				if mode == "budget" {
					return inference.ErrUnavailable
				}
				sends.Add(1)
				return start(c)
			})
			executor := quotaCollectionExecutor(func(ctx context.Context, scope core.Scope, r *http.Request) (*http.Response, error) {
				if scope != sc {
					t.Error("wrong tenant scope")
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > normalQuotaCollectionTimeout {
					t.Error("missing quota request timeout")
				}
				if mode == "transient" {
					return nil, errors.New("fixture transport")
				}
				status := 200
				body := `{"usage":{"rolling":{"percent":27},"weekly":{"percent":93}}}`
				switch mode {
				case "rejected":
					status = 401
					body = `{}`
				case "antigravity":
					body = `{"groups":[{"displayName":"Gemini","buckets":[{"window":"5h","remainingFraction":0.25}]},{"displayName":"Claude","buckets":[{"window":"weekly","remainingFraction":0.1}]}]}`
				case "fallback":
					if strings.HasSuffix(r.URL.Path, "retrieveUserQuotaSummary") {
						status = 500
						body = `{}`
					} else {
						body = `{"models":{"gemini-pro":{"quotaInfo":{"remainingFraction":0.01}}}}`
					}
				}
				var reader io.ReadCloser = io.NopCloser(strings.NewReader(body))
				if mode == "late-rotation" {
					reader = &quotaCollectionRotatingBody{ReadCloser: reader, rotate: func() { d.Secret = "late-rotated"; putRoutingProvider(t, a, sc, d) }}
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: reader}, nil
			})
			if mode == "rotated" {
				d.Secret = "rotated"
				putRoutingProvider(t, a, sc, d)
			}
			err := a.collectNormalQuotaWithExecutor(budgeted, p, inference.Target{}, c, executor)
			if mode == "late-rotation" {
				if err == nil || sends.Load() != 1 {
					t.Fatal("late retired publication accepted")
				}
				return
			}
			if mode == "rotated" {
				if err == nil || sends.Load() != 0 {
					t.Fatal("retired version was collected")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			q, ok, err := a.readNativeQuota(ctx, p, c.AccountID, version)
			if err != nil || !ok {
				t.Fatalf("read=%v %v", ok, err)
			}
			switch mode {
			case "key":
				if len(q.Windows) != 2 || q.Windows[0].Percent != 27 || q.Windows[1].Percent != 93 {
					t.Fatalf("key projection: %+v", q)
				}
			case "antigravity":
				if len(q.Windows) != 2 || q.Windows[0].Family != "gem" || q.Windows[1].Family != "cla" {
					t.Fatalf("summary projection: %+v", q)
				}
			case "rejected":
				if len(q.Windows) != 0 {
					t.Fatal("rejected key evidence survived")
				}
			default:
				if len(q.Windows) != 1 || q.Windows[0].Percent != 11 || !q.Windows[0].ObservedAt.Equal(oldTime) {
					t.Fatalf("old evidence was changed: %+v", q)
				}
			}
			// A separate request adapter and inference key share the durable collection cadence.
			_, key2, e := a.s.IssueKey(ctx, sc, "second", nil)
			if e != nil {
				t.Fatal(e)
			}
			b := &inferenceAdapter{s: a.s, telemetry: a.telemetry}
			p2 := admitRouting(t, b, key2)
			count := sends.Load()
			if e := b.collectNormalQuotaWithExecutor(budgeted, p2, inference.Target{}, c, executor); e != nil {
				t.Fatal(e)
			}
			if sends.Load() != count {
				t.Fatal("second instance bypassed collection cadence")
			}
		})
	}
}

func TestNormalQuotaCollectionMaximumPhysicalExchanges(t *testing.T) {
	scope := core.Scope{OrganizationID: "fixture-org", UserID: "fixture-user", Generation: 1}
	for _, tc := range []struct {
		name, base string
		want       int
	}{
		{"opencode", "https://opencode.ai/zen/go/v1", 1},
		{"cline", "https://api.cline.bot/api/v1", 1},
		{"zai", "https://api.z.ai/api/coding/paas/v4", 1},
		{"synthetic", "https://api.synthetic.new/openai/v1", 1},
		{"kimi", "https://api.kimi.com/coding/v1", 1},
		{"ollama", "https://ollama.com/v1", 2},
		{"command", "https://api.commandcode.ai/provider/v1", 4},
		{"antigravity", "", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := providerData{Metadata: &v1.ProviderConnection{ProviderId: "custom", BaseUrl: tc.base, AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: "fixture"}
			if tc.name == "antigravity" {
				d.Metadata.ProviderId = "google-antigravity"
				d.Metadata.AuthKind = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH
				d.Credential = &providers.Credential{Scope: scope, Provider: "google-antigravity", AccessToken: "fixture", ProjectID: "project"}
			}
			calls := 0
			x := quotaCollectionExecutor(func(ctx context.Context, s core.Scope, r *http.Request) (*http.Response, error) {
				calls++
				status := 200
				body := `{}`
				if tc.name == "command" {
					switch r.URL.Path {
					case "/alpha/whoami":
						body = `{"data":{"org":{"id":"org"}}}`
					case "/alpha/billing/credits":
						body = `{"credits":{"monthlyCredits":10},"windowLimits":{"fiveHour":{"percent":20}}}`
					case "/alpha/billing/subscriptions":
						body = `{"currentPeriodStart":"2026-10-01T00:00:00Z"}`
					case "/alpha/usage/summary":
						body = `{"totalCost":5}`
					default:
						t.Errorf("unexpected command path %s", r.URL.Path)
					}
				}
				if tc.name == "antigravity" && calls == 1 {
					status = 403
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			_, _ = providers.NewAuth(normalQuotaExecutor{x}).ProbeConnectionHealth(context.Background(), scope, providerConnection(d))
			if calls != tc.want || normalQuotaCollectionBudget(d) != tc.want {
				t.Fatalf("physical calls=%d budget=%d want=%d", calls, normalQuotaCollectionBudget(d), tc.want)
			}
		})
	}
}

// The rotation occurs while decoding, after the physical-write gate has ended.
type quotaCollectionRotatingBody struct {
	io.ReadCloser
	rotate func()
}

func (b *quotaCollectionRotatingBody) Read(p []byte) (int, error) {
	if b.rotate != nil {
		f := b.rotate
		b.rotate = nil
		f()
	}
	return b.ReadCloser.Read(p)
}
