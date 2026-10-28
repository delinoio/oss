// SPDX-License-Identifier: Apache-2.0
package google

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

type repairAuth struct{ denied atomic.Bool }

func (a *repairAuth) Admit(context.Context, string) (inference.Principal, func(), error) {
	return inference.Principal{OrganizationID: "org", UserID: "user", KeyID: "key"}, func() {}, nil
}
func (a *repairAuth) BeforeSend(ctx context.Context, _ inference.Principal) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if a.denied.Load() {
		return inference.ErrUnauthorized
	}
	return nil
}
func (a *repairAuth) Dispatch(ctx context.Context, p inference.Principal, send func(context.Context) error) error {
	if e := a.BeforeSend(ctx, p); e != nil {
		return e
	}
	return send(ctx)
}

type repairRouter struct{ route inference.Route }

func (r repairRouter) Resolve(context.Context, inference.Principal, string, inference.Operation) (inference.Route, error) {
	return r.route, nil
}
func (repairRouter) Models(context.Context, inference.Principal) ([]inference.Model, error) {
	return nil, nil
}

type repairCredentials struct{}

func (repairCredentials) Select(context.Context, inference.Principal, inference.Target, string) (inference.Credential, error) {
	return inference.Credential{AccountID: "account", Secret: "mock", Headers: http.Header{ProjectHeader: {"project"}}}, nil
}
func (repairCredentials) Refresh(context.Context, inference.Principal, inference.Target, inference.Credential) (inference.Credential, error) {
	return inference.Credential{}, inference.ErrNoCredential
}

type repairUsage struct{ record inference.UsageRecord }

func (u *repairUsage) Record(_ context.Context, _ inference.Principal, r inference.UsageRecord) error {
	u.record = r
	return nil
}

func TestCentralAuthorizedGoogleRepair(t *testing.T) {
	for _, tc := range []struct {
		name      string
		budget    int
		revoke    bool
		transient bool
		wantSends int
	}{
		{"changed body", 2, false, false, 2}, {"budget withheld", 1, false, false, 1}, {"revoked between sends", 2, true, false, 1}, {"transient after repair", 3, false, true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &repairAuth{}
			var sends atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := sends.Add(1)
				raw, _ := io.ReadAll(r.Body)
				body, e := decodeJSON(raw)
				if e != nil {
					t.Error(e)
				}
				gc := obj(obj(body["request"])["generationConfig"])
				w.Header().Set("Content-Type", "application/json")
				if n == 1 {
					if gc["thinkingConfig"] == nil {
						t.Error("fixture needs initial thinking config")
					}
					if tc.revoke {
						auth.denied.Store(true)
					}
					w.WriteHeader(400)
					io.WriteString(w, `{"error":{"status":"INVALID_ARGUMENT","message":"thinkingConfig unsupported"}}`)
					return
				}
				if gc["thinkingConfig"] != nil {
					t.Error("central repair sent unchanged config")
				}
				if tc.transient && n == 2 {
					w.WriteHeader(503)
					io.WriteString(w, `{"error":{"status":"UNAVAILABLE"}}`)
					return
				}
				io.WriteString(w, `{"response":{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}}`)
			}))
			defer server.Close()
			a := NewAntigravity(Options{})
			usage := &repairUsage{}
			h, e := inference.New(inference.Config{Authorizer: auth, Router: repairRouter{inference.Route{Targets: []inference.Target{{Provider: "google", BaseURL: server.URL, Model: "gemini-3.1-pro-preview", Wire: WireAntigravity, Auth: inference.OAuth, Retry: inference.RetryPolicy{TransientSends: 2}}}, MaxSends: tc.budget}}, Credentials: repairCredentials{}, Usage: usage, Adapters: map[inference.Wire]inference.Adapter{WireAntigravity: a}, RecoveryPolicies: map[inference.Wire]inference.HTTPRecoveryPolicy{WireAntigravity: a}, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{server.URL}}})
			if e != nil {
				t.Fatal(e)
			}
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"high"}`))
			r.Header.Set("Authorization", "Bearer mock-client")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if int(sends.Load()) != tc.wantSends || usage.record.Sends != tc.wantSends {
				t.Fatalf("physical=%d accounted=%d expected=%d", sends.Load(), usage.record.Sends, tc.wantSends)
			}
			if (w.Code == 200) != (tc.wantSends >= 2) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
