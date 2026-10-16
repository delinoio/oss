// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const (
	usageFixtureAccess  = "sk-ant-oat01-UsageFixtureAccessTokenABCDEFGHIJKLMNOP0123456789_-"
	usageFixtureRefresh = "sk-ant-ort01-UsageFixtureRefreshTokenABCDEFGHIJKLMNOP0123456789_-"
)

var usageFixtureNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func usageFixtureCredentials(expires time.Time, scopes ...string) []byte {
	if len(scopes) == 0 {
		scopes = []string{"user:inference", "user:profile", "user:sessions:claude_code"}
	}
	quoted := make([]string, len(scopes))
	for i, scope := range scopes {
		quoted[i] = fmt.Sprintf("%q", scope)
	}
	return []byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"refreshToken":%q,"expiresAt":%d,"scopes":[%s],"subscriptionType":"max","rateLimitTier":"default_claude_max_5x"},"mcpOAuth":{}}`, usageFixtureAccess, usageFixtureRefresh, expires.UnixMilli(), strings.Join(quoted, ",")))
}

type fakeUsageKeychain struct {
	items     map[string][]byte
	findErr   error
	deleteErr error
	deleted   []string
}

func (k *fakeUsageKeychain) Find(_ context.Context, service, account string) ([]byte, bool, error) {
	if k.findErr != nil {
		return nil, false, k.findErr
	}
	value, ok := k.items[service+"|"+account]
	return bytes.Clone(value), ok, nil
}
func (k *fakeUsageKeychain) Delete(_ context.Context, service, account string) error {
	if k.deleteErr != nil {
		return k.deleteErr
	}
	k.deleted = append(k.deleted, service+"|"+account)
	delete(k.items, service+"|"+account)
	return nil
}

func usageStorageFixture(t *testing.T, goos string) (UsageStorage, *fakeUsageKeychain) {
	t.Helper()
	config, _ := fixtureConfig(t, "usage-storage")
	keychain := &fakeUsageKeychain{items: map[string][]byte{}}
	return UsageStorage{GOOS: goos, ConfigDir: config.Home, Account: "fixture-user", Keychain: keychain, Now: func() time.Time { return usageFixtureNow }}, keychain
}

func writeUsageFile(t *testing.T, storage UsageStorage, raw []byte) {
	t.Helper()
	if err := os.WriteFile(storage.File(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUsageKeychainServiceIsDerivedFromTheExactProfileDirectory(t *testing.T) {
	a, b := UsageKeychainService("/data/p/one/claude"), UsageKeychainService("/data/p/two/claude")
	if a == b || a != UsageKeychainService("/data/p/one/claude") || !strings.HasPrefix(a, "Claude Code-credentials-") || len(a) != len("Claude Code-credentials-")+8 {
		t.Fatalf("unexpected derivation %q %q", a, b)
	}
}

func TestUsageEnvironmentIsolatesTheProfileAndSuppressesTheBrowser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows has no verified no-op browser opener")
	}
	config, _ := fixtureConfig(t, "usage-env")
	config.Process.Env = append(config.Process.Env, "BROWSER=/usr/bin/open", "ANTHROPIC_BASE_URL=https://foreign.invalid", "CLAUDE_CODE_OAUTH_TOKEN=foreign", "ANTHROPIC_API_KEY=foreign")
	env, err := UsageEnvironment(AuthConfig{Process: config.Process, Version: config.Version, Home: config.Home})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	browsers := 0
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
		if key == "BROWSER" {
			browsers++
		}
	}
	if values["CLAUDE_CONFIG_DIR"] != config.Home || values["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != config.Home {
		t.Fatalf("the profile was not exact: %q %q", values["CLAUDE_CONFIG_DIR"], values["CLAUDE_SECURESTORAGE_CONFIG_DIR"])
	}
	if browsers != 1 || values["BROWSER"] == "/usr/bin/open" || values["BROWSER"] == "" {
		t.Fatalf("the server browser was not suppressed: %q (%d)", values["BROWSER"], browsers)
	}
	for _, key := range []string{"ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		if _, present := values[key]; present {
			t.Fatalf("foreign %s reached the usage login", key)
		}
	}
}

func TestUsageLoginAcceptsOnlyTheManualApprovalCodeAddress(t *testing.T) {
	login := &AuthLogin{output: &loginOutput{}, manualOnly: true}
	_, _ = login.output.Write([]byte(authFixtureURL() + "\n"))
	progress, err := login.Progress()
	if err != nil || progress.Method != domain.SubscriptionBrowserCode || progress.URL != authFixtureURL() {
		t.Fatalf("manual address rejected: %v", err)
	}
	callback := strings.Replace(authFixtureURL(), "https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback", "http%3A%2F%2Flocalhost%3A4000%2Fcallback", 1)
	local := &AuthLogin{output: &loginOutput{}, manualOnly: true}
	_, _ = local.output.Write([]byte(callback + "\n"))
	if progress, err := local.Progress(); err == nil || progress.URL != "" || domain.SafeError(err).Code != domain.Unsupported {
		t.Fatalf("a client-localhost callback was accepted: %+v %v", progress, err)
	}
	ordinary := &AuthLogin{output: &loginOutput{}}
	_, _ = ordinary.output.Write([]byte(callback + "\n"))
	if progress, err := ordinary.Progress(); err != nil || progress.Method != domain.SubscriptionBrowserCallback {
		t.Fatal("the ordinary Runner login profile changed")
	}
}

func TestUsageStorageReadsOnlyTheProfileOwnedCredential(t *testing.T) {
	storage, _ := usageStorageFixture(t, "linux")
	if _, err := storage.Read(context.Background()); err == nil {
		t.Fatal("a profile without credentials produced a bundle")
	}
	writeUsageFile(t, storage, usageFixtureCredentials(usageFixtureNow.Add(time.Hour)))
	bundle, err := storage.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Clear()
	if string(bundle.AccessToken) != usageFixtureAccess || string(bundle.RefreshToken) != usageFixtureRefresh || bundle.ClientID != UsageClientID || !bundle.ExpiresAt.Equal(usageFixtureNow.Add(time.Hour)) || len(bundle.Scopes) != 3 {
		t.Fatalf("bundle was not captured exactly: %+v", bundle.Scopes)
	}
}

func TestUsageStorageRejectsInvalidCredentials(t *testing.T) {
	for name, raw := range map[string][]byte{
		"expired":        usageFixtureCredentials(usageFixtureNow.Add(-time.Minute)),
		"no profile":     usageFixtureCredentials(usageFixtureNow.Add(time.Hour), "user:inference"),
		"no inference":   usageFixtureCredentials(usageFixtureNow.Add(time.Hour), "user:profile"),
		"not json":       []byte("not json"),
		"no oauth":       []byte(`{"mcpOAuth":{}}`),
		"empty":          []byte(`{"claudeAiOauth":{}}`),
		"bad token":      []byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"short","expiresAt":%d,"scopes":["user:inference","user:profile"]}}`, usageFixtureNow.Add(time.Hour).UnixMilli())),
		"escaped token":  []byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-\\u0041BCDEFGHIJKLMNOP","expiresAt":%d,"scopes":["user:inference","user:profile"]}}`, usageFixtureNow.Add(time.Hour).UnixMilli())),
		"no expiry":      []byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"scopes":["user:inference","user:profile"]}}`, usageFixtureAccess)),
		"bad scope name": usageFixtureCredentials(usageFixtureNow.Add(time.Hour), "user:inference", "user:profile", "bad scope"),
	} {
		storage, _ := usageStorageFixture(t, "linux")
		writeUsageFile(t, storage, raw)
		if bundle, err := storage.Read(context.Background()); err == nil {
			bundle.Clear()
			t.Fatalf("%s credential was accepted", name)
		}
	}
}

func TestUsageStorageFailsClosedOnUnsafeFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes and symlinks")
	}
	storage, _ := usageStorageFixture(t, "linux")
	target := filepath.Join(t.TempDir(), "foreign.json")
	if err := os.WriteFile(target, usageFixtureCredentials(usageFixtureNow.Add(time.Hour)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, storage.File()); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Read(context.Background()); err == nil {
		t.Fatal("a symlinked credential file was read")
	}
	if present, err := storage.Present(context.Background()); err == nil || !present {
		t.Fatalf("a symlink was not reported as unsafe: %t %v", present, err)
	}
	if err := storage.Remove(context.Background()); err == nil {
		t.Fatal("a foreign symlink was removed instead of failing closed")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("the symlink target was deleted")
	}
	_ = os.Remove(storage.File())
	writeUsageFile(t, storage, usageFixtureCredentials(usageFixtureNow.Add(time.Hour)))
	if err := os.Chmod(storage.File(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Read(context.Background()); err == nil {
		t.Fatal("a group- and world-readable credential file was read")
	}
}

func TestUsageStorageMacOSUsesTheExactKeychainItemAndFileFallback(t *testing.T) {
	storage, keychain := usageStorageFixture(t, "darwin")
	key := UsageKeychainService(storage.ConfigDir) + "|fixture-user"
	keychain.items[key] = usageFixtureCredentials(usageFixtureNow.Add(time.Hour))
	keychain.items[UsageKeychainService("/some/other/claude")+"|fixture-user"] = usageFixtureCredentials(usageFixtureNow.Add(time.Hour))
	bundle, err := storage.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bundle.Clear()
	delete(keychain.items, key)
	if _, err := storage.Read(context.Background()); err == nil {
		t.Fatal("another profile's Keychain item was adopted")
	}
	writeUsageFile(t, storage, usageFixtureCredentials(usageFixtureNow.Add(time.Hour)))
	bundle, err = storage.Read(context.Background())
	if err != nil {
		t.Fatalf("the documented file fallback failed: %v", err)
	}
	bundle.Clear()
}

func TestUsageStorageMacOSAmbiguityAndErrorsFailClosed(t *testing.T) {
	storage, keychain := usageStorageFixture(t, "darwin")
	key := UsageKeychainService(storage.ConfigDir) + "|fixture-user"
	keychain.items[key] = usageFixtureCredentials(usageFixtureNow.Add(time.Hour))
	writeUsageFile(t, storage, usageFixtureCredentials(usageFixtureNow.Add(time.Hour)))
	if bundle, err := storage.Read(context.Background()); err == nil {
		bundle.Clear()
		t.Fatal("both a Keychain item and a credential file were accepted")
	}
	_ = os.Remove(storage.File())
	keychain.findErr = errors.New("locked")
	if _, err := storage.Read(context.Background()); err == nil {
		t.Fatal("a Keychain failure produced a bundle")
	}
	if present, err := storage.Present(context.Background()); err == nil || !present {
		t.Fatal("an unreadable Keychain was reported absent")
	}
	missing := UsageStorage{GOOS: "darwin", ConfigDir: storage.ConfigDir, Account: "fixture-user"}
	if _, err := missing.Read(context.Background()); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("a macOS host without a Keychain adapter did not fail explicitly")
	}
}

func TestUsageStorageUnsupportedPlatformsFailExplicitly(t *testing.T) {
	storage, _ := usageStorageFixture(t, "plan9")
	if _, err := storage.Read(context.Background()); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("an unrecognized platform was accepted")
	}
	if err := storage.Remove(context.Background()); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("an unrecognized platform removed storage")
	}
}

func TestUsageStorageRemoveDeletesOnlyThisProfilesCredentials(t *testing.T) {
	storage, keychain := usageStorageFixture(t, "darwin")
	own := UsageKeychainService(storage.ConfigDir) + "|fixture-user"
	other := UsageKeychainService("/some/other/claude") + "|fixture-user"
	keychain.items[own] = usageFixtureCredentials(usageFixtureNow.Add(time.Hour))
	keychain.items[other] = usageFixtureCredentials(usageFixtureNow.Add(time.Hour))
	writeUsageFile(t, storage, usageFixtureCredentials(usageFixtureNow.Add(time.Hour)))
	helper := filepath.Join(storage.ConfigDir, "settings-cache.json")
	if err := os.WriteFile(helper, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storage.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, found := keychain.items[own]; found {
		t.Fatal("the profile Keychain item survived")
	}
	if _, found := keychain.items[other]; !found {
		t.Fatal("another profile's Keychain item was deleted")
	}
	if _, err := os.Stat(storage.File()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the credential file survived")
	}
	if _, err := os.Stat(helper); err != nil {
		t.Fatal("a non-credential profile file was deleted")
	}
	if present, err := storage.Present(context.Background()); err != nil || present {
		t.Fatalf("storage remained after removal: %t %v", present, err)
	}
	keychain.deleteErr = errors.New("denied")
	if storage.Remove(context.Background()) == nil {
		t.Fatal("an unproven Keychain removal was reported as success")
	}
}

func TestUsageStorageRejectsNonCanonicalProfiles(t *testing.T) {
	for _, dir := range []string{"", "relative/claude", "/data/not-claude"} {
		storage := UsageStorage{GOOS: "linux", ConfigDir: dir}
		if _, err := storage.Read(context.Background()); err == nil {
			t.Fatalf("%q was accepted as a usage profile", dir)
		}
	}
}
