// SPDX-License-Identifier: Apache-2.0
package ollama

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

type fixtureAuth struct{ sends atomic.Int32 }

func (a *fixtureAuth) Admit(context.Context, string) (inference.Principal, func(), error) {
	return inference.Principal{OrganizationID: "org", UserID: "user", KeyID: "key"}, func() {}, nil
}
func (a *fixtureAuth) BeforeSend(context.Context, inference.Principal) error { return nil }
func (a *fixtureAuth) Dispatch(ctx context.Context, _ inference.Principal, send func(context.Context) error) error {
	a.sends.Add(1)
	return send(ctx)
}

type fixtureRouter struct{ target inference.Target }

func (r fixtureRouter) Resolve(context.Context, inference.Principal, string, inference.Operation) (inference.Route, error) {
	return inference.Route{Targets: []inference.Target{r.target}, MaxSends: 1}, nil
}
func (r fixtureRouter) Models(context.Context, inference.Principal) ([]inference.Model, error) {
	return nil, nil
}

type fixtureCredentials struct{}

func (fixtureCredentials) Select(context.Context, inference.Principal, inference.Target, string) (inference.Credential, error) {
	return inference.Credential{AccountID: "fixture", Secret: "must-not-reach-loopback", Headers: http.Header{"X-Api-Key": {"also-private"}}}, nil
}
func (fixtureCredentials) Refresh(context.Context, inference.Principal, inference.Target, inference.Credential) (inference.Credential, error) {
	return inference.Credential{}, inference.ErrNoCredential
}

type fixtureUsage struct{ record inference.UsageRecord }

func (u *fixtureUsage) Record(_ context.Context, _ inference.Principal, r inference.UsageRecord) error {
	u.record = r
	return nil
}
func TestCentralExecutorNativeWire(t *testing.T) {
	for _, tc := range []struct {
		path, body string
		stream     bool
	}{
		{"/v1/chat/completions", `{"model":"route","messages":[{"role":"user","content":"hi"}],"max_tokens":17}`, false},
		{"/v1/responses", `{"model":"route","input":"hi","max_output_tokens":17}`, false},
		{"/v1/messages", `{"model":"route","messages":[{"role":"user","content":"hi"}],"max_tokens":17}`, false},
		{"/v1/chat/completions", `{"model":"route","messages":[{"role":"user","content":"hi"}],"max_tokens":17,"stream":true}`, true},
	} {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			var sends atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				if r.URL.Path != "/api/chat" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "" {
					t.Errorf("bad transport %s %+v", r.URL, r.Header)
				}
				data, _ := io.ReadAll(r.Body)
				b, e := decode(data)
				if e != nil {
					t.Error(e)
				}
				if b["model"] != "upstream-model" || obj(b["options"])["num_predict"].(interface{ String() string }).String() != "17" {
					t.Errorf("bad body %s", data)
				}
				if tc.stream {
					w.Header().Set("Content-Type", "application/x-ndjson")
					io.WriteString(w, "{\"message\":{\"content\":\"answer\",\"thinking\":\"reason\"},\"done\":false}\n{\"done\":true,\"prompt_eval_count\":3,\"eval_count\":5}\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"message":{"content":"answer","thinking":"reason"},"done":true,"prompt_eval_count":3,"eval_count":5}`)
				}
			}))
			defer srv.Close()
			auth := &fixtureAuth{}
			usage := &fixtureUsage{}
			h, e := inference.New(inference.Config{Authorizer: auth, Router: fixtureRouter{inference.Target{BaseURL: srv.URL, Model: "upstream-model", Provider: "ollama-fixture", Wire: Wire, Auth: inference.APIKey}}, Credentials: fixtureCredentials{}, Usage: usage, Adapters: map[inference.Wire]inference.Adapter{Wire: New()}, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{srv.URL}}})
			if e != nil {
				t.Fatal(e)
			}
			r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer fixture")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "answer") {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if sends.Load() != 1 || auth.sends.Load() != 1 || usage.record.Sends != 1 {
				t.Fatalf("physical=%d auth=%d record=%d", sends.Load(), auth.sends.Load(), usage.record.Sends)
			}
			if usage.record.Usage.InputTokens == nil || *usage.record.Usage.InputTokens != 3 || usage.record.Usage.OutputTokens == nil || *usage.record.Usage.OutputTokens != 5 {
				t.Fatalf("usage %+v", usage.record.Usage)
			}
		})
	}
}
