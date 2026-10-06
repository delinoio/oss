// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestProxyLocalResponseErrorsGuardSelectedKey(t *testing.T) {
	for _, protocol := range []domain.APIProtocol{domain.OpenAIChat, domain.OpenAIResponses, domain.AnthropicMessages} {
		operation, path := ChatCompletion, "/chat/completions"
		keys := []string{"api_error", "error", "message", "provider", fixtureKey}
		switch protocol {
		case domain.OpenAIResponses:
			operation, path = ResponseCreate, "/responses"
		case domain.AnthropicMessages:
			operation, path = MessageCreate, "/messages"
			keys = append(keys, "request_id")
		}
		if protocol != domain.AnthropicMessages {
			keys = append(keys, "param", "unavailable")
		}
		for _, failure := range []string{"reflected-json", "reflected-sse", "sse-error", "malformed-json", "wrong-media"} {
			for _, key := range keys {
				t.Run(fmt.Sprintf("%s/%s/%s", protocol, failure, key), func(t *testing.T) {
					stream := failure == "reflected-sse" || failure == "sse-error"
					f := newProxyFixture(t, protocol, []Operation{operation}, func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("Set-Cookie", key)
						w.Header().Set("X-Request-Id", key)
						w.Header().Set("Retry-After", strings.Repeat("1", 129))
						w.Header().Set("Trailer", "X-Provider-Secret")
						w.Header().Set("X-"+key, key)
						switch failure {
						case "reflected-json":
							fmt.Fprintf(w, `{"value":%q}`, key)
						case "reflected-sse":
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprintf(w, "data: {\"value\":%q}\n\n", key)
						case "sse-error":
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprintf(w, "data: {\"error\":{\"message\":%q}}\n\n", key)
						case "malformed-json":
							fmt.Fprint(w, "{")
						case "wrong-media":
							w.Header().Set("Content-Type", "text/plain")
							fmt.Fprint(w, key)
						}
						w.Header().Set("X-Provider-Secret", key)
					})
					f.authority.key = key
					response, raw, err := f.request(t, path, fmt.Sprintf(`{"model":"fixed-model","stream":%t}`, stream), nil)
					if err != nil {
						t.Fatal(err)
					}
					wantStatus := http.StatusBadGateway
					if failure == "sse-error" && key == fixtureKey {
						wantStatus = http.StatusOK
					}
					if response.StatusCode != wantStatus || bytes.Contains(raw, []byte(key)) {
						t.Fatalf("unsafe local failure: status=%d body=%q", response.StatusCode, raw)
					}
					// The unsupported media response uses a different local error type.
					collision := key != fixtureKey && !(failure == "wrong-media" && (key == "api_error" || key == "provider" || key == "unavailable"))
					if collision && len(raw) != 0 {
						t.Fatalf("colliding local body was not omitted: %q", raw)
					}
					if !collision && len(raw) == 0 {
						t.Fatal("ordinary safe error body was omitted")
					}
					assertLocalErrorHeaders(t, response)
					if response.Header.Get("X-"+key) != "" || response.Header.Get("X-Provider-Secret") != "" {
						t.Fatal("provider metadata escaped through a local failure")
					}
					assertProxyInvocationReleased(t, f, 1)
					assertNoProxySecrets(t, f, raw)
				})
			}
		}
	}
}

func TestProxyLocalDiagnosticErrorsGuardSelectedKey(t *testing.T) {
	for _, publication := range []int{2, 3} {
		t.Run(fmt.Sprint(publication), func(t *testing.T) {
			f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(http.ResponseWriter, *http.Request) {
				t.Error("failed diagnostic publication reached provider")
			})
			f.authority.key = "api_error"
			publications := 0
			f.authority.publishDiagnostic = func(context.Context, domain.RequestDiagnostic) error {
				publications++
				if publications == publication {
					return domain.Fail(domain.Unavailable, "private diagnostic", "")
				}
				return nil
			}
			response, raw, err := f.request(t, "/chat/completions", `{"model":"fixed-model"}`, nil)
			if err != nil || response.StatusCode != http.StatusServiceUnavailable || len(raw) != 0 {
				t.Fatalf("unguarded diagnostic failure: %v %v %q", response, err, raw)
			}
			assertLocalErrorHeaders(t, response)
			assertProxyInvocationReleased(t, f, 0)
		})
	}
}

func TestProxyPreKeyDenialDoesNotLoadCollidingKey(t *testing.T) {
	f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(http.ResponseWriter, *http.Request) {
		t.Error("denied request reached provider")
	})
	f.authority.key = "permission_error"
	response, raw, err := f.request(t, "/chat/completions", `{"model":"other-model"}`, nil)
	if err != nil || response.StatusCode != http.StatusForbidden || !json.Valid(raw) || !bytes.Contains(raw, []byte("permission_error")) || f.authority.keys.Load() != 0 || f.calls.Load() != 0 {
		t.Fatalf("pre-key denial loaded a credential or lost its body: %v %v %q", response, err, raw)
	}
}

func TestProxyAfterStartFailureAbortsWithoutLocalError(t *testing.T) {
	const first = "data: {\"choices\":[]}\n\n"
	f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, first)
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "data: {\"value\":\"api_error\"}\n\ndata: [DONE]\n\n")
	})
	f.authority.key = "api_error"
	response, raw, err := f.request(t, "/chat/completions", `{"model":"fixed-model","stream":true}`, nil)
	if err == nil || response == nil || response.StatusCode != http.StatusOK || string(raw) != first {
		t.Fatalf("started stream did not retain only its original safe frame: %v %v %q", response, err, raw)
	}
	assertProxyInvocationReleased(t, f, 1)
	assertNoProxySecrets(t, f, raw)
}

type localErrorAuthority struct {
	*fixtureAuthority
	configure func(*Lease)
}

func (a localErrorAuthority) Acquire(ctx context.Context, token string) (*Lease, error) {
	lease, err := a.fixtureAuthority.Acquire(ctx, token)
	if err == nil {
		a.configure(lease)
	}
	return lease, err
}

func TestProxyLocalCredentialAndSubmissionErrorsGuardSelectedKey(t *testing.T) {
	for _, failure := range []string{"returned-key-error", "before-submit", "transport"} {
		t.Run(failure, func(t *testing.T) {
			f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
				if failure != "transport" {
					t.Error("local failure reached provider")
					return
				}
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				connection.Close()
			})
			f.authority.key = "api_error"
			authority := localErrorAuthority{fixtureAuthority: f.authority, configure: func(lease *Lease) {
				privateFailure := domain.Fail(domain.Unavailable, "private local failure", "")
				switch failure {
				case "returned-key-error":
					key := lease.Key
					lease.Key = func(ctx context.Context) ([]byte, error) {
						value, _ := key(ctx)
						return value, privateFailure
					}
				case "before-submit":
					lease.BeforeSubmit = func(context.Context, Operation) error { return privateFailure }
				}
			}}
			f.server.Close()
			f.server = httptest.NewServer(New(authority, slog.New(slog.NewJSONHandler(f.logs, nil))))
			response, raw, err := f.request(t, "/chat/completions", `{"model":"fixed-model"}`, nil)
			wantStatus, calls := http.StatusServiceUnavailable, int32(0)
			if failure == "transport" {
				wantStatus, calls = http.StatusBadGateway, 1
			}
			if err != nil || response.StatusCode != wantStatus || len(raw) != 0 {
				t.Fatalf("unguarded local failure: %v %v %q", response, err, raw)
			}
			assertLocalErrorHeaders(t, response)
			assertProxyInvocationReleased(t, f, calls)
		})
	}
}

func assertLocalErrorHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	if !domain.SafeDiagnosticID(response.Header.Get("X-Delidev-Correlation-Id")) || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("local failure lost safe correlation or security headers")
	}
	if response.Header.Get("Set-Cookie") != "" || response.Header.Get("X-Request-Id") != "" || response.Header.Get("Retry-After") != "" || response.Header.Get("Trailer") != "" || len(response.Trailer) != 0 {
		t.Fatal("provider headers or trailers escaped through a local failure")
	}
}

func assertProxyInvocationReleased(t *testing.T, f *proxyFixture, calls int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for f.authority.releases.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.authority.acquires.Load() != 1 || f.authority.keys.Load() != 1 || f.calls.Load() != calls || f.authority.releases.Load() != 1 {
		t.Fatalf("invocation ownership changed: acquires=%d keys=%d calls=%d releases=%d", f.authority.acquires.Load(), f.authority.keys.Load(), f.calls.Load(), f.authority.releases.Load())
	}
}
