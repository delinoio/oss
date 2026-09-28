package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func fixtureAPIProfile() *nativeAPIProfile {
	return &nativeAPIProfile{Settings: fixtureSettings(), BaseURL: "http://127.0.0.1:1234/v1", Token: "private-scoped-credential-sentinel", ContextLimit: 32000, OutputLimit: 1000, Rejection: StopOnInteractionRejection}
}

func fixtureEffectiveConfig(p *nativeAPIProfile) map[string]any {
	value, _ := p.config()
	value["agent"], value["mode"], value["command"], value["plugin"] = map[string]any{}, map[string]any{}, map[string]any{}, []any{}
	return value
}

func TestAPIProfileRetainsNativeUnknownLimitsAndExplicitEmptyRules(t *testing.T) {
	p := fixtureAPIProfile()
	p.Settings.Permission = []PermissionRule{}
	p.ContextLimit, p.OutputLimit = 0, 0
	raw, err := p.configBytes()
	if err != nil || !strings.Contains(string(raw), `"context":0`) || !strings.Contains(string(raw), `"output":0`) {
		t.Fatal("native unknown model limits were invented or rejected")
	}
	p.Settings.Permission = nil
	if _, err := p.configBytes(); err == nil {
		t.Fatal("missing permission selection became an explicit native default")
	}
}

func TestEffectiveAPIProfileRejectsAuthorityAndCapabilityDrift(t *testing.T) {
	p := fixtureAPIProfile()
	for _, test := range []struct {
		name   string
		config bool
		change func(map[string]any)
	}{
		{"foreign-enabled-provider", true, func(v map[string]any) { v["enabled_providers"] = []string{p.Settings.Provider, "foreign"} }},
		{"fallback-model", true, func(v map[string]any) { v["small_model"] = "foreign/model" }},
		{"plugin", true, func(v map[string]any) { v["plugin"] = []string{"private-plugin-sentinel"} }},
		{"agent", true, func(v map[string]any) { v["agent"] = map[string]any{"build": map[string]any{"permission": "allow"}} }},
		{"mcp", true, func(v map[string]any) { v["mcp"] = map[string]any{} }},
		{"policy", true, func(v map[string]any) { v["experimental"] = map[string]any{"continue_loop_on_deny": true} }},
		{"foreign-connected", false, func(v map[string]any) { v["connected"] = []string{"foreign"} }},
		{"fallback-default", false, func(v map[string]any) { v["default"] = map[string]any{p.Settings.Provider: "foreign"} }},
		{"credential", false, func(v map[string]any) {
			v["all"].([]any)[0].(map[string]any)["options"].(map[string]any)["apiKey"] = "foreign-secret"
		}},
		{"relay", false, func(v map[string]any) {
			v["all"].([]any)[0].(map[string]any)["options"].(map[string]any)["baseURL"] = "https://foreign.invalid"
		}},
		{"auth-source", false, func(v map[string]any) { v["all"].([]any)[0].(map[string]any)["source"] = "auth" }},
		{"ambient-environment", false, func(v map[string]any) { v["all"].([]any)[0].(map[string]any)["env"] = []string{"OPENAI_API_KEY"} }},
		{"model-header", false, func(v map[string]any) {
			v["all"].([]any)[0].(map[string]any)["models"].(map[string]any)[p.Settings.Model].(map[string]any)["headers"] = map[string]any{"Authorization": "foreign-secret"}
		}},
		{"model-capability", false, func(v map[string]any) {
			v["all"].([]any)[0].(map[string]any)["models"].(map[string]any)[p.Settings.Model].(map[string]any)["capabilities"].(map[string]any)["reasoning"] = true
		}},
		{"model-limit", false, func(v map[string]any) {
			v["all"].([]any)[0].(map[string]any)["models"].(map[string]any)[p.Settings.Model].(map[string]any)["limit"] = map[string]any{"context": 64000, "output": 1000}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, validate := p.provider(), p.validateProvider
			if test.config {
				value, validate = fixtureEffectiveConfig(p), p.validateConfig
			}
			raw, _ := json.Marshal(value)
			if err := validate(raw); err != nil {
				t.Fatal(err)
			}
			test.change(value)
			raw, _ = json.Marshal(value)
			if err := validate(raw); err == nil || domain.SafeError(err).Code != domain.Unsupported {
				t.Fatal("changed native authority was accepted")
			}
		})
	}
	for _, raw := range []string{"null", "[]", `{}`, `{"all":[],"all":[]}`, `{} {}`, `{"\u0061ll":[],"all":[]}`} {
		if p.validateProvider([]byte(raw)) == nil || p.validateConfig([]byte(raw)) == nil {
			t.Fatal("ambiguous or incomplete native authority was accepted")
		}
	}
	raw, _ := json.Marshal(p)
	if string(raw) != "{}" {
		t.Fatal("private native authority was serialized")
	}
}

func TestEffectiveAPIProfileVerificationIsBoundedAndClaimPreceding(t *testing.T) {
	for _, badStep := range []int{0, 1, 2, 3} {
		t.Run(string(rune('0'+badStep)), func(t *testing.T) {
			p := fixtureAPIProfile()
			var calls atomic.Int32
			var logs bytes.Buffer
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				i := int(calls.Add(1))
				want := "/config"
				if i == 2 {
					want = "/provider"
				}
				user, password, ok := r.BasicAuth()
				if i > 3 || r.Method != http.MethodGet || r.URL.Path != want || r.URL.RawQuery != "" || r.Header.Get("x-opencode-directory") != fixtureWorkspacePath() || !ok || user != "delidev" || password != "private-server-secret" {
					t.Error("verification escaped its exact read-only authority")
				}
				w.Header().Set("Content-Type", "application/json")
				value := fixtureEffectiveConfig(p)
				if i == 2 {
					value = p.provider()
				}
				if i == badStep {
					value["private-unexpected-secret"] = p.Token
				}
				_ = json.NewEncoder(w).Encode(value)
			}))
			defer server.Close()
			api := &sessionAPI{client: server.Client(), origin: server.URL, cwd: fixtureWorkspacePath(), password: "private-server-secret", gate: make(chan struct{}, 1), apiProfile: p, rejectionPolicy: p.Rejection, logger: slog.New(slog.NewJSONHandler(&logs, nil)), alive: func() error { return nil }}
			api.claim = func(context.Context, SessionClaim) error {
				t.Error("configuration inspection claimed a mutation")
				return nil
			}
			if _, err := api.create(context.Background(), domain.NewID(), p.Settings); err == nil || calls.Load() != 0 {
				t.Fatal("unverified configuration authorized native creation")
			}
			err := api.verifyAPIProfile(context.Background())
			if api.runtimeRead || (err == nil) != (badStep == 0) || api.apiVerified != (badStep == 0) || api.creation != nil {
				t.Fatal("verification changed mutation ownership or retained read authority")
			}
			if badStep != 0 {
				if api.problem == nil || int(calls.Load()) != badStep || api.verifyAPIProfile(context.Background()) == nil {
					t.Fatal("contradictory native profile regained authority")
				}
			} else {
				changed := p.Settings
				changed.Model = "foreign"
				if _, err := api.create(context.Background(), domain.NewID(), changed); err == nil {
					t.Fatal("verified selection authorized a different session")
				}
			}
			if _, _, err := api.request(context.Background(), http.MethodGet, "/config", nil, 200); err == nil {
				t.Fatal("temporary read authority survived verification")
			}
			for _, secret := range []string{p.Token, p.BaseURL, "private-server-secret", "private-unexpected-secret", api.cwd} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("private response leaked into verification diagnostics")
				}
			}
		})
	}
}
