package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
)

func apiFixtureToken() string {
	return apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
}

func TestOwnedProxyRebuildsEnvironmentAndRejectsInvalidLocalAuthority(t *testing.T) {
	local := "http://delidev:" + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("p", 32))) + "@127.0.0.1:12346"
	for _, platform := range []string{"linux", "darwin", "windows"} {
		t.Run(platform, func(t *testing.T) {
			got := proxyEnvironment([]string{"PATH=fixture", "Http_Proxy=foreign", "https_proxy=foreign", "ALL_PROXY=foreign", "No_Proxy=*"}, local, platform)
			seen := map[string]bool{}
			for _, entry := range got {
				key, value, _ := strings.Cut(entry, "=")
				if seen[key] || strings.Contains(entry, "foreign") {
					t.Fatal("proxy environment retained inherited authority")
				}
				seen[key] = true
				if proxyEnvironmentKey(key) {
					expected := ""
					if strings.EqualFold(key, "HTTP_PROXY") || strings.EqualFold(key, "HTTPS_PROXY") {
						expected = local
					}
					if value != expected || platform == "windows" && key != strings.ToUpper(key) {
						t.Fatal("inconsistent native proxy environment")
					}
				}
			}
			if len(got) != 9 && platform != "windows" || platform == "windows" && len(got) != 5 {
				t.Fatal("missing proxy exclusions")
			}
		})
	}
	for _, change := range []string{"valid", "zero", "leading-zero", "overflow", "base64", "credential-size", "host-alias"} {
		t.Run(change, func(t *testing.T) {
			endpoint := local
			switch change {
			case "zero":
				endpoint = strings.ReplaceAll(endpoint, ":12346", ":0")
			case "leading-zero":
				endpoint = strings.ReplaceAll(endpoint, ":12346", ":012346")
			case "overflow":
				endpoint = strings.ReplaceAll(endpoint, ":12346", ":65536")
			case "base64":
				endpoint = strings.ReplaceAll(endpoint, base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("p", 32))), strings.Repeat("!", 43))
			case "credential-size":
				endpoint = strings.ReplaceAll(endpoint, base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("p", 32))), base64.RawURLEncoding.EncodeToString([]byte("short")))
			case "host-alias":
				endpoint = strings.ReplaceAll(endpoint, "127.0.0.1", "localhost")
			}
			cfg := Config{Mode: ThreadProtocol, API: &APIConfig{ServerOrigin: "https://server.example", Token: apiFixtureToken(), LoopbackProxyURL: endpoint}}
			binding, err := configureAPI(&cfg)
			if (err == nil) != (change == "valid") {
				t.Fatal("invalid local authority classification", err)
			}
			if err != nil {
				return
			}
			if !binding.proxied || !slices.Contains(cfg.Process.Args, "features.respect_system_proxy=false") {
				t.Fatal("native system proxy discovery remained enabled")
			}
			if strings.Contains(strings.Join(cfg.Process.Args, " "), local) || !slices.Contains(cfg.Process.ProtectedValues, local) {
				t.Fatal("local authority escaped transient protection")
			}
		})
	}
}

func TestOwnedProxyMergedConfigurationCannotRestoreSystemOrShellAuthority(t *testing.T) {
	for _, change := range []string{"valid", "feature-absent", "feature-true", "feature-null", "exclude-missing", "setter"} {
		t.Run(change, func(t *testing.T) {
			provider := map[string]any{"name": "DeliDev", "base_url": "https://server.example/api-proxy/v1", "env_key": executionTokenEnv, "wire_api": "responses", "requires_openai_auth": false, "supports_websockets": false, "supports_standalone_web_search": false}
			shell := map[string]any{"exclude": append([]string{executionTokenEnv}, proxyEnvironmentKeys...)}
			config := map[string]any{"features": map[string]any{"respect_system_proxy": false}, "model_provider": APIProvider, "cli_auth_credentials_store": "ephemeral", "model_providers": map[string]any{APIProvider: provider}, "shell_environment_policy": shell}
			switch change {
			case "feature-absent":
				delete(config, "features")
			case "feature-true":
				config["features"] = map[string]any{"respect_system_proxy": true}
			case "feature-null":
				config["features"] = map[string]any{"respect_system_proxy": nil}
			case "exclude-missing":
				shell["exclude"] = []string{executionTokenEnv}
			case "setter":
				shell["set"] = map[string]string{"hTtPs_PrOxY": "foreign"}
			}
			raw, _ := json.Marshal(config)
			var merged map[string]json.RawMessage
			if err := json.Unmarshal(raw, &merged); err != nil {
				t.Fatal(err)
			}
			binding := apiBinding{endpoint: "https://server.example/api-proxy/v1", proxied: true}
			if err := binding.validateConfig(merged); (err == nil) != (change == "valid") {
				t.Fatal("merged proxy authority was misclassified", err)
			}
		})
	}
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

func TestExecutionAPIOptionalCatalogAndGatewayAuthority(t *testing.T) {
	for _, profile := range []struct {
		name    string
		title   bool
		proxied bool
	}{{"ordinary", false, false}, {"title", true, false}, {"proxy", false, true}, {"proxy-title", true, true}} {
		t.Run(profile.name, func(t *testing.T) {
			for _, change := range []struct {
				name   string
				fields string
				valid  bool
			}{
				{"omitted", "", true},
				{"catalog-null", `,"model_catalog_url":null`, true},
				{"gateway-null", `,"gateway_oauth":null`, true},
				{"both-null", `,"model_catalog_url":null,"gateway_oauth":null`, true},
				{"catalog-url", `,"model_catalog_url":"https://catalog.example"`, false},
				{"catalog-empty", `,"model_catalog_url":""`, false},
				{"catalog-object", `,"model_catalog_url":{}`, false},
				{"gateway-object", `,"gateway_oauth":{"issuer":"https://gateway.example"}`, false},
				{"gateway-empty-object", `,"gateway_oauth":{}`, false},
				{"gateway-string", `,"gateway_oauth":""`, false},
				{"gateway-array", `,"gateway_oauth":[]`, false},
				{"gateway-scalar", `,"gateway_oauth":false`, false},
				{"unknown-null", `,"other_authority":null`, false},
				{"catalog-duplicate", `,"model_catalog_url":null,"model_catalog_url":null`, false},
				{"gateway-duplicate", `,"gateway_oauth":null,"gateway_oauth":null`, false},
				{"catalog-populated-with-gateway-null", `,"model_catalog_url":"","gateway_oauth":null`, false},
				{"gateway-populated-with-catalog-null", `,"model_catalog_url":null,"gateway_oauth":{}`, false},
			} {
				t.Run(change.name, func(t *testing.T) {
					provider := `{"name":"DeliDev","base_url":"http://127.0.0.1:12345/api-proxy/v1","env_key":"DELIDEV_EXECUTION_TOKEN","wire_api":"responses","requires_openai_auth":false,"supports_websockets":false,"supports_standalone_web_search":false,"request_max_retries":0,"stream_max_retries":0` + change.fields + `}`
					config := apiOptionalAuthorityConfig(t, provider, profile.proxied)
					binding := apiBinding{endpoint: "http://127.0.0.1:12345/api-proxy/v1", title: profile.title, proxied: profile.proxied}
					if err := binding.validateConfig(config); (err == nil) != change.valid {
						t.Fatalf("optional provider authority was misclassified: %v", err)
					}
				})
			}
		})
	}
}

func apiOptionalAuthorityConfig(t *testing.T, provider string, proxied bool) map[string]json.RawMessage {
	t.Helper()
	excluded := []string{executionTokenEnv}
	if proxied {
		excluded = append(excluded, proxyEnvironmentKeys...)
	}
	raw, err := json.Marshal(map[string]any{
		"model_provider": APIProvider, "cli_auth_credentials_store": "ephemeral",
		"model_providers":          map[string]json.RawMessage{APIProvider: json.RawMessage(provider)},
		"features":                 map[string]bool{"respect_system_proxy": false},
		"shell_environment_policy": map[string]any{"exclude": excluded},
	})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestExecutionAPINullOptionalFieldsPreserveTitleRetryRestrictions(t *testing.T) {
	for _, proxied := range []bool{false, true} {
		for _, retries := range []string{
			``,
			`,"request_max_retries":0`,
			`,"request_max_retries":null,"stream_max_retries":0`,
			`,"request_max_retries":1,"stream_max_retries":0`,
			`,"request_max_retries":0,"stream_max_retries":1`,
		} {
			provider := `{"name":"DeliDev","base_url":"http://127.0.0.1:12345/api-proxy/v1","env_key":"DELIDEV_EXECUTION_TOKEN","wire_api":"responses","requires_openai_auth":false,"supports_websockets":false,"supports_standalone_web_search":false,"model_catalog_url":null,"gateway_oauth":null` + retries + `}`
			binding := apiBinding{endpoint: "http://127.0.0.1:12345/api-proxy/v1", title: true, proxied: proxied}
			if err := binding.validateConfig(apiOptionalAuthorityConfig(t, provider, proxied)); err == nil {
				t.Fatal("null optional authority bypassed the title zero-retry requirement")
			}
		}
	}
}
