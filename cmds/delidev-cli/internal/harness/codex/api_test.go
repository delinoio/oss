package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
)

func apiFixtureToken() string {
	return apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
}

func TestExecutionAPIRejectsInvalidAuthorityBeforeLaunch(t *testing.T) {
	for _, changed := range []string{"probe", "token", "remote-http", "path", "query", "duplicate-token"} {
		t.Run(changed, func(t *testing.T) {
			cfg := fixtureConfig(t, "thread-ready")
			cfg.Mode = ThreadProtocol
			cfg.API = &APIConfig{ServerOrigin: "http://127.0.0.1:12345", Token: apiFixtureToken()}
			switch changed {
			case "probe":
				cfg.Mode = ProbeProtocol
			case "token":
				cfg.API.Token = "ordinary-worker-token"
			case "remote-http":
				cfg.API.ServerOrigin = "http://192.0.2.1"
			case "path":
				cfg.API.ServerOrigin += "/foreign"
			case "query":
				cfg.API.ServerOrigin += "?"
			case "duplicate-token":
				cfg.Process.Env = append(cfg.Process.Env, executionTokenEnv+"=foreign")
			}
			if c, err := Open(context.Background(), cfg); err == nil {
				_ = c.Close()
				t.Fatal("invalid native authority started a process")
			}
			if _, err := os.Stat(cfg.Process.Directory); !os.IsNotExist(err) {
				t.Fatal("invalid authority crossed the start barrier")
			}
		})
	}
}

func TestExecutionAPIOnlyPlacesCredentialInExplicitEnvironment(t *testing.T) {
	for _, origin := range []string{"https://server.example", "http://localhost:12345/", "http://[::1]:12345"} {
		cfg := Config{Mode: ThreadProtocol, API: &APIConfig{ServerOrigin: origin, Token: apiFixtureToken()}}
		binding, err := configureAPI(&cfg)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Process.Env) != 1 || cfg.Process.Env[0] != executionTokenEnv+"="+apiFixtureToken() {
			t.Fatal("native token was not scoped to the explicit environment")
		}
		if strings.Contains(strings.Join(cfg.Process.Args, " "), apiFixtureToken()) {
			t.Fatal("native token entered argv")
		}
		if !strings.HasSuffix(binding.endpoint, apiproxy.Prefix) {
			t.Fatal("native endpoint escaped the fixed proxy route")
		}
		raw, err := json.Marshal(cfg.API)
		if err != nil || strings.Contains(string(raw), apiFixtureToken()) {
			t.Fatal("native token entered serialized configuration")
		}
	}
}

func TestExecutionAPIRejectsInheritedProviderAndShellOverrides(t *testing.T) {
	for _, change := range []string{"none", "endpoint", "env-key", "literal-token", "command-auth", "query", "header", "websocket", "retry", "shell-set", "shell-exclusion"} {
		t.Run(change, func(t *testing.T) {
			provider := map[string]any{"name": "DeliDev", "base_url": "http://127.0.0.1:12345/api-proxy/v1", "env_key": executionTokenEnv, "wire_api": "responses", "requires_openai_auth": false, "supports_websockets": false, "supports_standalone_web_search": false, "request_max_retries": 0, "stream_max_retries": 0}
			shell := map[string]any{"exclude": []string{executionTokenEnv}}
			switch change {
			case "endpoint":
				provider["base_url"] = "https://foreign.example"
			case "env-key":
				provider["env_key"] = "OPENAI_API_KEY"
			case "literal-token":
				provider["experimental_bearer_token"] = "foreign"
			case "command-auth":
				provider["auth"] = map[string]any{"command": "foreign"}
			case "query":
				provider["query_params"] = map[string]string{"api_key": "foreign"}
			case "header":
				provider["http_headers"] = map[string]string{"Authorization": "foreign"}
			case "websocket":
				provider["supports_websockets"] = true
			case "retry":
				provider["stream_max_retries"] = -1
			case "shell-set":
				shell["set"] = map[string]string{executionTokenEnv: "foreign"}
			case "shell-exclusion":
				shell["exclude"] = []string{}
			}
			raw, _ := json.Marshal(map[string]any{"model_provider": APIProvider, "cli_auth_credentials_store": "ephemeral", "model_providers": map[string]any{APIProvider: provider}, "shell_environment_policy": shell})
			var config map[string]json.RawMessage
			if err := json.Unmarshal(raw, &config); err != nil {
				t.Fatal(err)
			}
			binding := apiBinding{endpoint: "http://127.0.0.1:12345/api-proxy/v1"}
			if err := binding.validateConfig(config); (err == nil) != (change == "none") {
				t.Fatalf("inherited override was misclassified: %v", err)
			}
		})
	}
}
