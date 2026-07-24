package polar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/contracts"
)

func TestCreateHostedCheckoutAndPortalSession(t *testing.T) {
	t.Parallel()
	const organizationID = "0198a000-0000-7000-8000-000000000005"
	expiresAt := time.Date(2026, 7, 24, 13, 0, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Header.Get("Idempotency-Key") != "billing-key" {
			t.Error("missing provider idempotency key")
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["external_customer_id"] != organizationID {
			t.Errorf("external customer = %#v", payload["external_customer_id"])
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		switch request.URL.Path {
		case "/v1/checkouts":
			_, _ = io.WriteString(writer,
				`{"id":"checkout_1","url":"https://polar.sh/checkout/1","expires_at":"`+
					expiresAt.Format(time.RFC3339)+`"}`,
			)
		case "/v1/customer-sessions":
			_, _ = io.WriteString(writer,
				`{"customer_portal_url":"https://polar.sh/portal/1","expires_at":"`+
					expiresAt.Format(time.RFC3339)+`"}`,
			)
		default:
			t.Errorf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	client, err := newClient(server.URL+"/v1", "polar-access-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.productID = "product_1"
	checkout, err := client.CreateCheckout(
		context.Background(),
		contracts.CheckoutRequest{
			OrganizationID: organizationID,
			SuccessURL:     "https://deli.dev/success",
			CancelURL:      "https://deli.dev/cancel",
			IdempotencyKey: "billing-key",
		},
	)
	if err != nil || checkout.URL != "https://polar.sh/checkout/1" ||
		!checkout.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("checkout = %#v, %v", checkout, err)
	}
	session, err := client.CreatePortalSession(
		context.Background(),
		contracts.PortalRequest{
			OrganizationID: organizationID,
			ReturnURL:      "https://deli.dev/billing",
			IdempotencyKey: "billing-key",
		},
	)
	if err != nil || session.URL != "https://polar.sh/portal/1" ||
		!session.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("portal = %#v, %v", session, err)
	}
}

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

func TestEnsureCustomerCreatesAndReusesExternalCustomer(t *testing.T) {
	t.Parallel()
	const (
		organizationID = "0198a000-0000-7000-8000-000000000005"
		customerID     = "992fae2a-2a17-4b7a-8d9e-e287cf90131b"
	)
	created := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/v1/customers/external/"+organizationID:
			if !created {
				http.NotFound(writer, request)
				return
			}
		case request.Method == http.MethodPost &&
			request.URL.Path == "/v1/customers":
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload["external_id"] != organizationID ||
				payload["email"] != organizationID+"@delibase.deli.dev" ||
				payload["name"] != "Organization" ||
				payload["type"] != "team" {
				t.Errorf("customer payload = %#v", payload)
			}
			created = true
			writer.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected request = %s %s", request.Method, request.URL.Path)
			http.Error(writer, "unexpected request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"`+customerID+`"}`)
	}))
	defer server.Close()

	client, err := newClient(server.URL+"/v1", "polar-access-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	input := contracts.CustomerRequest{
		OrganizationID: organizationID,
		Name:           "Organization",
	}
	for range 2 {
		customer, ensureErr := client.EnsureCustomer(context.Background(), input)
		if ensureErr != nil || customer.ID != customerID {
			t.Fatalf("customer = %#v, %v", customer, ensureErr)
		}
	}
}
