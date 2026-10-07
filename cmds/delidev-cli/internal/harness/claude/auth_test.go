// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func authFixtureURL() string {
	q := url.Values{"client_id": {nativeClaudeClientID}, "response_type": {"code"}, "redirect_uri": {"https://platform.claude.com/oauth/code/callback"}, "scope": {"user:profile user:inference user:sessions:claude_code"}, "code_challenge": {strings.Repeat("a", 43)}, "code_challenge_method": {"S256"}, "state": {strings.Repeat("b", 32)}, "code": {"true"}}
	return "https://claude.com/cai/oauth/authorize?" + q.Encode()
}
func TestNativeClaudeLoginURLAndSingleLineCode(t *testing.T) {
	good := authFixtureURL()
	if err := ValidateLoginURL(good); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*url.URL){
		func(u *url.URL) { u.Host = "attacker.invalid" },
		func(u *url.URL) { q := u.Query(); q.Set("client_id", "another-client"); u.RawQuery = q.Encode() },
		func(u *url.URL) { q := u.Query(); q.Add("state", strings.Repeat("c", 32)); u.RawQuery = q.Encode() },
		func(u *url.URL) {
			q := u.Query()
			q.Set("redirect_uri", "http://localhost:0/callback")
			u.RawQuery = q.Encode()
		},
		func(u *url.URL) {
			q := u.Query()
			q.Set("scope", "user:profile user:inference unknown")
			u.RawQuery = q.Encode()
		},
	} {
		u, _ := url.Parse(good)
		mutate(u)
		if ValidateLoginURL(u.String()) == nil {
			t.Fatal("accepted substituted native authority")
		}
	}
	output := &loginOutput{}
	_, _ = output.Write([]byte("Open this address:\n" + good[:40]))
	if output.progress.URL != "" {
		t.Fatal("accepted partial URL")
	}
	_, _ = output.Write([]byte(good[40:] + "\nPaste code here if prompted > "))
	if output.progress.URL != good || output.progress.Method != domain.SubscriptionBrowserCode {
		t.Fatal("lost original native URL or method")
	}
	if !LoginCodeValid([]byte("fixture-code#fixture-state")) {
		t.Fatal("valid native code rejected")
	}
	for _, bad := range []string{"", "code\nsecond", " code", "code\x1b", strings.Repeat("x", 16385)} {
		if LoginCodeValid([]byte(bad)) {
			t.Fatal("accepted invalid original stdin input")
		}
	}
	output.data = nil
	output.progress = AuthLoginProgress{}
	if output.progress.URL != "" || len(output.data) != 0 {
		t.Fatal("sensitive presentation survived cleanup")
	}
}
func TestNativeClaudeStatusRejectsAPIAuthentication(t *testing.T) {
	email, org, plan := "fixture@example.invalid", "fixture-org", "pro"
	status := AuthStatus{LoggedIn: true, AuthMethod: "claude.ai", APIProvider: "firstParty", Email: &email, OrgID: &org, SubscriptionType: &plan}
	if !status.Subscription() {
		t.Fatal("subscription status rejected")
	}
	for _, method := range []string{"api_key", "api_key_helper", "oauth_token", "third_party", "none"} {
		bad := status
		bad.AuthMethod = method
		if bad.Subscription() {
			t.Fatal("API or imported token accepted")
		}
	}
	source := "apiKeyHelper"
	status.APIKeySource = &source
	if status.Subscription() {
		t.Fatal("managed API key accepted as subscription")
	}
}
func TestNativeClaudeSubscriptionInitializeIsSeparate(t *testing.T) {
	result := fixtureResult()
	result["commands"] = []any{map[string]any{"name": "compact", "description": "Compact native context", "argumentHint": "[instructions]"}}
	account := map[string]any{"apiProvider": "firstParty", "email": "fixture@example.invalid", "organization": "Fixture", "subscriptionType": "pro"}
	result["account"] = account
	raw, _ := json.Marshal(result)
	if validateInitializeMode(raw, "dontAsk", "ANTHROPIC_API_KEY", true) != nil {
		t.Fatal("native subscription initialize rejected")
	}
	if validateInitializeProfile(raw, "dontAsk", "ANTHROPIC_API_KEY") == nil || validateInitializeProfile(raw, "dontAsk", "") == nil {
		t.Fatal("subscription metadata admitted into API/discovery")
	}
	account["tokenSource"] = "ANTHROPIC_AUTH_TOKEN"
	raw, _ = json.Marshal(result)
	if validateInitializeMode(raw, "dontAsk", "ANTHROPIC_API_KEY", true) == nil {
		t.Fatal("mixed API/subscription authentication accepted")
	}
}
