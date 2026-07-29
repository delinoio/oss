package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/delinoio/oss/servers/internal/httpserver"
)

type healthCheck struct {
	err error
}

func (check healthCheck) Ping(context.Context) error { return check.err }

func TestHealthEndpointsAndBrowserBoundary(t *testing.T) {
	t.Parallel()
	liveResponse := httptest.NewRecorder()
	live(liveResponse, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if liveResponse.Code != http.StatusOK ||
		liveResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("liveness response = %#v", liveResponse)
	}
	readyResponse := httptest.NewRecorder()
	ready(healthCheck{}).ServeHTTP(readyResponse,
		httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readyResponse.Code != http.StatusOK {
		t.Fatalf("readiness status = %d", readyResponse.Code)
	}
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	request := httptest.NewRequest(http.MethodPost,
		"/devhud.deck.v1.DeckViewService/ListViews", nil)
	request.Header.Set("Origin", httpserver.DeliDevOrigin)
	response := httptest.NewRecorder()
	browserBoundary(next).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || called {
		t.Fatalf("browser view request was allowed: status=%d called=%v",
			response.Code, called)
	}
}
