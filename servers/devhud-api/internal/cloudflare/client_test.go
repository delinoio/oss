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
	marker := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 'm', 'a', 'r', 'k', 'e', 'r'}
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
			_, _ = response.Write(marker)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client := New(server.Client(), "token", "zone", "rule", server.URL)
	client.apiBaseURL = server.URL
	if err := client.PurgeAndRevalidate(context.Background(), server.URL+"/asset.png", marker); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(events, []string{"purge", "revalidate"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestPurgeRejectsAnotherValidPNG(t *testing.T) {
	marker := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 'm', 'a', 'r', 'k', 'e', 'r'}
	original := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 'o', 'r', 'i', 'g', 'i', 'n', 'a', 'l'}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/zones/zone/purge_cache" {
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"success":true}`))
			return
		}
		response.Header().Set("Content-Type", "image/png")
		_, _ = response.Write(original)
	}))
	defer server.Close()
	client := New(server.Client(), "token", "zone", "rule", server.URL)
	client.apiBaseURL = server.URL
	if err := client.PurgeAndRevalidate(context.Background(), server.URL+"/asset.png", marker); err == nil {
		t.Fatal("original PNG was accepted as the removal marker")
	}
}

func TestPublicRateLimitRequiresExact300PerIPPerMinute(t *testing.T) {
	requests := 300
	enabled := true
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(response, `{"success":true,"result":{"rules":[{"id":"rule","action":"block","enabled":%t,"expression":"(http.host eq \"assets.example.com\" and http.request.method eq \"GET\")","ratelimit":{"characteristics":["ip.src"],"period":60,"requests_per_period":%d,"mitigation_timeout":60}}]}}`, enabled, requests)
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
	requests = 300
	enabled = false
	if err := client.ValidatePublicRateLimit(context.Background()); err == nil {
		t.Fatal("disabled rule was accepted")
	}
}
