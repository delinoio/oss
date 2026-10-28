// SPDX-License-Identifier: Apache-2.0
package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerCodexCompatibilityPrefix(t *testing.T) {
	f := newProviderAcceptance(t, "custom", "fixture-model", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Error("unexpected upstream route")
		}
		providerAcceptanceResponse(w, providerAcceptanceChat)
	})
	// A configured console must not swallow either valid or unknown alias paths.
	f.srv.console = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("compatibility route reached console")
		w.WriteHeader(404)
	})
	providerAcceptanceStatus(t, f.call("/backend-api/codex/chat/completions", `{"model":"fixture","messages":[{"role":"user","content":"hello"}]}`, nil), 200)
	w := httptest.NewRecorder()
	f.srv.handler().ServeHTTP(w, httptest.NewRequest("POST", "http://router.test/backend-api/codex/unknown", nil))
	if w.Code != 404 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("unknown alias: %d %s", w.Code, w.Body.String())
	}
	// The WebSocket alias must use the production origin guard before upgrade.
	r := httptest.NewRequest("GET", "http://router.test/backend-api/codex/responses", nil)
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Origin", "https://untrusted.test")
	w = httptest.NewRecorder()
	f.srv.handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("WebSocket alias missed origin check: %d", w.Code)
	}
}
