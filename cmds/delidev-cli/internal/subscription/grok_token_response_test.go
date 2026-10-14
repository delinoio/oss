// SPDX-License-Identifier: Apache-2.0
package subscription_test

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription/grokidp"
)

// tokenResponseCase is one row of the token endpoint response table. The same
// rows run against the authorization_code exchange and the refresh_token grant:
// both are claimed before the request, so a response that is not exactly one
// JSON object must stay uncertain whatever its status.
type tokenResponseCase struct {
	name    string
	status  int
	body    string
	trailer string
	// reason is the expected failure; the empty reason expects success.
	reason subscription.GrokOAuthReason
}

var tokenResponseCases = []tokenResponseCase{
	{name: "200 clean"},
	{name: "200 trailing whitespace", trailer: " \n\t"},
	{name: "200 trailing JSON value", trailer: ` {"x":1}`, reason: subscription.GrokReasonTokenResponse},
	{name: "200 trailing bytes", trailer: `garbage`, reason: subscription.GrokReasonTokenResponse},
	{name: "200 trailing comma", trailer: `,`, reason: subscription.GrokReasonTokenResponse},
	{name: "200 duplicate member", body: `{"access_token":"a","access_token":"b","token_type":"Bearer","expires_in":3600}`, reason: subscription.GrokReasonTokenResponse},
	{name: "200 duplicate member differing in case", body: `{"access_token":"a","ACCESS_TOKEN":"b","token_type":"Bearer","expires_in":3600}`, status: 200, reason: subscription.GrokReasonTokenResponse},
	{name: "400 duplicate error member", status: 400, body: `{"error":"invalid_grant","error":"invalid_grant"}`, reason: subscription.GrokReasonTokenUncertain},
	{name: "401 duplicate error member differing in case", status: 401, body: `{"error":"invalid_client","Error":"invalid_client"}`, reason: subscription.GrokReasonTokenUncertain},
	{name: "200 not an object", body: `[{"access_token":"a"}]`, reason: subscription.GrokReasonTokenResponse},
	{name: "400 invalid_grant", status: 400, body: `{"error":"invalid_grant"}`, reason: subscription.GrokReasonTokenRejected},
	{name: "400 invalid_grant trailing JSON value", status: 400, body: `{"error":"invalid_grant"}`, trailer: ` {"x":1}`, reason: subscription.GrokReasonTokenUncertain},
	{name: "400 invalid_grant trailing bytes", status: 400, body: `{"error":"invalid_grant"}`, trailer: `junk`, reason: subscription.GrokReasonTokenUncertain},
	{name: "400 without an OAuth error", status: 400, body: `{"message":"bad"}`, reason: subscription.GrokReasonTokenUncertain},
	{name: "401 invalid_client", status: 401, body: `{"error":"invalid_client"}`, reason: subscription.GrokReasonTokenRejected},
	{name: "401 invalid_client trailing JSON value", status: 401, body: `{"error":"invalid_client"}`, trailer: `{"error":"invalid_grant"}`, reason: subscription.GrokReasonTokenUncertain},
	{name: "403 OAuth error", status: 403, body: `{"error":"access_denied"}`, reason: subscription.GrokReasonTokenUncertain},
	{name: "503 OAuth error", status: 503, body: `{"error":"temporarily_unavailable"}`, reason: subscription.GrokReasonTokenUncertain},
}

func TestGrokTokenResponseTable(t *testing.T) {
	for _, c := range tokenResponseCases {
		t.Run("exchange "+c.name, func(t *testing.T) {
			idp := grokidp.New(t)
			idp.TokenStatus, idp.TokenBody, idp.TokenTrailer = c.status, c.body, c.trailer
			client, endpoints, a, grant := exchangeFixture(t, idp)
			_, err := client.ExchangeCode(context.Background(), endpoints, []byte(grant.Code), a.Verifier, grant.RedirectURI)
			assertTokenResponse(t, c, err, idp.TokenRequests(), 1)
		})
		t.Run("refresh "+c.name, func(t *testing.T) {
			idp := grokidp.New(t)
			client, _, _ := loggedIn(t, idp, time.Now())
			before := idp.TokenRequests()
			idp.TokenStatus, idp.TokenBody, idp.TokenTrailer = c.status, c.body, c.trailer
			_, err := client.Refresh(context.Background(), []byte("fake-refresh-token"))
			assertTokenResponse(t, c, err, idp.TokenRequests(), before+1)
		})
	}
}

func assertTokenResponse(t *testing.T, c tokenResponseCase, err error, requests, wantRequests int) {
	t.Helper()
	if requests != wantRequests {
		t.Fatalf("token requests = %d, want %d", requests, wantRequests)
	}
	if c.reason == "" {
		if err != nil {
			t.Fatalf("unexpected failure: %v", err)
		}
		return
	}
	if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != c.reason {
		t.Fatalf("reason = %v, %v; want %v", reason, err, c.reason)
	}
}
