// SPDX-License-Identifier: Apache-2.0
package main

import (
	"encoding/json"
	"fmt"
	"image/png"
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
		if safeReturnTo(value) != "/overview" {
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

func TestRetiredConsoleRoutesAreRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("fixture console"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := consoleHandler(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{
		"/", "/?organization=&page=keys", "/organizations", "/connect-client",
		"/invitations/accept?token=secret", "/instance", "/instance/usage",
		"/providers?page=keys", "/keys?org=fixture", "/keys?organization_id=fixture",
		"/device?page=keys", "/keys?page=", "/keys?org=", "/keys?organization_id=",
	} {
		t.Run(route, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", route, nil))
			if w.Code != 404 {
				t.Fatalf("retired console route %s: %d", route, w.Code)
			}
			if got := safeReturnTo(route); got != "/overview" {
				t.Fatalf("retired return target %s: %s", route, got)
			}
		})
	}
	for _, target := range []string{
		"/ownership?organization_id=fixture&transfer_id=transfer",
		"/invitation?token=fixture", "/device?user_code=fixture",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		if w.Code != 200 || safeReturnTo(target) != target {
			t.Fatalf("standalone flow target lost: %s (%d)", target, w.Code)
		}
	}
}

func TestConsoleInstallationAssets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("fixture console"), 0600); err != nil {
		t.Fatal(err)
	}
	public := filepath.Join("..", "..", "apps", "aiprouter", "public")
	for _, name := range []string{"manifest.webmanifest", "assets/aiprouter-180.png", "assets/aiprouter-192.png", "assets/aiprouter-512.png"} {
		data, err := os.ReadFile(filepath.Join(public, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// A neighboring file must remain private even when it exists in the artifact.
	if err := os.WriteFile(filepath.Join(dir, "private.webmanifest"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := consoleHandler(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "HEAD"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/manifest.webmanifest", nil))
		if w.Code != 200 || w.Header().Get("Content-Type") != "application/manifest+json" || w.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("manifest %s: %d %v", method, w.Code, w.Header())
		}
		if method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD returned a body")
		}
		if method == "GET" {
			var manifest struct {
				ID       string `json:"id"`
				StartURL string `json:"start_url"`
				Scope    string `json:"scope"`
				Display  string `json:"display"`
				Icons    []struct {
					Src   string `json:"src"`
					Sizes string `json:"sizes"`
				} `json:"icons"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.ID != "/overview" || !knownConsoleRoute(manifest.StartURL) || manifest.Scope != "/" || manifest.Display != "standalone" || len(manifest.Icons) != 2 {
				t.Fatalf("invalid installation manifest: %+v", manifest)
			}
			for _, icon := range manifest.Icons {
				asset := httptest.NewRecorder()
				handler.ServeHTTP(asset, httptest.NewRequest("GET", icon.Src, nil))
				if asset.Code != 200 || asset.Header().Get("Content-Type") != "image/png" {
					t.Fatalf("icon %s: %d %v", icon.Src, asset.Code, asset.Header())
				}
				dimensions, err := png.DecodeConfig(asset.Body)
				if err != nil {
					t.Fatal(err)
				}
				if dimensions.Width != dimensions.Height || fmt.Sprintf("%dx%d", dimensions.Width, dimensions.Height) != icon.Sizes {
					t.Fatalf("incorrect icon dimensions: %s", icon.Src)
				}
			}
		}
	}
	for _, target := range []string{"/private.webmanifest", "/manifest.webmanifest/extra", "/assets/../manifest.webmanifest"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		if w.Code != 404 {
			t.Fatalf("unexpected metadata access: %s (%d)", target, w.Code)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/manifest.webmanifest", nil))
	if w.Code != 405 {
		t.Fatalf("manifest POST: %d", w.Code)
	}
}
