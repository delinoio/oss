// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

const (
	managedTokenFixtureToken = "sk-ant-oat01-ManagedFixtureTokenABCDEFGHIJKLMNOP0123456789_-"
	otherFixtureToken        = "sk-ant-oat01-OtherFixtureTokenZYXWVUTSRQPONMLKJIHG9876543210_-"
	setupFixtureCode         = "fixture-approval#original-state"
)

func setupTokenFixtureURL() string {
	q := url.Values{"code": {"true"}, "client_id": {nativeClaudeClientID}, "response_type": {"code"}, "redirect_uri": {"https://platform.claude.com/oauth/code/callback"}, "scope": {"user:inference"}, "code_challenge": {strings.Repeat("a", 43)}, "code_challenge_method": {"S256"}, "state": {strings.Repeat("b", 43)}}
	return "https://claude.com/cai/oauth/authorize?" + q.Encode()
}

// The test binary doubles as the pinned native CLI. The Home directory name
// selects one fixture behavior; the process runs on the harness-supplied PTY.
func init() {
	if len(os.Args) != 2 || os.Args[1] != "setup-token" || !strings.HasPrefix(filepath.Base(os.Getenv("HOME")), "fixture-setup-token-") {
		return
	}
	mode := strings.TrimPrefix(filepath.Base(os.Getenv("HOME")), "fixture-setup-token-")
	home := os.Getenv("CLAUDE_CONFIG_DIR")
	if home != filepath.Join(os.Getenv("HOME"), "claude") || os.Getenv("CLAUDE_SECURESTORAGE_CONFIG_DIR") != home {
		os.Exit(80)
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST", "HTTPS_PROXY", "NODE_OPTIONS", "CI"} {
		if os.Getenv(key) != "" {
			os.Exit(81)
		}
	}
	if os.Getenv("TERM") != "xterm-256color" {
		os.Exit(82)
	}
	if info, err := os.Stdout.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		os.Exit(83)
	}
	emit := func(text string) { fmt.Print(strings.ReplaceAll(text, "\n", "\r\n")) }
	fixtureURL := setupTokenFixtureURL()
	switch mode {
	case "foreign-url":
		fixtureURL = strings.Replace(fixtureURL, "scope=user%3Ainference", "scope=user%3Ainference+user%3Aprofile", 1)
	case "callback-url":
		fixtureURL = strings.Replace(fixtureURL, "https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback", "http%3A%2F%2Flocalhost%3A4000%2Fcallback", 1)
	}
	emit("\x1b[2J\x1b[HBrowser didn't open? Use the url below to sign in\n" + fixtureURL + "\x1b[0m\nPaste code here if prompted > ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() || scanner.Text() != setupFixtureCode {
		os.Exit(84)
	}
	success := "\x1b[2J\x1b[H\x1b[32m✓ Long-lived authentication token created successfully!\x1b[0m\n\nYour OAuth token (valid for 1 year):\n\n"
	trailer := "\n\nStore this token securely. You won't be able to see it again.\n\nUse this token by setting: export CLAUDE_CODE_OAUTH_TOKEN=<token>\n"
	switch mode {
	case "success", "foreign-url", "callback-url":
		emit(success + managedTokenFixtureToken + trailer)
	case "redraw":
		emit(success + managedTokenFixtureToken + trailer + "\x1b[2K(Copied!)\n" + managedTokenFixtureToken + "\n")
	case "ambiguous":
		emit(success + managedTokenFixtureToken + "\n" + otherFixtureToken + trailer)
	case "malformed":
		emit(success + "sk-ant-oat01-short" + trailer)
	case "no-token":
		emit(success + trailer)
	case "no-marker":
		emit("Your OAuth token (valid for 1 year):\n" + managedTokenFixtureToken + trailer)
	case "oversized":
		emit(strings.Repeat("x", setupTokenOutputLimit+1))
	case "rejected":
		emit("Invalid code. Please make sure the full code was copied\n")
		time.Sleep(200 * time.Millisecond)
		os.Exit(1)
	case "exit-failure":
		emit(success + managedTokenFixtureToken + trailer)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestSetupTokenURLProfileIsSeparateFromAuthLogin(t *testing.T) {
	good := setupTokenFixtureURL()
	if err := ValidateSetupTokenURL(good); err != nil {
		t.Fatal(err)
	}
	if ValidateLoginURL(good) == nil {
		t.Fatal("the inference-only setup-token address was accepted by the auth-login profile")
	}
	if ValidateSetupTokenURL(authFixtureURL()) == nil {
		t.Fatal("the auth-login address was accepted by the setup-token profile")
	}
	for name, mutate := range map[string]func(url.Values) string{
		"foreign client": func(q url.Values) string { q.Set("client_id", "another-client"); return "" },
		"profile scope":  func(q url.Values) string { q.Set("scope", "user:inference user:profile"); return "" },
		"other scope":    func(q url.Values) string { q.Set("scope", "org:create_api_key"); return "" },
		"no code marker": func(q url.Values) string { q.Del("code"); return "" },
		"callback":       func(q url.Values) string { q.Set("redirect_uri", "http://localhost:4000/callback"); return "" },
		"duplicate":      func(q url.Values) string { q.Add("state", strings.Repeat("c", 43)); return "" },
		"unknown":        func(q url.Values) string { q.Set("unexpected", "1"); return "" },
	} {
		u, _ := url.Parse(good)
		q := u.Query()
		mutate(q)
		u.RawQuery = q.Encode()
		if ValidateSetupTokenURL(u.String()) == nil {
			t.Fatalf("%s address was accepted", name)
		}
	}
	other, _ := url.Parse(good)
	other.Host = "attacker.invalid"
	if ValidateSetupTokenURL(other.String()) == nil {
		t.Fatal("foreign host was accepted")
	}
}

func TestSetupTokenScreenParserAcceptsOneDistinctEmission(t *testing.T) {
	address := setupTokenFixtureURL()
	// Partial address and partial token never become progress or credentials.
	partial := parseSetupTokenScreen([]byte("sign in:\r\n" + address[:60]))
	if partial.URL != "" || partial.Problem != nil {
		t.Fatal("accepted a partial address")
	}
	screen := parseSetupTokenScreen([]byte("\x1b[1;1H\x1b[2Kurl:\r\n" + address + "\x1b[0m\r\n> "))
	if screen.URL != address || screen.Problem != nil {
		t.Fatalf("lost the original address: %v", screen.Problem)
	}
	again := parseSetupTokenScreen([]byte(address + "\n" + address + "\n"))
	if again.URL != address || again.Problem != nil {
		t.Fatal("identical redraws of the address were rejected")
	}
	other := strings.Replace(address, "b", "c", 1)
	if parseSetupTokenScreen([]byte(address+"\n"+other+"\n")).Problem == nil {
		t.Fatal("two different addresses were accepted")
	}
	if parseSetupTokenScreen([]byte("https://claude.com/cai/oauth/authorize?client_id=x\n")).Problem == nil {
		t.Fatal("a foreign address profile was accepted")
	}
	// A token without a following delimiter is incomplete.
	if got := parseSetupTokenScreen([]byte("created successfully\n" + managedTokenFixtureToken)); got.Token != nil {
		t.Fatal("accepted a trailing partial token")
	}
	one := parseSetupTokenScreen([]byte("created successfully\n" + managedTokenFixtureToken + "\nexport CLAUDE_CODE_OAUTH_TOKEN=<token>\n"))
	if string(one.Token) != managedTokenFixtureToken || !one.Completed || one.Problem != nil {
		t.Fatal("rejected one unambiguous emission")
	}
	repeated := parseSetupTokenScreen([]byte("created successfully\n" + managedTokenFixtureToken + "\n" + managedTokenFixtureToken + "\n"))
	if string(repeated.Token) != managedTokenFixtureToken || repeated.Problem != nil {
		t.Fatal("identical redraws of one token were rejected")
	}
	for name, value := range map[string]string{
		"ambiguous":  managedTokenFixtureToken + "\n" + otherFixtureToken + "\n",
		"malformed":  "sk-ant-oat01-short\n",
		"too many":   strings.Repeat(managedTokenFixtureToken+"\n", setupTokenMaxEmissions+1),
		"bad suffix": managedTokenFixtureToken + "!\n",
	} {
		if got := parseSetupTokenScreen([]byte("created successfully\n" + value)); got.Problem == nil && name != "bad suffix" {
			t.Fatalf("%s output was accepted", name)
		}
	}
	if sanitized := sanitizeSetupTokenScreen([]byte("a\x1b[31mb\x1b[2Kc\x1b]8;;https://example.invalid\x07d\r\ne")); string(sanitized) != "ab\nc\nd\ne" {
		t.Fatalf("unexpected sanitized text %q", sanitized)
	}
}

// setupTokenFixture skips on Windows, where the adapter deliberately fails
// closed because no verified no-op browser opener exists.
func setupTokenFixture(t *testing.T, mode string) SetupTokenConfig {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("setup-token fails closed on Windows without a verified browser blocker")
	}
	return setupTokenFixtureConfig(t, mode)
}

func setupTokenFixtureConfig(t *testing.T, mode string) SetupTokenConfig {
	t.Helper()
	config, _ := fixtureConfig(t, "setup-token-"+mode)
	config.Process.Env = append(config.Process.Env, "ANTHROPIC_API_KEY=foreign-fixture", "ANTHROPIC_AUTH_TOKEN=foreign-fixture", "CLAUDE_CODE_OAUTH_TOKEN=foreign-fixture", "HTTPS_PROXY=http://foreign.invalid", "CI=1")
	return SetupTokenConfig{Process: config.Process, Version: config.Version, Home: config.Home}
}

func waitSetupTokenURL(t *testing.T, login *SetupTokenLogin) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		progress, err := login.Progress()
		if err != nil {
			return ""
		}
		if progress.URL != "" {
			return progress.URL
		}
		select {
		case <-deadline:
			t.Fatal("native address missing")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestSetupTokenRunsOriginalCLIOnAPTYAndEmitsOneToken(t *testing.T) {
	config := setupTokenFixture(t, "success")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	login, err := StartSetupToken(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Close()
	if got := waitSetupTokenURL(t, login); got != setupTokenFixtureURL() {
		t.Fatal("original address changed")
	}
	if _, err := login.Token(); err == nil {
		t.Fatal("a token was released before the original child finished")
	}
	code := []byte(setupFixtureCode)
	if err := login.Submit(code); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(code, make([]byte, len(code))) {
		t.Fatal("submitted bytes retained")
	}
	if login.Submit([]byte(setupFixtureCode)) == nil {
		t.Fatal("approval code consumed twice")
	}
	select {
	case <-login.Done():
	case <-ctx.Done():
		t.Fatal("original child did not exit")
	}
	if err := login.Wait(); err != nil {
		t.Fatal(err)
	}
	token, err := login.Token()
	if err != nil || string(token) != managedTokenFixtureToken {
		t.Fatalf("token was not emitted: %v", err)
	}
	clear(token)
	if _, err := login.Token(); err == nil {
		t.Fatal("the emitted token was released twice")
	}
	if err := login.Close(); err != nil {
		t.Fatal(err)
	}
	if progress, err := login.Progress(); err != nil || progress.URL != "" {
		t.Fatal("joined process retained the address")
	}
	if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
		t.Fatal(err)
	}
}

func TestSetupTokenAcceptsCleanExitWithOneTokenWithoutFinalHeading(t *testing.T) {
	config := setupTokenFixture(t, "no-marker")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	login, err := StartSetupToken(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Close()
	if got := waitSetupTokenURL(t, login); got != setupTokenFixtureURL() {
		t.Fatal("original address changed")
	}
	if err := login.Submit([]byte(setupFixtureCode)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-login.Done():
	case <-ctx.Done():
		t.Fatal("original child did not exit")
	}
	if err := login.Wait(); err != nil {
		t.Fatal(err)
	}
	token, err := login.Token()
	if err != nil || string(token) != managedTokenFixtureToken {
		t.Fatalf("cleanly emitted token was rejected: %v", err)
	}
	clear(token)
}

func TestSetupTokenFailureModesNeverReleaseAToken(t *testing.T) {
	for _, mode := range []string{"foreign-url", "callback-url", "ambiguous", "malformed", "no-token", "oversized", "rejected", "exit-failure", "redraw"} {
		t.Run(mode, func(t *testing.T) {
			config := setupTokenFixture(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			login, err := StartSetupToken(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer login.Close()
			url := waitSetupTokenURL(t, login)
			if mode == "foreign-url" || mode == "callback-url" {
				if url != "" {
					t.Fatal("a foreign authorization profile was presented")
				}
				if _, err := login.Progress(); err == nil {
					t.Fatal("a foreign authorization profile was accepted")
				}
				return
			}
			if mode != "oversized" && url == "" {
				t.Fatal("native address missing")
			}
			if url != "" {
				if err := login.Submit([]byte(setupFixtureCode)); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-login.Done():
			case <-ctx.Done():
				t.Fatal("original child did not exit")
			}
			waitErr := login.Wait()
			token, tokenErr := login.Token()
			defer clear(token)
			if mode == "redraw" {
				if waitErr != nil || tokenErr != nil || string(token) != managedTokenFixtureToken {
					t.Fatalf("identical redraws were rejected: %v %v", waitErr, tokenErr)
				}
				return
			}
			if tokenErr == nil || token != nil {
				t.Fatal("an unsafe emission produced a token")
			}
			if strings.Contains(fmt.Sprint(tokenErr), "sk-ant") || strings.Contains(domain.SafeError(tokenErr).Message, "sk-ant") {
				t.Fatal("failure exposed token material")
			}
		})
	}
}

func TestSetupTokenBrowserBlockerFailsClosedWithoutVerifiedNoopOpener(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "noop")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	for _, tc := range []struct {
		name       string
		goos       string
		candidates []string
		want       string
	}{
		{"unix executable", "linux", []string{missing, executable}, executable},
		{"unix without candidate", "linux", []string{missing}, ""},
		{"unix non-executable file", "darwin", []string{plain}, ""},
		{"windows never trusts a candidate", "windows", []string{executable}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := setupTokenBrowserBlocker(tc.goos, tc.candidates)
			if tc.want != "" {
				if err != nil || got != tc.want {
					t.Fatalf("blocker = %q, %v", got, err)
				}
				return
			}
			if got != "" || domain.SafeError(err).Code != domain.Unsupported {
				t.Fatalf("a host without a verified opener did not fail closed: %q %v", got, err)
			}
		})
	}
}

func TestSetupTokenEnvironmentIsolation(t *testing.T) {
	config := setupTokenFixtureConfig(t, "success")
	env, err := setupTokenEnvironment(config)
	if runtime.GOOS == "windows" {
		if domain.SafeError(err).Code != domain.Unsupported {
			t.Fatal("Windows must fail closed instead of launching the server user's browser")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if _, duplicate := values[key]; duplicate {
			t.Fatal("duplicate environment authority", key)
		}
		values[key] = value
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL", "HTTPS_PROXY", "CI", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"} {
		if _, present := values[key]; present {
			t.Fatalf("%s reached the setup-token child", key)
		}
	}
	if values["BROWSER"] == "" {
		t.Fatal("the child can open the server user's browser")
	}
	if values["TERM"] != "xterm-256color" || values["CLAUDE_CONFIG_DIR"] != config.Home || values["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != config.Home || values["HOME"] != filepath.Dir(config.Home) {
		t.Fatal("setup-token runtime ownership is not private")
	}
}

func TestManagedTokenExecutionEnvironmentAndInitialization(t *testing.T) {
	for _, mode := range []string{"managed-token-valid", "managed-token-api-source", "managed-token-identity", "managed-token-wrong-source", "managed-token-no-source"} {
		t.Run(mode, func(t *testing.T) {
			config, logs := apiFixtureConfig(t, mode)
			config.ManagedToken = []byte(managedTokenFixtureToken)
			// Launch preparation writes the one-time instruction file, so inspect a
			// separate identical runtime before opening the real stream.
			inspect, _ := apiFixtureConfig(t, mode)
			inspect.ManagedToken = []byte(managedTokenFixtureToken)
			prepared, err := prepareAPIStream(inspect)
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]string{}
			for _, entry := range prepared.Env {
				key, value, _ := strings.Cut(entry, "=")
				if _, duplicate := values[key]; duplicate {
					t.Fatal("duplicate environment authority", key)
				}
				values[key] = value
			}
			for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "HTTPS_PROXY", "NODE_OPTIONS", "BASH_ENV"} {
				if _, present := values[key]; present {
					t.Fatalf("%s survived suppression", key)
				}
			}
			if values["CLAUDE_CODE_OAUTH_TOKEN"] != managedTokenFixtureToken || values["CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"] != "1" || values["CLAUDE_CONFIG_DIR"] != inspect.Home || values["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != inspect.Home {
				t.Fatal("managed token environment is incomplete")
			}
			for _, arg := range prepared.Args {
				if arg == "--bare" {
					t.Fatal("--bare ignores the managed token source")
				}
			}
			if !strings.Contains(strings.Join(inspect.ProtectedValues(), "\x00"), managedTokenFixtureToken) {
				t.Fatal("the managed token is not a protected output value")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			stream, err := OpenAPIStream(ctx, config)
			if mode == "managed-token-valid" {
				if err != nil {
					t.Fatal(err)
				}
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || stream != nil {
				t.Fatal("token presence alone was accepted as native readiness")
			}
			if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(logs.String(), managedTokenFixtureToken) {
				t.Fatal("the managed token entered logs")
			}
		})
	}
}

func TestManagedTokenRejectsMixedAuthorityAndCheckpointsAreDistinct(t *testing.T) {
	config, _ := apiFixtureConfig(t, "managed-token-valid")
	config.ManagedToken = []byte("not-a-token")
	if _, err := prepareAPIStream(config); err == nil {
		t.Fatal("a malformed managed token was launched")
	}
	config.ManagedToken = []byte(managedTokenFixtureToken)
	profile, _ := fixtureConfig(t, "auth-native-profile")
	config.Subscription = &NativeSubscriptionProfile{ID: domain.NewID(), Home: profile.Home}
	if _, err := prepareAPIStream(config); err == nil {
		t.Fatal("a managed token was mixed with a device profile")
	}
	config.Subscription = nil
	managed := checkpointConfiguration(config, config.API.ServerOrigin)
	config.ManagedToken = nil
	if managed == checkpointConfiguration(config, config.API.ServerOrigin) {
		t.Fatal("a managed-token checkpoint is interchangeable with a relay checkpoint")
	}
}
