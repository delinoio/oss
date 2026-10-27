// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCompactionPortableRetentionAndArchiveReplay(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(map[bool]string{false: "inline", true: "sql"}[overflow], func(t *testing.T) {
			calls := 0
			h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				calls++
				if calls == 1 && strings.Contains(string(raw), "private thought") {
					t.Error("reasoning sent to summarizer")
				}
				if calls == 2 && !strings.Contains(string(raw), "private thought") {
					t.Error("reasoning not restored")
				}
				io.WriteString(w, fixture(Chat))
			}, Chat)
			h.c.Credentials = &retentionCredentials{}
			mem := receiptMemory{}
			h.c.ProviderStates = mem
			rt := h.c.Router.(testRouter)
			rt.route.Targets[0].ReasoningRetention = &ReasoningRetentionPolicy{Enabled: true}
			if overflow {
				rt.route.Targets[0].ReasoningRetention.MaxTokens = 1
			}
			h.c.Router = rt
			out := call(h, "/v1/responses/compact", `{"model":"m","input":[{"role":"system","content":"preserve system"},{"role":"user","content":"task"},{"type":"reasoning","summary":[{"type":"summary_text","text":"private thought"}]}]}`)
			if out.Code != 200 {
				t.Fatalf("%d %s", out.Code, out.Body)
			}
			if !strings.Contains(out.Body.String(), "preserve system") {
				t.Fatal("system prefix lost")
			}
			if overflow {
				if len(mem) != 1 || strings.Contains(out.Body.String(), "private thought") {
					t.Fatalf("not archived: %s", out.Body)
				}
				for k, v := range mem {
					if !strings.HasPrefix(k, CompactionReasoningNamespace) || v.ExpiresAt.After(time.Now().Add(24*time.Hour)) {
						t.Fatal("wrong state scope/ttl")
					}
				}
			} else if len(mem) != 0 || !strings.Contains(out.Body.String(), "historical context") {
				t.Fatalf("not portable: %s", out.Body)
			}
			var result object
			json.Unmarshal(out.Body.Bytes(), &result)
			request, _ := json.Marshal(object{"model": "m", "input": result["output"], "store": false})
			replay := call(h, "/v1/responses", string(request))
			if replay.Code != 200 {
				t.Fatalf("replay %d %s", replay.Code, replay.Body)
			}
			if overflow {
				for k, v := range mem {
					v.ExpiresAt = time.Now().Add(-time.Second)
					mem[k] = v
				}
				miss := call(h, "/v1/responses", string(request))
				if miss.Code == 200 || calls != 2 {
					t.Fatal("expired state sent")
				}
			}
		})
	}
}
func TestCompactionArchiveFailureBeforeSend(t *testing.T) {
	h, _, _, _, u := harness(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected send") }, Chat)
	rt := h.c.Router.(testRouter)
	rt.route.Targets[0].ReasoningRetention = &ReasoningRetentionPolicy{Enabled: true, MaxTokens: 1}
	h.c.Router = rt
	out := call(h, "/v1/responses/compact", `{"model":"m","input":[{"type":"reasoning","summary":[{"text":"private thought"}]}]}`)
	if out.Code == 200 || u.records[0].Sends != 0 {
		t.Fatalf("%d %s", out.Code, out.Body)
	}
}
func TestRetentionLimitsAndPortableCiphertextExclusion(t *testing.T) {
	for _, tc := range []struct {
		p    ReasoningRetentionPolicy
		want int64
	}{{ReasoningRetentionPolicy{}, 100000}, {ReasoningRetentionPolicy{ContextWindow: 1000}, 200}, {ReasoningRetentionPolicy{ContextWindow: 1000000}, 100000}} {
		got, e := retentionLimit(&tc.p)
		if e != nil || got != tc.want {
			t.Fatalf("%d %v", got, e)
		}
	}
	text := "exact readable"
	value := encodeReasoning(reasoningEnvelope{Text: &text, Signature: "must not retain"})
	got, ok, e := readableRetainedReasoning(object{"type": "reasoning", "encrypted_content": value})
	if !ok || e != nil || got != text {
		t.Fatal(got, e)
	}
	h := &Handler{c: Config{Authorizer: &testAuth{}, ProviderStates: receiptMemory{}}}
	scope := scopedAdapterState{h: h, t: Target{ReasoningRetention: &ReasoningRetentionPolicy{Enabled: true}}, c: Credential{AccountID: "a"}}
	state := &compactionRetention{}
	ctx := context.WithValue(context.Background(), compactionRetentionKey{}, state)
	_, e = h.prepareCompactionRetention(ctx, scope, object{"input": []any{object{"type": "reasoning", "encrypted_content": value}}})
	raw, _ := json.Marshal(state.retained)
	if e != nil || strings.Contains(string(raw), "must not retain") {
		t.Fatal("ciphertext/signature retained")
	}
}

type retentionCredentials struct{ testCredentials }

func (*retentionCredentials) Select(context.Context, Principal, Target, string) (Credential, error) {
	return Credential{AccountID: "a", Secret: "secret", CredentialVersion: "generation"}, nil
}
