package adminassets

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedAdminSPAAndCaching(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/admin/", "/admin/callback", "/admin/users"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "id=\"root\"") {
			t.Fatalf("%s status=%d body=%q", route, response.Code, response.Body.String())
		}
		if response.Header().Get("Cache-Control") != "no-store" ||
			!strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatalf("%s headers=%v", route, response.Header())
		}
	}
	assets, err := fs.Glob(embedded, "dist/static/js/assets/index.*.js")
	if err != nil || len(assets) != 1 {
		t.Fatalf("embedded JavaScript assets=%v err=%v", assets, err)
	}
	route := "/admin/" + strings.TrimPrefix(assets[0], "dist/")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route, nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset status=%d headers=%v", response.Code, response.Header())
	}
}

func TestEmbeddedAdminRejectsMutationMethods(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", response.Code)
	}
}
