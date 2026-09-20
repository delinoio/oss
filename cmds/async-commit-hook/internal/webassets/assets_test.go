package webassets

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedUIAndAssetBoundary(t *testing.T) {
	handler := Handler()
	get := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		return response
	}
	index := get("GET", "/")
	if index.Code != 200 || !strings.Contains(index.Body.String(), `<div id="root">`) {
		t.Fatal(index.Code, index.Body.String())
	}
	if index.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(index.Header())
	}
	policy := index.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "connect-src 'self'") || strings.Contains(policy, "unsafe-") {
		t.Fatal(policy)
	}
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(index.Body.String(), -1)
	if len(assets) < 2 {
		t.Fatal("missing script and stylesheet references")
	}
	for _, asset := range assets {
		response := get("GET", asset[1])
		if response.Code != 200 || response.Body.Len() == 0 || !strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
			t.Fatal(asset[1], response.Code, response.Header())
		}
		head := get("HEAD", asset[1])
		if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Type") != response.Header().Get("Content-Type") {
			t.Fatal(head)
		}
	}
	for _, path := range []string{"/missing", "/assets/missing.00000000.js", "/index.html/", "/../index.html", "/docs/", "/install.sh", "/async_commit_hook.v1.LocalService/Unknown"} {
		if response := get("GET", path); response.Code != 404 {
			t.Fatal(path, response.Code)
		}
	}
	if response := get("POST", "/"); response.Code != 405 {
		t.Fatal(response.Code)
	}
}
