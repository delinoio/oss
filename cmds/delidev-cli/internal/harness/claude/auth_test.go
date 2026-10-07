// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
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

func init() {
	if len(os.Args) < 2 || !strings.HasPrefix(filepath.Base(os.Getenv("HOME")), "fixture-auth-native-") {
		return
	}
	home := os.Getenv("CLAUDE_CONFIG_DIR")
	if home != filepath.Join(os.Getenv("HOME"), "claude") || os.Getenv("CLAUDE_SECURESTORAGE_CONFIG_DIR") != home {
		os.Exit(80)
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST", "HTTPS_PROXY", "NODE_OPTIONS"} {
		if os.Getenv(key) != "" {
			os.Exit(81)
		}
	}
	marker := filepath.Join(home, "fixture-account-state.json")
	if os.Args[1] == "--version" {
		fmt.Println(SupportedVersion + " (Claude Code)")
		os.Exit(0)
	}
	if len(os.Args) < 3 || os.Args[1] != "auth" {
		os.Exit(82)
	}
	switch os.Args[2] {
	case "login":
		if len(os.Args) != 4 || os.Args[3] != "--claudeai" {
			os.Exit(83)
		}
		fmt.Println(authFixtureURL())
		fmt.Print("Paste code here if prompted > ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || scanner.Text() != "fixture-approval#original-state" {
			os.Exit(84)
		}
		if os.WriteFile(marker, []byte(`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","email":"fixture@example.invalid","orgId":"fixture-org","subscriptionType":"pro"}`), 0600) != nil {
			os.Exit(85)
		}
		fmt.Println("Fixture login complete")
		os.Exit(0)
	case "status":
		data, err := os.ReadFile(marker)
		if err != nil {
			fmt.Println(`{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}`)
			os.Exit(1)
		}
		_, _ = os.Stdout.Write(data)
		os.Exit(0)
	case "logout":
		_ = os.Remove(marker)
		fmt.Println("Fixture logout complete")
		os.Exit(0)
	}
	os.Exit(86)
}

func TestNativeClaudeAuthProcessIsolationExitAndLogout(t *testing.T) {
	first, _ := fixtureConfig(t, "auth-native-first")
	second, _ := fixtureConfig(t, "auth-native-second")
	config := AuthConfig{Process: first.Process, Version: first.Version, Home: first.Home}
	config.Process.Env = append(config.Process.Env, "ANTHROPIC_API_KEY=foreign-fixture", "CLAUDE_CODE_OAUTH_TOKEN=foreign-fixture", "HTTPS_PROXY=http://foreign.invalid")
	// Several original child processes are joined in sequence. Race-instrumented
	// fixture children also wait on exit; this aggregate budget is independent
	// of the production command and login deadlines.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := VerifyAuthVersion(ctx, config); err != nil {
		t.Fatal(err)
	}
	before, err := ReadAuthStatus(ctx, config)
	if err != nil || before.LoggedIn {
		t.Fatal("empty profile was not isolated", err)
	}
	login, err := StartSubscriptionLogin(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Close()
	deadline := time.After(3 * time.Second)
	for {
		p, err := login.Progress()
		if err != nil {
			t.Fatal(err)
		}
		if p.URL != "" {
			if p.URL != authFixtureURL() || p.Method != domain.SubscriptionBrowserCode {
				t.Fatal("original progress changed")
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("native URL missing")
		case <-time.After(10 * time.Millisecond):
		}
	}
	code := []byte("fixture-approval#original-state")
	if err := login.Submit(code); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(code, make([]byte, len(code))) {
		t.Fatal("submitted bytes retained")
	}
	duplicate := []byte("fixture-approval#original-state")
	if login.Submit(duplicate) == nil {
		t.Fatal("original stdin consumed twice")
	}
	select {
	case <-login.Done():
	case <-ctx.Done():
		t.Fatal("original login did not exit")
	}
	if err := login.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := login.Close(); err != nil {
		t.Fatal(err)
	}
	status, err := ReadAuthStatus(ctx, config)
	if err != nil || !status.Subscription() {
		t.Fatal("native status did not confirm original process", err)
	}
	other, err := ReadAuthStatus(ctx, AuthConfig{Process: second.Process, Version: second.Version, Home: second.Home})
	if err != nil || other.LoggedIn {
		t.Fatal("other account profile borrowed authentication", err)
	}
	if err := LogoutSubscription(ctx, config); err != nil {
		t.Fatal(err)
	}
	after, err := ReadAuthStatus(ctx, config)
	if err != nil || after.LoggedIn {
		t.Fatal("native logout not observed", err)
	}
	p, err := login.Progress()
	if err != nil || p.URL != "" {
		t.Fatal("joined process retained URL")
	}
}

func TestNativeClaudeExecutionDoesNotInjectRelayAuthentication(t *testing.T) {
	config, _ := apiFixtureConfig(t, "valid")
	profile, _ := fixtureConfig(t, "auth-native-profile")
	config.Subscription = &NativeSubscriptionProfile{ID: domain.NewID(), Home: profile.Home}
	prepared, err := prepareAPIStream(config)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, entry := range prepared.Env {
		key, value, _ := strings.Cut(entry, "=")
		if _, duplicate := values[key]; duplicate {
			t.Fatal("duplicate environment authority")
		}
		values[key] = value
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"} {
		if _, present := values[key]; present {
			t.Fatal("API relay or imported authentication mixed with native subscription", key)
		}
	}
	if values["CLAUDE_CONFIG_DIR"] != profile.Home || values["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != profile.Home || values["HOME"] != filepath.Dir(config.Home) {
		t.Fatal("profile/history and execution runtime ownership mixed")
	}
	original := checkpointConfiguration(config, config.API.ServerOrigin)
	config.Subscription = &NativeSubscriptionProfile{ID: domain.NewID(), Home: profile.Home}
	if checkpointConfiguration(config, config.API.ServerOrigin) == original {
		t.Fatal("checkpoint admitted a replacement native profile")
	}
	raw, _ := json.Marshal(config)
	if bytes.Contains(raw, []byte(profile.Home)) || bytes.Contains(raw, []byte(config.API.Token)) {
		t.Fatal("checkpoint configuration serialized profile or authority")
	}
}

func TestNativeClaudeSubscriptionStreamValidatesOwnedProfileBeforeInput(t *testing.T) {
	for _, mode := range []string{"native-subscription-valid", "native-subscription-api-source", "native-subscription-foreign-provider"} {
		t.Run(mode, func(t *testing.T) {
			config, logs := apiFixtureConfig(t, mode)
			profile := filepath.Join(config.Process.Cwd, "owned-profile", "claude")
			if err := security.PrivateDir(filepath.Dir(profile)); err != nil {
				t.Fatal(err)
			}
			if err := security.PrivateDir(profile); err != nil {
				t.Fatal(err)
			}
			config.Subscription = &NativeSubscriptionProfile{ID: domain.NewID(), Home: profile}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			stream, err := OpenAPIStream(ctx, config)
			if mode == "native-subscription-valid" {
				if err != nil {
					t.Fatal(err)
				}
				applied, ok := stream.InitialAppliedSettings()
				if !ok || applied.Model != config.Model || applied.Effort == nil || *applied.Effort != config.Effort {
					t.Fatal("native subscription settings changed")
				}
				if err = stream.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || stream != nil {
				t.Fatal("mixed native API authority admitted before input")
			}
			if err = process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(logs.String(), "fixture@example.invalid") || strings.Contains(logs.String(), nativeAPIFixtureToken) {
				t.Fatal("native identity or publication authority entered logs")
			}
		})
	}
}
