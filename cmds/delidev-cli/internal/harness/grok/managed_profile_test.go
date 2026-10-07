// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/pelletier/go-toml/v2"
)

func fixtureManagedProfile(t *testing.T) (managedProfileConfig, managedProfile) {
	t.Helper()
	api, _ := fixtureAPIConfig(t, "valid")
	auth := subscription.GrokAuth{Mode: subscription.GrokOIDC, Key: "fixture-managed-access-token", Refresh: "fixture-managed-refresh-token", Created: time.Now().UTC().Add(-time.Minute), Expires: time.Now().UTC().Add(time.Hour), User: "fixture-managed-user", Issuer: subscription.GrokIssuer, ClientID: subscription.GrokClientID}
	bundle, err := json.Marshal(map[string]subscription.GrokAuth{subscription.GrokScope: auth})
	if err != nil {
		t.Fatal(err)
	}
	config := managedProfileConfig{Probe: api.Probe, Workspace: api.Workspace, Model: "grok-4.6", ModelName: "Grok 4.6", ContextTokens: 32000, Bundle: bundle}
	profile, err := buildManagedProfile(config)
	if err != nil {
		t.Fatal(err)
	}
	return config, profile
}

func fixtureManagedAuthMeta() map[string]any {
	return map[string]any{"email": nil, "auth_mode": "Oidc", "team_id": nil, "is_team_principal": false, "team_name": nil, "is_zdr": false, "team_role": nil, "coding_data_retention_opt_out": false, "can_administer_team": nil, "show_resolved_model": nil, "gate": nil, "subscription_tier": nil, "feedback_trace_offer": false, "backend_billed": false}
}

func TestManagedProfilePinsOIDCAndBuiltinModelsWithoutCredentials(t *testing.T) {
	config, profile := fixtureManagedProfile(t)
	var value map[string]any
	if toml.Unmarshal(profile.profile.configuration, &value) != nil || len(value) != 4 || value["auth"].(map[string]any)["preferred_method"] != "oidc" {
		t.Fatal("managed profile did not pin its closed OIDC configuration")
	}
	models := value["models"].(map[string]any)
	for _, key := range []string{"default", "session_summary", "image_description", "web_search"} {
		if models[key] != config.Model {
			t.Fatal("auxiliary model escaped the selected built-in model")
		}
	}
	for _, secret := range []string{"fixture-managed-access-token", "fixture-managed-refresh-token", "fixture-managed-user", credentialVariable, selectedModel, "base_url", "env_key"} {
		if bytes.Contains(profile.profile.configuration, []byte(secret)) {
			t.Fatal("private credential or API authentication entered managed configuration")
		}
	}
	for _, change := range []func(*managedProfileConfig){
		func(c *managedProfileConfig) { c.Probe.Version = domain.GrokLegacyProtocolVersion },
		func(c *managedProfileConfig) { c.Model = selectedModel },
		func(c *managedProfileConfig) { c.Workspace = c.Probe.Process.Cwd },
		func(c *managedProfileConfig) {
			c.Bundle = bytes.ReplaceAll(c.Bundle, []byte(subscription.GrokIssuer), []byte("https://foreign.example.invalid"))
		},
		func(c *managedProfileConfig) { c.Bundle = []byte(`{"auth_mode":"chatgpt"}`) },
	} {
		changed := config
		change(&changed)
		if _, err := buildManagedProfile(changed); err == nil {
			t.Fatal("foreign or unsafe managed profile was accepted")
		}
	}
}

func TestManagedAuthenticationRejectsGatePartialMetadataAndChangedIdentity(t *testing.T) {
	config, profile := fixtureManagedProfile(t)
	encode := func(meta map[string]any) []byte {
		raw, _ := json.Marshal(map[string]any{"_meta": meta})
		return raw
	}
	if err := profile.validateAuthentication(encode(fixtureManagedAuthMeta()), config.Bundle); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(map[string]any){
		func(m map[string]any) { delete(m, "backend_billed") },
		func(m map[string]any) { m["auth_mode"] = "ApiKey" },
		func(m map[string]any) {
			m["gate"] = map[string]any{"message": "fixture-native-secret", "url": "https://foreign.example.invalid"}
		},
		func(m map[string]any) { m["team_id"] = "foreign-principal" },
		func(m map[string]any) { m["is_team_principal"] = true },
		func(m map[string]any) { m["backend_billed"] = true },
		func(m map[string]any) { m["feedback_trace_offer"] = true },
		func(m map[string]any) { m["token"] = "fixture-native-secret" },
	} {
		meta := fixtureManagedAuthMeta()
		change(meta)
		err := profile.validateAuthentication(encode(meta), config.Bundle)
		if err == nil || strings.Contains(domain.SafeError(err).Message, "fixture-") {
			t.Fatal("invalid native metadata was accepted or exposed")
		}
	}
	foreign := bytes.ReplaceAll(config.Bundle, []byte("fixture-managed-user"), []byte("foreign-managed-user"))
	if profile.validateAuthentication(encode(fixtureManagedAuthMeta()), foreign) == nil {
		t.Fatal("native authentication changed the original protected identity")
	}
}

func TestManagedInitializeCannotBorrowAPIAuthenticationOrSelector(t *testing.T) {
	config, profile := fixtureManagedProfile(t)
	value := fixtureObject(initializeFixture)
	meta := value["_meta"].(map[string]any)
	meta["currentWorkingDirectory"] = config.Probe.Process.Cwd
	meta["defaultAuthMethodId"] = "cached_token"
	meta["modelState"] = map[string]any{"currentModelId": config.Model, "availableModels": []any{map[string]any{"modelId": config.Model, "name": config.ModelName, "_meta": map[string]any{"totalContextTokens": config.ContextTokens, "agentType": "grok-build-plan"}}}}
	value["authMethods"] = []any{map[string]any{"id": "cached_token", "name": "Cached", "description": "Original private OIDC session"}, map[string]any{"id": "grok.com", "name": "Grok", "description": "Interactive advertisement only"}}
	raw, _ := json.Marshal(value)
	if validateInitializeResult(raw, config.Probe.Process.Cwd, &profile.profile) != nil {
		t.Fatal("closed managed initialization was rejected")
	}
	api := apiProfile{contextTokens: config.ContextTokens}
	if validateInitializeResult(raw, config.Probe.Process.Cwd, &api) == nil {
		t.Fatal("API profile acquired cached subscription authentication")
	}
	meta["defaultAuthMethodId"] = "xai.api_key"
	raw, _ = json.Marshal(value)
	if validateInitializeResult(raw, config.Probe.Process.Cwd, &profile.profile) == nil {
		t.Fatal("managed profile accepted API authentication")
	}
}

func TestManagedEnvironmentUsesOnlyTheFixedOIDCModelRoute(t *testing.T) {
	config, _ := fixtureManagedProfile(t)
	original, err := probeEnvironment(config.Probe)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := managedEnvironment(original, "", config.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(observed, "\n")
	if !strings.Contains(joined, "GROK_CLI_CHAT_PROXY_BASE_URL="+managedProxyOrigin) || strings.Contains(joined, "GROK_MODELS_") || strings.Contains(joined, "GROK_XAI_API_BASE_URL=") || strings.Contains(joined, "fixture-managed-access-token") {
		t.Fatal("managed environment retained discovery API routing or credentials")
	}
	if _, err := managedEnvironment(original, "http://127.0.0.1:12345/v1", bytes.ReplaceAll(config.Bundle, []byte("fixture-managed-access-token"), []byte("foreign-access-token"))); err == nil {
		t.Fatal("fixture routing accepted a foreign credential")
	}
	if _, err := managedEnvironment(append(original, credentialVariable+"=foreign"), "", config.Bundle); err == nil {
		t.Fatal("managed routing accepted an API credential")
	}
}

func TestManagedHistorySummaryKeepsItsOriginalModelProfile(t *testing.T) {
	api, files, _ := textHistoryFixture(t)
	home := filepath.Dir(api.profile.path)
	if verifyTextSummary(files["summary.json"], home, api.workspace, api.session, api.completedText) != nil {
		t.Fatal("historical API summary was rejected")
	}
	_, managed := fixtureManagedProfile(t)
	var summary textSummary
	if decode(files["summary.json"], &summary) != nil {
		t.Fatal("fixture summary is invalid")
	}
	summary.Model = managed.profile.selector()
	raw, _ := json.Marshal(summary)
	if verifyProfileTextSummary(raw, home, api.workspace, api.session, api.completedText, managed.profile) != nil {
		t.Fatal("managed summary lost its selected built-in model")
	}
	if verifyTextSummary(raw, home, api.workspace, api.session, api.completedText) == nil || verifyProfileTextSummary(files["summary.json"], home, api.workspace, api.session, api.completedText, managed.profile) == nil {
		t.Fatal("native history crossed its original authentication/model profile")
	}
}

func TestManualNativeGrokManagedInitialization(t *testing.T) {
	nativeManagedProfile(t, false)
}

func TestManualNativeGrokManagedText(t *testing.T) {
	nativeManagedProfile(t, true)
}

func nativeManagedProfile(t *testing.T, textInput bool) {
	t.Helper()
	binary := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit isolated native Grok binary required")
	}
	config, _ := fixtureManagedProfile(t)
	config.Probe.Process.Executable = binary
	var logs bytes.Buffer
	config.Probe.Process.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	var requests atomic.Uint32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if textInput && r.Method == http.MethodPost && r.URL.Path == "/v1/responses" {
			raw, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
			defer clear(raw)
			var body struct {
				Model string `json:"model"`
			}
			if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &body) != nil || body.Model != config.Model || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer fixture-managed-access-token" || requests.Add(1) > 8 {
				t.Error("managed native request changed its original authentication/model authority")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			for _, chunk := range []string{
				`{"type":"response.created","sequence_number":0,"response":{"id":"managed-fixture","object":"response","created_at":1,"model":"grok-4.6","status":"in_progress","output":[]}}`,
				`{"type":"response.output_text.delta","sequence_number":1,"item_id":"managed-item","output_index":0,"content_index":0,"delta":"Private managed fixture response."}`,
				`{"type":"response.completed","sequence_number":2,"response":{"id":"managed-fixture","object":"response","created_at":1,"model":"grok-4.6","status":"completed","output":[{"type":"message","id":"managed-message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Private managed fixture response.","annotations":[]}]}],"usage":{"input_tokens":11,"output_tokens":5,"total_tokens":16,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}}`,
			} {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			}
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": config.Model, "model": config.Model, "name": config.ModelName, "context_window": config.ContextTokens, "api_backend": "chat_completions", "supports_backend_search": false, "agent_type": "grok-build-plan"}}})
	}))
	defer provider.Close()
	root := filepath.Dir(config.Probe.Home)
	original, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	boundary := false
	connection, err := openManaged(ctx, managedOpeningConfig{managedProfileConfig: config, fixtureProxy: provider.URL + "/v1", beforeAuthentication: func(home string) error {
		if home != config.Probe.Home || boundary {
			return incompatible()
		}
		boundary = true
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if !boundary || connection.profile.authentication != managedAuthentication {
		t.Fatal("native startup bypassed original credential ownership")
	}
	claims := 0
	if _, err := connection.Create(ctx, domain.NewID(), domain.NewID(), func(_ context.Context, value CreationClaim) error {
		claims++
		return value.Validate()
	}); err != nil {
		t.Fatalf("native managed creation failed with %d retained claims: %v; %s", claims, err, logs.String())
	}
	if textInput {
		accepted, completed, inputClaims := false, false, 0
		var response strings.Builder
		_, err := connection.RunText(ctx, domain.NewID(), "Return a private managed fixture response.", func(_ context.Context, claim InputClaim) error {
			inputClaims++
			if claim.Phase == ClaimInput && requests.Load() != 0 {
				return sessionUncertain()
			}
			return claim.Validate()
		}, func(_ context.Context, value InputObservation) error {
			switch value.Kind {
			case InputAccepted:
				if accepted {
					return incompatible()
				}
				accepted = true
			case InputText:
				if !accepted || completed || value.Chunk == nil {
					return incompatible()
				}
				response.WriteString(value.Chunk.Update.Content.Text)
			case InputCompleted:
				if !accepted || completed || value.Result == nil {
					return incompatible()
				}
				completed = true
			case InputTitle, InputResponse:
				if !accepted {
					return incompatible()
				}
			default:
				return incompatible()
			}
			return nil
		})
		if err != nil || !accepted || !completed || inputClaims != 2 || response.String() != "Private managed fixture response." {
			t.Fatalf("managed native input failed: %v; %s", err, logs.String())
		}
		if _, err := connection.CloseText(ctx, domain.NewID(), func(_ context.Context, claim ClosureClaim) error { return claim.Validate() }); err != nil {
			t.Fatal("managed original native closure was not confirmed", err)
		}
		if _, err := connection.verifyClosedText(ctx); err != nil {
			t.Fatalf("managed original native text history was not confirmed: %v; %s", err, logs.String())
		}
	}
	if claims != 2 || connection.Close() != nil || process.ReconcileOwner(config.Probe.Process.Directory, config.Probe.Process.OwnerID) != nil {
		t.Fatal("original native session or process cleanup was not confirmed")
	}
	path := filepath.Join(config.Probe.Home, "auth.json")
	retained, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(retained, config.Bundle) {
		t.Fatal("fixture native authentication changed its protected bundle")
	}
	clear(retained)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"fixture-managed-access-token", "fixture-managed-refresh-token"} {
		patterns := [][]byte{[]byte(secret)}
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			patterns = append(patterns, []byte(encoding.EncodeToString([]byte(secret))))
		}
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			raw, err := os.ReadFile(path)
			defer clear(raw)
			for _, pattern := range patterns {
				if bytes.Contains(raw, pattern) {
					t.Error("native credentials escaped authentication storage")
				}
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := subscription.CleanupRuntime(root, original); err != nil {
		t.Fatal(err)
	}
}
