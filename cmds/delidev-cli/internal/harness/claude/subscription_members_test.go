// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func preparedEnvironment(t *testing.T, config APIStreamConfig) map[string]string {
	t.Helper()
	prepared, err := prepareAPIStream(config)
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
	return values
}

// Version 2 separates the session-private history from the one canonical
// secure-storage namespace. DeliDev passes only the directory name; the native
// CLI alone reads and renews the authentication inside it.
func TestNativeClaudeVersion2SplitsPrivateHistoryFromCanonicalSecureStorage(t *testing.T) {
	config, _ := apiFixtureConfig(t, "valid")
	canonical, _ := fixtureConfig(t, "canonical-secure-storage")
	profile := domain.NewID()
	config.Subscription = &NativeSubscriptionProfile{ID: profile, Home: config.Home, SecureStorageHome: canonical.Home}
	values := preparedEnvironment(t, config)
	if values["CLAUDE_CONFIG_DIR"] != config.Home {
		t.Fatal("the session did not keep its private configuration and history directory", values["CLAUDE_CONFIG_DIR"])
	}
	if values["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != canonical.Home {
		t.Fatal("secure storage did not name the canonical profile", values["CLAUDE_SECURESTORAGE_CONFIG_DIR"])
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"} {
		if _, present := values[key]; present {
			t.Fatal("relay or imported authentication mixed with native subscription", key)
		}
	}
	if NativeHistoryHome(config) != config.Home {
		t.Fatal("history must be read from the session-private directory")
	}
}

func TestNativeClaudeVersion2RejectsAmbiguousDirectories(t *testing.T) {
	canonical, _ := fixtureConfig(t, "canonical-secure-storage")
	other, _ := fixtureConfig(t, "other-history")
	for name, change := range map[string]func(*APIStreamConfig){
		"history is not the stream home":     func(c *APIStreamConfig) { c.Subscription.Home = other.Home },
		"secure storage equals history":      func(c *APIStreamConfig) { c.Subscription.SecureStorageHome = c.Home },
		"relative secure storage":            func(c *APIStreamConfig) { c.Subscription.SecureStorageHome = "claude" },
		"missing secure storage":             func(c *APIStreamConfig) { c.Subscription.SecureStorageHome = c.Home + "-missing/claude" },
		"secure storage is not named claude": func(c *APIStreamConfig) { c.Subscription.SecureStorageHome = c.Process.Cwd },
	} {
		t.Run(name, func(t *testing.T) {
			config, _ := apiFixtureConfig(t, "valid")
			config.Subscription = &NativeSubscriptionProfile{ID: domain.NewID(), Home: config.Home, SecureStorageHome: canonical.Home}
			change(&config)
			if _, err := prepareAPIStream(config); err == nil {
				t.Fatal("an ambiguous profile split was admitted")
			}
		})
	}
}

// Version 1 histories stay valid under their exact old digest; version 2 binds
// both directories and cannot be confused with version 1 or with another profile.
func TestNativeClaudeCheckpointDigestsKeepVersion1AndBindVersion2Independently(t *testing.T) {
	config, _ := apiFixtureConfig(t, "valid")
	canonical, _ := fixtureConfig(t, "canonical-secure-storage")
	other, _ := fixtureConfig(t, "other-secure-storage")
	profile := domain.NewID()
	config.Subscription = &NativeSubscriptionProfile{ID: profile, Home: canonical.Home}
	v1 := checkpointConfiguration(config, config.API.ServerOrigin)
	config.Subscription = &NativeSubscriptionProfile{ID: profile, Home: canonical.Home, SecureStorageHome: ""}
	if checkpointConfiguration(config, config.API.ServerOrigin) != v1 {
		t.Fatal("version 1 digest changed")
	}
	config.Subscription = &NativeSubscriptionProfile{ID: profile, Home: config.Home, SecureStorageHome: canonical.Home}
	v2 := checkpointConfiguration(config, config.API.ServerOrigin)
	if v2 == v1 {
		t.Fatal("version 2 reused the version 1 digest")
	}
	config.Subscription = &NativeSubscriptionProfile{ID: profile, Home: config.Home, SecureStorageHome: other.Home}
	if checkpointConfiguration(config, config.API.ServerOrigin) == v2 {
		t.Fatal("version 2 admitted a replacement secure-storage directory")
	}
	config.Subscription = &NativeSubscriptionProfile{ID: domain.NewID(), Home: config.Home, SecureStorageHome: canonical.Home}
	if checkpointConfiguration(config, config.API.ServerOrigin) == v2 {
		t.Fatal("version 2 admitted a replacement profile")
	}
}

func TestNativeClaudeVersion2StreamOpensWithSplitDirectories(t *testing.T) {
	config, logs := apiFixtureConfig(t, "native-subscription-v2-valid")
	secure := filepath.Join(config.Process.Cwd, "owned-profile", "claude")
	if err := security.PrivateDir(filepath.Dir(secure)); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(secure); err != nil {
		t.Fatal(err)
	}
	config.Subscription = &NativeSubscriptionProfile{ID: domain.NewID(), Home: config.Home, SecureStorageHome: secure}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := OpenAPIStream(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), secure) {
		t.Fatal("the canonical secure-storage path entered logs")
	}
}
