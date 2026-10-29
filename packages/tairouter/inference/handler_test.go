// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testAuth struct {
	denied   atomic.Bool
	dbDown   atomic.Bool
	released atomic.Int32
}

func (a *testAuth) Admit(ctx context.Context, s string) (Principal, func(), error) {
	p := Principal{OrganizationID: "org", UserID: "user", KeyID: "key", MembershipGeneration: 1}
	if e := a.BeforeSend(ctx, p); e != nil {
		return Principal{}, nil, e
	}
	if s != "client-secret" {
		return Principal{}, nil, ErrUnauthorized
	}
	return p, func() { a.released.Add(1) }, nil
}
func (a *testAuth) BeforeSend(ctx context.Context, _ Principal) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if a.dbDown.Load() {
		return ErrUnavailable
	}
	if a.denied.Load() {
		return ErrUnauthorized
	}
	return nil
}

func (a *testAuth) Dispatch(ctx context.Context, p Principal, start func(context.Context) error) error {
	if e := a.BeforeSend(ctx, p); e != nil {
		return e
	}
	return start(ctx)
}

type testRouter struct{ route Route }

func (r testRouter) Resolve(context.Context, Principal, string, Operation) (Route, error) {
	return r.route, nil
}
func (r testRouter) Models(context.Context, Principal) ([]Model, error) {
	return []Model{{ID: "m", Object: "model"}}, nil
}

type testCredentials struct {
	refreshes  atomic.Int32
	selections atomic.Int32
	version    string
}

func (c *testCredentials) Select(_ context.Context, _ Principal, _ Target, previous string) (Credential, error) {
	c.selections.Add(1)
	if previous != "" {
		return Credential{AccountID: "b", Secret: "second"}, nil
	}
	return Credential{AccountID: "a", Secret: "provider-secret", CredentialVersion: c.version}, nil
}
func (c *testCredentials) Refresh(context.Context, Principal, Target, Credential) (Credential, error) {
	c.refreshes.Add(1)
	return Credential{AccountID: "a", Secret: "refreshed"}, nil
}

type testUsage struct {
	mu      sync.Mutex
	records []UsageRecord
	fail    bool
}

func (u *testUsage) Record(_ context.Context, _ Principal, r UsageRecord) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.records = append(u.records, r)
	if u.fail {
		return errors.New("secret must never be logged")
	}
	return nil
}
func harness(t *testing.T, up http.HandlerFunc, wire Wire) (*Handler, *httptest.Server, *testAuth, *testCredentials, *testUsage) {
	t.Helper()
	srv := httptest.NewServer(up)
	t.Cleanup(srv.Close)
	auth := &testAuth{}
	creds := &testCredentials{}
	usage := &testUsage{}
	h, e := New(Config{Authorizer: auth, Credentials: creds, Usage: usage, Router: testRouter{Route{Targets: []Target{{BaseURL: srv.URL, Wire: wire, Auth: APIKey, Provider: "fixture", Model: "upstream-model"}}, MaxSends: 10}}, Destinations: DestinationPolicy{ApprovedOrigins: []string{srv.URL}}})
	if e != nil {
		t.Fatal(e)
	}
	return h, srv, auth, creds, usage
}
func call(h *Handler, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer client-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func fixture(wire Wire) string {
	switch wire {
	case Responses:
		return `{"id":"resp_fixture","object":"response","model":"m","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":7,"output_tokens":2}}`
	case Anthropic:
		return `{"id":"msg_fixture","type":"message","model":"m","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":7,"output_tokens":2}}`
	default:
		return `{"id":"chat_fixture","object":"chat.completion","model":"m","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`
	}
}
func streamFixture(w Wire) string {
	switch w {
	case Responses:
		return "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":" + fixture(w) + "}\n\n"
	case Anthropic:
		return "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7}}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
	default:
		return "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n"
	}
}
func TestProtocolMatrix(t *testing.T) {
	for _, in := range []Wire{Chat, Responses, Anthropic} {
		for _, out := range []Wire{Chat, Responses, Anthropic} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s_to_%s_stream_%t", in, out, stream), func(t *testing.T) {
					h, _, a, _, u := harness(t, func(w http.ResponseWriter, r *http.Request) {
						if r.Header.Get("Authorization") != "Bearer provider-secret" && r.Header.Get("x-api-key") != "provider-secret" {
							t.Error("wrong provider credential")
						}
						if r.URL.Path != wirePath(out) {
							t.Errorf("path %s", r.URL.Path)
						}
						raw, _ := io.ReadAll(r.Body)
						b, e := decode(raw)
						if e != nil || str(b["model"]) != "upstream-model" {
							t.Error("model was not routed")
						}
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							io.WriteString(w, streamFixture(out))
						} else {
							io.WriteString(w, fixture(out))
						}
					}, out)
					input := object{"model": "m", "stream": stream}
					if in == Responses {
						input["input"] = "hi"
					} else {
						input["messages"] = []any{object{"role": "user", "content": "hi"}}
					}
					b, _ := json.Marshal(input)
					w := call(h, "/v1"+wirePath(in), string(b))
					if w.Code != 200 || !strings.Contains(w.Body.String(), "hello") {
						t.Fatalf("%d %s", w.Code, w.Body.String())
					}
					if a.released.Load() != 1 {
						t.Fatal("lease leaked")
					}
					if len(u.records) != 1 || u.records[0].Usage.InputTokens == nil || *u.records[0].Usage.InputTokens != 7 {
						t.Fatalf("usage %+v", u.records)
					}
				})
			}
		}
	}
}
func TestAmbiguousSendNeverReplays(t *testing.T) {
	var sends atomic.Int32
	h, _, _, _, u := harness(t, func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		c, _, e := w.(http.Hijacker).Hijack()
		if e != nil {
			t.Fatal(e)
		}
		c.Close()
	}, Chat)
	rt := h.c.Router.(testRouter)
	rt.route.Targets[0].Retry = RetryPolicy{TransientSends: 10, RotateOn429: true, FallbackOn5xx: true}
	rt.route.Targets = append(rt.route.Targets, rt.route.Targets[0])
	h.c.Router = rt
	w := call(h, "/v1/chat/completions", `{"model":"m","messages":[]}`)
	if w.Code != 429 || w.Header().Get("x-should-retry") != "false" || w.Header().Get("Retry-After") != "" || sends.Load() != 1 {
		t.Fatalf("%d %v sends=%d", w.Code, w.Header(), sends.Load())
	}
	if u.records[0].Usage.InputTokens != nil {
		t.Fatal("unknown usage became zero")
	}
}
func TestRevocationAndDBOutageBlockRecovery(t *testing.T) {
	for _, db := range []bool{false, true} {
		t.Run(fmt.Sprint(db), func(t *testing.T) {
			var auth *testAuth
			var sends atomic.Int32
			h, _, a, c, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				if db {
					auth.dbDown.Store(true)
				} else {
					auth.denied.Store(true)
				}
				w.WriteHeader(401)
			}, Chat)
			auth = a
			rt := h.c.Router.(testRouter)
			rt.route.Targets[0].Auth = OAuth
			rt.route.Targets[0].Retry.RefreshOn401 = true
			h.c.Router = rt
			w := call(h, "/v1/chat/completions", `{"model":"m","messages":[]}`)
			want := 401
			if db {
				want = 503
			}
			if w.Code != want || sends.Load() != 1 || c.refreshes.Load() != 0 {
				t.Fatalf("status=%d sends=%d refresh=%d", w.Code, sends.Load(), c.refreshes.Load())
			}
		})
	}
}
func TestRateBudgetAndImageSingleSend(t *testing.T) {
	for _, image := range []bool{false, true} {
		t.Run(fmt.Sprint(image), func(t *testing.T) {
			var sends atomic.Int32
			h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(429)
			}, Chat)
			rt := h.c.Router.(testRouter)
			rt.route.MaxSends = 4
			rt.route.Targets[0].Retry.RateLimitRetries = 6
			h.c.Router = rt
			path := "/v1/chat/completions"
			want := int32(4)
			if image {
				path = "/v1/images/generations"
				want = 1
			}
			w := call(h, path, `{"model":"m","messages":[]}`)
			if w.Code != 429 || sends.Load() != want {
				t.Fatalf("%d sends %d", w.Code, sends.Load())
			}
		})
	}
}
func TestOversizedRetryAfterCanRotate(t *testing.T) {
	var sends atomic.Int32
	h, _, _, c, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		if r.Header.Get("Authorization") == "Bearer second" {
			io.WriteString(w, fixture(Chat))
			return
		}
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
	}, Chat)
	rt := h.c.Router.(testRouter)
	rt.route.Targets[0].Retry = RetryPolicy{RateLimitRetries: 6, MaxWait: time.Second, RotateOn429: true}
	h.c.Router = rt
	w := call(h, "/v1/chat/completions", `{"model":"m","messages":[]}`)
	if w.Code != 200 || sends.Load() != 2 || c.selections.Load() != 2 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
func TestCancellationReachesUpstream(t *testing.T) {
	seen := make(chan struct{})
	canceled := make(chan struct{})
	h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(seen)
		<-r.Context().Done()
		close(canceled)
	}, Chat)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer client-secret")
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), req) }()
	<-seen
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("upstream not canceled")
	}
	<-done
}
func TestTruncatedStreamCannotSucceed(t *testing.T) {
	h, _, _, _, u := harness(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}, Chat)
	w := call(h, "/v1/responses", `{"model":"m","input":"hi","stream":true}`)
	if strings.Contains(w.Body.String(), "response.completed") || !strings.Contains(w.Body.String(), "upstream_stream_incomplete") {
		t.Fatal(w.Body.String())
	}
	if u.records[0].Status != 502 {
		t.Fatal(u.records)
	}
}

type fixedResolver []netip.Addr

func (r fixedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r, nil
}
func TestDestinationPolicy(t *testing.T) {
	for _, raw := range []string{"http://public.example", "https://user:pass@example.com", "https://example.com/?x=1", "https://example.com/#x", "https://example.com.", "file:///tmp/secret"} {
		if _, _, e := (DestinationPolicy{}).client(raw); e == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, ip := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "169.254.169.254", "100.100.100.200", "10.0.0.1", "2001:db8::1"} {
		p := DestinationPolicy{Resolver: fixedResolver{netip.MustParseAddr(ip)}}
		c, _, e := p.client("https://provider.example")
		if e != nil {
			t.Fatal(e)
		}
		_, e = c.Get("https://provider.example/path")
		if !errors.Is(e, ErrDestination) {
			t.Errorf("%s: %v", ip, e)
		}
	}
}
func TestRedirectDoesNotLeakCredential(t *testing.T) {
	var other atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { other.Add(1) }))
	defer target.Close()
	h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }, Chat)
	w := call(h, "/v1/chat/completions", `{"model":"m","messages":[]}`)
	if w.Code != 403 || other.Load() != 0 {
		t.Fatalf("%d leaked=%d", w.Code, other.Load())
	}
}
func TestToolAndImageTranslation(t *testing.T) {
	b, _ := decode([]byte(`{"model":"m","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,YQ=="}]},{"type":"function_call","call_id":"call1","name":"lookup","arguments":"{\"x\":1}"},{"type":"function_call_output","call_id":"call1","output":"done"}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`))
	a, e := translateRequest(b, Responses, Anthropic)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(a)
	for _, want := range []string{"tool_use", "tool_result", "base64", "lookup"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s: %s", want, raw)
		}
	}
	if e = validateTools(result{Tools: []toolCall{{"id", "undeclared", "{}"}}}, b, Responses); e == nil {
		t.Fatal("undeclared tool accepted")
	}
}

func TestUpstreamReplayRefusalMarkerStopsRotation(t *testing.T) {
	var sends atomic.Int32
	h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"code":"upstream_reset_replay_refused"}}`)
	}, Chat)
	rt := h.c.Router.(testRouter)
	rt.route.Targets[0].Retry = RetryPolicy{RateLimitRetries: 6, RotateOn429: true, FallbackOn429: true}
	h.c.Router = rt
	w := call(h, "/v1/chat/completions", `{"model":"m","messages":[]}`)
	if sends.Load() != 1 || w.Header().Get("x-should-retry") != "false" || w.Header().Get("Retry-After") != "" {
		t.Fatalf("%d %v", sends.Load(), w.Header())
	}
}
