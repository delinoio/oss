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
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

const (
	usageFixtureAccess  = "sk-ant-oat01-UsageFixtureAccessTokenABCDEFGHIJKLMNOP0123456789"
	usageFixtureRefresh = "sk-ant-ort01-UsageFixtureRefreshTokenABCDEFGHIJKLMNOP0123456789"
	usageFixtureRotated = "sk-ant-ort01-UsageFixtureRotatedTokenABCDEFGHIJKLMNOP0123456789"
	usageFixtureCode    = "usage-approval#original-state"
)

func usageFixtureURL(redirect string) string {
	q := url.Values{"client_id": {nativeClaudeClientID}, "response_type": {"code"}, "redirect_uri": {redirect}, "scope": {"user:profile user:inference user:sessions:claude_code"}, "code_challenge": {strings.Repeat("a", 43)}, "code_challenge_method": {"S256"}, "state": {strings.Repeat("b", 32)}}
	if redirect == usageManualCallback {
		q.Set("code", "true")
	}
	return "https://claude.com/cai/oauth/authorize?" + q.Encode()
}

func usageFixtureRecord(refresh string) []byte {
	raw, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": usageFixtureAccess, "refreshToken": refresh, "expiresAt": time.Now().Add(time.Hour).UnixMilli(), "scopes": []string{"user:inference", "user:profile", "user:sessions:claude_code"}, "subscriptionType": "max"}})
	return raw
}

// The test binary doubles as the native CLI for usage-profile launches. The
// Home directory name selects the behavior.
func init() {
	if len(os.Args) < 3 || os.Args[1] != "auth" || !strings.HasPrefix(filepath.Base(os.Getenv("HOME")), "fixture-usage-") {
		return
	}
	mode := strings.TrimPrefix(filepath.Base(os.Getenv("HOME")), "fixture-usage-")
	home := os.Getenv("CLAUDE_CONFIG_DIR")
	if home != filepath.Join(os.Getenv("HOME"), "claude") || os.Getenv("CLAUDE_SECURESTORAGE_CONFIG_DIR") != home {
		os.Exit(80)
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL", "HTTPS_PROXY", "NODE_OPTIONS"} {
		if os.Getenv(key) != "" {
			os.Exit(81)
		}
	}
	if browser := os.Getenv("BROWSER"); os.Args[2] == "login" && (browser == "" || !filepath.IsAbs(browser)) {
		os.Exit(82)
	}
	credentials := filepath.Join(home, ".credentials.json")
	marker := filepath.Join(home, "fixture-usage-state.json")
	switch os.Args[2] {
	case "login":
		if len(os.Args) != 4 || os.Args[3] != "--claudeai" {
			os.Exit(83)
		}
		if refresh := os.Getenv("CLAUDE_CODE_OAUTH_REFRESH_TOKEN"); refresh != "" {
			if refresh != usageFixtureRefresh || os.Getenv("CLAUDE_CODE_OAUTH_SCOPES") != "user:inference user:profile user:sessions:claude_code" {
				os.Exit(84)
			}
			if mode == "refresh-url" {
				fmt.Println(usageFixtureURL(usageManualCallback))
				os.Exit(1)
			}
			if mode == "refresh-rejected" {
				os.Exit(1)
			}
			if os.WriteFile(credentials, usageFixtureRecord(usageFixtureRotated), 0600) != nil {
				os.Exit(85)
			}
			os.Exit(0)
		}
		redirect := usageManualCallback
		if mode == "localhost" {
			redirect = "http://localhost:4567/callback"
		}
		fmt.Println(usageFixtureURL(redirect))
		fmt.Print("Paste code here if prompted > ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || scanner.Text() != usageFixtureCode {
			os.Exit(86)
		}
		if os.WriteFile(credentials, usageFixtureRecord(usageFixtureRefresh), 0600) != nil || os.WriteFile(marker, []byte(`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","email":"alex@example.com","orgId":"org-fixture","orgName":"Example Org","subscriptionType":"max"}`), 0600) != nil {
			os.Exit(87)
		}
		os.Exit(0)
	case "status":
		data, err := os.ReadFile(marker)
		if err != nil {
			fmt.Println(`{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}`)
			os.Exit(1)
		}
		_, _ = os.Stdout.Write(data)
		os.Exit(0)
	}
	os.Exit(88)
}

func TestValidateUsageLoginURLAcceptsOnlyTheManualApprovalCodeCallback(t *testing.T) {
	if err := ValidateUsageLoginURL(usageFixtureURL(usageManualCallback)); err != nil {
		t.Fatal(err)
	}
	err := ValidateUsageLoginURL(usageFixtureURL("http://localhost:4567/callback"))
	if domain.ClaudeLimitFailureOf(err) != domain.ClaudeLimitFailureLoginNotInteractive {
		t.Fatalf("a callback to the server loopback must be refused with the manual-code failure, got %v", err)
	}
	for _, mutate := range []func(*url.URL){
		func(u *url.URL) { u.Host = "attacker.invalid" },
		func(u *url.URL) { q := u.Query(); q.Set("client_id", "another-client"); u.RawQuery = q.Encode() },
		func(u *url.URL) { q := u.Query(); q.Set("scope", "user:inference"); u.RawQuery = q.Encode() },
		func(u *url.URL) {
			q := u.Query()
			q.Set("redirect_uri", "https://attacker.invalid/cb")
			u.RawQuery = q.Encode()
		},
	} {
		u, _ := url.Parse(usageFixtureURL(usageManualCallback))
		mutate(u)
		if ValidateUsageLoginURL(u.String()) == nil {
			t.Fatal("accepted a substituted native authority")
		}
	}
	output := &loginOutput{validate: ValidateUsageLoginURL}
	if _, err := output.Write([]byte(usageFixtureURL("http://localhost:4567/callback") + "\n")); err == nil || output.progress.URL != "" {
		t.Fatal("the localhost callback variant must never become progress")
	}
}

func usageFixtureConfig(t *testing.T, mode string) (UsageConfig, string) {
	t.Helper()
	first, _ := fixtureConfig(t, "usage-"+mode)
	return UsageConfig{Process: first.Process, Version: first.Version, Home: first.Home}, filepath.Dir(first.Home)
}

func TestUsageEnvironmentPinsTheProfileAndSuppressesTheBrowser(t *testing.T) {
	config, root := usageFixtureConfig(t, "ok")
	config.Process.Env = append(config.Process.Env, "BROWSER=/usr/bin/open", "CLAUDE_CODE_OAUTH_TOKEN=foreign", "ANTHROPIC_BASE_URL=http://foreign.invalid")
	env, err := usageEnvironment(config)
	if runtime.GOOS == "windows" {
		if domain.ClaudeLimitFailureOf(err) != domain.ClaudeLimitFailureLoginUnsupported {
			t.Fatalf("expected fail-closed browser suppression, got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	values := map[string][]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = append(values[key], value)
	}
	home := filepath.Join(root, "claude")
	if len(values["CLAUDE_CONFIG_DIR"]) != 1 || values["CLAUDE_CONFIG_DIR"][0] != home || len(values["CLAUDE_SECURESTORAGE_CONFIG_DIR"]) != 1 || values["CLAUDE_SECURESTORAGE_CONFIG_DIR"][0] != home {
		t.Fatalf("both native storage variables must name the exact profile: %v", values)
	}
	if len(values["BROWSER"]) != 1 || values["BROWSER"][0] == "/usr/bin/open" || !filepath.IsAbs(values["BROWSER"][0]) {
		t.Fatalf("server browser opening must be suppressed: %v", values["BROWSER"])
	}
	if values["ANTHROPIC_BASE_URL"] != nil {
		t.Fatal("foreign base URL leaked")
	}
	for key := range values {
		if strings.HasPrefix(key, "CLAUDE_CODE_OAUTH_REFRESH") || key == "CLAUDE_CODE_OAUTH_SCOPES" {
			t.Fatalf("a login child must not carry refresh material: %s", key)
		}
	}
}

func TestUsageLoginRefreshAndStoreRoundTripWithTheNativeFixture(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture browser opener is Unix-only")
	}
	config, root := usageFixtureConfig(t, "ok")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := NewUsageStore(root)
	if present, err := store.Present(ctx); err != nil || present {
		t.Fatalf("a fresh profile must hold no credentials: %v %v", present, err)
	}
	login, err := StartUsageLogin(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Close()
	deadline := time.After(5 * time.Second)
	for {
		progress, err := login.Progress()
		if err != nil {
			t.Fatal(err)
		}
		if progress.URL != "" {
			if progress.URL != usageFixtureURL(usageManualCallback) || progress.Method != domain.SubscriptionBrowserCode {
				t.Fatalf("progress %+v", progress)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("native address missing")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := login.Submit([]byte(usageFixtureCode)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-login.Done():
	case <-ctx.Done():
		t.Fatal("login did not exit")
	}
	if err := login.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := login.Close(); err != nil {
		t.Fatal(err)
	}
	identity, err := ReadUsageIdentity(ctx, config)
	if err != nil || identity.Email != "alex@example.com" || identity.Plan != "max" || identity.Organization != "Example Org" || identity.OrgID != "org-fixture" {
		t.Fatalf("identity %+v %v", identity, err)
	}
	raw, err := store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := subscription.ParseClaudeUsageNative(raw, time.Now())
	clear(raw)
	if err != nil || string(bundle.RefreshToken) != usageFixtureRefresh {
		t.Fatalf("bundle %v", err)
	}
	// One supervised native refresh-login in the same profile rotates the bundle.
	refresh, err := StartUsageRefresh(ctx, config, UsageRefreshInput{RefreshToken: bundle.RefreshToken, Scopes: bundle.Scopes, ClientID: bundle.ClientID})
	if err != nil {
		t.Fatal(err)
	}
	defer refresh.Close()
	select {
	case <-refresh.Done():
	case <-ctx.Done():
		t.Fatal("refresh did not exit")
	}
	if err := refresh.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := refresh.Close(); err != nil {
		t.Fatal(err)
	}
	rotatedRaw, err := store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := subscription.ParseClaudeUsageNative(rotatedRaw, time.Now())
	clear(rotatedRaw)
	if err != nil || string(rotated.RefreshToken) != usageFixtureRotated {
		t.Fatalf("the rotated bundle was not captured: %v", err)
	}
	bundle.Clear()
	rotated.Clear()
	if err := store.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(ctx); err != nil {
		t.Fatalf("removal must be idempotent: %v", err)
	}
	if present, err := store.Present(ctx); err != nil || present {
		t.Fatalf("credentials remained: %v %v", present, err)
	}
}

func TestUsageRefreshRefusesInteractiveFallbackAndReportsRejection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture browser opener is Unix-only")
	}
	for _, mode := range []string{"refresh-url", "refresh-rejected"} {
		config, _ := usageFixtureConfig(t, mode)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		refresh, err := StartUsageRefresh(ctx, config, UsageRefreshInput{RefreshToken: []byte(usageFixtureRefresh), Scopes: []string{"user:inference", "user:profile", "user:sessions:claude_code"}, ClientID: nativeClaudeClientID})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		<-refresh.Done()
		if refresh.Wait() == nil {
			t.Fatalf("%s: a non-zero native exit must never read as success", mode)
		}
		if mode == "refresh-url" {
			if _, err := refresh.Progress(); domain.ClaudeLimitFailureOf(err) != domain.ClaudeLimitFailureRefreshUnsupported {
				t.Fatalf("an interactive authorization address is unsupported renewal: %v", err)
			}
		}
		_ = refresh.Close()
		cancel()
	}
}

func fakeKeychain(records map[string]string, calls *[][]string) func(context.Context, ...string) ([]byte, int, error) {
	return func(_ context.Context, args ...string) ([]byte, int, error) {
		*calls = append(*calls, append([]string(nil), args...))
		key := ""
		for i, arg := range args {
			if arg == "-s" && i+1 < len(args) {
				key = args[i+1]
			}
		}
		value, ok := records[key]
		switch args[0] {
		case "find-generic-password":
			if !ok {
				return nil, 44, fmt.Errorf("exit 44")
			}
			return []byte(value + "\n"), 0, nil
		case "delete-generic-password":
			if !ok {
				return nil, 44, fmt.Errorf("exit 44")
			}
			delete(records, key)
			return nil, 0, nil
		}
		return nil, 1, fmt.Errorf("unexpected")
	}
}

func darwinStore(root string, records map[string]string, calls *[][]string) UsageStore {
	store := NewUsageStore(root)
	store.goos = "darwin"
	store.user = func() (string, error) { return "fixture-user", nil }
	store.keychain = fakeKeychain(records, calls)
	return store
}

func TestUsageStoreReadsOnlyTheExactDerivedKeychainItemThenTheProfileFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "profile")
	if err := os.MkdirAll(filepath.Join(root, "claude"), 0700); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	records := map[string]string{}
	store := darwinStore(root, records, &calls)
	service := store.KeychainService()
	if !strings.HasPrefix(service, "Claude Code-credentials-") || len(service) != len("Claude Code-credentials-")+8 {
		t.Fatalf("service %q", service)
	}
	other := darwinStore(filepath.Join(t.TempDir(), "other"), records, &calls)
	if other.KeychainService() == service {
		t.Fatal("each profile derives its own Keychain item")
	}
	records["Claude Code-credentials"] = "personal-default-profile-record"
	records[other.KeychainService()] = "another-profile"
	ctx := context.Background()
	if _, err := store.Read(ctx); domain.ClaudeLimitFailureOf(err) != domain.ClaudeLimitFailureCredentials {
		t.Fatalf("absent exact item and file must fail closed, got %v", err)
	}
	for _, call := range calls {
		joined := strings.Join(call, " ")
		if !strings.Contains(joined, "-a fixture-user") || !strings.Contains(joined, "-s "+service) {
			t.Fatalf("only the exact item may be addressed: %v", call)
		}
	}
	records[service] = string(usageFixtureRecord(usageFixtureRefresh))
	raw, err := store.Read(ctx)
	if err != nil || !bytes.Contains(raw, []byte(usageFixtureAccess)) {
		t.Fatalf("exact item not read: %v", err)
	}
	if err := store.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := records[service]; ok {
		t.Fatal("exact item not removed")
	}
	if records["Claude Code-credentials"] == "" || records[other.KeychainService()] == "" {
		t.Fatal("unrelated Keychain records must never be touched")
	}
	// The documented file fallback applies only when the exact item is absent.
	if err := os.WriteFile(filepath.Join(root, "claude", ".credentials.json"), usageFixtureRecord(usageFixtureRefresh), 0600); err != nil {
		t.Fatal(err)
	}
	if raw, err = store.Read(ctx); err != nil || !bytes.Contains(raw, []byte(usageFixtureAccess)) {
		t.Fatalf("file fallback: %v", err)
	}
	if err := store.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if present, err := store.Present(ctx); err != nil || present {
		t.Fatalf("storage remained: %v %v", present, err)
	}
}

func TestUsageStoreFileAdapterFailsClosedOnUnsafeStorage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission and symlink semantics")
	}
	ctx := context.Background()
	newRoot := func() string {
		root := filepath.Join(t.TempDir(), "profile")
		if err := os.MkdirAll(filepath.Join(root, "claude"), 0700); err != nil {
			t.Fatal(err)
		}
		return root
	}
	t.Run("group readable file", func(t *testing.T) {
		root := newRoot()
		path := filepath.Join(root, "claude", ".credentials.json")
		if err := os.WriteFile(path, usageFixtureRecord(usageFixtureRefresh), 0640); err != nil {
			t.Fatal(err)
		}
		store := NewUsageStore(root)
		store.goos = "linux"
		if _, err := store.Read(ctx); domain.ClaudeLimitFailureOf(err) != domain.ClaudeLimitFailureProfileUnsafe {
			t.Fatalf("unsafe permissions must be refused: %v", err)
		}
	})
	t.Run("symlinked credential file", func(t *testing.T) {
		root := newRoot()
		foreign := filepath.Join(t.TempDir(), "foreign.json")
		if err := os.WriteFile(foreign, usageFixtureRecord(usageFixtureRefresh), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(foreign, filepath.Join(root, "claude", ".credentials.json")); err != nil {
			t.Fatal(err)
		}
		store := NewUsageStore(root)
		store.goos = "linux"
		if _, err := store.Read(ctx); domain.ClaudeLimitFailureOf(err) != domain.ClaudeLimitFailureProfileUnsafe {
			t.Fatalf("a symlink must never be followed: %v", err)
		}
		if err := store.Remove(ctx); err == nil {
			t.Fatal("a symlink must not be treated as removable owned storage")
		}
		if _, err := os.Stat(foreign); err != nil {
			t.Fatal("the foreign target was touched")
		}
	})
	t.Run("interrupted write remnant", func(t *testing.T) {
		root := newRoot()
		if err := os.WriteFile(filepath.Join(root, "claude", ".pending-.credentials.json-123"), []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
		store := NewUsageStore(root)
		store.goos = "linux"
		if present, err := store.Present(ctx); err == nil || !present {
			t.Fatalf("a remnant must not read as an empty profile: %v %v", present, err)
		}
	})
	t.Run("windows without a profile file is unsupported", func(t *testing.T) {
		root := newRoot()
		store := NewUsageStore(root)
		store.goos = "windows"
		if _, err := store.Read(ctx); domain.ClaudeLimitFailureOf(err) != domain.ClaudeLimitFailureStorage {
			t.Fatalf("unrecognized Windows storage must fail explicitly: %v", err)
		}
	})
}
