package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestOwnedAPIEnvironmentExcludesAmbientAuthority(t *testing.T) {
	config := fixtureOwnedAPIConfig(t)
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENCODE_PERMISSION", "OPENCODE_CONFIG", "OPENCODE_TEST_HOME", "OPENCODE_TEST_MANAGED_CONFIG_DIR", "OPENCODE_DB", "OPENCODE_MODELS_PATH", "OPENCODE_SERVER_PASSWORD", "HTTPS_PROXY", "SSH_AUTH_SOCK", "BASH_ENV", "NODE_OPTIONS"} {
		config.Probe.Process.Env = append(config.Probe.Process.Env, key+"=ambient-private-sentinel")
	}
	env, profile, err := prepareAPISession(config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(env, "\n"), "ambient-private-sentinel") || profile.BaseURL != config.ServerOrigin+apiproxy.Prefix || profile.Token != config.Token || !slices.Contains(env, "OPENCODE_PURE=true") || !slices.Contains(env, "npm_config_offline=true") {
		t.Fatal("owned environment inherited authority or changed the fixed relay")
	}
	config.Settings.Permission[0].Action = PermissionAllow
	if profile.Settings.Permission[0].Action != PermissionAsk {
		t.Fatal("caller changed retained native permission settings")
	}
	raw, _ := json.Marshal(config)
	if string(raw) != "{}" {
		t.Fatal("private runtime configuration was serialized")
	}
}

func TestOwnedServerExtraPartialOutputImmediatelyRevokesAuthority(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &startupOutput{expected: "opencode server listening on http://127.0.0.1:1", ready: make(chan struct{}), cancel: cancel}
	_, _ = output.Write([]byte(output.expected + "\n"))
	if output.status() != nil || ctx.Err() != nil {
		t.Fatal("valid startup failed")
	}
	_, _ = output.Write([]byte("{"))
	if output.status() == nil || ctx.Err() == nil {
		t.Fatal("partial extra output retained live native HTTP authority")
	}
}

func TestOwnedAPIInvalidSettingsFailBeforeNativeLaunch(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*apiSessionConfig)
	}{
		{"version", func(c *apiSessionConfig) { c.Probe.Version = "foreign" }},
		{"owner", func(c *apiSessionConfig) { c.Probe.Process.OwnerID = "" }},
		{"executable", func(c *apiSessionConfig) { c.Probe.Process.Executable = "opencode" }},
		{"claim", func(c *apiSessionConfig) { c.Claim = nil }},
		{"key", func(c *apiSessionConfig) { c.Token = "upstream-secret" }},
		{"remote-http", func(c *apiSessionConfig) { c.ServerOrigin = "http://remote.invalid" }},
		{"origin-path", func(c *apiSessionConfig) { c.ServerOrigin += "/upstream" }},
		{"origin-credentials", func(c *apiSessionConfig) { c.ServerOrigin = "https://user:private-secret@server.invalid" }},
		{"origin-query", func(c *apiSessionConfig) { c.ServerOrigin += "?" }},
		{"native-agent", func(c *apiSessionConfig) { c.Settings.Agent = "general" }},
		{"native-root", func(c *apiSessionConfig) { c.NativeRoot = filepath.Dir(c.Probe.Home) }},
		{"private-workspace-overlap", func(c *apiSessionConfig) { c.Workspace = c.Probe.Home }},
		{"unclean-workspace", func(c *apiSessionConfig) { c.Workspace += string(filepath.Separator) + "." }},
		{"context", func(c *apiSessionConfig) { c.ContextLimit = -1 }},
		{"output", func(c *apiSessionConfig) { c.OutputLimit = c.ContextLimit + 1 }},
		{"policy", func(c *apiSessionConfig) { c.Rejection = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := fixtureOwnedAPIConfig(t)
			test.edit(&config)
			if api, err := openAPISession(context.Background(), config); err == nil || api != nil {
				t.Fatal("invalid owned configuration launched a native server")
			}
			if _, err := os.Lstat(config.Probe.Process.Directory); !os.IsNotExist(err) {
				t.Fatal("invalid settings created native process ownership")
			}
		})
	}
}

func TestManagedPolicyInspectionNeverReadsOrOverridesContent(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0700); err != nil {
		t.Fatal(err)
	}
	if err := inspectManagedConfig([]string{filepath.Join(root, "absent"), empty}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(empty, "opencode.json")
	const secret = "private-managed-policy-sentinel"
	if err := os.WriteFile(file, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, empty} {
		if err := inspectManagedConfig([]string{path}); err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), path) {
			t.Fatal("managed content was accepted or disclosed")
		}
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "missing"), link); err == nil {
		if inspectManagedConfig([]string{link}) == nil {
			t.Fatal("uninspectable managed symlink was accepted")
		}
	}
	raw, err := os.ReadFile(file)
	if err != nil || string(raw) != secret {
		t.Fatal("managed content changed during inspection")
	}
}

func fixtureAgentInventory(root string) []any {
	build, _ := expectedPrimaryAgent(BuildAgent, root, root)
	agents := []any{build}
	for _, name := range []string{"plan", "general", "explore", "compaction", "title", "summary"} {
		agents = append(agents, map[string]any{"name": name, "mode": "primary", "native": true, "permission": []any{}, "options": map[string]any{}})
	}
	return agents
}

func TestSelectedNativeBuildPolicyCannotGainOverrides(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-runtime")
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"foreign-agent", func(v map[string]any) { v["name"] = "foreign" }},
		{"hidden", func(v map[string]any) { v["hidden"] = true }},
		{"model", func(v map[string]any) { v["model"] = map[string]any{"providerID": "foreign", "modelID": "other"} }},
		{"mode", func(v map[string]any) { v["mode"] = "subagent" }},
		{"prompt", func(v map[string]any) { v["prompt"] = "private-injected-instruction" }},
		{"options", func(v map[string]any) { v["options"] = map[string]any{"apiKey": "private-secret"} }},
		{"policy", func(v map[string]any) { v["permission"] = []PermissionRule{{"*", "*", PermissionAllow}} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			agents := fixtureAgentInventory(root)
			raw, _ := json.Marshal(agents)
			if err := validatePrimaryAgent(raw, BuildAgent, root, root); err != nil {
				t.Fatal(err)
			}
			test.change(agents[0].(map[string]any))
			raw, _ = json.Marshal(agents)
			if err := validatePrimaryAgent(raw, BuildAgent, root, root); err == nil || domain.SafeError(err).Code != domain.Unsupported {
				t.Fatal("changed native agent policy gained initialization authority")
			}
		})
	}
}

func TestSelectedNativePlanPolicyPreservesExceptionsAndOrder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-runtime")
	worktree := filepath.Join(t.TempDir(), "workspace")
	for _, change := range []string{"none", "missing-edit-deny", "different-native-root", "absolute-relative-plan", "reordered-rules", "build-policy", "system-prompt", "model", "extra-rule", "hidden"} {
		t.Run(change, func(t *testing.T) {
			selected, err := expectedPrimaryAgent(PlanAgent, root, worktree)
			if err != nil {
				t.Fatal(err)
			}
			agents := fixtureAgentInventory(root)
			agents[1] = selected
			rules := selected["permission"].([]PermissionRule)
			switch change {
			case "missing-edit-deny":
				for i := range rules {
					if rules[i].Permission == "edit" && rules[i].Pattern == "*" {
						rules[i].Action = PermissionAllow
					}
				}
			case "different-native-root":
				agents[1], _ = expectedPrimaryAgent(PlanAgent, root, filepath.Join(worktree, "foreign"))
			case "absolute-relative-plan":
				rules[len(rules)-2].Pattern = filepath.Join(root, "data", "opencode", "plans", "*.md")
			case "reordered-rules":
				rules[0], rules[1] = rules[1], rules[0]
			case "build-policy":
				build, _ := expectedPrimaryAgent(BuildAgent, root, worktree)
				selected["permission"] = build["permission"]
			case "system-prompt":
				selected["prompt"] = "foreign private override"
			case "model":
				selected["model"] = map[string]any{"providerID": "foreign", "modelID": "foreign"}
			case "extra-rule":
				selected["permission"] = append(rules, PermissionRule{"edit", "*", PermissionAllow})
			case "hidden":
				selected["hidden"] = true
			}
			raw, _ := json.Marshal(agents)
			if err := validatePrimaryAgent(raw, PlanAgent, root, worktree); (err == nil) != (change == "none") {
				t.Fatal("native Plan selection ignored exact original ordered policy")
			}
		})
	}
}
