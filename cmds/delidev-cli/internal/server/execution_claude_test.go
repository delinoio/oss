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

func TestClaudeRegistrationRestrictsNativeOperationAndModel(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				raw, err := io.ReadAll(r.Body)
				if err != nil || string(raw) != `{"model":"fixture-model","messages":[],"stream":false}` || r.URL.Path != "/messages" || r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" || r.Header.Get("HTTP-Referer") != "https://deli.dev" {
					t.Error("registered Claude relay escaped its original request scope")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"msg_fixture","type":"message","role":"assistant","content":[{"type":"text","text":"original result"}],"stop_reason":"end_turn"}`)
			}))
			defer upstream.Close()
			f := newProfileAuthorityFixture(t, upstream.URL, domain.ClaudeCode, domain.AnthropicMessages, func(input *domain.ExecutionJobInput) { input.Input.Mode = mode }, false)
			if response := f.registerGrant(t); response.Replayed || response.ProxyPath != apiproxy.Prefix || !f.registerGrant(t).Replayed {
				t.Fatal("original Claude registration did not preserve its exact receipt")
			}
			lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
			if err != nil {
				t.Fatal(err)
			}
			if len(lease.Scope.Operations) != 1 || lease.Scope.Operations[0] != apiproxy.MessageCreate || lease.Scope.AccountID != f.input.AccountID || lease.Scope.ConnectionID != f.input.ConnectionID || lease.Scope.NativeModel != f.input.Configuration.NativeModel {
				t.Error("Claude registration changed its immutable account/model or added operations")
			}
			lease.Release()
			for _, rejected := range []struct{ token, path, body string }{
				{f.workerToken, "/messages", `{"model":"fixture-model"}`},
				{f.service.Identity.Token, "/messages", `{"model":"fixture-model"}`},
				{f.token, "/responses", `{"model":"fixture-model"}`},
				{f.token, "/responses/compact", `{"model":"fixture-model"}`},
				{f.token, "/chat/completions", `{"model":"fixture-model"}`},
				{f.token, "/messages/count_tokens", `{"model":"fixture-model"}`},
				{f.token, "/messages", `{"model":"another-model"}`},
				{f.token, "/messages", `{"model":"fixture-model","fallbacks":[]}`},
				{f.token, "/messages?beta=true&extra=true", `{"model":"fixture-model"}`},
			} {
				response := f.requestPath(t, rejected.token, rejected.path, rejected.body)
				_, _ = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode < 400 || calls.Load() != 0 {
					t.Fatal("foreign credential, operation or model reached the provider")
				}
			}
			response := f.requestPath(t, f.token, "/messages", `{"model":"fixture-model","messages":[],"stream":false}`)
			raw, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(raw), "original result") || calls.Load() != 1 {
				t.Fatal("original Claude scoped request did not complete exactly once")
			}
		})
	}
}

func TestClaudeRegistrationRejectsUnimplementedProfiles(t *testing.T) {
	for _, test := range []struct {
		name      string
		protocol  domain.APIProtocol
		configure func(*domain.ExecutionJobInput)
	}{
		{name: "responses", protocol: domain.OpenAIResponses},
		{name: "chat", protocol: domain.OpenAIChat},
		{name: "version", configure: func(i *domain.ExecutionJobInput) { i.Installation.Version = "2.1.237" }},
		{name: "protocol-discovery", configure: func(i *domain.ExecutionJobInput) { i.Installation.ProtocolVerified = false }},
		{name: "effort", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Effort = "invalid\x00effort" }},
		{name: "subagent-model", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.SubagentModel = "other-model" }},
		{name: "subagent-effort", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.SubagentEffort = "high" }},
		{name: "concurrency", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.MaxConcurrency = 2 }},
		{name: "review-model", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.ApprovalReviewModel = "other-model" }},
		{name: "service-tier", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.ServiceTier = "fast" }},
		{name: "codex-permission", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.Permission = domain.PermissionFullAccess }},
		{name: "unknown-permission", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.ClaudePermission = "accept-edits" }},
		{name: "codex-approval", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.ApprovalPolicy = "never" }},
		{name: "unproved-generation", configure: func(i *domain.ExecutionJobInput) { i.Version = 2 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			protocol := test.protocol
			if protocol == "" {
				protocol = domain.AnthropicMessages
			}
			f := newProfileAuthorityFixture(t, "http://127.0.0.1:1", domain.ClaudeCode, protocol, func(input *domain.ExecutionJobInput) {
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
