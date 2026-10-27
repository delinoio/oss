// SPDX-License-Identifier: Apache-2.0
package workbuddy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type fixtureAuth struct{ checks atomic.Int32 }

func (*fixtureAuth) Admit(context.Context, string) (inference.Principal, func(), error) {
	return inference.Principal{OrganizationID: "org", UserID: "user", KeyID: "key"}, func() {}, nil
}
func (a *fixtureAuth) BeforeSend(context.Context, inference.Principal) error {
	a.checks.Add(1)
	return nil
}
func (a *fixtureAuth) Dispatch(ctx context.Context, p inference.Principal, f func(context.Context) error) error {
	if e := a.BeforeSend(ctx, p); e != nil {
		return e
	}
	return f(ctx)
}

type fixtureRouter struct{ target inference.Target }

func (r fixtureRouter) Resolve(context.Context, inference.Principal, string, inference.Operation) (inference.Route, error) {
	return inference.Route{Targets: []inference.Target{r.target}, MaxSends: 1}, nil
}
func (fixtureRouter) Models(context.Context, inference.Principal) ([]inference.Model, error) {
	return nil, nil
}

type fixtureCredentials struct{}

func (fixtureCredentials) Select(context.Context, inference.Principal, inference.Target, string) (inference.Credential, error) {
	return fixture().Credential, nil
}
func (fixtureCredentials) Refresh(context.Context, inference.Principal, inference.Target, inference.Credential) (inference.Credential, error) {
	return inference.Credential{}, inference.ErrNoCredential
}

type fixtureUsage struct{ record inference.UsageRecord }

func (u *fixtureUsage) Record(_ context.Context, _ inference.Principal, r inference.UsageRecord) error {
	u.record = r
	return nil
}

// fixtureTransport substitutes a loopback destination only after the production
// adapter has validated/built its pinned request. Actual server registration and
// fixed-origin TLS transport are tested by the server acceptance owner.
type fixtureTransport struct {
	*Adapter
	url string
	t   *testing.T
}

func (a fixtureTransport) Build(ctx context.Context, in inference.AdapterInput) (inference.AdapterRequest, error) {
	in.Target.BaseURL = BaseURL
	r, e := a.Adapter.Build(ctx, in)
	if e != nil {
		return r, e
	}
	if r.URL != BaseURL+"/chat/completions" {
		a.t.Fatal(r.URL)
	}
	r.URL = a.url + "/chat/completions"
	return r, nil
}

func TestComposedExecutorBuffersForcedSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, wire := range []inference.Wire{inference.Chat, inference.Responses, inference.Anthropic} {
			t.Run(fmt.Sprintf("%s/stream=%t", wire, stream), func(t *testing.T) {
				var sends atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sends.Add(1)
					var b object
					if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
						t.Error(e)
					}
					if b["stream"] != true || r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("Authorization") != "Bearer fixture-access" || r.Header.Get("X-User-Id") != "fixture-user" || r.Header.Get("X-Tenant-Id") != "fixture-team" || r.Header.Get("X-Api-Key") != "" {
						t.Error("outbound policy mismatch")
					}
					if obj(arr(b["messages"])[0])["role"] != "system" {
						t.Error("missing system")
					}
					name := str(obj(obj(arr(b["tools"])[0])["function"])["name"])
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"fixture-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\n", name)
					io.WriteString(w, "data: {\"id\":\"chatcmpl-fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
				}))
				defer server.Close()
				target := fixture().Target
				target.BaseURL = server.URL
				target.Auth = inference.OAuth
				auth := &fixtureAuth{}
				usage := &fixtureUsage{}
				a := fixtureTransport{New(), server.URL, t}
				h, e := inference.New(inference.Config{Authorizer: auth, Router: fixtureRouter{target}, Credentials: fixtureCredentials{}, Usage: usage, Adapters: map[inference.Wire]inference.Adapter{Wire: a}, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{server.URL}}})
				if e != nil {
					t.Fatal(e)
				}
				path := "/v1/chat/completions"
				body := object{"model": "m", "stream": stream, "messages": []any{object{"role": "user", "content": "Hello"}}, "tools": []any{object{"type": "function", "function": object{"name": "lookup.item", "parameters": object{"type": "object"}}}}}
				if wire == inference.Responses {
					path = "/v1/responses"
					delete(body, "messages")
					body["input"] = "Hello"
					body["tools"] = []any{object{"type": "function", "name": "lookup.item", "parameters": object{"type": "object"}}}
				}
				if wire == inference.Anthropic {
					path = "/v1/messages"
					body["max_tokens"] = 50
					body["tools"] = []any{object{"name": "lookup.item", "input_schema": object{"type": "object"}}}
				}
				raw, _ := json.Marshal(body)
				request := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
				request.Header.Set("Authorization", "Bearer client")
				out := httptest.NewRecorder()
				h.ServeHTTP(out, request)
				if out.Code != 200 || !strings.Contains(out.Body.String(), "lookup.item") || strings.Contains(out.Body.String(), "wb_") {
					t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
				}
				if sends.Load() != 1 || usage.record.Sends != 1 || auth.checks.Load() == 0 {
					t.Fatal("executor accounting/authorization bypass")
				}
				if !stream && !json.Valid(out.Body.Bytes()) {
					t.Fatal("unary not buffered JSON")
				}
				if stream && !strings.HasPrefix(out.Header().Get("Content-Type"), "text/event-stream") {
					t.Fatal("stream not SSE")
				}
			})
		}
	}
}
