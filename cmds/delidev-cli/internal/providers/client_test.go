package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func localProvider(endpoint string) domain.Provider {
	return domain.Provider{Name: "Local fixture", Endpoint: endpoint + "/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth, Discovery: true}
}
func TestInspectRealHTTPHeadersAndIsolation(t *testing.T) {
	const key = "private-fixture-key-not-a-user-secret"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.String() != "/v1/models" || r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get("HTTP-Referer") != "https://deli.dev" || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected request metadata")
		}
		fmt.Fprint(w, `{"data":[{"id":"z-model","secret_ignored":"private metadata"},{"id":"a-model"}]}`)
	}))
	defer server.Close()
	// The request must ignore environment proxy credentials and configuration.
	t.Setenv("HTTP_PROXY", "http://bad:private@127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://bad:private@127.0.0.1:1")
	p := localProvider(server.URL)
	p.Authentication = domain.BearerAuth
	o, err := Inspect(context.Background(), p, []byte(key))
	if err != nil || o.Problem() != nil || o.Authentication != AuthenticationUnknown || len(o.Models) != 2 || o.Models[0].ID != "a-model" || calls.Load() != 1 {
		t.Fatalf("inspect: %+v %v", o, err)
	}
	raw, _ := json.Marshal(o)
	if strings.Contains(string(raw), "private") {
		t.Fatal("provider metadata escaped")
	}
}

func TestInspectAnthropicPagination(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.URL.Query().Get("limit") != "1000" || r.Header.Get("x-api-key") != "fixture-anthropic-key" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected Anthropic request")
		}
		if n == 1 {
			if r.URL.Query().Get("after_id") != "" {
				t.Error("first page cursor")
			}
			fmt.Fprint(w, `{"data":[{"id":"model-a","display_name":"Model A","max_input_tokens":1234}],"has_more":true,"last_id":"model-a"}`)
		} else {
			if r.URL.Query().Get("after_id") != "model-a" {
				t.Error("second page cursor")
			}
			fmt.Fprint(w, `{"data":[{"id":"model-b","display_name":"Model B"}],"has_more":false,"last_id":"model-b"}`)
		}
	}))
	defer server.Close()
	p := localProvider(server.URL)
	p.Protocol = domain.AnthropicMessages
	p.Authentication = domain.APIKeyAuth
	o, err := Inspect(context.Background(), p, []byte("fixture-anthropic-key"))
	if err != nil || o.Problem() != nil || calls.Load() != 2 || len(o.Models) != 2 || o.Models[0].ContextLimit == nil || *o.Models[0].ContextLimit != 1234 {
		t.Fatalf("pagination: %+v %v", o, err)
	}
}

func TestInspectRefusesRedirectsAndSanitizesErrors(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1); t.Error("redirect followed") }))
	defer target.Close()
	for _, tc := range []struct {
		status  int
		failure Failure
	}{{301, RedirectRefused}, {307, RedirectRefused}, {401, AuthenticationRejected}, {403, AccessDenied}, {404, Unsupported}, {405, Unsupported}, {429, RateLimited}, {500, ProviderUnavailable}, {204, ProviderUnavailable}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", target.URL+"/private-fixture-key")
				w.Header().Set("Retry-After", "5")
				w.Header().Set("WWW-Authenticate", "private-fixture-key")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"error":"private-fixture-key and private email/path"}`)
			}))
			defer server.Close()
			o, err := Inspect(context.Background(), localProvider(server.URL), nil)
			if err != nil || o.Failure != tc.failure || o.Problem() == nil || calls.Load() != 1 || o.RetryAfterSeconds == nil || *o.RetryAfterSeconds != 5 {
				t.Fatalf("result: %+v %v", o, err)
			}
			raw, _ := json.Marshal(struct {
				Observation Observation
				Error       *domain.Error
			}{o, o.Problem()})
			if strings.Contains(string(raw), "private") || strings.Contains(string(raw), target.URL) {
				t.Fatal("untrusted provider detail escaped")
			}
		})
	}
	if forwarded.Load() != 0 {
		t.Fatal("redirect target reached")
	}
}

func TestInspectMalformedAndBoundedModels(t *testing.T) {
	for name, body := range map[string]string{
		"null":              `{"data":null}`,
		"wrong-case":        `{"Data":[]}`,
		"duplicate":         `{"data":[],"data":[]}`,
		"nested-duplicate":  `{"data":[{"id":"a","id":"b"}]}`,
		"trailing":          `{"data":[]} {}`,
		"control":           `{"data":[{"id":"a\nb"}]}`,
		"no-id":             `{"data":[{"name":"Model"}]}`,
		"duplicate-id":      `{"data":[{"id":"a"},{"id":"a"}]}`,
		"silent-pagination": `{"data":[{"id":"a"}],"has_more":true}`,
		"null-pagination":   `{"data":[],"has_more":null}`,
		"deep":              `{"data":[],"extra":` + strings.Repeat("[", 70) + `0` + strings.Repeat("]", 70) + `}`,
		"invalid-utf8":      "{\"data\":[],\"extra\":\"\xff\"}",
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			o, err := Inspect(context.Background(), localProvider(server.URL), nil)
			if err != nil || o.Failure != InvalidResponse || len(o.Models) > 0 {
				t.Fatalf("accepted malformed response: %+v %v", o, err)
			}
		})
	}
	t.Run("body-bound", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat(" ", maxBody+1)) }))
		defer server.Close()
		o, err := Inspect(context.Background(), localProvider(server.URL), nil)
		if err != nil || o.Failure != ResponseTooLarge {
			t.Fatalf("bound: %+v %v", o, err)
		}
	})
	t.Run("reflected-key", func(t *testing.T) {
		p := domain.Provider{Protocol: domain.OpenAIChat}
		for _, id := range []string{"secret-fixture-key", "prefix-secret-fixture-key", "c2VjcmV0LWZpeHR1cmUta2V5"} {
			raw, _ := json.Marshal(map[string]any{"data": []map[string]string{{"id": id}}})
			if _, _, err := parseModels(raw, p.Protocol, []byte("secret-fixture-key")); err == nil {
				t.Fatal("reflected key accepted")
			}
		}
	})
}

func TestInspectCanceledAndTLSFailure(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Observation, 1)
	go func() { o, _ := Inspect(ctx, localProvider(server.URL), nil); done <- o }()
	<-started
	cancel()
	select {
	case o := <-done:
		if o.Failure != Canceled {
			t.Fatalf("cancel: %+v", o)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation stuck")
	}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted TLS request reached handler") }))
	defer tlsServer.Close()
	o, err := Inspect(context.Background(), localProvider(tlsServer.URL), nil)
	if err != nil || o.Failure != TLSFailure {
		t.Fatalf("TLS: %+v %v", o, err)
	}
}

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGatewayCredentialCheckBeforePublicCatalog(t *testing.T) {
	for _, tc := range []struct {
		endpoint, path, body string
		kind                 profile
	}{
		{"https://openrouter.ai/api/v1", "/api/v1/key", `{"data":{"is_free_tier":false,"label":"private-ignored"}}`, openRouter},
		{"https://ai-gateway.vercel.sh/v1", "/v1/credits", `{"balance":"1.25","total_used":"15.00"}`, vercelGateway},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			var paths []string
			client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				paths = append(paths, r.URL.Path)
				w := httptest.NewRecorder()
				if len(paths) == 1 {
					if r.URL.Path != tc.path {
						t.Error("wrong credential route")
					}
					fmt.Fprint(w, tc.body)
				} else {
					fmt.Fprint(w, `{"data":[{"id":"model-a"}]}`)
				}
				return w.Result(), nil
			})}
			p := domain.Provider{Name: "Gateway", Endpoint: tc.endpoint, Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth}
			o := inspect(context.Background(), client, p, []byte("fixture-key"))
			if o.Problem() != nil || o.Authentication != CredentialAccepted || len(paths) != 2 || len(o.Models) != 1 {
				t.Fatalf("gateway: %+v %v", o, paths)
			}
			if validGatewayCredential([]byte(`{"data":{},"balance":"NaN","total_used":"0"}`), tc.kind) {
				t.Fatal("malformed credential response")
			}
		})
	}
}

func TestProfilesDoNotTrustNamesOrSimilarAuthorities(t *testing.T) {
	for _, endpoint := range []string{"https://api.openai.com.evil.test/v1", "https://api.openai.com:444/v1", "http://api.openai.com/v1", "https://api.openai.com/v1/other", "https://api.openai.com./v1"} {
		u, _ := url.Parse(endpoint)
		if endpointProfile(*u, domain.Provider{Name: "OpenAI", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth}) != custom {
			t.Fatal("trusted lookalike", endpoint)
		}
	}
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	for _, value := range []string{"-1", "86401", "private-token", strings.Repeat("1", 129)} {
		if retryAfter(value, now) != nil {
			t.Fatal("invalid retry delay")
		}
	}
	if got := retryAfter(now.Add(3*time.Second).Format(http.TimeFormat), now); got == nil || *got != 3 {
		t.Fatal("HTTP date retry delay")
	}
}
