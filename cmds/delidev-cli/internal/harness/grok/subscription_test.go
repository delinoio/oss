// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/pelletier/go-toml/v2"
)

// subscriptionFixtureSentinel is the synthetic credential content the scripted
// native receives. It must never reach logs or errors.
const subscriptionFixtureSentinel = "synthetic-subscription-credential-sentinel"

// subscriptionFixtureCredential reports whether the fixture's private home holds
// a materialized credential file, which marks the managed subscription lane.
func subscriptionFixtureCredential(root string) bool {
	_, err := os.Lstat(filepath.Join(root, "grok", "auth.json"))
	return err == nil
}

// subscriptionFixtureEnvironment verifies the launched environment of the
// subscription lane: no scoped key, no loopback endpoint block and every other
// isolation variable kept.
func subscriptionFixtureEnvironment(root string) bool {
	for _, key := range []string{credentialVariable, "XAI_API_KEY", "HTTPS_PROXY", "NODE_OPTIONS", "GROK_XAI_API_BASE_URL", "GROK_MODELS_BASE_URL", "GROK_MODELS_LIST_URL"} {
		if _, present := os.LookupEnv(key); present {
			return false
		}
	}
	grok := filepath.Join(root, "grok")
	for key, want := range map[string]string{
		"HOME": root, "USERPROFILE": root, "GROK_HOME": grok, "TMPDIR": filepath.Join(root, "tmp"),
		"XDG_CONFIG_HOME": filepath.Join(root, "config"), "APPDATA": filepath.Join(root, "config"), "GROK_DISABLE_AUTOUPDATER": "1",
		"GROK_CLAUDE_SKILLS_ENABLED": "0", "GROK_CURSOR_HOOKS_ENABLED": "0", "GROK_CLAUDE_MCPS_ENABLED": "0",
	} {
		if os.Getenv(key) != want {
			return false
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Lstat(filepath.Join(grok, "auth.json"))
		if err != nil || info.Mode().Perm() != 0o600 {
			return false
		}
	}
	return true
}

type subscriptionFixture struct {
	config   apiConfig
	logs     *fixtureLogBuffer
	prepared atomic.Uint32
}

// newSubscriptionFixture returns a subscription configuration whose credential
// callback writes a synthetic auth.json with owner-only permission.
func newSubscriptionFixture(t *testing.T, mode string) *subscriptionFixture {
	t.Helper()
	config, logs := fixtureAPIConfig(t, mode)
	config.Model = turnFixtureModel
	config.Token, config.ServerOrigin = "", ""
	config.Subscription = true
	config.Probe.Version = "1.0.52"
	f := &subscriptionFixture{config: config, logs: logs}
	path := filepath.Join(config.Probe.Home, "auth.json")
	f.config.PrepareCredential = func(context.Context) error {
		f.prepared.Add(1)
		if _, err := os.Lstat(path); err == nil {
			t.Error("the credential existed before it was prepared")
		}
		// Every native inspection of the empty profile precedes the credential.
		if _, err := os.Lstat(filepath.Join(config.Probe.Home, "config.toml")); err != nil {
			t.Error("the credential was prepared before the profile was written")
		}
		if _, err := os.Lstat(filepath.Join(config.Probe.Process.Cwd, "tmp", "input-started")); err == nil {
			t.Error("the credential was prepared after input")
		}
		return os.WriteFile(path, []byte(`{"synthetic":"`+subscriptionFixtureSentinel+`"}`), 0o600)
	}
	return f
}

func TestSubscriptionTextTurnAuthenticatesWithCachedToken(t *testing.T) {
	f := newSubscriptionFixture(t, "input-valid")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	api, err := openAPI(ctx, f.config)
	if err != nil {
		t.Fatalf("subscription initialization failed: %v; %s", err, f.logs.String())
	}
	defer api.Close()
	if f.prepared.Load() != 1 {
		t.Fatal("the credential was not prepared exactly once")
	}
	if _, err := api.Create(ctx, domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var observations []InputObservation
	request := domain.NewID()
	result, err := api.RunText(ctx, request, "Original subscription input.", func(context.Context, InputClaim) error { return nil }, func(_ context.Context, observation InputObservation) error {
		observations = append(observations, observation)
		return nil
	})
	if err != nil || len(observations) != 5 || observations[0].Kind != InputAccepted || observations[4].Kind != InputCompleted || result.Reason != EndTurn {
		t.Fatalf("the subscription text turn did not complete: %v", err)
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	if err := process.ReconcileOwner(f.config.Probe.Process.Directory, f.config.Probe.Process.OwnerID); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{subscriptionFixtureSentinel, f.config.Workspace, f.config.Probe.Home, "Original subscription input."} {
		if strings.Contains(f.logs.String(), private) {
			t.Fatal("subscription diagnostics disclosed private data")
		}
	}
}

func TestSubscriptionAuthenticationFailuresAreTypedUnavailable(t *testing.T) {
	for _, mode := range []string{"auth-rejected", "auth-exit"} {
		t.Run(mode, func(t *testing.T) {
			f := newSubscriptionFixture(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			api, err := openAPI(ctx, f.config)
			if err == nil {
				_ = api.Close()
				t.Fatal("a rejected credential handshake was accepted")
			}
			if code := domain.SafeError(err).Code; code != domain.Unavailable {
				t.Fatalf("authenticate failure was not typed Unavailable: %s", code)
			}
			if strings.Contains(err.Error(), "private-auth-sentinel") || strings.Contains(err.Error(), "account@example.invalid") {
				t.Fatal("the native explanation reached the error")
			}
			if err := process.ReconcileOwner(f.config.Probe.Process.Directory, f.config.Probe.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(f.logs.String()), "\n")
			var diagnostic map[string]any
			if json.Unmarshal([]byte(lines[len(lines)-1]), &diagnostic) != nil || diagnostic["phase"] != "EXECUTION" || diagnostic["step"] != string(authenticatePhase) || diagnostic["code"] != string(domain.Unavailable) {
				t.Fatalf("failure did not log the closed EXECUTION phase: %v", diagnostic)
			}
			for _, private := range []string{subscriptionFixtureSentinel, "private-auth-sentinel", "account@example.invalid", f.config.Probe.Home} {
				if strings.Contains(f.logs.String(), private) {
					t.Fatal("subscription diagnostics disclosed private data")
				}
			}
		})
	}
}

func TestSubscriptionCredentialFailureStopsBeforeLaunch(t *testing.T) {
	f := newSubscriptionFixture(t, "input-valid")
	f.config.PrepareCredential = func(context.Context) error {
		return domain.Fail(domain.Unavailable, "The credential could not be prepared.", "Retry.")
	}
	api, err := openAPI(context.Background(), f.config)
	if err == nil {
		_ = api.Close()
		t.Fatal("a failed credential preparation launched the native process")
	}
	if _, statErr := os.Lstat(filepath.Join(f.config.Probe.Process.Cwd, "tmp", "input-started")); statErr == nil {
		t.Fatal("native input started without a credential")
	}
}

func TestSubscriptionProfileReplacesOnlyTheCredentialSource(t *testing.T) {
	f := newSubscriptionFixture(t, "valid")
	profile, err := buildAPIProfile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if toml.Unmarshal(profile.configuration, &parsed) != nil {
		t.Fatal("invalid subscription configuration")
	}
	model := parsed["model"].(map[string]any)[selectedModel].(map[string]any)
	for _, key := range []string{"base_url", "env_key", "api_backend"} {
		if _, present := model[key]; present {
			t.Fatalf("the subscription profile carries the relay setting %s", key)
		}
	}
	if model["model"] != f.config.Model || !profile.subscription {
		t.Fatal("selected model or lane changed")
	}
	api := fixtureAPIConfigFor(t, "valid")
	apiProfile, err := buildAPIProfile(api)
	if err != nil || bytes.Equal(apiProfile.configuration, profile.configuration) || apiProfile.subscription {
		t.Fatal("the API profile changed")
	}
	for name, mutate := range map[string]func(*apiConfig){
		"scoped token":      func(c *apiConfig) { c.Token = apiFixtureToken },
		"relay origin":      func(c *apiConfig) { c.ServerOrigin = "http://127.0.0.1:1" },
		"no credential":     func(c *apiConfig) { c.PrepareCredential = nil },
		"legacy baseline":   func(c *apiConfig) { c.Probe.Version = domain.GrokProtocolVersion },
		"unknown baseline":  func(c *apiConfig) { c.Probe.Version = "" },
		"prerelease of 46":  func(c *apiConfig) { c.Probe.Version = "1.0.46-rc.1" },
		"zero context size": func(c *apiConfig) { c.ContextTokens = 0 },
	} {
		changed := f.config
		mutate(&changed)
		if _, err := buildAPIProfile(changed); err == nil {
			t.Fatalf("unsafe subscription profile accepted: %s", name)
		}
	}
}

func fixtureAPIConfigFor(t *testing.T, mode string) apiConfig {
	t.Helper()
	config, _ := fixtureAPIConfig(t, mode)
	return config
}

func TestWithoutLoopbackBlocksRemovesOnlyTheThreeEndpointBlocks(t *testing.T) {
	config, _ := fixtureConfig(t, "api-valid")
	env, err := probeEnvironment(config)
	if err != nil {
		t.Fatal(err)
	}
	stripped := withoutLoopbackBlocks(env)
	if len(stripped) != len(env)-3 {
		t.Fatalf("removed %d entries instead of exactly three", len(env)-len(stripped))
	}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		blocked := slices.Contains(loopbackBlockedVariables, key)
		if blocked == slices.Contains(stripped, entry) {
			t.Fatalf("variable %s was handled incorrectly", key)
		}
	}
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "APPDATA", "LOCALAPPDATA", "TMPDIR", "GROK_HOME", "GROK_CLAUDE_SKILLS_ENABLED", "GROK_CURSOR_HOOKS_ENABLED"} {
		found := false
		for _, entry := range stripped {
			found = found || strings.HasPrefix(entry, key+"=")
		}
		if !found {
			t.Fatalf("isolation variable %s was removed", key)
		}
	}
}

func TestSubscriptionInitializeProfileAdvertisesCachedToken(t *testing.T) {
	f := newSubscriptionFixture(t, "valid")
	profile, err := buildAPIProfile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	value := fixtureObject(initializeFixture)
	meta := value["_meta"].(map[string]any)
	meta["currentWorkingDirectory"] = f.config.Probe.Process.Cwd
	meta["modelState"] = map[string]any{"currentModelId": selectedModel, "availableModels": []any{map[string]any{"modelId": selectedModel, "name": selectedModelName, "_meta": map[string]any{"totalContextTokens": 32000, "agentType": "grok-build-plan"}}}}
	methods := func(ids ...string) []any {
		var list []any
		for _, id := range ids {
			list = append(list, map[string]any{"id": id, "name": id, "description": "fixture"})
		}
		return list
	}
	for _, test := range []struct {
		name    string
		methods []string
		def     any
		valid   bool
	}{
		{"cached token default", []string{"cached_token", "grok.com"}, "cached_token", true},
		{"cached token without default", []string{"cached_token", "grok.com"}, nil, true},
		{"api key methods", []string{"xai.api_key", "grok.com"}, "xai.api_key", false},
		{"foreign default", []string{"cached_token", "grok.com"}, "xai.api_key", false},
		{"missing browser method", []string{"cached_token"}, "cached_token", false},
	} {
		meta["defaultAuthMethodId"] = test.def
		value["authMethods"] = methods(test.methods...)
		raw, _ := json.Marshal(value)
		err := validateInitializeResult(raw, f.config.Probe.Process.Cwd, &profile)
		if (err == nil) != test.valid {
			t.Fatalf("%s: validity=%v want %v (%v)", test.name, err == nil, test.valid, err)
		}
	}
}
