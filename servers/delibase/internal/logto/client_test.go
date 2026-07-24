package logto

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDeleteUserUsesManagementTokenAndCachesIt(t *testing.T) {
	t.Parallel()
	const (
		clientSecret = "management-secret-that-must-never-leak"
		accessToken  = "management-access-token-that-must-never-leak"
	)
	var tokenRequests atomic.Int32
	var deleteRequests atomic.Int32
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/oidc/token":
			tokenRequests.Add(1)
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			if values.Get("client_secret") != clientSecret ||
				values.Get("scope") != "all" ||
				values.Get("resource") != server.URL+"/api" {
				t.Errorf("token form = %#v", values)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(
				writer,
				`{"access_token":"`+accessToken+`","expires_in":3600,"token_type":"Bearer"}`,
			)
		case request.Method == http.MethodDelete &&
			strings.HasPrefix(request.URL.Path, "/api/users/"):
			deleteRequests.Add(1)
			if request.Header.Get("Authorization") != "Bearer "+accessToken {
				t.Errorf("authorization header was not the management token")
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := New(server.URL+"/oidc", "client-id", clientSecret, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteUser(context.Background(), "user-1"); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteUser(context.Background(), "user-2"); err != nil {
		t.Fatal(err)
	}
	if tokenRequests.Load() != 1 || deleteRequests.Load() != 2 {
		t.Fatalf(
			"requests = token %d, delete %d",
			tokenRequests.Load(),
			deleteRequests.Load(),
		)
	}
}

func TestDeleteUserErrorsAreRedactedAndNotFoundIsSuccess(t *testing.T) {
	t.Parallel()
	const secret = "client-secret"
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path == "/oidc/token" {
			http.Error(writer, "provider leaked "+secret, http.StatusUnauthorized)
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	client, err := New(server.URL+"/oidc", "client-id", secret, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = client.DeleteUser(context.Background(), "user-1")
	if err == nil || strings.Contains(err.Error(), secret) ||
		strings.Contains(err.Error(), "provider leaked") {
		t.Fatalf("unsafe error = %v", err)
	}
}

func TestDeleteUserTreatsMissingProviderUserAsSuccess(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path == "/oidc/token" {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(
				writer,
				`{"access_token":"token","expires_in":3600,"token_type":"Bearer"}`,
			)
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	client, err := New(server.URL+"/oidc", "client-id", "client-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteUser(context.Background(), "already-deleted"); err != nil {
		t.Fatal(err)
	}
}
