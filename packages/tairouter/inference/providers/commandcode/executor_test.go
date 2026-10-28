// SPDX-License-Identifier: Apache-2.0
package commandcode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

type fixtureAuth struct{}

func (fixtureAuth) Admit(context.Context, string) (inference.Principal, func(), error) {
	return inference.Principal{OrganizationID: "org", UserID: "user", KeyID: "key"}, func() {}, nil
}
func (fixtureAuth) BeforeSend(ctx context.Context, _ inference.Principal) error { return ctx.Err() }
func (fixtureAuth) Dispatch(ctx context.Context, _ inference.Principal, send func(context.Context) error) error {
	return send(ctx)
}

type fixtureRouter struct {
	url    string
	budget int
}

func (r fixtureRouter) Resolve(context.Context, inference.Principal, string, inference.Operation) (inference.Route, error) {
	return inference.Route{Targets: []inference.Target{{Provider: "command-code", BaseURL: r.url, Model: "glm-5.2", Wire: Wire, Auth: inference.OAuth}}, MaxSends: r.budget}, nil
}
func (fixtureRouter) Models(context.Context, inference.Principal) ([]inference.Model, error) {
	return nil, nil
}

type fixtureCredentials struct{}

func (fixtureCredentials) Select(_ context.Context, _ inference.Principal, _ inference.Target, previous string) (inference.Credential, error) {
	id := "a"
	if previous == "a" {
		id = "b"
	}
	if previous == "b" {
		return inference.Credential{}, inference.ErrNoCredential
	}
	return inference.Credential{AccountID: id, Secret: "fixture-" + id}, nil
}
func (fixtureCredentials) Refresh(context.Context, inference.Principal, inference.Target, inference.Credential) (inference.Credential, error) {
	return inference.Credential{}, inference.ErrNoCredential
}

type fixtureUsage struct{ record inference.UsageRecord }

func (u *fixtureUsage) Record(_ context.Context, _ inference.Principal, r inference.UsageRecord) error {
	u.record = r
	return nil
}
func TestComposedExecutor(t *testing.T) {
	for _, tc := range []struct {
		name, path, body      string
		status, budget, sends int
		refusal               string
	}{
		{"buffered chat", "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, 200, 1, 1, ""},
		{"stream responses", "/v1/responses", `{"model":"m","input":"hi","stream":true}`, 200, 1, 1, ""},
		{"buffered anthropic", "/v1/messages", `{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":64}`, 200, 1, 1, ""},
		{"model plan", "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, 400, 3, 1, "plan"},
		{"credit rotation", "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, 200, 2, 2, "credits"},
		{"credit budget", "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, 429, 1, 1, "credits"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sends atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := sends.Add(1)
				if r.URL.Path != "/alpha/generate" {
					t.Errorf("wrong endpoint %s", r.URL.Path)
				}
				var b object
				if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
					t.Error(err)
				}
				p := obj(b["params"])
				if p["stream"] != true || p["model"] != "zai-org/GLM-5.2" || b["config"] == nil {
					t.Errorf("not proprietary %v", b)
				}
				if tc.refusal == "plan" {
					w.WriteHeader(403)
					io.WriteString(w, `{"success":false,"error":{"code":"FORBIDDEN","message":"MODEL_NOT_IN_PLAN: unsupported reasoning_effort"}}`)
					return
				}
				if tc.refusal == "credits" && n == 1 {
					w.WriteHeader(400)
					io.WriteString(w, `{"success":false,"error":{"code":"BAD_REQUEST","message":"You have insufficient credits to make this request."}}`)
					return
				}
				if tc.refusal == "credits" && r.Header.Get("Authorization") != "Bearer fixture-b" {
					t.Error("account did not rotate")
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				io.WriteString(w, "{\"type\":\"text-delta\",\"text\":\"ok\"}\n{\"type\":\"finish\",\"finishReason\":\"stop\",\"usage\":{\"inputTokens\":2,\"outputTokens\":1}}\n")
			}))
			defer server.Close()
			u := &fixtureUsage{}
			a := New()
			h, e := inference.New(inference.Config{Authorizer: fixtureAuth{}, Router: fixtureRouter{server.URL, tc.budget}, Credentials: fixtureCredentials{}, Usage: u, Adapters: map[inference.Wire]inference.Adapter{Wire: a}, RecoveryPolicies: map[inference.Wire]inference.HTTPRecoveryPolicy{Wire: a}, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{server.URL}}})
			if e != nil {
				t.Fatal(e)
			}
			r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer mock-client")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || int(sends.Load()) != tc.sends {
				t.Fatalf("status=%d sends=%d body=%s", w.Code, sends.Load(), w.Body.String())
			}
			if tc.status == 200 {
				if !strings.Contains(w.Body.String(), "ok") || u.record.Usage.InputTokens == nil || *u.record.Usage.InputTokens != 2 {
					t.Fatalf("output/accounting %s %+v", w.Body.String(), u.record)
				}
			}
		})
	}
}
