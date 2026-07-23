package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCORSAllowsOnlyConfiguredOrigin(t *testing.T) {
	t.Parallel()
	middleware, err := CORS(DefaultCORSConfig())
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))

	allowed := httptest.NewRequest(http.MethodOptions, "/", nil)
	allowed.Header.Set("Origin", DeliDevOrigin)
	allowed.Header.Set("Access-Control-Request-Method", http.MethodPost)
	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, allowed)
	if allowedResponse.Code != http.StatusNoContent ||
		allowedResponse.Header().Get("Access-Control-Allow-Origin") != DeliDevOrigin {
		t.Fatalf("allowed preflight = %d %#v", allowedResponse.Code, allowedResponse.Header())
	}
	allowedHeaders := allowedResponse.Header().Get("Access-Control-Allow-Headers")
	for _, required := range []string{"Connect-Timeout-Ms", "X-User-Agent"} {
		if !strings.Contains(allowedHeaders, required) {
			t.Fatalf("allowed headers missing %q: %s", required, allowedHeaders)
		}
	}

	rejected := httptest.NewRequest(http.MethodOptions, "/", nil)
	rejected.Header.Set("Origin", "https://attacker.example")
	rejected.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rejectedResponse := httptest.NewRecorder()
	handler.ServeHTTP(rejectedResponse, rejected)
	if rejectedResponse.Code != http.StatusForbidden ||
		rejectedResponse.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("rejected preflight = %d %#v", rejectedResponse.Code, rejectedResponse.Header())
	}
}

func TestServerAndTimeoutDefaults(t *testing.T) {
	t.Parallel()
	defaults := DefaultTimeouts()
	server := Server(":8080", http.NotFoundHandler(), defaults)
	if server.ReadHeaderTimeout <= 0 || server.WriteTimeout <= 0 ||
		server.IdleTimeout <= 0 || server.MaxHeaderBytes <= 0 {
		t.Fatalf("unsafe server defaults: %#v", server)
	}

	handler := Timeout(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(20 * time.Millisecond)
	}), time.Millisecond)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("timeout status = %d, body = %s", response.Code, response.Body)
	}

	server = Server(":8080", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(20 * time.Millisecond)
	}), Defaults{HandlerTimeout: time.Millisecond})
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("server handler timeout status = %d, body = %s", response.Code, response.Body)
	}
}

func TestServerFillsPartialDefaultsFromBaseline(t *testing.T) {
	t.Parallel()
	baseline := DefaultTimeouts()
	server := Server(":8080", http.NotFoundHandler(), Defaults{
		WriteTimeout: time.Minute,
	})
	if server.ReadHeaderTimeout != baseline.ReadHeaderTimeout ||
		server.ReadTimeout != baseline.ReadTimeout ||
		server.WriteTimeout != time.Minute ||
		server.IdleTimeout != baseline.IdleTimeout ||
		server.MaxHeaderBytes != baseline.MaxHeaderBytes {
		t.Fatalf("partial defaults produced unsafe server: %#v", server)
	}
}

func TestCORSRejectsWildcardAndURLPaths(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{"*", "http://deli.dev", "https://deli.dev/path"} {
		if _, err := CORS(CORSConfig{AllowedOrigins: []string{origin}}); err == nil {
			t.Fatalf("CORS() accepted unsafe origin %q", origin)
		}
	}
}
