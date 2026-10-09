// SPDX-License-Identifier: Apache-2.0
package mcpmanagement

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCPOAuthProtectedStateAndOnceOnlyExchange(t *testing.T) {
	for _, scenario := range []string{"success", "lost-response", "expired", "cancel-recovery"} {
		lost := scenario == "lost-response"
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			registrations, exchanges := 0, 0
			var endpoint string
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource/mcp":
					json.NewEncoder(w).Encode(map[string]any{"resource": endpoint + "/mcp", "authorization_servers": []string{endpoint}})
				case "/.well-known/oauth-authorization-server":
					json.NewEncoder(w).Encode(map[string]any{"issuer": endpoint, "authorization_endpoint": endpoint + "/authorize", "token_endpoint": endpoint + "/token", "registration_endpoint": endpoint + "/register", "code_challenge_methods_supported": []string{"S256"}})
				case "/register":
					registrations++
					json.NewEncoder(w).Encode(map[string]any{"client_id": "fixture-client", "token_endpoint_auth_method": "none"})
				case "/token":
					exchanges++
					r.ParseForm()
					if r.Form.Get("code") != "original-code" || r.Form.Get("code_verifier") == "" {
						t.Error("original exchange proof missing")
					}
					if lost {
						h := w.(http.Hijacker)
						connection, _, _ := h.Hijack()
						connection.Close()
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"access_token": "private-fixture-token", "refresh_token": "private-fixture-refresh", "token_type": "Bearer", "expires_in": 3600})
				default:
					http.NotFound(w, r)
				}
			}))
			defer fixture.Close()
			endpoint = fixture.URL
			root := t.TempDir()
			vault := &fixtureSecrets{values: map[credentials.Ref][]byte{}}
			scope, device, actor, id := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
			m, e := open(root, scope, device, vault)
			if e != nil {
				t.Fatal(e)
			}
			def := domain.MCPDefinition{ID: id, Name: "oauth-fixture", Transport: domain.MCPHTTP, Endpoint: endpoint + "/mcp", Authentication: domain.MCPOAuth, Enabled: true}
			if _, e = m.Mutate(ctx, Request{ID: domain.NewID(), Actor: actor, ServerID: id, Operation: Save, Definition: &def}); e != nil {
				t.Fatal(e)
			}
			start := &pb.AuthenticateMcpServerRequest{Mutation: &pb.Mutation{Id: string(id), RequestId: string(domain.NewID()), ExpectedRevision: 1}, Operation: pb.McpAuthenticationOperation_MCP_AUTHENTICATION_OPERATION_START, CallbackUrl: "http://127.0.0.1:55451/oauth/mcp/callback"}
			result, e := m.Authenticate(ctx, actor, start)
			if e != nil {
				t.Fatal(e)
			}
			authorization, _ := url.Parse(result.AuthorizationUrl)
			state := authorization.Query().Get("state")
			if state == "" || authorization.Query().Get("code_challenge_method") != "S256" {
				t.Fatal("missing PKCE/state")
			}
			bytes, e := os.ReadFile(filepath.Join(root, "catalog.json"))
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(bytes), state) {
				t.Fatal("raw state in plaintext journal")
			}
			if replay, e := m.Authenticate(ctx, actor, start); e != nil || !replay.Replayed || registrations != 1 {
				t.Fatal(replay, e, registrations)
			}
			callback := start.CallbackUrl + "?code=original-code&state=" + url.QueryEscape(state)
			complete := &pb.AuthenticateMcpServerRequest{Mutation: &pb.Mutation{Id: string(id), RequestId: string(domain.NewID()), ExpectedRevision: 1}, Operation: pb.McpAuthenticationOperation_MCP_AUTHENTICATION_OPERATION_COMPLETE, AttemptId: result.AttemptId, CallbackUrl: callback}
			wrong := *complete
			wrong.CallbackUrl = strings.Replace(callback, state, "foreign-state", 1)
			if _, e = m.Authenticate(ctx, actor, &wrong); e == nil || exchanges != 0 {
				t.Fatal("foreign callback exchanged")
			}
			if scenario == "expired" || scenario == "cancel-recovery" {
				if scenario == "expired" {
					attempt := m.state.OAuth[domain.ID(result.AttemptId)]
					attempt.Expires = time.Now().Add(-time.Minute)
					m.state.OAuth[attempt.ID] = attempt
					if e = m.persist(); e != nil {
						t.Fatal(e)
					}
					if _, e = m.Authenticate(ctx, actor, complete); e == nil || exchanges != 0 {
						t.Fatal("expired original callback exchanged", e)
					}
				}
				cancel := &pb.AuthenticateMcpServerRequest{Mutation: &pb.Mutation{Id: string(id), RequestId: string(domain.NewID()), ExpectedRevision: 1}, Operation: pb.McpAuthenticationOperation_MCP_AUTHENTICATION_OPERATION_CANCEL, AttemptId: result.AttemptId}
				if scenario == "cancel-recovery" {
					vault.failDelete = 1
					if _, e = m.Authenticate(ctx, actor, cancel); e == nil {
						t.Fatal("failed callback cleanup claimed cancellation")
					}
					m, e = open(root, scope, device, vault)
					if e != nil {
						t.Fatal(e)
					}
				}
				canceled, e := m.Authenticate(ctx, actor, cancel)
				if e != nil || canceled.State != pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_CANCELED || exchanges != 0 {
					t.Fatal(canceled, e)
				}
				replay, e := m.Authenticate(ctx, actor, cancel)
				if e != nil || !replay.Replayed || registrations != 1 {
					t.Fatal("original cancellation changed", e)
				}
				return
			}
			result, e = m.Authenticate(ctx, actor, complete)
			if lost {
				if e == nil {
					t.Fatal("lost exchange claimed success")
				}
			} else if e != nil || result.State != pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_READY {
				t.Fatal(result, e)
			}
			m, e = open(root, scope, device, vault)
			if e != nil {
				t.Fatal(e)
			}
			replay, e := m.Authenticate(ctx, actor, complete)
			if e != nil || !replay.Replayed || exchanges != 1 {
				t.Fatal(replay, e, exchanges)
			}
			if lost && replay.State != pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_UNCERTAIN {
				t.Fatal("uncertainty cleared")
			}
			bytes, _ = os.ReadFile(filepath.Join(root, "catalog.json"))
			for _, secret := range []string{"original-code", "private-fixture-token", "private-fixture-refresh", state} {
				if strings.Contains(string(bytes), secret) {
					t.Fatal("plaintext journal retained secret", secret)
				}
			}
		})
	}
}
