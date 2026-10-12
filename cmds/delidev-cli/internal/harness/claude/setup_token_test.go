// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupURL() string {
	return "https://claude.com/cai/oauth/authorize?code=true&client_id=" + nativeClaudeClientID + "&response_type=code&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback&scope=user%3Ainference&code_challenge=" + strings.Repeat("c", 43) + "&code_challenge_method=S256&state=" + strings.Repeat("s", 43)
}
func TestSetupTokenExactManualProfile(t *testing.T) {
	u := setupURL()
	if ValidateSetupTokenURL(u) != nil || !SetupTokenCodeValid([]byte("original#"+strings.Repeat("s", 43)), u) {
		t.Fatal("supported profile")
	}
	for _, bad := range []string{u + "&login_hint=x", strings.Replace(u, "user%3Ainference", "user%3Ainference+user%3Aprofile", 1), u + "&scope=user%3Ainference", strings.Replace(u, "code=true", "code=false", 1)} {
		if ValidateSetupTokenURL(bad) == nil {
			t.Fatal("accepted unsupported profile")
		}
	}
	if SetupTokenCodeValid([]byte("original#wrong"), u) || SetupTokenCodeValid([]byte("original#"+strings.Repeat("s", 43)+"#x"), u) {
		t.Fatal("code state not fenced")
	}
}
func successScreen(token string) string {
	return "✓ Long-lived authentication token created successfully!\r\n\r\nYour OAuth token (valid for 1 year):\r\n\r\n" + token + "\r\n\r\nStore this token securely. You won't be able to see it again.\r\n\r\nUse this token by setting: export CLAUDE_CODE_OAUTH_TOKEN=<token>\r\n"
}
func TestSetupTokenPrivateScreenSplitAndRedraw(t *testing.T) {
	s := &setupScreen{}
	defer s.close()
	input := setupURL() + "\r\n\r\nPaste code here if prompted > "
	for _, b := range []byte(input) {
		if _, e := s.Write([]byte{b}); e != nil {
			t.Fatal(e)
		}
	}
	if s.progress.URL != setupURL() {
		t.Fatal("original URL missing")
	}
	frame := "\r\x1b[2J\x1b[H" + successScreen("opaque.synthetic.token")
	for _, b := range []byte(frame) {
		if _, e := s.Write([]byte{b}); e != nil {
			t.Fatal(e)
		}
	}
	if !s.success || !bytes.Equal(s.token, []byte("opaque.synthetic.token")) {
		t.Fatal("semantic token missing")
	}
	if _, e := s.Write([]byte(frame)); e != nil {
		t.Fatal("same native redraw", e)
	}
	if _, e := s.Write([]byte("\x1b[2J\x1b[H" + successScreen("different.synthetic.token"))); e == nil {
		t.Fatal("conflicting token accepted")
	}
}
func TestSetupTokenPrivateScreenRejectsAmbiguity(t *testing.T) {
	for _, raw := range []string{"\x1b]52;c;secret\a", "\x1b[999B", "\xff", strings.Repeat("x", 512<<10+1), successScreen("a token"), successScreen("token") + successScreen("other"), "Press Enter to retry"} {
		s := &setupScreen{}
		if _, e := s.Write([]byte(raw)); e == nil {
			t.Fatal("accepted ambiguous terminal")
		}
		s.close()
	}
}
func TestSetupTokenNativeReadinessRequiresSelectedSource(t *testing.T) {
	result := fixtureResult()
	result["commands"] = []any{map[string]any{"name": "compact", "description": "Compact native context", "argumentHint": "[instructions]"}}
	account := map[string]any{"apiProvider": "firstParty", "tokenSource": "CLAUDE_CODE_OAUTH_TOKEN"}
	result["account"] = account
	raw, _ := json.Marshal(result)
	if validateInitializeAuthentication(raw, "dontAsk", "ANTHROPIC_API_KEY", TokenAuthentication) != nil {
		t.Fatal("token source rejected")
	}
	for _, key := range []string{"apiKeySource", "email", "organization", "subscriptionType"} {
		account[key] = "unexpected"
		raw, _ = json.Marshal(result)
		if validateInitializeAuthentication(raw, "dontAsk", "ANTHROPIC_API_KEY", TokenAuthentication) == nil {
			t.Fatal("mixed source accepted")
		}
		delete(account, key)
	}
	account["tokenSource"] = "ANTHROPIC_AUTH_TOKEN"
	raw, _ = json.Marshal(result)
	if validateInitializeAuthentication(raw, "dontAsk", "ANTHROPIC_API_KEY", TokenAuthentication) == nil {
		t.Fatal("API bearer adopted")
	}
}

func init() {
	if len(os.Args) < 2 || os.Args[1] != "setup-token" || !strings.HasPrefix(filepath.Base(os.Getenv("HOME")), "fixture-setup-token-") {
		return
	}
	mode := strings.TrimPrefix(filepath.Base(os.Getenv("HOME")), "fixture-setup-token-")
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" || os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("BROWSER") != "/usr/bin/true" {
		os.Exit(70)
	}
	_, _ = os.Stdout.WriteString(setupURL() + "\r\n\r\nPaste code here if prompted > ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() || scanner.Text() != "original#"+strings.Repeat("s", 43) {
		os.Exit(71)
	}
	_, _ = os.Stdout.WriteString("\r\x1b[2J\x1b[H" + successScreen("synthetic-joined-token"))
	if mode == "nonzero" {
		os.Exit(2)
	}
	os.Exit(0)
}
func TestSetupTokenSupervisedPTYRequiresOriginalInputAndJoinedExit(t *testing.T) {
	for _, mode := range []string{"valid", "nonzero", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			cfg, _ := fixtureConfig(t, "setup-token-"+mode)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			login, e := StartSetupToken(ctx, AuthConfig{Process: cfg.Process, Home: cfg.Home, Version: SupportedVersion}, "/usr/bin/true")
			if e != nil {
				t.Fatal(e)
			}
			defer login.Close()
			deadline := time.Now().Add(3 * time.Second)
			for {
				p, e := login.Progress()
				if e != nil {
					t.Fatal(e)
				}
				if p.URL != "" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("no private original prompt")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if mode == "canceled" {
				cancel()
				if _, e = login.Token(); e == nil {
					t.Fatal("canceled login captured token")
				}
				return
			}
			if e = login.Submit([]byte("original#" + strings.Repeat("s", 43))); e != nil {
				t.Fatal(e)
			}
			if login.Submit([]byte("original#"+strings.Repeat("s", 43))) == nil {
				t.Fatal("original input replay")
			}
			select {
			case <-login.Done():
			case <-ctx.Done():
				t.Fatal("native process did not settle")
			}
			token, e := login.Token()
			defer clear(token)
			if mode == "valid" {
				if e != nil || string(token) != "synthetic-joined-token" {
					t.Fatal("no joined semantic token", e)
				}
			} else if e == nil {
				t.Fatal("nonzero exit accepted")
			}
		})
	}
}

func TestManagedClaudeTokenSystemInitializeCannotUseAPISource(t *testing.T) {
	b, cfg := lifecycleFixture(t)
	cfg.Authentication = TokenAuthentication
	b, e := BindExecution(cfg, b.input, lifecycleFixtureText)
	if e != nil {
		t.Fatal(e)
	}
	original := lifecycleInit(t, b)
	if b.validateSystemInit(original.Body) == nil {
		t.Fatal("API source accepted for managed token")
	}
	token := lifecycleChange(t, original, "apiKeySource", "none")
	if b.validateSystemInit(token.Body) != nil {
		t.Fatal("pinned token source refused")
	}
}
