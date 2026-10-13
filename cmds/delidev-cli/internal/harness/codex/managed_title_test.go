// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func managedTitleConfigFixture() map[string]any {
	config := sidechatConfigFixture()
	features := config["features"].(map[string]any)
	for _, key := range managedTitleDisabledFeatures {
		features[key] = false
	}
	config["project_doc_max_bytes"] = 0
	return config
}

func decodeManagedTitleConfig(t *testing.T, config map[string]any) map[string]json.RawMessage {
	t.Helper()
	raw, _ := json.Marshal(config)
	var merged map[string]json.RawMessage
	if json.Unmarshal(raw, &merged) != nil {
		t.Fatal("invalid fixture")
	}
	return merged
}

func TestManagedTitleConfigProvesNoToolsOrAmbientInstructions(t *testing.T) {
	for _, change := range []string{"valid", "shell-tool", "view-image", "skill-search", "mcp", "plugins", "hooks", "project-docs", "sandbox", "web-search", "instructions", "missing-feature", "approval"} {
		t.Run(change, func(t *testing.T) {
			config := managedTitleConfigFixture()
			features := config["features"].(map[string]any)
			switch change {
			case "shell-tool":
				features["shell_tool"] = true
			case "view-image":
				features["view_image"] = true
			case "skill-search":
				features["skill_search"] = true
			case "mcp":
				config["mcp_servers"] = map[string]any{"inherited": map[string]any{}}
			case "plugins":
				config["plugins"] = map[string]any{"inherited": map[string]any{}}
			case "hooks":
				config["hooks"] = map[string]any{"PreToolUse": []string{"x"}}
			case "project-docs":
				config["project_doc_max_bytes"] = 32768
			case "sandbox":
				config["sandbox_mode"] = "workspace-write"
			case "web-search":
				config["web_search"] = "live"
			case "instructions":
				config["developer_instructions"] = "ambient"
			case "missing-feature":
				delete(features, "sleep_tool")
			case "approval":
				config["approval_policy"] = "on-request"
			}
			err := validateManagedTitleConfig(decodeManagedTitleConfig(t, config))
			if (err == nil) != (change == "valid") {
				t.Fatalf("%s: classification = %v", change, err)
			}
		})
	}
}

func TestManagedTitleEffectiveFeaturesMustBeObservedOff(t *testing.T) {
	enabled := map[string]bool{"skip_host_skill_discovery": true}
	for _, key := range managedTitleDisabledFeatures {
		enabled[key] = false
	}
	if err := verifyManagedTitleFeatures(enabled); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"shell_tool", "view_image", "hooks", "plugins"} {
		on := copyFlags(enabled)
		on[key] = true
		if verifyManagedTitleFeatures(on) == nil {
			t.Fatalf("effective %s was accepted", key)
		}
	}
	missing := copyFlags(enabled)
	delete(missing, "skill_search")
	if verifyManagedTitleFeatures(missing) == nil {
		t.Fatal("an unobserved tool flag was accepted")
	}
	off := copyFlags(enabled)
	off["skip_host_skill_discovery"] = false
	if verifyManagedTitleFeatures(off) == nil {
		t.Fatal("host skill discovery was accepted")
	}
}

func copyFlags(source map[string]bool) map[string]bool {
	copied := map[string]bool{}
	for key, value := range source {
		copied[key] = value
	}
	return copied
}

func TestManagedTitleProviderIsRetryFreeBuiltInAuthorityAlias(t *testing.T) {
	valid := map[string]any{"name": "OpenAI", "wire_api": "responses", "requires_openai_auth": true, "supports_websockets": false, "request_max_retries": 0, "stream_max_retries": 0, "base_url": nil, "env_key": nil}
	decode := func(value map[string]any) map[string]json.RawMessage {
		raw, _ := json.Marshal(value)
		var provider map[string]json.RawMessage
		_ = json.Unmarshal(raw, &provider)
		return provider
	}
	if err := validateManagedTitleProvider(decode(valid)); err != nil {
		t.Fatal(err)
	}
	disabledSearch := map[string]any{"supports_standalone_web_search": false}
	for key, value := range valid {
		disabledSearch[key] = value
	}
	if err := validateManagedTitleProvider(decode(disabledSearch)); err != nil {
		t.Fatal("disabled standalone web search was refused", err)
	}
	for name, change := range map[string]func(map[string]any){
		"request-retries": func(p map[string]any) { p["request_max_retries"] = 4 },
		"stream-retries":  func(p map[string]any) { p["stream_max_retries"] = 5 },
		"missing-retries": func(p map[string]any) { delete(p, "stream_max_retries") },
		"websockets":      func(p map[string]any) { p["supports_websockets"] = true },
		"api-key-auth":    func(p map[string]any) { p["requires_openai_auth"] = false },
		"base-url":        func(p map[string]any) { p["base_url"] = "https://example.invalid/v1" },
		"bearer":          func(p map[string]any) { p["experimental_bearer_token"] = "secret" },
		"headers":         func(p map[string]any) { p["http_headers"] = map[string]string{"x": "y"} },
		"name":            func(p map[string]any) { p["name"] = "Other" },
		"protocol":        func(p map[string]any) { p["wire_api"] = "chat" },
		"unknown-field":   func(p map[string]any) { p["new_provider_authority"] = true },
		"unknown-null":    func(p map[string]any) { p["new_provider_authority"] = nil },
		"web-search":      func(p map[string]any) { p["supports_standalone_web_search"] = true },
		"timeout":         func(p map[string]any) { p["stream_idle_timeout_ms"] = 1 },
	} {
		changed := map[string]any{}
		for key, value := range valid {
			changed[key] = value
		}
		change(changed)
		if validateManagedTitleProvider(decode(changed)) == nil {
			t.Fatalf("%s: unsafe provider alias was accepted", name)
		}
	}
}

func TestManagedTitleProfileBeforeLaunch(t *testing.T) {
	for _, change := range []string{"valid", "api", "sidechat", "probe", "unmanaged", "observation", "dynamic-tools", "fork-history", "images"} {
		t.Run(change, func(t *testing.T) {
			cfg := Config{ManagedTitle: true, ManagedAuthentication: true, Mode: ThreadProtocol, Home: t.TempDir(), Process: process.Config{OwnerID: "01a135eb-fa99-7add-a658-072d167e96a1"}}
			switch change {
			case "api":
				cfg.API = &APIConfig{}
			case "sidechat":
				cfg.Sidechat = ReadOnlySidechatV1
			case "probe":
				cfg.Mode = ProbeProtocol
			case "unmanaged":
				cfg.ManagedAuthentication = false
			case "observation":
				cfg.ModelObservation = true
			case "dynamic-tools":
				cfg.DynamicTools = true
			case "fork-history":
				cfg.ManagedForkHistory = true
			case "images":
				cfg.EnableImageGeneration = true
			}
			_, err := Open(context.Background(), cfg)
			if change != "valid" {
				if err == nil {
					t.Fatal("incompatible managed title profile launched")
				}
				return
			}
			// The valid profile passes the pre-launch gate and then fails only
			// because no executable exists in this synthetic fixture.
			if err == nil {
				t.Fatal("fixture has no native executable")
			}
		})
	}
}

func TestManagedTitleArgumentsPinProviderAliasAndDisabledTools(t *testing.T) {
	cfg := Config{ManagedTitle: true, ManagedAuthentication: true, Mode: ThreadProtocol}
	if err := configureManagedTitle(&cfg); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cfg.Process.Args, "\n")
	for _, want := range []string{`model_provider="` + ManagedTitleProvider + `"`, "request_max_retries=0", "stream_max_retries=0", "requires_openai_auth=true", "supports_websockets=false", "features.shell_tool=false", "features.view_image=false", "project_doc_max_bytes=0", `sandbox_mode="read-only"`, `approval_policy="never"`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing pinned native argument %q", want)
		}
	}
	if strings.Contains(joined, "base_url") || strings.Contains(joined, "env_key") {
		t.Fatal("the alias named provider authority beyond the built-in default")
	}
	for _, key := range managedTitleDisabledFeatures {
		if !slices.Contains(cfg.Process.Args, "features."+key+"=false") {
			t.Fatalf("feature %s was not disabled", key)
		}
	}
	plain := Config{ManagedAuthentication: true, Mode: ThreadProtocol}
	if err := configureManagedTitle(&plain); err != nil || len(plain.Process.Args) != 0 {
		t.Fatal("ordinary managed execution gained the title profile")
	}
}
