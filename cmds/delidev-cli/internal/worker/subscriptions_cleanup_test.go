// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func TestManagedExecutionCleanupRejectsEncodedAuthentication(t *testing.T) {
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	parsed, _, err := subscription.Parse(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for name, retained := range map[string]string{
		"clean":               "ordinary retained history",
		"raw":                 parsed.Tokens.Access,
		"base64":              base64.StdEncoding.EncodeToString([]byte(parsed.Tokens.Access)),
		"base64-unpadded":     base64.RawStdEncoding.EncodeToString([]byte(parsed.Tokens.Refresh)),
		"base64-url":          base64.URLEncoding.EncodeToString([]byte(parsed.Tokens.ID)),
		"base64-url-unpadded": base64.RawURLEncoding.EncodeToString([]byte(parsed.Tokens.Refresh)),
	} {
		t.Run(name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "codex")
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			if err := security.WriteAtomic(filepath.Join(home, "auth.json"), bundle); err != nil {
				t.Fatal(err)
			}
			history := filepath.Join(home, "history.json")
			if err := security.WriteAtomic(history, []byte(retained)); err != nil {
				t.Fatal(err)
			}
			err := cleanupExecutionAuthentication(home, bundle, bundle)
			if (name == "clean") != (err == nil) {
				t.Fatal("credential-remnant cleanup classification is incorrect", err)
			}
			if _, err := os.Stat(history); err != nil {
				t.Fatal("cleanup erased retained native evidence", err)
			}
		})
	}
}

func workerGrokSubscriptionBundle(t *testing.T, suffix, user string) []byte {
	t.Helper()
	value := subscription.GrokAuth{Key: "synthetic-grok-access-" + suffix, Mode: subscription.GrokOIDC, Created: time.Now().UTC().Add(-time.Minute), User: user, Refresh: "synthetic-grok-refresh-" + suffix, Expires: time.Now().UTC().Add(time.Hour), Issuer: subscription.GrokIssuer, ClientID: subscription.GrokClientID}
	raw, err := json.Marshal(map[string]subscription.GrokAuth{subscription.GrokScope: value})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestManagedGrokExecutionCleanupRetainsHistoryAndRejectsBothBundleRemnants(t *testing.T) {
	original := workerGrokSubscriptionBundle(t, "original", "synthetic-grok-user")
	latest := workerGrokSubscriptionBundle(t, "latest", "synthetic-grok-user")
	defer clear(original)
	defer clear(latest)
	for _, token := range []string{"synthetic-grok-access-original", "synthetic-grok-refresh-original", "synthetic-grok-access-latest", "synthetic-grok-refresh-latest"} {
		for _, encoding := range []*base64.Encoding{nil, base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			home := filepath.Join(t.TempDir(), "grok")
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			retained := []byte(token)
			if encoding != nil {
				retained = []byte(encoding.EncodeToString(retained))
			}
			path := filepath.Join(home, "history.json")
			if security.WriteAtomic(path, retained) != nil || security.WriteAtomic(filepath.Join(home, "auth.json"), latest) != nil {
				t.Fatal("fixture write failed")
			}
			if cleanupExecutionAuthenticationForService(domain.SubscriptionGrok, home, latest, original) == nil {
				t.Fatal("Grok credential remnant released its protected ownership")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("uncertain cleanup erased native history", err)
			}
		}
	}
	home := filepath.Join(t.TempDir(), "grok")
	if security.PrivateDir(home) != nil || security.WriteAtomic(filepath.Join(home, "history.json"), []byte("ordinary native history")) != nil || security.WriteAtomic(filepath.Join(home, "auth.json"), latest) != nil {
		t.Fatal("fixture write failed")
	}
	if err := cleanupExecutionAuthenticationForService(domain.SubscriptionGrok, home, latest, original); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "history.json")); err != nil {
		t.Fatal("clean credential removal erased native history", err)
	}
	foreign := workerGrokSubscriptionBundle(t, "foreign", "synthetic-other-user")
	defer clear(foreign)
	if err := scanExecutionAuthenticationForService(domain.SubscriptionGrok, home, foreign, original); err == nil {
		t.Fatal("changed native principal acquired original cleanup authority")
	}
	if err := cleanupUnusedExecutionAuthenticationForService(domain.SubscriptionChatGPT, home, original); err == nil {
		t.Fatal("Grok authentication acquired ChatGPT cleanup authority")
	}
}
