package polar

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCancelSubscriptionRequestsPeriodEndCancellation(t *testing.T) {
	t.Parallel()
	const accessToken = "polar-access-token"
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Method != http.MethodPatch ||
			request.URL.Path != "/v1/subscriptions/subscription-1" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+accessToken {
			t.Error("request omitted the Polar access token")
		}
		body, _ := io.ReadAll(request.Body)
		if string(body) != `{"cancel_at_period_end":true}` {
			t.Errorf("request body = %q", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{}`)
	}))
	defer server.Close()

	client, err := newClient(server.URL+"/v1", accessToken, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CancelSubscription(context.Background(), "subscription-1"); err != nil {
		t.Fatal(err)
	}
}

func TestCancelSubscriptionRedactsProviderErrorsAndAcceptsNotFound(t *testing.T) {
	t.Parallel()
	const secret = "polar-secret-that-must-not-leak"
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if strings.HasSuffix(request.URL.Path, "/missing") {
			http.NotFound(writer, request)
			return
		}
		http.Error(writer, "provider leaked "+secret, http.StatusUnauthorized)
	}))
	defer server.Close()

	client, err := newClient(server.URL+"/v1", secret, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CancelSubscription(context.Background(), "missing"); err != nil {
		t.Fatal(err)
	}
	err = client.CancelSubscription(context.Background(), "present")
	if err == nil || strings.Contains(err.Error(), secret) ||
		strings.Contains(err.Error(), "provider leaked") {
		t.Fatalf("unsafe error = %v", err)
	}
}
