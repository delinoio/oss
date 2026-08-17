package cloudflare

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestPurgeCompletesBeforeNoCacheRevalidation(t *testing.T) {
	events := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/zones/zone/purge_cache":
			events = append(events, "purge")
			if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer token" {
				t.Fatalf("purge request = %s headers=%v", request.Method, request.Header)
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"success":true}`))
		case "/asset.png":
			events = append(events, "revalidate")
			if request.Header.Get("Cache-Control") != "no-cache" {
				t.Fatalf("cache control = %q", request.Header.Get("Cache-Control"))
			}
			response.Header().Set("Content-Type", "image/png")
			_, _ = response.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client := New(server.Client(), "token", "zone", "rule", server.URL)
	client.apiBaseURL = server.URL
	if err := client.PurgeAndRevalidate(context.Background(), server.URL+"/asset.png"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(events, []string{"purge", "revalidate"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestPublicRateLimitRequiresExact300PerIPPerMinute(t *testing.T) {
	requests := 300
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(response, `{"success":true,"result":{"rules":[{"id":"rule","action":"block","expression":"(http.host eq \"assets.example.com\" and http.request.method eq \"GET\")","ratelimit":{"characteristics":["ip.src"],"period":60,"requests_per_period":%d,"mitigation_timeout":60}}]}}`, requests)
	}))
	defer server.Close()
	client := New(server.Client(), "token", "zone", "rule", "https://assets.example.com")
	client.apiBaseURL = server.URL
	if err := client.ValidatePublicRateLimit(context.Background()); err != nil {
		t.Fatal(err)
	}
	requests = 301
	if err := client.ValidatePublicRateLimit(context.Background()); err == nil {
		t.Fatal("301-request rule was accepted")
	}
}
