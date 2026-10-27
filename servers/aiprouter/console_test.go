// SPDX-License-Identifier: Apache-2.0
package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConsoleServingBoundaries(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>fixture console</title>"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(dir, "static"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "static", "app.js"), []byte("console.log('fixture')"), 0600); e != nil {
		t.Fatal(e)
	}
	h, e := consoleHandler(dir)
	if e != nil {
		t.Fatal(e)
	}
	for route := range consoleRoutes {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", route, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "fixture console") || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("console route %s: %d", route, w.Code)
		}
	}
	for _, route := range []string{"/v1/unknown", "/auth/unknown", "/aiprouter.v1.Unknown/Call", "/unknown", "/.env", "/static/../index.html", "/static/app.js.map"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", route, nil))
		if w.Code == 200 || strings.Contains(w.Body.String(), "fixture console") {
			t.Fatalf("unsafe SPA fallback %s", route)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/static/app.js", nil))
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("asset MIME: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
}
func TestReturnToRejectsOpenRedirects(t *testing.T) {
	for _, value := range []string{"https://evil.test/device", "//evil.test/device", "/\\evil.test", "/auth/logout", "/v1/models", "/unknown", "/%2F%2Fevil.test", "/device\r\nLocation: https://evil.test"} {
		if safeReturnTo(value) != "/" {
			t.Fatalf("accepted unsafe return %q", value)
		}
	}
	for route := range consoleRoutes {
		target := route + "?organization=fixture"
		if safeReturnTo(target) != target {
			t.Fatalf("console return lost: %s", target)
		}
	}
}
