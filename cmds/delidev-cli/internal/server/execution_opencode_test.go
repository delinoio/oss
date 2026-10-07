package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestOpenCodeRegistrationRestrictsNativeOperationAndModel(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				raw, err := io.ReadAll(r.Body)
				if err != nil || string(raw) != `{"model":"fixture-model","messages":[],"stream":false}` || r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" || r.Header.Get("HTTP-Referer") != "https://deli.dev" {
					t.Error("registered OpenCode relay escaped its original request scope")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"chatcmpl-fixture","choices":[{"message":{"role":"assistant","content":"original result"},"finish_reason":"stop"}]}`)
			}))
			defer upstream.Close()
			f := newProfileAuthorityFixture(t, upstream.URL, domain.OpenCode, domain.OpenAIChat, func(input *domain.ExecutionJobInput) {
				input.Input.Mode = mode
				input.Configuration.Effort = "high"
				input.ConfigurationDigest, _ = input.Configuration.Digest()
			}, false)
			if response := f.registerGrant(t); response.Replayed || response.ProxyPath != apiproxy.Prefix || !f.registerGrant(t).Replayed {
				t.Fatal("original OpenCode registration did not preserve its exact receipt")
			}
			lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
			if err != nil {
				t.Fatal(err)
			}
			if len(lease.Scope.Operations) != 1 || lease.Scope.Operations[0] != apiproxy.ChatCompletion || lease.Scope.AccountID != f.input.AccountID || lease.Scope.ConnectionID != f.input.ConnectionID || lease.Scope.NativeModel != f.input.Configuration.NativeModel {
				t.Error("OpenCode registration changed its immutable account/model or added operations")
			}
			lease.Release()
			for _, rejected := range []struct{ token, path, body string }{
				{f.workerToken, "/chat/completions", `{"model":"fixture-model"}`},
				{f.service.Identity.Token, "/chat/completions", `{"model":"fixture-model"}`},
				{f.token, "/responses", `{"model":"fixture-model"}`},
				{f.token, "/responses/compact", `{"model":"fixture-model"}`},
				{f.token, "/messages", `{"model":"fixture-model"}`},
				{f.token, "/messages/count_tokens", `{"model":"fixture-model"}`},
				{f.token, "/chat/completions", `{"model":"another-model"}`},
				{f.token, "/chat/completions", `{"model":"fixture-model","fallbacks":[]}`},
				{f.token, "/chat/completions?beta=true", `{"model":"fixture-model"}`},
			} {
				response := f.requestPath(t, rejected.token, rejected.path, rejected.body)
				_, _ = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode < 400 || calls.Load() != 0 {
					t.Fatal("foreign credential, operation or model reached the provider")
				}
			}
			response := f.requestPath(t, f.token, "/chat/completions", `{"model":"fixture-model","messages":[],"stream":false}`)
			raw, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(raw), "original result") || calls.Load() != 1 {
				t.Fatal("original OpenCode scoped request did not complete exactly once")
			}
		})
	}
}

func TestOpenCodeRegistrationRejectsUnimplementedProfiles(t *testing.T) {
	for _, test := range []struct {
		name      string
		protocol  domain.APIProtocol
		configure func(*domain.ExecutionJobInput)
	}{
		{name: "responses", protocol: domain.OpenAIResponses},
		{name: "messages", protocol: domain.AnthropicMessages},
		{name: "version", configure: func(i *domain.ExecutionJobInput) { i.Installation.Version = "1.18.33" }},
		{name: "protocol-discovery", configure: func(i *domain.ExecutionJobInput) { i.Installation.ProtocolVerified = false }},
		{name: "subagent-model", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.SubagentModel = "other-model" }},
		{name: "subagent-effort", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.SubagentEffort = "high" }},
		{name: "concurrency", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.MaxConcurrency = 2 }},
		{name: "review-model", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.ApprovalReviewModel = "other-model" }},
		{name: "service-tier", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.ServiceTier = "fast" }},
		{name: "permission", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.Permission = domain.PermissionFullAccess }},
	} {
		t.Run(test.name, func(t *testing.T) {
			protocol := test.protocol
			if protocol == "" {
				protocol = domain.OpenAIChat
			}
			f := newProfileAuthorityFixture(t, "http://127.0.0.1:1", domain.OpenCode, protocol, func(input *domain.ExecutionJobInput) {
				if test.configure != nil {
					test.configure(input)
					// Valid but unsupported selections still have an intact digest.
					if digest, err := input.Configuration.Digest(); err == nil {
						input.ConfigurationDigest = digest
					}
				}
			}, false)
			request := connect.NewRequest(f.register)
			request.Header().Set("Authorization", "Bearer "+f.workerToken)
			if _, err := f.client.RegisterExecution(context.Background(), request); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("unsupported profile registered an execution credential: %v", err)
			}
			if lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token); err == nil {
				lease.Release()
				t.Fatal("rejected registration retained relay authority")
			}
		})
	}
}
