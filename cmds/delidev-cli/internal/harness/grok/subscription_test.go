package grok

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// fixtureSubscriptionConfig is a managed-subscription launch: no execution key,
// no relay endpoint, and a Credential hook that writes the cached login the
// fixture requires (an owner-only auth.json in the private home).
func fixtureSubscriptionConfig(t *testing.T, mode string) (apiConfig, *fixtureLogBuffer, *int) {
	t.Helper()
	config, logs := fixtureAPIConfig(t, mode)
	config.Token, config.ServerOrigin = "", ""
	calls := new(int)
	path := filepath.Join(config.Probe.Home, "auth.json")
	config.Credential = func() error {
		*calls++
		return os.WriteFile(path, []byte(`{"fixture":"cached-login-sentinel"}`), 0600)
	}
	return config, logs, calls
}

func TestSubscriptionEnvironmentRemovesOnlyTheEndpointBlocks(t *testing.T) {
	probe, _ := fixtureConfig(t, "api-valid")
	api, err := probeEnvironment(probe)
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := subscriptionEnvironment(probe)
	if err != nil {
		t.Fatal(err)
	}
	blocks := []string{"GROK_XAI_API_BASE_URL=http://127.0.0.1:1", "GROK_MODELS_BASE_URL=http://127.0.0.1:1", "GROK_MODELS_LIST_URL=http://127.0.0.1:1/models"}
	for _, block := range blocks {
		if !slices.Contains(api, block) {
			t.Fatalf("the API environment lost %q", block)
		}
		if slices.Contains(subscription, block) {
			t.Fatalf("the subscription environment kept %q", block)
		}
	}
	for _, entry := range subscription {
		if strings.HasPrefix(entry, "GROK_XAI_API_BASE_URL=") || strings.HasPrefix(entry, "GROK_MODELS_BASE_URL=") || strings.HasPrefix(entry, "GROK_MODELS_LIST_URL=") {
			t.Fatalf("the subscription environment still names an endpoint: %q", entry)
		}
	}
	var withoutBlocks []string
	for _, entry := range api {
		if !slices.Contains(blocks, entry) {
			withoutBlocks = append(withoutBlocks, entry)
		}
	}
	if !slices.Equal(withoutBlocks, subscription) {
		t.Fatal("the subscription environment differs from the API environment by more than the three endpoint blocks")
	}
	for _, vendor := range []string{"CLAUDE", "CURSOR"} {
		for _, surface := range []string{"SKILLS", "RULES", "AGENTS", "MCPS", "HOOKS"} {
			if !slices.Contains(subscription, "GROK_"+vendor+"_"+surface+"_ENABLED=0") {
				t.Fatalf("the subscription environment lost the %s %s import block", vendor, surface)
			}
		}
	}
	for _, key := range []string{"GROK_HOME=", "HOME=", "GROK_DISABLE_AUTOUPDATER=1", "CI=1", "NO_COLOR=1", "XDG_CONFIG_HOME="} {
		if !slices.ContainsFunc(subscription, func(entry string) bool { return strings.HasPrefix(entry, key) }) {
			t.Fatalf("the subscription environment lost %q", key)
		}
	}
}

// The API execution profile is part of the byte-for-byte contract: its native
// configuration and loopback blocks must not change when the subscription
// profile exists.
func TestAPIProfileBytesAreUnchangedBySubscriptionSupport(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "valid")
	profile, err := buildAPIProfile(config)
	if err != nil {
		t.Fatal(err)
	}
	const expected = `[cli]
auto_update = false

[model]
[model.delidev-selected]
api_backend = 'chat_completions'
base_url = 'http://127.0.0.1:1/api-proxy/v1'
context_window = 32000
env_key = 'DELIDEV_GROK_EXECUTION_TOKEN'
max_retries = 0
model = 'provider/model'
name = 'DeliDev selected model'
supports_backend_search = false

[models]
allowed_models = ['delidev-selected']
default = 'delidev-selected'
image_description = 'delidev-selected'
max_retries = 0
session_summary = 'delidev-selected'
web_search = 'delidev-selected'

[session]
load_envrc = false
`
	if string(profile.configuration) != expected || profile.subscription {
		t.Fatalf("the API configuration changed:\n%s", profile.configuration)
	}
}

func TestSubscriptionProfileDeclaresNoRelayOrKey(t *testing.T) {
	config, _, _ := fixtureSubscriptionConfig(t, "valid")
	profile, err := buildAPIProfile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.subscription {
		t.Fatal("the managed profile was not marked")
	}
	for _, forbidden := range []string{"base_url", "env_key", credentialVariable, "api_backend", "127.0.0.1", "api-proxy"} {
		if strings.Contains(string(profile.configuration), forbidden) {
			t.Fatalf("the managed configuration names %q", forbidden)
		}
	}
	for _, required := range []string{"model = 'provider/model'", "default = 'delidev-selected'", "allowed_models = ['delidev-selected']"} {
		if !strings.Contains(string(profile.configuration), required) {
			t.Fatalf("the managed configuration lost %q", required)
		}
	}
	for _, mixed := range []func(*apiConfig){func(c *apiConfig) { c.Token = apiFixtureToken }, func(c *apiConfig) { c.ServerOrigin = "http://127.0.0.1:1" }} {
		invalid, _, _ := fixtureSubscriptionConfig(t, "valid")
		mixed(&invalid)
		if _, err := buildAPIProfile(invalid); err == nil {
			t.Fatal("a managed profile accepted an execution key or relay endpoint")
		}
	}
}

func TestSubscriptionAuthenticatesWithTheCachedTokenAndCompletesATextTurn(t *testing.T) {
	config, logs, calls := fixtureSubscriptionConfig(t, "input-valid")
	config.Model = turnFixtureModel
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	api, err := openAPI(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if *calls != 1 {
		t.Fatalf("the credential hook ran %d times", *calls)
	}
	// The fixture exits unless it received cached_token, the cached login with
	// owner-only permission, no execution key and no endpoint block.
	if _, err := api.Create(ctx, domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
		t.Fatal(err)
	}
	request := domain.NewID()
	var observations []InputObservation
	result, err := api.RunText(ctx, request, "Original input.", func(context.Context, InputClaim) error { return nil }, func(_ context.Context, observation InputObservation) error {
		observations = append(observations, observation)
		return nil
	})
	if err != nil || len(observations) != 5 || observations[0].Kind != InputAccepted || observations[4].Kind != InputCompleted || result.Meta.Prompt == "" {
		t.Fatal("the managed text turn did not complete", err, len(observations))
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	if err := process.ReconcileOwner(config.Probe.Process.Directory, config.Probe.Process.OwnerID); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "cached-login-sentinel") || strings.Contains(logs.String(), config.Workspace) {
		t.Fatal("the managed run logged credential or path data")
	}
}

func TestSubscriptionLaunchDoesNotMaterializeBeforeInspection(t *testing.T) {
	config, _, calls := fixtureSubscriptionConfig(t, "valid")
	config.Model = turnFixtureModel
	authPath := filepath.Join(config.Probe.Home, "auth.json")
	if err := os.WriteFile(authPath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	// A leftover login in the private home is refused before the hook runs or any
	// native process starts, exactly like every user-authored configuration file.
	if _, err := openAPI(context.Background(), config); err == nil || *calls != 0 {
		t.Fatal("a leftover login was accepted")
	}
}

func TestSubscriptionCredentialFailureStopsTheLaunch(t *testing.T) {
	config, _, _ := fixtureSubscriptionConfig(t, "valid")
	config.Credential = func() error { return domain.Fail(domain.Internal, "Credential unavailable.", "") }
	api, err := openAPI(context.Background(), config)
	if err == nil {
		_ = api.Close()
		t.Fatal("a failed credential hook launched the native process")
	}
	if _, statErr := os.Stat(filepath.Join(config.Probe.Home, "auth.json")); !os.IsNotExist(statErr) {
		t.Fatal("a failed credential hook left a login")
	}
}

func TestSubscriptionAuthenticationFailureIsTypedUnavailable(t *testing.T) {
	config, logs, _ := fixtureSubscriptionConfig(t, "sub-auth-refused")
	api, err := openAPI(context.Background(), config)
	if err == nil {
		_ = api.Close()
		t.Fatal("a refused managed login was accepted")
	}
	safe := domain.SafeError(err)
	if safe.Code != domain.Unavailable || strings.Contains(safe.Message, "private-auth-sentinel") || strings.Contains(logs.String(), "private-auth-sentinel") {
		t.Fatalf("authenticate failure is not a clean typed unavailable error: %v", safe)
	}
	if err := process.ReconcileOwner(config.Probe.Process.Directory, config.Probe.Process.OwnerID); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionInitializationAdvertisementIsEnforced(t *testing.T) {
	for _, test := range []struct {
		mode string
		code domain.Code
	}{{"sub-unset-default", ""}, {"sub-old-version", domain.Unsupported}, {"sub-api-method", domain.Unsupported}} {
		t.Run(test.mode, func(t *testing.T) {
			config, _, _ := fixtureSubscriptionConfig(t, test.mode)
			api, err := openAPI(context.Background(), config)
			if test.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				_ = api.Close()
			} else if err == nil || domain.SafeError(err).Code != test.code {
				if err == nil {
					_ = api.Close()
				}
				t.Fatalf("unexpected result: %v", err)
			}
			if err := process.ReconcileOwner(config.Probe.Process.Directory, config.Probe.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
