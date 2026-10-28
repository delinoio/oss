// SPDX-License-Identifier: Apache-2.0
package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/providers"
)

func TestProviderAcceptanceCommandCodeProfileRepair(t *testing.T) {
	var mu sync.Mutex
	generations, profiles := 0, 0
	f := newProviderAcceptance(t, "command-code", "glm-5.2", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Host {
		case "commandcode.ai":
			profiles++
			if r.Method != "GET" || r.URL.Path != "/models/glm-5-2" || r.URL.RawQuery != "" {
				t.Error("profile did not use fixed public URL")
			}
			for _, name := range []string{"Authorization", "Api-Key", "X-Api-Key", "Cookie", "X-Session-Id"} {
				if r.Header.Get(name) != "" {
					t.Errorf("profile leaked %s header", name)
				}
			}
			// A synthetic pinned React Router loader, with exact model identity and indexed efforts.
			table := []any{"id", "reasoningEfforts", "zai-org/GLM-5.2", []any{5, 6}, map[string]any{"_0": 2, "_1": 3}, "high", "max"}
			raw, _ := json.Marshal(table)
			quoted, _ := json.Marshal(string(raw))
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "window.__reactRouterContext.streamController.enqueue("+string(quoted)+");")
		case "api.commandcode.ai":
			generations++
			if r.URL.Path != "/alpha/generate" {
				t.Error("generation endpoint changed")
			}
			b := providerAcceptanceBody(t, r)
			params, _ := b["params"].(map[string]any)
			if generations == 1 {
				if params["reasoning_effort"] != "high" {
					t.Error("initial shipped effort missing")
				}
				w.WriteHeader(422)
				providerAcceptanceResponse(w, `{"error":{"message":"invalid reasoning_effort"}}`)
				return
			}
			if _, exists := params["reasoning_effort"]; exists {
				t.Error("rejected effort survived repair or learned policy")
			}
			w.Header().Set("Content-Type", "application/x-ndjson")
			io.WriteString(w, "{\"type\":\"text-delta\",\"text\":\"fixture ok\"}\n{\"type\":\"finish\",\"finishReason\":\"stop\",\"usage\":{\"inputTokens\":2,\"outputTokens\":1}}\n")
		default:
			t.Error("unexpected profile/generation origin")
			w.WriteHeader(404)
		}
	})
	request := `{"model":"fixture","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`
	providerAcceptanceStatus(t, f.call("/v1/chat/completions", request, nil), 200)
	mu.Lock()
	if generations != 2 || profiles != 1 {
		t.Error("profile recovery did not consume exactly three physical sends")
	}
	mu.Unlock()
	// A new handler/adaptor instance must read learned evidence from PostgreSQL.
	providerAcceptanceStatus(t, f.call("/v1/chat/completions", request, nil), 200)
	mu.Lock()
	if generations != 3 || profiles != 1 {
		t.Error("learned rejection was not reused across requests")
	}
	mu.Unlock()
	rows, e := f.db.Query(`SELECT value,account_id,credential_version FROM "` + f.schema + `_temp".provider_states WHERE namespace='command-code-efforts'`)
	acceptanceOK(t, e)
	defer rows.Close()
	count := 0
	for rows.Next() {
		var ciphertext []byte
		var account, version string
		acceptanceOK(t, rows.Scan(&ciphertext, &account, &version))
		count++
		if account != f.data.Metadata.Id || version == "" {
			t.Error("learned efforts lost account generation")
		}
		if bytes.Contains(ciphertext, []byte("zai-org/GLM-5.2")) {
			t.Error("learned policy stored in plaintext")
		}
	}
	acceptanceOK(t, rows.Err())
	if count != 1 {
		t.Fatalf("learned policy rows=%d want=1", count)
	}
}
func TestProviderAcceptanceCommandCodeAuthoritativeEffort(t *testing.T) {
	var mu sync.Mutex
	generations, profiles := 0, 0
	f := newProviderAcceptance(t, "command-code", "glm-5.2", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Host == "commandcode.ai" {
			profiles++
			t.Error("authoritative effort triggered profile refresh")
			w.WriteHeader(500)
			return
		}
		generations++
		b := providerAcceptanceBody(t, r)
		params, _ := b["params"].(map[string]any)
		if params["reasoning_effort"] != "high" {
			t.Error("authoritative selected effort missing")
		}
		w.WriteHeader(422)
		providerAcceptanceResponse(w, `{"error":{"message":"invalid reasoning_effort"}}`)
	})
	f.data.Models = &providers.ModelCatalog{State: providers.ValidationAccepted, Models: []providers.Model{{ID: "glm-5.2", ReasoningEfforts: []string{"high"}}}}
	f.data.ModelsDiscoveredAt = time.Now()
	f.save()
	w := f.call("/v1/chat/completions", `{"model":"fixture","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`, nil)
	providerAcceptanceStatus(t, w, 422)
	mu.Lock()
	defer mu.Unlock()
	if generations != 1 || profiles != 0 {
		t.Fatal("authoritative refusal replayed")
	}
}

func TestProviderAcceptanceCommandCodeRevokedBeforeProfile(t *testing.T) {
	var mu sync.Mutex
	generations, profiles := 0, 0
	var f *providerAcceptanceFixture
	f = newProviderAcceptance(t, "command-code", "glm-5.2", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Host == "commandcode.ai" {
			profiles++
			t.Error("revoked key physically fetched a profile")
			w.WriteHeader(500)
			return
		}
		generations++
		// Revoke after the admitted model write, before its refusal can trigger a profile send.
		if err := f.s.RevokeKey(r.Context(), f.scope, f.keyID); err != nil {
			t.Error("fixture key revocation failed")
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(422)
		providerAcceptanceResponse(w, `{"error":{"message":"invalid reasoning_effort"}}`)
	})
	w := f.call("/v1/chat/completions", `{"model":"fixture","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`, nil)
	providerAcceptanceStatus(t, w, 401)
	mu.Lock()
	defer mu.Unlock()
	if generations != 1 || profiles != 0 {
		t.Fatal("revocation allowed a profile or model resend")
	}
	var count int
	acceptanceOK(t, f.db.QueryRow(`SELECT count(*) FROM "`+f.schema+`_temp".provider_states WHERE namespace='command-code-efforts'`).Scan(&count))
	if count != 0 {
		t.Fatal("revoked request persisted learned policy")
	}
}
