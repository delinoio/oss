// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeCatalogSurfaceAliasesAndMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing scoped authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.RequestURI() {
		case "/v1/models?ids=desktop&format=desktop-config":
			_, _ = w.Write([]byte(`{"version":1,"models":[{"name":"claude-exact-service-alias","labelOverride":"Exact label","anthropicFamilyTier":"sonnet","isFamilyDefault":false,"supports1m":true}]}`))
		case "/v1/models?ids=cli":
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-exact-cli-alias","display_name":"CLI label","description":"Service description"}]}`))
		case "/v1/catalog":
			_, _ = w.Write([]byte(`{"models":[{"slug":"exact/codex-slug","display_name":"Codex label","default_reasoning_level":"high","supports_websockets":false,"base_instructions":"service instructions","additional_speed_tiers":["fast"]}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.RequestURI())
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	a := &ConnectAPI{HTTP: server.Client(), Server: server.URL}
	desktop, err := a.ModelsForClient(context.Background(), "test-key", "claude-desktop")
	if err != nil || len(desktop) != 1 {
		t.Fatalf("desktop: %v", err)
	}
	d := desktop[0].Desktop
	if desktop[0].ID != "claude-exact-service-alias" || d.IsFamilyDefault == nil || *d.IsFamilyDefault || d.Prefer1M != nil || d.Supports1M == nil || !*d.Supports1M {
		t.Fatal("alias or optional booleans changed")
	}
	cli, err := a.ModelsForClient(context.Background(), "test-key", "claude")
	if err != nil || len(cli) != 1 || cli[0].ID != "claude-exact-cli-alias" || cli[0].Description != "Service description" {
		t.Fatalf("cli metadata: %v", err)
	}
	codex, err := a.ModelsForClient(context.Background(), "test-key", "codex")
	if err != nil || len(codex) != 1 || codex[0].CodexCatalog["base_instructions"] != "service instructions" || codex[0].CodexCatalog["supports_websockets"] != false {
		t.Fatalf("native metadata: %v", err)
	}
}
func TestNativeCatalogRejectsRedirectAndMalformedEnvelope(t *testing.T) {
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer target.Close()
	for _, body := range []string{"redirect", `{"models":[{"slug":"same"},{"slug":"same"}]}`, `{}`, `{"models":null}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if body == "redirect" {
					http.Redirect(w, r, target.URL, 302)
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			a := &ConnectAPI{HTTP: server.Client(), Server: server.URL}
			if _, err := a.ModelsForClient(context.Background(), "test-key", "codex"); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
	if calls != 0 {
		t.Fatal("credential redirect followed")
	}
}
