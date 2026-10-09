// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestOpenCodeGoRelayUsesOnlyOriginalNativeSessionAndFixedProfile(t *testing.T) {
	scope := Scope{ExecutionID: domain.NewID(), SessionID: domain.NewID(), AccountID: domain.NewID(), ConnectionID: domain.NewID(), ModelID: domain.NewID(), NativeModel: "fixture-model", Harness: domain.OpenCode, SubscriptionService: domain.SubscriptionOpenCodeGo, Provider: domain.OpenCodeGoProvider(), Operations: []Operation{ChatCompletion}, OpenCodeSession: "ses_0123456789abABCDEFGHIJKLMN"}
	if err := scope.Validate(); err != nil {
		t.Fatal(err)
	}
	incoming := httptest.NewRequest("POST", Prefix+"/chat/completions", nil)
	incoming.Header.Set("x-opencode-session", "ses_caller-controlled")
	incoming.Header.Set("User-Agent", "caller-controlled")
	incoming.Header.Set("Authorization", "Bearer caller-controlled")
	request, err := upstreamRequest(context.Background(), incoming, []byte(`{"model":"fixture-model"}`), scope, []byte("protected-fixture-key"), ChatCompletion, false, string(domain.NewID()))
	if err != nil {
		t.Fatal(err)
	}
	if request.URL.String() != domain.OpenCodeGoEndpoint+"/chat/completions" || request.Header.Get("x-opencode-session") != scope.OpenCodeSession || request.Header.Get("User-Agent") != "delidev/0.1.0" || request.Header.Get("Authorization") != "Bearer protected-fixture-key" || request.GetBody != nil {
		t.Fatal("relay accepted caller authority or retryable body")
	}
	scope.OpenCodeSession = ""
	if _, err := upstreamRequest(context.Background(), incoming, nil, scope, nil, ChatCompletion, false, ""); err == nil {
		t.Fatal("relay without native session proof")
	}
	scope.Provider.Endpoint = "https://caller.example/v1"
	if scope.Validate() == nil {
		t.Fatal("arbitrary endpoint became Go authority")
	}
	scope.Provider = domain.OpenCodeGoProvider()
	scope.SubscriptionService = domain.SubscriptionChatGPT
	if scope.Validate() == nil {
		t.Fatal("native login subscription acquired relay authority")
	}
}

func TestOpenCodeGoFailuresNeverRetryOrFallBack(t *testing.T) {
	for _, status := range []int{401, 403, 429, 200} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			authority := &fixtureAuthority{ctx: ctx, scope: Scope{ExecutionID: domain.NewID(), SessionID: domain.NewID(), AccountID: domain.NewID(), ConnectionID: domain.NewID(), ModelID: domain.NewID(), NativeModel: "fixture-model", Harness: domain.OpenCode, SubscriptionService: domain.SubscriptionOpenCodeGo, Provider: domain.OpenCodeGoProvider(), Operations: []Operation{ChatCompletion}, OpenCodeSession: "ses_0123456789abABCDEFGHIJKLMN"}}
			var calls int
			handler := New(authority, nil)
			handler.client.Transport = oauthProxyTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.String() != domain.OpenCodeGoEndpoint+"/chat/completions" || req.Header.Get("x-opencode-session") != authority.scope.OpenCodeSession || req.Header.Get("Authorization") != "Bearer "+fixtureKey {
					t.Error("upstream request escaped original fixed scope")
				}
				body := `{"error":{"message":"private-body-` + fixtureKey + `","type":"invalid_api_key"}}`
				content := "application/json"
				if status == 200 {
					body = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n"
					content = "text/event-stream"
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{content}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			})
			server := httptest.NewServer(handler)
			defer server.Close()
			request, err := http.NewRequest("POST", server.URL+Prefix+"/chat/completions", strings.NewReader(`{"model":"fixture-model","messages":[{"role":"user","content":"fixture"}],"stream":true}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+fixtureToken)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(response.Body)
			response.Body.Close()
			if status != 200 && err != nil {
				t.Fatal(err)
			}
			if status == 200 && err == nil {
				t.Fatal("interrupted stream manufactured terminal success")
			}
			if calls != 1 || authority.releases.Load() != 1 || strings.Contains(string(raw), fixtureKey) || strings.Contains(string(raw), "private-body") {
				t.Fatal("provider failure leaked content, retried, or lost lease settlement")
			}

		})
	}
}
