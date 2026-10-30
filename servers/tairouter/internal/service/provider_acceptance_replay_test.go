// SPDX-License-Identifier: Apache-2.0
package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

const providerAcceptanceReplayStart = `{"model":"fixture","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`
const providerAcceptanceReplayNext = `{"model":"fixture","messages":[{"role":"user","content":"hello"},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"fixture result"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`
const providerAcceptanceRawThought = "raw fixture thought"

func TestProviderAcceptanceReasoningReplay(t *testing.T) {
	for _, mode := range []string{"restore", "stream-restore", "expire", "revoke", "thread", "organization", "user", "account", "model", "generation", "deleted", "destination", "key"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			f := newProviderAcceptance(t, "custom", "deepseek-v3.2-pro", func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				b := providerAcceptanceBody(t, r)
				if n == 1 {
					if mode == "stream-restore" {
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, "data: {\"id\":\"chat_replay\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"raw fixture \"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat_replay\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"thought\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat_replay\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n")
						return
					}
					providerAcceptanceResponse(w, `{"id":"chat_replay","object":"chat.completion","model":"deepseek-v3.2-pro","choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"raw fixture thought","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`)
					return
				}
				messages, _ := b["messages"].([]any)
				if len(messages) != 3 {
					t.Error("replay changed message count")
				} else {
					assistant, _ := messages[1].(map[string]any)
					result, _ := messages[2].(map[string]any)
					if assistant["reasoning_content"] != providerAcceptanceRawThought || result["content"] != "fixture result" || result["tool_call_id"] != "call_1" {
						t.Error("reasoning restoration or tool result changed")
					}
				}
				providerAcceptanceResponse(w, providerAcceptanceChat)
			})
			headers := http.Header{"Thread-Id": {"replay-fixture"}}
			first := providerAcceptanceReplayStart
			if mode == "stream-restore" {
				first = strings.Replace(first, `"model":"fixture"`, `"model":"fixture","stream":true`, 1)
			}
			providerAcceptanceStatus(t, f.call("/v1/chat/completions", first, headers), 200)
			// Assert encrypted durable storage, not a process-local adapter cache.
			rows, e := f.db.Query(`SELECT value,expires_at>clock_timestamp(),expires_at<=clock_timestamp()+interval '1 hour',quota_entries,quota_bytes,account_id,credential_version FROM "` + f.schema + `_temp".provider_states WHERE namespace='tool-reasoning-replay'`)
			acceptanceOK(t, e)
			count := 0
			for rows.Next() {
				var ciphertext []byte
				var live, bounded bool
				var entries, limit int64
				var account, version string
				acceptanceOK(t, rows.Scan(&ciphertext, &live, &bounded, &entries, &limit, &account, &version))
				count++
				if account != f.data.Metadata.Id || version == "" {
					t.Error("replay row lost account generation binding")
				}
				if bytes.Contains(ciphertext, []byte(providerAcceptanceRawThought)) {
					t.Error("raw reasoning stored in plaintext")
				}
				if !live || !bounded || entries != 64 || limit != 256<<10 {
					t.Error("replay TTL/quota contract missing")
				}
			}
			acceptanceOK(t, rows.Err())
			rows.Close()
			if count == 0 {
				t.Fatal("composed response did not persist reasoning replay")
			}
			switch mode {
			case "expire":
				_, e = f.db.Exec(`UPDATE "` + f.schema + `_temp".provider_states SET expires_at=clock_timestamp()-interval '1 second' WHERE namespace='tool-reasoning-replay'`)
				acceptanceOK(t, e)
			case "revoke":
				acceptanceOK(t, f.s.RevokeKey(context.Background(), f.scope, f.keyID))
			case "thread":
				headers.Set("Thread-Id", "different-thread")
			case "organization", "user":
				userID := f.scope.UserID
				if mode == "user" {
					u, _ := testIdentity(t, f.s, "other-replay-user")
					userID = u.ID
					invitation, e := f.s.Invite(context.Background(), f.scope, u.Email)
					acceptanceOK(t, e)
					_ = invitation
					mail, e := f.s.ClaimMail(context.Background())
					acceptanceOK(t, e)
					var token string
					acceptanceOK(t, f.s.WithMailAttempt(context.Background(), mail, func(_ context.Context, m store.Mail) store.MailOutcome { token = m.Token; return store.MailSuccess }))
					f.scope, e = f.s.AcceptInvite(context.Background(), userID, token)
					acceptanceOK(t, e)
				} else {
					org, e := f.s.CreateOrganization(context.Background(), userID, "other replay organization")
					acceptanceOK(t, e)
					f.scope, _, e = f.s.Membership(context.Background(), org.ID, userID)
					acceptanceOK(t, e)
				}
				_, f.key, e = f.s.IssueKey(context.Background(), f.scope, "cross scope", nil)
				acceptanceOK(t, e)
				f.data.Metadata.OrganizationId = f.scope.OrganizationID
				f.save()
				f.replayRoute("deepseek-v3.2-pro")
			case "account":
				f.data.Metadata.Id = "other-fixture-account"
				f.save()
				f.replayRoute("deepseek-v3.2-pro")
			case "generation":
				f.data.Secret = "replacement-" + f.secret
				f.secret = f.data.Secret
				f.save()
			case "deleted":
				acceptanceOK(t, f.s.DeletePrivate(context.Background(), f.scope, string(providerKind), f.data.Metadata.Id))
			case "destination":
				f.data.Metadata.BaseUrl = "https://provider.example/other-v1"
				f.save()
			case "key":
				_, f.key, e = f.s.IssueKey(context.Background(), f.scope, "other replay key", nil)
				acceptanceOK(t, e)
			case "model":
				f.replayRoute("deepseek-v3.2-pro-new")
			}
			w := f.call("/v1/chat/completions", providerAcceptanceReplayNext, headers)
			if mode == "restore" || mode == "stream-restore" {
				providerAcceptanceStatus(t, w, 200)
				if calls.Load() != 2 {
					t.Fatal("restored request not sent")
				}
			} else {
				if w.Code < 400 || calls.Load() != 1 {
					t.Fatal("unavailable/cross-scope reasoning was sent upstream")
				}
				if mode != "revoke" && mode != "deleted" && !strings.Contains(w.Body.String(), "continuation") {
					t.Error("missing reasoning did not return explicit continuation error")
				}
			}
			if strings.Contains(f.logs.text(), providerAcceptanceRawThought) {
				t.Fatal("raw reasoning leaked into logs")
			}
		})
	}
}
func (f *providerAcceptanceFixture) replayRoute(model string) {
	f.t.Helper()
	putRoutingRoute(f.t, &inferenceAdapter{s: f.s}, f.scope, &v1.Route{Id: "fixture-route", OrganizationId: f.scope.OrganizationID, ModelId: "fixture", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: f.data.Metadata.Id}, ModelId: model}}})
}
