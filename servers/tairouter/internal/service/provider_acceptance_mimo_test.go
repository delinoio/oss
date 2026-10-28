// SPDX-License-Identifier: Apache-2.0
package service

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference/providers/mimofree"
)

func TestProviderAcceptanceMiMoRecoveryAndScopedState(t *testing.T) {
	var mu sync.Mutex
	bootstrap, chat := 0, 0
	var affinity, client string
	tokens := []string{"fixture.first.signature", "fixture.second.signature", "fixture.third.signature"}
	f := newProviderAcceptance(t, "mimo-free", "mimo-auto", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Host != "api.xiaomimimo.com" {
			t.Error("MiMo host mismatch")
		}
		b := providerAcceptanceBody(t, r)
		switch r.URL.Path {
		case "/api/free-ai/bootstrap":
			if r.Header.Get("Authorization") != "" {
				t.Error("bootstrap leaked credential")
			}
			id, _ := b["client"].(string)
			if len(id) != 36 {
				t.Error("bootstrap client missing")
			}
			if bootstrap == 1 && client != id {
				t.Error("401 recovery changed client identity")
			}
			client = id
			if bootstrap >= len(tokens) {
				t.Error("unexpected bootstrap retry")
				w.WriteHeader(500)
				return
			}
			providerAcceptanceResponse(w, `{"jwt":"`+tokens[bootstrap]+`"}`)
			bootstrap++
		case "/api/free-ai/openai/chat":
			chat++
			if r.Header.Get("Authorization") != "Bearer "+tokens[bootstrap-1] {
				t.Error("MiMo scoped JWT not used")
			}
			if r.Header.Get("X-Mimo-Source") != "mimocode-cli-free" || !strings.Contains(r.UserAgent(), "Chrome/") {
				t.Error("MiMo client headers missing")
			}
			a := r.Header.Get("X-Session-Affinity")
			if !strings.HasPrefix(a, "ses_") {
				t.Error("MiMo affinity missing")
			}
			if chat == 2 && a != affinity {
				t.Error("MiMo recovery changed affinity")
			}
			affinity = a
			messages, _ := b["messages"].([]any)
			if b["model"] != "mimo-auto" || len(messages) == 0 || messages[0].(map[string]any)["content"] != mimofree.SystemMarker {
				t.Error("MiMo anti-abuse marker/model missing")
			}
			if chat == 1 {
				w.WriteHeader(401)
				providerAcceptanceResponse(w, `{"error":{"message":"expired fixture"}}`)
				return
			}
			providerAcceptanceResponse(w, providerAcceptanceChat)
		default:
			t.Error("unexpected MiMo URL")
			w.WriteHeader(404)
		}
	})
	request := `{"model":"fixture","messages":[{"role":"user","content":"hello"}],"stream":false}`
	providerAcceptanceStatus(t, f.call("/v1/chat/completions", request, nil), 200)
	mu.Lock()
	if bootstrap != 2 || chat != 2 {
		t.Errorf("initial recovery sends bootstrap=%d chat=%d", bootstrap, chat)
	}
	mu.Unlock()
	providerAcceptanceStatus(t, f.call("/v1/chat/completions", request, nil), 200)
	mu.Lock()
	if bootstrap != 2 || chat != 3 {
		t.Error("second server request did not reuse scoped state")
	}
	mu.Unlock()
	// A new key in the same membership must not inherit the previous key's JWT.
	_, raw, e := f.s.IssueKey(context.Background(), f.scope, "other fixture key", nil)
	acceptanceOK(t, e)
	f.key = raw
	providerAcceptanceStatus(t, f.call("/v1/chat/completions", request, nil), 200)
	mu.Lock()
	if bootstrap != 3 || chat != 4 {
		t.Error("MiMo state crossed API key scope")
	}
	mu.Unlock()
	rows, e := f.db.Query(`SELECT value FROM "` + f.schema + `_temp".provider_states WHERE namespace='mimo-free'`)
	acceptanceOK(t, e)
	defer rows.Close()
	count := 0
	for rows.Next() {
		var ciphertext []byte
		acceptanceOK(t, rows.Scan(&ciphertext))
		count++
		for _, token := range tokens {
			if bytes.Contains(ciphertext, []byte(token)) || strings.Contains(f.logs.text(), token) {
				t.Fatal("MiMo JWT stored/logged in plaintext")
			}
		}
	}
	acceptanceOK(t, rows.Err())
	if count != 2 {
		t.Fatalf("scoped bootstrap rows=%d want=2", count)
	}
}
func TestProviderAcceptanceMiMo403DoesNotReplay(t *testing.T) {
	var mu sync.Mutex
	bootstrap, chat := 0, 0
	f := newProviderAcceptance(t, "mimo-free", "mimo-auto", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/api/free-ai/bootstrap" {
			bootstrap++
			providerAcceptanceResponse(w, `{"jwt":"fixture.forbidden.signature"}`)
			return
		}
		chat++
		w.WriteHeader(403)
		providerAcceptanceResponse(w, `{"error":{"message":"fixture forbidden"}}`)
	})
	w := f.call("/v1/chat/completions", `{"model":"fixture","messages":[{"role":"user","content":"hello"}]}`, nil)
	providerAcceptanceStatus(t, w, 403)
	mu.Lock()
	defer mu.Unlock()
	if bootstrap != 1 || chat != 1 {
		t.Fatal("MiMo 403 replayed")
	}
}
