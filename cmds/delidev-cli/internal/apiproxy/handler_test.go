package apiproxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const fixtureKey = "proxy-upstream-private-fixture-key"

var fixtureToken = TokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{23}, 32))

type fixtureAuthority struct {
	key                      string
	scope                    Scope
	ctx                      context.Context
	cancel                   context.CancelFunc
	keys, releases, acquires atomic.Int32
	authorize                func(context.Context, ReferenceKind, string) error
	observe                  func(context.Context, ReferenceKind, string) error
}

func (a *fixtureAuthority) Acquire(ctx context.Context, token string) (*Lease, error) {
	a.acquires.Add(1)
	if token != fixtureToken {
		return nil, domain.Fail(domain.Unauthenticated, "private diagnostic must be discarded", "")
	}
	return &Lease{Scope: a.scope, Context: a.ctx, Release: func() { a.releases.Add(1) }, Key: func(ctx context.Context) ([]byte, error) {
		a.keys.Add(1)
		key := a.key
		if key == "" {
			key = fixtureKey
		}
		return []byte(key), ctx.Err()
	}, AuthorizeReference: a.authorize, ObserveReference: a.observe}, nil
}

type lockedLog struct {
	mu   sync.Mutex
	body bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.body.Write(p)
}
func (l *lockedLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.body.String() }

type proxyFixture struct {
	authority *fixtureAuthority
	server    *httptest.Server
	upstream  *httptest.Server
	logs      *lockedLog
	calls     atomic.Int32
}

func newProxyFixture(t *testing.T, protocol domain.APIProtocol, operations []Operation, handler http.HandlerFunc) *proxyFixture {
	t.Helper()
	f := &proxyFixture{logs: &lockedLog{}}
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.calls.Add(1); handler(w, r) }))
	ctx, cancel := context.WithCancel(context.Background())
	auth := domain.BearerAuth
	if protocol == domain.AnthropicMessages {
		auth = domain.APIKeyAuth
	}
	f.authority = &fixtureAuthority{scope: Scope{ExecutionID: domain.NewID(), SessionID: domain.NewID(), AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: "fixed-model", Provider: domain.Provider{Name: "Fixture", Endpoint: f.upstream.URL + "/provider", Protocol: protocol, Authentication: auth}, Operations: operations}, ctx: ctx, cancel: cancel}
	f.server = httptest.NewServer(New(f.authority, slog.New(slog.NewJSONHandler(f.logs, nil))))
	t.Cleanup(func() { cancel(); f.server.Close(); f.upstream.Close() })
	return f
}
func (f *proxyFixture) request(t *testing.T, path, body string, modify func(*http.Request)) (*http.Response, []byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, f.server.URL+Prefix+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+fixtureToken)
	r.Header.Set("Content-Type", "application/json")
	if modify != nil {
		modify(r)
	}
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		return response, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	return response, raw, err
}
func assertNoProxySecrets(t *testing.T, f *proxyFixture, raw []byte) {
	t.Helper()
	for _, secret := range []string{fixtureKey, fixtureToken, base64.StdEncoding.EncodeToString([]byte(fixtureKey))} {
		if bytes.Contains(raw, []byte(secret)) || strings.Contains(f.logs.String(), secret) {
			t.Fatal("protected key or execution credential escaped")
		}
	}
}

func TestNativeProtocolsPreserveAuthorizedJSONAndInjectOnlyServerKey(t *testing.T) {
	for _, tc := range []struct {
		protocol                domain.APIProtocol
		operation               Operation
		path, request, response string
	}{
		{domain.OpenAIChat, ChatCompletion, "/chat/completions", `{"model":"fixed-model","messages":[{"role":"user","content":"fixture prompt"}],"stream":false}`, `{"id":"chatcmpl-test","choices":[{"message":{"role":"assistant","content":"fixture result"},"finish_reason":"stop"}]}`},
		{domain.OpenAIResponses, ResponseCreate, "/responses", `{"model":"fixed-model","input":"fixture prompt","store":false}`, `{"id":"resp_test","object":"response","status":"completed","error":null,"output":[]}`},
		{domain.OpenAIResponses, ResponseCompact, "/responses/compact", `{"model":"fixed-model","input":[]}`, `{"id":"cmp_test","object":"response.compaction","output":[]}`},
		{domain.AnthropicMessages, MessageCreate, "/messages", `{"model":"fixed-model","messages":[{"role":"user","content":"fixture prompt"}],"max_tokens":32}`, `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"fixture result"}],"stop_reason":"end_turn"}`},
		{domain.AnthropicMessages, MessageCountTokens, "/messages/count_tokens", `{"model":"fixed-model","messages":[]}`, `{"input_tokens":3}`},
	} {
		t.Run(string(tc.operation), func(t *testing.T) {
			f := newProxyFixture(t, tc.protocol, []Operation{tc.operation}, func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				if string(raw) != tc.request || r.Method != "POST" || r.URL.String() != "/provider"+tc.path {
					t.Error("native request was translated or rerouted")
				}
				if r.Header.Get("HTTP-Referer") != "https://deli.dev" || r.Header.Get("Cookie") != "" || r.Header.Get("OpenAI-Organization") != "" || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("X-Forwarded-Host") != "" {
					t.Error("caller-controlled header crossed authority boundary")
				}
				if tc.protocol == domain.AnthropicMessages {
					if r.Header.Get("x-api-key") != fixtureKey || r.Header.Get("Authorization") != "" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("anthropic-beta") != "context-management-2025-06-27" {
						t.Error("Anthropic authentication/version mismatch")
					}
				} else if r.Header.Get("Authorization") != "Bearer "+fixtureKey || r.Header.Get("x-api-key") != "" {
					t.Error("OpenAI-compatible authentication mismatch")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Set-Cookie", fixtureKey)
				w.Header().Set("X-Request-Id", fixtureKey)
				fmt.Fprint(w, tc.response)
			})
			response, raw, err := f.request(t, tc.path, tc.request, func(r *http.Request) {
				r.Header.Set("HTTP-Referer", "https://untrusted.example")
				r.Header.Set("OpenAI-Organization", "other-account")
				r.Header.Set("Proxy-Authorization", "Basic untrusted")
				r.Header.Set("X-Forwarded-Host", "untrusted.example")
				if tc.protocol == domain.AnthropicMessages {
					r.Header.Del("Authorization")
					r.Header.Set("x-api-key", fixtureToken)
					r.Header.Set("anthropic-version", "untrusted")
					r.Header.Set("anthropic-beta", "context-management-2025-06-27")
				}
			})
			if err != nil || response.StatusCode != 200 || string(raw) != tc.response || f.calls.Load() != 1 || f.authority.keys.Load() != 1 {
				t.Fatalf("native forwarding: status=%v err=%v body=%s", response, err, raw)
			}
			if response.Header.Get("Set-Cookie") != "" || response.Header.Get("X-Request-Id") != "" || response.Header.Get("X-Delidev-Correlation-Id") == "" {
				t.Fatal("unsafe response headers or missing correlation")
			}
			assertNoProxySecrets(t, f, raw)
		})
	}
}

func TestProxyRejectsScopeEscapesBeforeSecretOrNetwork(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		modify           func(*http.Request)
	}{
		{"model", "/responses", `{"model":"other-model"}`, nil},
		{"missing-model", "/responses", `{"input":"hello"}`, nil},
		{"case-model", "/responses", `{"model":"fixed-model","Model":"other-model"}`, nil},
		{"duplicate", "/responses", `{"model":"fixed-model","model":"other-model"}`, nil},
		{"fallback", "/responses", `{"model":"fixed-model","models":["other-model"]}`, nil},
		{"query", "/responses?model=other-model", `{"model":"fixed-model"}`, nil},
		{"empty-query", "/responses?", `{"model":"fixed-model"}`, nil},
		{"wrong-operation", "/messages", `{"model":"fixed-model"}`, nil},
		{"encoded-path", "/respon%73es", `{"model":"fixed-model"}`, nil},
		{"unowned-native-state", "/responses", `{"model":"fixed-model","previous_response_id":"resp_foreign"}`, nil},
		{"unowned-conversation", "/responses", `{"model":"fixed-model","conversation":{"id":"conv_foreign"}}`, nil},
		{"browser", "/responses", `{"model":"fixed-model"}`, func(r *http.Request) { r.Header.Set("Origin", "http://localhost") }},
		{"duplicate-auth", "/responses", `{"model":"fixed-model"}`, func(r *http.Request) { r.Header.Add("Authorization", "Bearer "+fixtureToken) }},
		{"mixed-auth", "/responses", `{"model":"fixed-model"}`, func(r *http.Request) { r.Header.Set("x-api-key", fixtureToken) }},
		{"compressed", "/responses", `{"model":"fixed-model"}`, func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }},
		{"owner-token", "/responses", `{"model":"fixed-model"}`, func(r *http.Request) { r.Header.Set("Authorization", "Bearer owner-fixture-token") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProxyFixture(t, domain.OpenAIResponses, []Operation{ResponseCreate}, func(http.ResponseWriter, *http.Request) { t.Error("unauthorized request reached provider") })
			response, raw, err := f.request(t, tc.path, tc.body, tc.modify)
			if err != nil || response.StatusCode < 400 || f.calls.Load() != 0 || f.authority.keys.Load() != 0 {
				t.Fatalf("scope escape accepted: %v %v", response, err)
			}
			assertNoProxySecrets(t, f, raw)
		})
	}
}

func TestProxyErrorsAreRedactedAndNeverRetried(t *testing.T) {
	for _, status := range []int{301, 307, 400, 401, 403, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "http://127.0.0.1:1/"+fixtureKey)
				w.Header().Set("WWW-Authenticate", fixtureKey)
				w.Header().Set("Retry-After", "9")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				fmt.Fprintf(w, `{"error":{"message":%q,"code":"context_length_exceeded","details":%q}}`, fixtureKey, fixtureToken)
			})
			response, raw, err := f.request(t, "/chat/completions", `{"model":"fixed-model","messages":[]}`, nil)
			want := status
			if status < 400 {
				want = 502
			}
			if err != nil || response.StatusCode != want || f.calls.Load() != 1 || response.Header.Get("Retry-After") != "9" || response.Header.Get("Location") != "" || response.Header.Get("WWW-Authenticate") != "" {
				t.Fatalf("error relay: %v %v", response, err)
			}
			if !bytes.Contains(raw, []byte("context_length_exceeded")) {
				t.Fatal("native compaction error code lost")
			}
			assertNoProxySecrets(t, f, raw)
		})
	}
}

func TestProxyRejectsReflectedSecretsInSuccessfulJSON(t *testing.T) {
	for _, encoding := range []string{"raw", "base64", "escaped"} {
		t.Run(encoding, func(t *testing.T) {
			value := fixtureKey
			if encoding == "base64" {
				value = base64.StdEncoding.EncodeToString([]byte(value))
			}
			raw, _ := json.Marshal(map[string]string{"id": "safe", "value": value})
			if encoding == "escaped" {
				var escaped strings.Builder
				for _, r := range fixtureKey {
					fmt.Fprintf(&escaped, "\\u%04x", r)
				}
				raw = []byte(`{"id":"safe","value":"` + escaped.String() + `"}`)
			}
			f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write(raw)
			})
			response, result, err := f.request(t, "/chat/completions", `{"model":"fixed-model"}`, nil)
			if err != nil || response.StatusCode != 502 {
				t.Fatalf("reflected secret accepted: %v %v", response, err)
			}
			assertNoProxySecrets(t, f, result)
		})
	}
}

func TestProxyStreamingFramesAndTerminalStates(t *testing.T) {
	for _, tc := range []struct {
		protocol   domain.APIProtocol
		op         Operation
		path, body string
	}{
		{domain.OpenAIChat, ChatCompletion, "/chat/completions", "data: {\"id\":\"chat\",\"choices\":[{\"delta\":{\"content\":\"한글\"}}]}\r\n\r\ndata: [DONE]\r\n\r\n"},
		{domain.OpenAIResponses, ResponseCreate, "/responses", "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\",\"error\":null}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"error\":null}}\n\n"},
		{domain.AnthropicMessages, MessageCreate, "/messages", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
	} {
		t.Run(string(tc.protocol), func(t *testing.T) {
			f := newProxyFixture(t, tc.protocol, []Operation{tc.op}, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, b := range []byte(tc.body) {
					w.Write([]byte{b})
					w.(http.Flusher).Flush()
				}
			})
			response, raw, err := f.request(t, tc.path, `{"model":"fixed-model","stream":true}`, nil)
			if err != nil || response.StatusCode != 200 || string(raw) != tc.body || f.calls.Load() != 1 {
				t.Fatalf("stream changed: %v %v %q", response, err, raw)
			}
		})
	}
}

func TestProxyIncompleteOrSecretStreamCannotClaimCompletion(t *testing.T) {
	for _, body := range []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\ndata: {\"secret\":\"" + fixtureKey + "\"}\n\ndata: [DONE]\n\n",
	} {
		f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, body)
		})
		_, raw, err := f.request(t, "/chat/completions", `{"model":"fixed-model","stream":true}`, nil)
		if err == nil || bytes.Contains(raw, []byte("[DONE]")) || f.calls.Load() != 1 {
			t.Fatal("incomplete stream was accepted or retried")
		}
		assertNoProxySecrets(t, f, raw)
	}
}

func TestProxyNativeStreamErrorsKeepOnlyMachineCode(t *testing.T) {
	f := newProxyFixture(t, domain.AnthropicMessages, []Operation{MessageCreate}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":%q}}\n\n", fixtureKey)
	})
	response, raw, err := f.request(t, "/messages", `{"model":"fixed-model","stream":true}`, nil)
	if err != nil || response.StatusCode != 200 || !bytes.Contains(raw, []byte("overloaded_error")) {
		t.Fatalf("native error: %v %v %s", response, err, raw)
	}
	assertNoProxySecrets(t, f, raw)
}

func TestProxyRevocationCancelsUpstreamAndRefusesLaterRequests(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-time.After(5 * time.Second):
			t.Error("upstream cancellation deadline")
		}
	})
	done := make(chan struct{})
	go func() { defer close(done); f.request(t, "/chat/completions", `{"model":"fixed-model"}`, nil) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider request did not start")
	}
	f.authority.cancel()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("revocation left upstream active")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("revoked request did not return")
	}
	response, _, err := f.request(t, "/chat/completions", `{"model":"fixed-model"}`, nil)
	if err != nil || response.StatusCode != 401 || f.calls.Load() != 1 || f.authority.keys.Load() != 1 {
		t.Fatal("revoked execution reused upstream credential")
	}
}

func TestProxyFailedResponseStreamRetainsNativeFailure(t *testing.T) {
	f := newProxyFixture(t, domain.OpenAIResponses, []Operation{ResponseCreate}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_failed\",\"object\":\"response\",\"status\":\"failed\",\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":%q}}}\n\n", fixtureKey)
	})
	response, raw, err := f.request(t, "/responses", `{"model":"fixed-model","stream":true}`, nil)
	if err != nil || response.StatusCode != 200 || !bytes.Contains(raw, []byte("response.failed")) || !bytes.Contains(raw, []byte("rate_limit_exceeded")) {
		t.Fatalf("native failure changed: %v %v %s", response, err, raw)
	}
	assertNoProxySecrets(t, f, raw)
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(f.logs.String(), `"error_code":"resource_exhausted"`) {
		if time.Now().After(deadline) {
			t.Fatalf("native failure missing from completion log: %s", f.logs.String())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestProxyChecksNativeReferenceOwnership(t *testing.T) {
	f := newProxyFixture(t, domain.OpenAIResponses, []Operation{ResponseCreate}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"resp_new","object":"response","error":null}`)
	})
	var observations atomic.Int32
	f.authority.authorize = func(ctx context.Context, kind ReferenceKind, id string) error {
		if kind != ResponseReference || id != "resp_owned" {
			return domain.Fail(domain.PermissionDenied, "foreign native reference", "")
		}
		return nil
	}
	f.authority.observe = func(ctx context.Context, kind ReferenceKind, id string) error {
		if kind != ResponseReference || id != "resp_new" {
			t.Error("incorrect native reference observation")
		}
		observations.Add(1)
		return nil
	}
	response, _, err := f.request(t, "/responses", `{"model":"fixed-model","previous_response_id":"resp_owned"}`, nil)
	if err != nil || response.StatusCode != 200 || observations.Load() != 1 {
		t.Fatal("owned continuation failed")
	}
	response, _, err = f.request(t, "/responses", `{"model":"fixed-model","previous_response_id":"resp_foreign"}`, nil)
	if err != nil || response.StatusCode != 403 || f.calls.Load() != 1 || f.authority.keys.Load() != 1 {
		t.Fatal("foreign native state reached provider")
	}
}

func TestProxyDoesNotReleaseFragmentedProtectedValues(t *testing.T) {
	for _, value := range []string{fixtureKey, base64.StdEncoding.EncodeToString([]byte(fixtureKey)), fixtureToken} {
		middle := len(value) / 2
		f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, fragment := range []string{value[:middle], value[middle:]} {
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", fragment)
				w.(http.Flusher).Flush()
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		})
		response, raw, err := f.request(t, "/chat/completions", `{"model":"fixed-model","stream":true}`, nil)
		if err != nil || response.StatusCode != 502 || bytes.Contains(raw, []byte(value[:middle])) || bytes.Contains(raw, []byte("[DONE]")) {
			t.Fatalf("protected prefix released: %v %v %s", response, err, raw)
		}
		assertNoProxySecrets(t, f, raw)
	}
}

func TestProxyPreservesBenignPrefixAtNativeCompletion(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"proxy-\"}}]}\n\ndata: [DONE]\n\n"
	f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, body)
	})
	response, raw, err := f.request(t, "/chat/completions", `{"model":"fixed-model","stream":true}`, nil)
	if err != nil || response.StatusCode != 200 || string(raw) != body {
		t.Fatal("safe native output changed")
	}
}

func TestProxyDirectTLSAndDroppedResponse(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://invalid:private@127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://invalid:private@127.0.0.1:1")
	f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	})
	response, raw, err := f.request(t, "/chat/completions", `{"model":"fixed-model"}`, nil)
	if err != nil || response.StatusCode != 502 || f.calls.Load() != 1 {
		t.Fatal("network failure was retried or environment proxy applied")
	}
	assertNoProxySecrets(t, f, raw)
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("untrusted TLS request reached provider") }))
	defer untrusted.Close()
	f.authority.scope.Provider.Endpoint = untrusted.URL + "/provider"
	response, raw, err = f.request(t, "/chat/completions", `{"model":"fixed-model"}`, nil)
	if err != nil || response.StatusCode != 502 {
		t.Fatal("untrusted certificate accepted")
	}
	assertNoProxySecrets(t, f, raw)
}

func TestProxyKeylessAndBodyBounds(t *testing.T) {
	f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
			t.Error("keyless provider received a credential")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[]}`)
	})
	f.authority.scope.Provider.Authentication = domain.KeylessAuth
	response, _, err := f.request(t, "/chat/completions", `{"model":"fixed-model"}`, nil)
	if err != nil || response.StatusCode != 200 || f.authority.keys.Load() != 0 {
		t.Fatal("keyless request retrieved a server key")
	}
	body := `{"model":"fixed-model","input":"` + strings.Repeat("x", maxBody) + `"}`
	response, _, err = f.request(t, "/chat/completions", body, nil)
	if err != nil || response.StatusCode != 413 || f.calls.Load() != 1 {
		t.Fatal("oversized request reached provider")
	}
}

func TestProxyDoesNotPersistReflectedNativeReferences(t *testing.T) {
	f := newProxyFixture(t, domain.OpenAIResponses, []Operation{ResponseCreate}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":%q,\"error\":null}}\n\n", fixtureKey)
	})
	var references atomic.Int32
	f.authority.observe = func(context.Context, ReferenceKind, string) error { references.Add(1); return nil }
	response, raw, err := f.request(t, "/responses", `{"model":"fixed-model","stream":true}`, nil)
	if err != nil || response.StatusCode != 502 || references.Load() != 0 {
		t.Fatal("reflected key reached reference persistence")
	}
	assertNoProxySecrets(t, f, raw)
}

func TestProxyDoesNotReleaseFragmentedSSEMetadata(t *testing.T) {
	for _, field := range []string{"", "id", "event", "retry", "extension"} {
		for _, data := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/data=%t", field, data), func(t *testing.T) {
				middle := len(fixtureKey) / 2
				f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					for _, part := range []string{fixtureKey[:middle], fixtureKey[middle:]} {
						fmt.Fprintf(w, "%s: %s\n", field, part)
						if data {
							fmt.Fprint(w, "data: {}\n")
						}
						fmt.Fprint(w, "\n")
						w.(http.Flusher).Flush()
					}
					fmt.Fprint(w, "data: [DONE]\n\n")
				})
				response, raw, err := f.request(t, "/chat/completions", `{"model":"fixed-model","stream":true}`, nil)
				if err != nil || response.StatusCode != 502 || bytes.Contains(raw, []byte(fixtureKey[:middle])) || bytes.Contains(raw, []byte("[DONE]")) {
					t.Fatalf("metadata prefix released: %v %v %s", response, err, raw)
				}
				assertNoProxySecrets(t, f, raw)
			})
		}
	}
}

func TestProxyPreservesBenignSSEMetadata(t *testing.T) {
	body := ": keepalive\n\nid: observation\nretry: 1000\ndata: {\"choices\":[]}\n\ndata: [DONE]\n\n"
	f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, body)
	})
	response, raw, err := f.request(t, "/chat/completions", `{"model":"fixed-model","stream":true}`, nil)
	if err != nil || response.StatusCode != 200 || string(raw) != body {
		t.Fatalf("benign metadata changed: %v %v %s", response, err, raw)
	}
}

func TestProxyErrorMachineCodesCannotReflectSelectedKey(t *testing.T) {
	for _, key := range []string{"invalid_api_key", "authentication_error", "rate_limit_exceeded", "api_error"} {
		t.Run(key, func(t *testing.T) {
			f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+key {
					t.Error("incorrect key")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprintf(w, `{"error":{"code":%q}}`, key)
			})
			f.authority.key = key
			response, raw, err := f.request(t, "/chat/completions", `{"model":"fixed-model"}`, nil)
			if err != nil || response.StatusCode != http.StatusUnauthorized || bytes.Contains(raw, []byte(key)) {
				t.Fatalf("error reflected protected machine code: %v %v %s", response, err, raw)
			}
			if strings.Contains(f.logs.String(), key) {
				t.Fatal("native machine code key escaped into logs")
			}
		})
	}
}
