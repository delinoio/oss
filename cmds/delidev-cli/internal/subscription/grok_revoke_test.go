// SPDX-License-Identifier: Apache-2.0
package subscription_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription/grokidp"
)

func TestGrokRevokeIsAPublicClientRevocationRequest(t *testing.T) {
	idp := grokidp.New(t)
	client, endpoints, bundle := loggedIn(t, idp, time.Now().Add(-5*time.Hour))
	outcome, err := client.Revoke(context.Background(), []byte(currentRefreshToken(t, bundle)))
	if err != nil || outcome != subscription.GrokRevoked || !outcome.Settled() {
		t.Fatalf("revoke = %v, %v", outcome, err)
	}
	forms := idp.RevokeForms()
	if idp.RevokeRequests() != 1 || len(forms) != 1 || len(forms[0]) != 3 || forms[0].Get("token") != "fake-refresh-token" || forms[0].Get("token_type_hint") != "refresh_token" || forms[0].Get("client_id") != subscription.GrokClientID || forms[0].Get("client_secret") != "" || idp.RevokeHeaders()[0].Get("Authorization") != "" {
		t.Fatal("revocation is not a public-client refresh-token revocation")
	}
	if subscription.GrokRevocationEndpoint != "https://auth.x.ai/oauth2/revoke" {
		t.Fatal("revocation endpoint drifted")
	}
	// The provider now refuses the revoked token.
	if _, err := refreshOnce(t, client, endpoints, bundle, time.Now()); err == nil {
		t.Fatal("a revoked refresh token was still accepted")
	} else if reason, _ := subscription.GrokOAuthFailure(err); reason != subscription.GrokReasonTokenRejected {
		t.Fatalf("refresh after revocation = %v", reason)
	}
}

func TestGrokRevokeClassifiesEveryOutcome(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		status  int
		body    string
		want    subscription.GrokRevocation
		reason  subscription.GrokOAuthReason
		settled bool
	}{
		{"ok", http.StatusOK, "", subscription.GrokRevoked, "", true},
		{"invalid-token", http.StatusBadRequest, `{"error":"invalid_token"}`, subscription.GrokAlreadyInvalid, "", true},
		{"invalid-grant", http.StatusBadRequest, `{"error":"invalid_grant"}`, subscription.GrokAlreadyInvalid, "", true},
		{"unauthorized-invalid-token", http.StatusUnauthorized, `{"error":"invalid_token"}`, subscription.GrokAlreadyInvalid, "", true},
		{"invalid-client", http.StatusUnauthorized, `{"error":"invalid_client"}`, "", subscription.GrokReasonRevokeRejected, false},
		{"unsupported-token-type", http.StatusBadRequest, `{"error":"unsupported_token_type"}`, "", subscription.GrokReasonRevokeRejected, false},
		{"bad-request-without-error", http.StatusBadRequest, `not json`, "", subscription.GrokReasonRevokeRejected, false},
		{"forbidden", http.StatusForbidden, `{"error":"invalid_token"}`, "", subscription.GrokReasonRevokeRejected, false},
		{"server-error", http.StatusInternalServerError, ``, "", subscription.GrokReasonRevokeUncertain, false},
		{"unavailable", http.StatusServiceUnavailable, ``, "", subscription.GrokReasonRevokeUncertain, false},
		{"redirect", http.StatusFound, ``, "", subscription.GrokReasonRevokeUncertain, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			idp := grokidp.New(t)
			idp.RevokeStatus, idp.RevokeBody = scenario.status, scenario.body
			client := subscription.GrokOAuthClient{Transport: idp.Transport}
			outcome, err := client.Revoke(context.Background(), []byte("fake-refresh-token"))
			if scenario.reason == "" {
				if err != nil || outcome != scenario.want || outcome.Settled() != scenario.settled {
					t.Fatalf("revoke = %v, %v", outcome, err)
				}
				return
			}
			if reason, _ := subscription.GrokOAuthFailure(err); err == nil || reason != scenario.reason || outcome.Settled() {
				t.Fatalf("revoke = %v, %v", outcome, err)
			}
			if idp.RevokeRequests() != 1 {
				t.Fatal("a failed revocation must not be retried")
			}
		})
	}
}

func TestGrokRevokeTimeoutAndTransportFailureAreUncertain(t *testing.T) {
	idp := grokidp.New(t)
	idp.RevokeGate = make(chan struct{})
	defer close(idp.RevokeGate)
	client := subscription.GrokOAuthClient{Transport: idp.Transport}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := client.Revoke(ctx, []byte("fake-refresh-token")); err == nil {
		t.Fatal("a timed-out revocation succeeded")
	} else if reason, _ := subscription.GrokOAuthFailure(err); reason != subscription.GrokReasonRevokeUncertain {
		t.Fatalf("timeout reason = %v", reason)
	}
	if _, err := (subscription.GrokOAuthClient{}).Revoke(context.Background(), []byte("x")); err == nil {
		t.Fatal("a missing transport succeeded")
	}
}
