package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestIdentityClosedAuthorityMinimalProjection(t *testing.T) {
	client := New()
	calls := 0
	client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.String() != "https://api.github.com/user" || r.Header.Get("Authorization") != "Bearer fixture-pat" || r.Header.Get("X-GitHub-Api-Version") != apiVersion || r.Header.Get("Cookie") != "" {
			t.Fatal("unexpected request authority")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":9007199254740993,"node_id":"U_fixture","login":"test-user","type":"User","email":"must-not-retain@example.test","extra":{"new":true}}`))}, nil
	})
	observed, err := client.Identity(context.Background(), []byte("fixture-pat"))
	if err != nil || observed.State != domain.IntegrationIdentityVerified || observed.Identity.ID != "9007199254740993" || calls != 1 {
		t.Fatalf("identity: %+v %v", observed, err)
	}
	raw, _ := json.Marshal(observed)
	if strings.Contains(string(raw), "must-not-retain") || strings.Contains(string(raw), "fixture-pat") || strings.Contains(string(raw), "extra") {
		t.Fatal("unselected data retained")
	}
}
func TestIdentityMalformedAndRestrictedResponses(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		headers http.Header
		body    string
		state   domain.IntegrationValidationState
	}{
		{"expired", 401, nil, `secret-body`, domain.IntegrationInvalidToken},
		{"sso", 403, http.Header{"X-Github-Sso": []string{"required; url=https://private.example/secret"}}, `secret-body`, domain.IntegrationSSORequired},
		{"restricted", 403, nil, `secret-body`, domain.IntegrationAccessRestricted},
		{"rate", 403, http.Header{"X-Ratelimit-Remaining": []string{"0"}}, `secret-body`, domain.IntegrationRateLimited},
		{"rate429", 429, nil, `secret-body`, domain.IntegrationRateLimited},
		{"redirect", 302, http.Header{"Location": []string{"https://private.example/secret"}}, `secret-body`, domain.IntegrationUnavailable},
		{"duplicate", 200, nil, `{"id":17,"id":18,"node_id":"U_1","login":"user","type":"User"}`, domain.IntegrationUnavailable},
		{"null", 200, nil, `null`, domain.IntegrationUnavailable},
		{"wrong-type", 200, nil, `{"id":17,"node_id":"U_1","login":"user","type":"Organization"}`, domain.IntegrationUnavailable},
		{"rounded-id", 200, nil, `{"id":1.7,"node_id":"U_1","login":"user","type":"User"}`, domain.IntegrationUnavailable},
		{"too-large", 200, nil, strings.Repeat(" ", 64<<10+1), domain.IntegrationUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := New()
			calls := 0
			client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Header: tc.headers, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			result, err := client.Identity(context.Background(), []byte("test-token"))
			if err != nil || result.State != tc.state || result.Identity != nil || result.Problem == nil || calls != 1 {
				t.Fatalf("wrong classification %+v %v calls=%d", result, err, calls)
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "private.example") {
				t.Fatal("raw diagnostic retained")
			}
		})
	}
}
func TestIdentityTransportIsolationAndCancellation(t *testing.T) {
	client := New()
	transport := client.http.Transport.(*http.Transport)
	if transport.Proxy != nil || !transport.DisableKeepAlives || client.http.Jar != nil || client.http.CheckRedirect(&http.Request{}, nil) != http.ErrUseLastResponse || transport.ResponseHeaderTimeout == 0 || transport.TLSHandshakeTimeout == 0 || transport.MaxResponseHeaderBytes > 32<<10 {
		t.Fatal("unbounded or ambient transport")
	}
	client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Identity(ctx, []byte("test-token")); err == nil || domain.SafeError(err).Code != domain.Canceled {
		t.Fatal("cancellation lost", err)
	}
}
