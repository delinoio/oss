// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/pelletier/go-toml/v2"
)

const (
	mcpSecretEnv    = "stdio-secret-value-1"
	mcpSecretHeader = "header-secret-value-2"
	mcpSecretBearer = "bearer-secret-value-3"
)

func mcpStdioServer(name string) ManagedMCPServer {
	return ManagedMCPServer{Identity: domain.MCPServerIdentity{DefinitionID: domain.NewID(), Revision: 3, Server: name}, Transport: domain.MCPStdio, Command: "/usr/bin/env", Arguments: []string{"server", "--flag=\"quoted\""}, Cwd: "/tmp", Environment: map[string]string{"FIXTURE_TOKEN": mcpSecretEnv}}
}

func mcpHTTPServer(name string) ManagedMCPServer {
	return ManagedMCPServer{Identity: domain.MCPServerIdentity{DefinitionID: domain.NewID(), Revision: 2, Server: name}, Transport: domain.MCPStreamableHTTP, Endpoint: "https://mcp.example.test/stream", Headers: map[string]string{"X-Api-Key": mcpSecretHeader}, BearerToken: ""}
}

func mcpOAuthServer(name string) ManagedMCPServer {
	return ManagedMCPServer{Identity: domain.MCPServerIdentity{DefinitionID: domain.NewID(), Revision: 1, Server: name}, Transport: domain.MCPStreamableHTTP, Endpoint: "http://127.0.0.1:8080/mcp", BearerToken: mcpSecretBearer}
}

func mcpConfig(servers ...ManagedMCPServer) Config {
	return Config{Mode: ThreadProtocol, ManagedMCP: servers, Process: processConfigFixture()}
}

// parseMCPArgs reads native `-c mcp_servers.NAME={...}` values exactly as the
// installed TOML layer would, proving the launch syntax is valid TOML.
func parseMCPArgs(t *testing.T, args []string) map[string]map[string]any {
	t.Helper()
	servers := map[string]map[string]any{}
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "-c" || !strings.HasPrefix(args[i+1], "mcp_servers.") {
			continue
		}
		key, value, _ := strings.Cut(strings.TrimPrefix(args[i+1], "mcp_servers."), "=")
		var parsed map[string]any
		if err := toml.Unmarshal([]byte("v = "+value), &parsed); err != nil {
			t.Fatalf("invalid native TOML for %s: %v", key, err)
		}
		servers[key] = parsed["v"].(map[string]any)
	}
	return servers
}

func TestManagedMCPLaunchConfigurationKeepsSecretsOutOfArguments(t *testing.T) {
	config := mcpConfig(mcpHTTPServer("web_aaaa"), mcpStdioServer("fx_bbbb"), mcpOAuthServer("oauth_cccc"))
	config.Process.Args = []string{"existing"}
	bindings, err := configureManagedMCP(&config)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 3 {
		t.Fatalf("expected one binding per selection, got %d", len(bindings))
	}
	joined := strings.Join(config.Process.Args, "\n")
	for _, secret := range []string{mcpSecretEnv, mcpSecretHeader, mcpSecretBearer} {
		if strings.Contains(joined, secret) {
			t.Fatalf("protected value reached native arguments: %q", secret)
		}
		if !slices.Contains(config.Process.ProtectedValues, secret) {
			t.Fatalf("protected value is not registered for diagnostics redaction: %q", secret)
		}
	}
	servers := parseMCPArgs(t, config.Process.Args)
	if len(servers) != 3 {
		t.Fatalf("native arguments describe %d servers", len(servers))
	}
	stdio := servers["fx_bbbb"]
	if stdio["command"] != "/usr/bin/env" || stdio["cwd"] != "/tmp" || len(stdio["args"].([]any)) != 2 || stdio["args"].([]any)[1] != "--flag=\"quoted\"" {
		t.Fatalf("stdio launch changed: %v", stdio)
	}
	if names := stdio["env_vars"].([]any); len(names) != 1 || names[0] != "FIXTURE_TOKEN" {
		t.Fatalf("stdio environment must be inherited by name only: %v", stdio["env_vars"])
	}
	web := servers["web_aaaa"]
	if web["url"] != "https://mcp.example.test/stream" || web["bearer_token_env_var"] != nil {
		t.Fatalf("http launch changed: %v", web)
	}
	headers := web["env_http_headers"].(map[string]any)
	if len(headers) != 1 || !strings.HasPrefix(headers["X-Api-Key"].(string), "DELIDEV_MCP_") {
		t.Fatalf("headers must reference private environment: %v", headers)
	}
	if oauth := servers["oauth_cccc"]; !strings.HasPrefix(oauth["bearer_token_env_var"].(string), "DELIDEV_MCP_") {
		t.Fatalf("oauth bearer must reference private environment: %v", oauth)
	}
	env := strings.Join(config.Process.Env, "\n")
	for _, secret := range []string{mcpSecretEnv, mcpSecretHeader, mcpSecretBearer} {
		if !strings.Contains(env, secret) {
			t.Fatalf("private environment lacks a selected value")
		}
	}
	if !slices.Contains(config.mcpExcluded, "FIXTURE_TOKEN") || len(config.mcpExcluded) != 3 {
		t.Fatalf("native shell tools must not inherit protected values: %v", config.mcpExcluded)
	}
	for _, binding := range bindings {
		if binding.server.Environment != nil || binding.server.Headers != nil || binding.server.BearerToken != "" {
			t.Fatal("retained binding kept protected values")
		}
	}
}

func TestManagedMCPLaunchRejectsUnsafeSelections(t *testing.T) {
	cases := map[string]func(*Config){
		"sidechat":          func(c *Config) { c.Sidechat = ReadOnlySidechatV1 },
		"code-review":       func(c *Config) { c.CodeReviewModel = "model" },
		"probe-mode":        func(c *Config) { c.Mode = ProbeProtocol },
		"reserved-env":      func(c *Config) { c.ManagedMCP[0].Environment = map[string]string{"PATH": "/evil"} },
		"codex-env":         func(c *Config) { c.ManagedMCP[0].Environment = map[string]string{"CODEX_HOME": "/evil"} },
		"invalid-env-name":  func(c *Config) { c.ManagedMCP[0].Environment = map[string]string{"A=B": "x"} },
		"existing-env":      func(c *Config) { c.Process.Env = append(c.Process.Env, "FIXTURE_TOKEN=host") },
		"duplicate-name":    func(c *Config) { c.ManagedMCP = append(c.ManagedMCP, c.ManagedMCP[0]) },
		"bad-native-name":   func(c *Config) { c.ManagedMCP[0].Identity.Server = "Bad Name" },
		"stdio-with-url":    func(c *Config) { c.ManagedMCP[0].Endpoint = "https://example.test" },
		"stdio-no-command":  func(c *Config) { c.ManagedMCP[0].Command = "" },
		"unknown-transport": func(c *Config) { c.ManagedMCP[0].Transport = "carrier-pigeon" },
		"zero-revision":     func(c *Config) { c.ManagedMCP[0].Identity.Revision = 0 },
		"case-folded-conflict": func(c *Config) {
			second := mcpStdioServer("second_dddd")
			second.Environment = map[string]string{"fixture_token": "different"}
			c.ManagedMCP = append(c.ManagedMCP, second)
		},
		"conflicting-values": func(c *Config) {
			second := mcpStdioServer("second_dddd")
			second.Environment = map[string]string{"FIXTURE_TOKEN": "different"}
			c.ManagedMCP = append(c.ManagedMCP, second)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			config := mcpConfig(mcpStdioServer("fx_bbbb"))
			mutate(&config)
			_, err := configureManagedMCP(&config)
			assertCode(t, err, domain.Unsupported)
			if strings.Contains(strings.Join(config.Process.Args, "\n"), "fx_bbbb={") && name != "duplicate-name" && name != "conflicting-values" && name != "case-folded-conflict" {
				// A rejected selection never leaves a partial native server behind.
				t.Fatal("rejected selection left native arguments")
			}
		})
	}
	for name, mutate := range map[string]func(*ManagedMCPServer){
		"http-with-command":        func(s *ManagedMCPServer) { s.Command = "/bin/true" },
		"http-with-env":            func(s *ManagedMCPServer) { s.Environment = map[string]string{"X": "y"} },
		"http-credential-url":      func(s *ManagedMCPServer) { s.Endpoint = "https://user:pw@example.test/mcp" },
		"http-plain-remote":        func(s *ManagedMCPServer) { s.Endpoint = "http://example.test/mcp" },
		"header-injection":         func(s *ManagedMCPServer) { s.Headers = map[string]string{"X-A\r\nB": "v"} },
		"bearer-and-authorization": func(s *ManagedMCPServer) { s.BearerToken = "t"; s.Headers = map[string]string{"Authorization": "x"} },
	} {
		t.Run(name, func(t *testing.T) {
			server := mcpHTTPServer("web_aaaa")
			mutate(&server)
			config := mcpConfig(server)
			_, err := configureManagedMCP(&config)
			assertCode(t, err, domain.Unsupported)
		})
	}
}

// nativeEffectiveConfig mirrors what the installed app-server reports after the
// launch arguments merge with defaults (captured from Codex 0.159.2).
func nativeEffectiveConfig(t *testing.T, args []string) map[string]json.RawMessage {
	t.Helper()
	servers := parseMCPArgs(t, args)
	effective := map[string]any{}
	for name, fields := range servers {
		fields["environment_id"] = "local"
		fields["enabled"] = true
		fields["tool_timeout_sec"] = nil
		effective[name] = fields
	}
	raw, err := json.Marshal(effective)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]json.RawMessage{"mcp_servers": raw}
}

func mcpClient(t *testing.T, servers ...ManagedMCPServer) (*Client, Config) {
	t.Helper()
	config := mcpConfig(servers...)
	bindings, err := configureManagedMCP(&config)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{managedMCP: bindings}, config
}

func TestManagedMCPInventoryRequiresTheExactSelectedConfiguration(t *testing.T) {
	c, config := mcpClient(t, mcpStdioServer("fx_bbbb"), mcpHTTPServer("web_aaaa"))
	effective := nativeEffectiveConfig(t, config.Process.Args)
	if err := c.validateMCPConfig(effective); err != nil {
		t.Fatalf("exact native configuration rejected: %v %s %s", err, domain.SafeError(err).Guidance, effective["mcp_servers"])
	}
	mutate := func(edit func(map[string]map[string]any)) map[string]json.RawMessage {
		var servers map[string]map[string]any
		_ = json.Unmarshal(effective["mcp_servers"], &servers)
		edit(servers)
		raw, _ := json.Marshal(servers)
		return map[string]json.RawMessage{"mcp_servers": raw}
	}
	for name, edit := range map[string]func(map[string]map[string]any){
		"unselected-server":   func(s map[string]map[string]any) { s["project_eeee"] = map[string]any{"command": "/bin/evil"} },
		"missing-server":      func(s map[string]map[string]any) { delete(s, "web_aaaa") },
		"changed-command":     func(s map[string]map[string]any) { s["fx_bbbb"]["command"] = "/usr/bin/other" },
		"changed-arguments":   func(s map[string]map[string]any) { s["fx_bbbb"]["args"] = []any{"x"} },
		"changed-cwd":         func(s map[string]map[string]any) { s["fx_bbbb"]["cwd"] = "/elsewhere" },
		"layered-env":         func(s map[string]map[string]any) { s["fx_bbbb"]["env"] = map[string]any{"EVIL": "1"} },
		"extra-env-var":       func(s map[string]map[string]any) { s["fx_bbbb"]["env_vars"] = []any{"FIXTURE_TOKEN", "AWS_SECRET"} },
		"changed-endpoint":    func(s map[string]map[string]any) { s["web_aaaa"]["url"] = "https://evil.example.test/" },
		"layered-headers":     func(s map[string]map[string]any) { s["web_aaaa"]["http_headers"] = map[string]any{"X": "y"} },
		"layered-oauth":       func(s map[string]map[string]any) { s["web_aaaa"]["oauth"] = map[string]any{"client_id": "x"} },
		"layered-tool-policy": func(s map[string]map[string]any) { s["web_aaaa"]["default_tools_approval_mode"] = "approve" },
		"disabled":            func(s map[string]map[string]any) { s["web_aaaa"]["enabled"] = false },
		"header-redirected": func(s map[string]map[string]any) {
			s["web_aaaa"]["env_http_headers"] = map[string]any{"X-Api-Key": "HOME"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := c.validateMCPConfig(mutate(edit)); err == nil || domain.SafeError(err).Code != domain.Unsupported {
				t.Fatalf("drifted native configuration accepted: %v", err)
			}
		})
	}
}

func TestManagedMCPStatusMatchingIsTypedAndExact(t *testing.T) {
	c, _ := mcpClient(t, mcpStdioServer("fx_bbbb"), mcpHTTPServer("web_aaaa"))
	status := func(name, runtime string) mcpStatusEntry {
		entry := mcpStatusEntry{Name: name, RuntimeStatus: &runtime}
		if name == "web_aaaa" {
			origin := "https://mcp.example.test"
			entry.HTTPOrigin = &origin
		}
		return entry
	}
	outcomes, pending, err := c.matchMCPInventory([]mcpStatusEntry{status("web_aaaa", "connected"), status("fx_bbbb", "connected")})
	if err != nil || pending || len(outcomes) != 2 || outcomes[0].State != domain.MCPStartupReady || outcomes[1].State != domain.MCPStartupReady {
		t.Fatalf("ready inventory rejected: %v %v", outcomes, err)
	}
	for runtime, want := range map[string]domain.MCPStartupOutcome{"failed": domain.MCPStartupFailed, "authenticationRequired": domain.MCPStartupAuthenticationRequired, "cancelled": domain.MCPStartupCancelled, "disabled": domain.MCPStartupFailed} {
		outcomes, pending, err = c.matchMCPInventory([]mcpStatusEntry{status("web_aaaa", runtime), status("fx_bbbb", "connected")})
		if err != nil || pending || outcomes[1].State != want || outcomes[0].State != domain.MCPStartupReady {
			t.Fatalf("%s mapped to %v (%v)", runtime, outcomes, err)
		}
	}
	if _, pending, _ = c.matchMCPInventory([]mcpStatusEntry{status("web_aaaa", "starting"), status("fx_bbbb", "connected")}); !pending {
		t.Fatal("starting server was treated as settled")
	}
	plugin := "plugin"
	foreign := status("fx_bbbb", "connected")
	foreign.PluginID = &plugin
	wrongOrigin := status("web_aaaa", "connected")
	other := "https://other.example.test"
	wrongOrigin.HTTPOrigin = &other
	stdioWithOrigin := status("fx_bbbb", "connected")
	stdioWithOrigin.HTTPOrigin = &other
	for name, entries := range map[string][]mcpStatusEntry{
		"extra-server":      {status("web_aaaa", "connected"), status("fx_bbbb", "connected"), status("unselected_ffff", "connected")},
		"missing-server":    {status("web_aaaa", "connected")},
		"plugin-server":     {status("web_aaaa", "connected"), foreign},
		"wrong-origin":      {wrongOrigin, status("fx_bbbb", "connected")},
		"stdio-http-origin": {status("web_aaaa", "connected"), stdioWithOrigin},
		"duplicate":         {status("web_aaaa", "connected"), status("web_aaaa", "connected"), status("fx_bbbb", "connected")},
		"built-in":          {status("web_aaaa", "connected"), status("fx_bbbb", "connected"), status("codex_apps", "connected")},
	} {
		if _, _, err := c.matchMCPInventory(entries); err == nil {
			t.Fatalf("%s inventory accepted", name)
		}
	}
	c.managedHome = "/managed"
	if _, _, err := c.matchMCPInventory([]mcpStatusEntry{status("web_aaaa", "connected"), status("fx_bbbb", "connected"), status("codex_apps", "connected")}); err != nil {
		t.Fatalf("managed ChatGPT connector must keep its separate boundary: %v", err)
	}
}

func mcpEventClient(t *testing.T) (*Client, domain.ID, ManagedMCPServer) {
	t.Helper()
	server := mcpStdioServer("fx_bbbb")
	c, _ := mcpClient(t, server)
	observed, turn := observationClient()
	c.version, c.thread, c.execution = observed.version, observed.thread, observed.execution
	return c, turn, server
}

func mcpItem(id, server, tool, status string, extra map[string]any) map[string]any {
	item := map[string]any{"type": "mcpToolCall", "id": id, "server": server, "tool": tool, "status": status, "arguments": map[string]any{"secret": "never-published"}}
	for key, value := range extra {
		item[key] = value
	}
	return item
}

func TestManagedMCPToolCallsKeepOriginalServerIdentityAndNeverPublishArguments(t *testing.T) {
	c, turn, server := mcpEventClient(t)
	started, err := observeFixture(c, "item/started", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 1, "item": mcpItem("call-1", "fx_bbbb", "write", "inProgress", map[string]any{"readOnlyHint": false})})
	if err != nil || started.Kind != ToolStartedEvent || started.Tool == nil || started.Tool.Kind != MCPToolKind || started.Tool.MCP.Identity != server.Identity || started.Tool.MCP.Tool != "write" || started.Tool.MCP.ReadOnlyHint == nil || *started.Tool.MCP.ReadOnlyHint {
		t.Fatalf("started call lost identity: %+v %v", started, err)
	}
	raw, _ := json.Marshal(started)
	if strings.Contains(string(raw), "never-published") {
		t.Fatal("call arguments escaped into the observation")
	}
	done, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 2, "item": mcpItem("call-1", "fx_bbbb", "write", "completed", map[string]any{"durationMs": 7, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "first"}, map[string]any{"type": "image", "data": "x"}, map[string]any{"type": "text", "text": "second"}}}})})
	if err != nil || done.Kind != ToolCompletedEvent || done.Tool.Status != ToolCompleted || *done.Tool.MCP.ResultText != "first\nsecond" || done.Tool.MCP.ContentBlocks != 3 || *done.Tool.MCP.DurationMS != 7 || done.Tool.MCP.Error != nil {
		t.Fatalf("completed call result changed: %+v %v", done.Tool, err)
	}
	if _, err = observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 3, "item": mcpItem("call-1", "fx_bbbb", "write", "completed", nil)}); err == nil {
		t.Fatal("a call completed twice")
	}
}

func TestManagedMCPFailedCallCarriesTypedErrorAndNoReplayAuthority(t *testing.T) {
	c, turn, _ := mcpEventClient(t)
	if _, err := observeFixture(c, "item/started", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 1, "item": mcpItem("call-2", "fx_bbbb", "write", "inProgress", nil)}); err != nil {
		t.Fatal(err)
	}
	failed, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 2, "item": mcpItem("call-2", "fx_bbbb", "write", "failed", map[string]any{"error": map[string]any{"message": "user cancelled MCP tool call"}})})
	if err != nil || failed.Tool.Status != ToolFailed || failed.Tool.MCP.Error == nil || *failed.Tool.MCP.Error != "user cancelled MCP tool call" {
		t.Fatalf("failed call mapped incorrectly: %+v %v", failed.Tool, err)
	}
	if len(c.UncertainMCPCalls()) != 0 {
		t.Fatal("a definitively failed call became uncertain")
	}
}

func TestManagedMCPUnfinishedCallBecomesUncertainAndIsNeverReplayed(t *testing.T) {
	c, turn, server := mcpEventClient(t)
	if _, err := observeFixture(c, "item/started", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 1, "item": mcpItem("call-3", "fx_bbbb", "write", "inProgress", nil)}); err != nil {
		t.Fatal(err)
	}
	c.markUncertainMCPCallsLocked(turn)
	uncertain := c.UncertainMCPCalls()
	if len(uncertain) != 1 || uncertain[0] != server.Identity {
		t.Fatalf("unfinished call is not reported as uncertain: %v", uncertain)
	}
	// The adapter has no operation that replays a tool call; a later native
	// completion for the abandoned item is a validation failure, not a retry.
	if _, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": domain.NewID(), "completedAtMs": 3, "item": mcpItem("call-3", "fx_bbbb", "write", "completed", nil)}); err == nil {
		t.Fatal("completion from another turn adopted an uncertain call")
	}
}

func TestManagedMCPForeignAndMalformedCallsAreNotAttributed(t *testing.T) {
	c, turn, _ := mcpEventClient(t)
	event, err := observeFixture(c, "item/started", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 1, "item": mcpItem("foreign", "codex_apps", "tool", "inProgress", nil)})
	if err != nil || event.Kind != NativeExtensionEvent || event.Tool != nil {
		t.Fatalf("foreign server call was attributed: %+v %v", event, err)
	}
	event, err = observeFixture(c, "item/started", map[string]any{"threadId": domain.NewID(), "turnId": turn, "startedAtMs": 1, "item": mcpItem("other-thread", "fx_bbbb", "write", "inProgress", nil)})
	if err != nil || event.Kind != NativeExtensionEvent {
		t.Fatalf("foreign thread call was attributed: %+v %v", event, err)
	}
	for name, item := range map[string]map[string]any{
		"unknown-status":  mcpItem("a", "fx_bbbb", "write", "mystery", nil),
		"plugin-owned":    mcpItem("b", "fx_bbbb", "write", "inProgress", map[string]any{"pluginId": "p"}),
		"completed-first": mcpItem("c", "fx_bbbb", "write", "completed", nil),
		"empty-tool":      mcpItem("d", "fx_bbbb", " ", "inProgress", nil),
	} {
		if _, err := observeFixture(c, "item/started", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 1, "item": item}); err == nil {
			t.Fatalf("%s call accepted", name)
		}
	}
	if _, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": mcpItem("never-started", "fx_bbbb", "write", "completed", nil)}); err == nil {
		t.Fatal("completion without a start was accepted")
	}
}

func TestManagedMCPStartupNotificationsBindToSelectedGenerations(t *testing.T) {
	c, _, server := mcpEventClient(t)
	for status, want := range map[string]domain.MCPStartupOutcome{"starting": domain.MCPStartupPending, "ready": domain.MCPStartupReady, "cancelled": domain.MCPStartupCancelled} {
		event, err := observeFixture(c, "mcpServer/startupStatus/updated", map[string]any{"threadId": c.thread, "name": "fx_bbbb", "status": status, "error": nil, "failureReason": nil})
		if err != nil || event.Kind != MetadataEvent || event.Metadata != MetadataMCPStartup || event.MCPStartup == nil || event.MCPStartup.Identity != server.Identity || event.MCPStartup.State != want {
			t.Fatalf("%s startup mapped to %+v (%v)", status, event, err)
		}
	}
	event, err := observeFixture(c, "mcpServer/startupStatus/updated", map[string]any{"threadId": c.thread, "name": "fx_bbbb", "status": "failed", "error": "protected diagnostic", "failureReason": "reauthenticationRequired"})
	raw, _ := json.Marshal(event)
	if err != nil || event.MCPStartup.State != domain.MCPStartupAuthenticationRequired || strings.Contains(string(raw), "protected diagnostic") {
		t.Fatalf("authentication-required startup lost its classification or leaked diagnostics: %v", err)
	}
	event, err = observeFixture(c, "mcpServer/startupStatus/updated", map[string]any{"threadId": c.thread, "name": "fx_bbbb", "status": "failed", "error": "boom", "failureReason": nil})
	if err != nil || event.MCPStartup.State != domain.MCPStartupFailed || c.ManagedMCPOutcomes()[0].State != domain.MCPStartupFailed {
		t.Fatalf("failed startup not retained: %+v %v", event, err)
	}
	if _, err = observeFixture(c, "mcpServer/startupStatus/updated", map[string]any{"threadId": c.thread, "name": "unselected_ffff", "status": "ready", "error": nil, "failureReason": nil}); err == nil {
		t.Fatal("an unselected native server was accepted as ordinary startup")
	}
	event, err = observeFixture(c, "mcpServer/startupStatus/updated", map[string]any{"threadId": domain.NewID(), "name": "unselected_ffff", "status": "ready", "error": nil, "failureReason": nil})
	if err != nil || event.Kind != NativeExtensionEvent {
		t.Fatalf("a foreign thread's startup was not private: %+v %v", event, err)
	}
}

func elicitationParams(c *Client, turn domain.ID, server string, schema map[string]any) map[string]any {
	return map[string]any{"threadId": c.thread, "turnId": turn, "serverName": server, "mode": "form", "message": "Allow write?", "requestedSchema": schema}
}

func TestManagedMCPElicitationMapsToOnceOnlyQuestionAndExactNativeResponse(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"note":   map[string]any{"type": "string", "maxLength": 5},
		"count":  map[string]any{"type": "integer", "minimum": 1, "maximum": 10},
		"ratio":  map[string]any{"type": "number"},
		"force":  map[string]any{"type": "boolean"},
		"mode":   map[string]any{"type": "string", "enum": []any{"fast", "slow"}},
		"labels": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "enum": []any{"a", "b"}}},
	}, "required": []any{"note", "mode"}}
	form, ok := decodeElicitationSchema("form", mcpJSON(t, schema))
	if !ok || len(form.Fields) != 6 {
		t.Fatalf("form rejected: %v", ok)
	}
	questions := buildElicitationQuestions("fx_bbbb", "Allow write?", form)
	if questions[0].ID != "action" || len(questions[0].Options) != 3 || questions[0].Text != "Allow write?" || len(questions) != 7 {
		t.Fatalf("questions changed: %+v", questions)
	}
	if (&QuestionRequest{Questions: nil}) == nil || (domain.QuestionRequest{Questions: toDomainQuestions(questions)}).Validate() != nil {
		t.Fatal("generated questions are not valid product questions")
	}
	accept := QuestionAnswers{Answers: map[string][]string{"action": {"accept"}, "field:note": {"hello"}, "field:count": {"3"}, "field:ratio": {"2.5"}, "field:force": {"true"}, "field:mode": {"fast"}, "field:labels": {"a", "b"}}}
	response, err := validateElicitationAnswers(form, accept)
	if err != nil || response.Action != "accept" || response.Content["note"] != "hello" || response.Content["count"] != int64(3) || response.Content["ratio"] != 2.5 || response.Content["force"] != true || response.Content["mode"] != "fast" || len(response.Content["labels"].([]string)) != 2 {
		t.Fatalf("accepted content changed: %+v %v", response, err)
	}
	optional := QuestionAnswers{Answers: map[string][]string{"action": {"accept"}, "field:note": {"x"}, "field:count": {}, "field:ratio": {}, "field:force": {}, "field:mode": {"slow"}, "field:labels": {}}}
	if response, err = validateElicitationAnswers(form, optional); err != nil || len(response.Content) != 2 {
		t.Fatalf("optional fields must be omitted: %+v %v", response, err)
	}
	for name, mutate := range map[string]func(map[string][]string){
		"missing-required": func(a map[string][]string) { a["field:note"] = []string{} },
		"too-long":         func(a map[string][]string) { a["field:note"] = []string{"toolong"} },
		"below-minimum":    func(a map[string][]string) { a["field:count"] = []string{"0"} },
		"fractional":       func(a map[string][]string) { a["field:count"] = []string{"2.5"} },
		"unknown-enum":     func(a map[string][]string) { a["field:mode"] = []string{"warp"} },
		"duplicate-multi":  func(a map[string][]string) { a["field:labels"] = []string{"a", "a"} },
		"bad-bool":         func(a map[string][]string) { a["field:force"] = []string{"yes"} },
		"extra-question":   func(a map[string][]string) { a["field:ghost"] = []string{"x"} },
		"unknown-action":   func(a map[string][]string) { a["action"] = []string{"approve"} },
		"two-actions":      func(a map[string][]string) { a["action"] = []string{"accept", "decline"} },
	} {
		t.Run(name, func(t *testing.T) {
			answers := QuestionAnswers{Answers: map[string][]string{}}
			for key, value := range accept.Answers {
				answers.Answers[key] = slices.Clone(value)
			}
			mutate(answers.Answers)
			if _, err := validateElicitationAnswers(form, answers); err == nil {
				t.Fatal("invalid elicitation answer accepted")
			}
		})
	}
	// A refusal carries no content, and rejects values that could be misread as consent.
	for _, action := range []string{"decline", "cancel"} {
		refusal := QuestionAnswers{Answers: map[string][]string{"action": {action}}}
		for _, field := range form.Fields {
			refusal.Answers[field.QuestionID] = []string{}
		}
		response, err := validateElicitationAnswers(form, refusal)
		if err != nil || response.Action != action || response.Content != nil {
			t.Fatalf("%s must send no content: %+v %v", action, response, err)
		}
		raw, _ := json.Marshal(response)
		if !strings.Contains(string(raw), `"content":null`) {
			t.Fatalf("%s native shape: %s", action, raw)
		}
		refusal.Answers["field:note"] = []string{"leaked"}
		if _, err := validateElicitationAnswers(form, refusal); err == nil {
			t.Fatalf("%s accepted accompanying values", action)
		}
	}
}

func TestManagedMCPElicitationRejectsUnpresentableSchemas(t *testing.T) {
	for name, schema := range map[string]map[string]any{
		"object-property": {"type": "object", "properties": map[string]any{"x": map[string]any{"type": "object"}}},
		"array-no-items":  {"type": "object", "properties": map[string]any{"x": map[string]any{"type": "array"}}},
		"duplicate-enum":  {"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string", "enum": []any{"a", "a"}}}},
		"required-ghost":  {"type": "object", "properties": map[string]any{}, "required": []any{"ghost"}},
		"not-object":      {"type": "string"},
	} {
		if _, ok := decodeElicitationSchema("form", mcpJSON(t, schema)); ok {
			t.Fatalf("%s schema accepted", name)
		}
	}
	for _, mode := range []string{"url", "openai/form", "openaiForm", ""} {
		if _, ok := decodeElicitationSchema(mode, mcpJSON(t, map[string]any{"type": "object", "properties": map[string]any{}})); ok {
			t.Fatalf("mode %q accepted", mode)
		}
	}
}

func mcpJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func toDomainQuestions(questions []Question) []domain.Question {
	result := make([]domain.Question, 0, len(questions))
	for _, q := range questions {
		d := domain.Question{ID: q.ID, Header: q.Header, Text: q.Text, Other: q.Other, Secret: q.Secret}
		for _, o := range q.Options {
			d.Options = append(d.Options, domain.QuestionOption{Label: o.Label, Description: o.Description})
		}
		result = append(result, d)
	}
	return result
}

// The fixture app-server reflects launch arguments, so these tests exercise the
// real configuration the adapter produced rather than a hand-written expectation.
func (f *threadFixture) handleMCP(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if !strings.HasPrefix(f.mode, "thread-turn-mcp") {
		return false
	}
	capture := func(value any) {
		if file := os.Getenv("DELIDEV_CODEX_CAPTURE"); file != "" {
			out, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				os.Exit(60)
			}
			_ = json.NewEncoder(out).Encode(value)
			_ = out.Close()
		}
	}
	names := []string{}
	servers := map[string]map[string]any{}
	for i := 0; i+1 < len(os.Args); i++ {
		if os.Args[i] == "-c" && strings.HasPrefix(os.Args[i+1], "mcp_servers.") {
			key, value, _ := strings.Cut(strings.TrimPrefix(os.Args[i+1], "mcp_servers."), "=")
			var parsed map[string]any
			if toml.Unmarshal([]byte("v = "+value), &parsed) != nil {
				os.Exit(61)
			}
			fields := parsed["v"].(map[string]any)
			fields["environment_id"], fields["enabled"], fields["tool_timeout_sec"] = "local", true, nil
			servers[key] = fields
			names = append(names, key)
		}
	}
	switch method {
	case "config/read":
		capture(map[string]any{"method": method})
		config := map[string]any{}
		for key, value := range servers {
			config[key] = value
		}
		switch f.mode {
		case "thread-turn-mcp-extra-config":
			config["project_eeee"] = map[string]any{"command": "/bin/evil", "enabled": true}
		case "thread-turn-mcp-override":
			for _, value := range config {
				value.(map[string]any)["env"] = map[string]any{"EVIL": "1"}
			}
		}
		top := map[string]any{"mcp_servers": config}
		if f.mode == "thread-turn-mcp-plugins" {
			top["plugins"] = map[string]any{"vendor": map[string]any{"enabled": true}}
		}
		write(id, map[string]any{"config": top, "origins": nil, "layers": nil})
	case "mcpServerStatus/list":
		var params struct {
			ThreadID   string `json:"threadId"`
			ServerName string `json:"serverName"`
		}
		_ = json.Unmarshal(raw, &params)
		capture(map[string]any{"method": method, "threadId": params.ThreadID, "serverName": params.ServerName})
		if params.ThreadID == "" || params.ServerName == "" {
			// An unscoped listing would launch a second copy of every server.
			os.Exit(63)
		}
		f.mcpListCalls++
		entries := []any{}
		for _, name := range []string{params.ServerName} {
			state := "connected"
			switch {
			case f.mode == "thread-turn-mcp-failed" && !f.mcpReloaded && name == names[0]:
				state = "failed"
			case f.mode == "thread-turn-mcp-auth" && !f.mcpReloaded && name == names[0]:
				state = "authenticationRequired"
			case f.mode == "thread-turn-mcp-pending" && f.mcpListCalls < 4:
				state = "starting"
			case f.mode == "thread-turn-mcp-stuck":
				state = "starting"
			}
			entry := map[string]any{"name": name, "runtimeStatus": state, "pluginId": nil, "httpOrigin": nil, "serverInfo": nil, "serverCapabilities": nil, "tools": map[string]any{}, "toolsError": nil, "resources": []any{}, "resourceTemplates": []any{}, "authStatus": "unsupported"}
			if url, ok := servers[name]["url"].(string); ok {
				origin := strings.SplitN(url, "/", 4)
				entry["httpOrigin"] = strings.Join(origin[:3], "/")
			}
			entries = append(entries, entry)
		}
		if f.mode == "thread-turn-mcp-plugin-status" {
			entries[0].(map[string]any)["pluginId"] = "plugin"
		}
		write(id, map[string]any{"data": entries, "nextCursor": nil})
	case "config/mcpServer/reload":
		capture(map[string]any{"method": method})
		f.mcpReloaded = true
		write(id, map[string]any{})
	case "fixture/elicitation":
		var params map[string]any
		_ = json.Unmarshal(raw, &params)
		fields := params["params"].(map[string]any)
		fields["threadId"] = f.thread["id"]
		if fields["turnId"] == "active" {
			fields["turnId"] = f.turn
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": params["requestId"], "method": "mcpServer/elicitation/request", "params": fields})
		write(id, map[string]any{})
		if f.mode == "thread-turn-mcp-blocked" {
			// The owned fixture stops reading its pipe so a large answer is
			// canceled after native delivery becomes uncertain.
			time.Sleep(30 * time.Second)
		}
	default:
		return false
	}
	return true
}

// mcpReply records the exact native response to an elicitation and resolves it.
func (f *threadFixture) mcpReply(id, raw json.RawMessage, errored bool) {
	if file := os.Getenv("DELIDEV_CODEX_CAPTURE"); file != "" {
		out, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(62)
		}
		_ = json.NewEncoder(out).Encode(map[string]any{"method": "reply", "id": id, "result": json.RawMessage(raw)})
		_ = out.Close()
	}
	if f.mode != "thread-turn-mcp-unresolved" {
		f.notify("serverRequest/resolved", map[string]any{"threadId": f.thread["id"], "requestId": id})
	}
}

func openMCPFixture(t *testing.T, mode string, servers ...ManagedMCPServer) (*Client, string, ThreadSettings) {
	t.Helper()
	config := fixtureConfig(t, "thread-turn-"+mode)
	config.Mode = ThreadProtocol
	config.ManagedMCP = servers
	capture := filepath.Join(t.TempDir(), "requests.jsonl")
	config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_CAPTURE="+capture)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	client, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	settings := threadSettings(t)
	if _, err := client.StartThread(ctx, domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	return client, capture, settings
}

// waitCapture polls the fixture's request log: the fixture writes a native
// reply asynchronously after the adapter's pipe write returns.
func waitCapture(t *testing.T, capture, want string) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, _ := os.ReadFile(capture)
		if strings.Contains(string(raw), want) || time.Now().After(deadline) {
			return raw
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func captureMethods(t *testing.T, capture string) []string {
	t.Helper()
	raw, err := os.ReadFile(capture)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var row struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(line), &row)
		methods = append(methods, row.Method)
	}
	return methods
}

func TestManagedMCPInventoryIsVerifiedBeforeAnyInput(t *testing.T) {
	servers := []ManagedMCPServer{mcpStdioServer("fx_bbbb"), mcpHTTPServer("web_aaaa")}
	client, capture, _ := openMCPFixture(t, "mcp", servers...)
	if _, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode)); err != nil {
		t.Fatal(err)
	}
	methods := captureMethods(t, capture)
	configAt, statusAt, turnAt := slices.Index(methods, "config/read"), slices.Index(methods, "mcpServerStatus/list"), slices.Index(methods, "turn/start")
	if configAt < 0 || statusAt < configAt || turnAt < statusAt {
		t.Fatalf("input preceded exact inventory verification: %v", methods)
	}
	for _, outcome := range client.ManagedMCPOutcomes() {
		if outcome.State != domain.MCPStartupReady {
			t.Fatalf("outcome %v", outcome)
		}
	}
}

func TestManagedMCPDriftOrFailureBlocksInputWithTypedOutcomes(t *testing.T) {
	servers := func() []ManagedMCPServer {
		return []ManagedMCPServer{mcpStdioServer("fx_bbbb"), mcpHTTPServer("web_aaaa")}
	}
	for mode, want := range map[string]domain.Code{"mcp-extra-config": domain.Unsupported, "mcp-override": domain.Unsupported, "mcp-plugin-status": domain.Unsupported, "mcp-plugins": domain.Unsupported} {
		t.Run(mode, func(t *testing.T) {
			client, capture, _ := openMCPFixture(t, mode, servers()...)
			_, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			assertCode(t, err, want)
			if slices.Contains(captureMethods(t, capture), "turn/start") {
				t.Fatal("input was sent after inventory drift")
			}
		})
	}
	for mode, state := range map[string]domain.MCPStartupOutcome{"mcp-failed": domain.MCPStartupFailed, "mcp-auth": domain.MCPStartupAuthenticationRequired} {
		t.Run(mode, func(t *testing.T) {
			list := servers()
			client, capture, _ := openMCPFixture(t, mode, list...)
			_, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			if domain.SafeError(err).Code != domain.Unavailable {
				t.Fatalf("startup failure was not typed: %v", err)
			}
			states := map[string]domain.MCPStartupOutcome{}
			for _, outcome := range client.ManagedMCPOutcomes() {
				states[outcome.Identity.Server] = outcome.State
			}
			if states["fx_bbbb"] == "" || len(states) != 2 {
				t.Fatalf("outcomes lost server identity: %v", states)
			}
			first := client.managedMCP[0].name
			if states[first] != state {
				t.Fatalf("%s mapped to %v", mode, states)
			}
			if slices.Contains(captureMethods(t, capture), "turn/start") {
				t.Fatal("input was sent while a selected server was down")
			}
			// Reconnect is limited to an originally selected definition and never resends work.
			if _, err := client.ReconnectManagedMCP(context.Background(), domain.NewID()); err == nil || domain.SafeError(err).Code != domain.Conflict {
				t.Fatalf("foreign definition reconnect accepted: %v", err)
			}
			outcomes, err := client.ReconnectManagedMCP(context.Background(), client.managedMCP[0].server.Identity.DefinitionID)
			if err != nil || len(outcomes) != 2 {
				t.Fatalf("original server did not reconnect: %v", err)
			}
			methods := captureMethods(t, capture)
			if !slices.Contains(methods, "config/mcpServer/reload") || slices.Contains(methods, "turn/start") {
				t.Fatalf("reconnect resent work or skipped reload: %v", methods)
			}
			if _, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode)); err != nil {
				t.Fatalf("input still blocked after reconnect: %v", err)
			}
		})
	}
}

func TestManagedMCPStartingServersAreAwaitedAndStuckServersTimeOut(t *testing.T) {
	client, _, _ := openMCPFixture(t, "mcp-pending", mcpStdioServer("fx_bbbb"))
	if _, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode)); err != nil {
		t.Fatalf("a server that finishes starting must be accepted: %v", err)
	}
	stuck, _, _ := openMCPFixture(t, "mcp-stuck", mcpStdioServer("fx_bbbb"))
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	if _, err := stuck.StartTurn(ctx, domain.NewID(), domain.NewID(), input(domain.ExecuteMode)); err == nil {
		t.Fatal("a server that never started was accepted")
	}
}

func TestManagedMCPReconnectRefusesBusyOrUnmanagedExecutions(t *testing.T) {
	client, _, _ := openMCPFixture(t, "mcp", mcpStdioServer("fx_bbbb"))
	id := client.managedMCP[0].server.Identity.DefinitionID
	if _, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReconnectManagedMCP(context.Background(), id); err == nil || domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("reconnect during an active turn: %v", err)
	}
	plain, _, _ := openMCPFixture(t, "mcp")
	if _, err := plain.ReconnectManagedMCP(context.Background(), id); err == nil {
		t.Fatal("reconnect without any managed selection")
	}
}

func TestManagedMCPElicitationResponseIsSentOnceAndAcceptedOnNativeResolution(t *testing.T) {
	client, capture, _ := openMCPFixture(t, "mcp", mcpStdioServer("fx_bbbb"))
	turn, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, client, "elicitation", map[string]any{"requestId": 41, "params": map[string]any{"turnId": "active", "serverName": "fx_bbbb", "mode": "form", "message": "Write the file?", "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []any{"path"}}}})
	requested := nextKind(t, client, InteractionRequestedEvent)
	if requested.Interaction == nil || requested.Interaction.NativeID.Kind != NumberRequestID || *requested.Interaction.NativeID.Number != 41 || requested.Interaction.Questions.Questions[0].Text != "Write the file?" || !strings.HasPrefix(requested.ItemID, "mcp-elicitation/") {
		t.Fatalf("elicitation lost its request identity: %+v", requested)
	}
	id := requested.Interaction.ID
	response := domain.NewID()
	answers := QuestionAnswers{Answers: map[string][]string{"action": {"accept"}, "field:path": {"/tmp/out.txt"}}}
	status, err := client.AnswerQuestions(context.Background(), response, id, turn.TurnID, answers)
	if err != nil || status.Delivery != QuestionTransmitted || !status.Elicitation {
		t.Fatalf("answer was not transmitted: %+v %v", status, err)
	}
	if _, err = client.AnswerQuestions(context.Background(), domain.NewID(), id, turn.TurnID, answers); err == nil || domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("a second response was sent: %v", err)
	}
	if _, err = client.AnswerQuestions(context.Background(), response, id, turn.TurnID, answers); err == nil {
		t.Fatal("the original response identity was replayed")
	}
	closed := nextKind(t, client, InteractionClosedEvent)
	accepted := nextKind(t, client, QuestionAcceptedEvent)
	if closed.InteractionState.ID != id || accepted.InteractionState.ID != id || !accepted.InteractionState.Accepted || !accepted.InteractionState.Elicitation || accepted.InteractionState.ResponseID != response {
		t.Fatalf("native resolution did not accept the original response: %+v %+v", closed.InteractionState, accepted.InteractionState)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	replies := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var row struct {
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
			Result struct {
				Action  string         `json:"action"`
				Content map[string]any `json:"content"`
			} `json:"result"`
		}
		_ = json.Unmarshal([]byte(line), &row)
		if row.Method == "reply" {
			replies++
			if string(row.ID) != "41" || row.Result.Action != "accept" || row.Result.Content["path"] != "/tmp/out.txt" {
				t.Fatalf("native response changed: %s", line)
			}
		}
	}
	if replies != 1 {
		t.Fatalf("native received %d responses", replies)
	}
}

func TestManagedMCPDeniedElicitationSendsNoContent(t *testing.T) {
	client, capture, _ := openMCPFixture(t, "mcp", mcpStdioServer("fx_bbbb"))
	turn, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, client, "elicitation", map[string]any{"requestId": "write-approval", "params": map[string]any{"turnId": "active", "serverName": "fx_bbbb", "mode": "form", "message": "Delete the repository?", "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{"confirm": map[string]any{"type": "boolean"}}}}})
	requested := nextKind(t, client, InteractionRequestedEvent)
	answers := QuestionAnswers{Answers: map[string][]string{"action": {"decline"}, "field:confirm": {}}}
	if _, err := client.AnswerQuestions(context.Background(), domain.NewID(), requested.Interaction.ID, turn.TurnID, answers); err != nil {
		t.Fatal(err)
	}
	nextKind(t, client, QuestionAcceptedEvent)
	raw := waitCapture(t, capture, `"result":{"action":"decline","content":null}`)
	if !strings.Contains(string(raw), `"result":{"action":"decline","content":null}`) {
		t.Fatalf("a denied request must carry no content: %s", raw)
	}
}

func TestManagedMCPElicitationFromUnselectedOrUnpresentableSourcesNeverReachesTheOwner(t *testing.T) {
	client, capture, _ := openMCPFixture(t, "mcp", mcpStdioServer("fx_bbbb"))
	if _, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode)); err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, client, "elicitation", map[string]any{"requestId": 50, "params": map[string]any{"turnId": "active", "serverName": "codex_apps", "mode": "form", "message": "foreign", "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{}}}})
	if event := nextKind(t, client, NativeExtensionEvent); event.Interaction != nil {
		t.Fatal("foreign elicitation became a product interaction")
	}
	fixtureSignal(t, client, "elicitation", map[string]any{"requestId": 51, "params": map[string]any{"turnId": "active", "serverName": "fx_bbbb", "mode": "url", "message": "open a url", "elicitationId": "e", "url": "https://example.test"}})
	event := nextKind(t, client, MetadataEvent)
	if event.Metadata != MetadataMCPElicitationRefused {
		t.Fatalf("unsupported mode metadata: %v", event.Metadata)
	}
	raw := waitCapture(t, capture, `"result":{"action":"cancel","content":null}`)
	if !strings.Contains(string(raw), `"id":51`) {
		t.Fatalf("unsupported request was not cancelled exactly once: %s", raw)
	}
	if strings.Count(string(raw), `"id":50`) != 0 {
		t.Fatal("foreign request received a response")
	}
}

func processConfigFixture() process.Config {
	return process.Config{Env: []string{"HOME=/tmp"}}
}

func TestManagedMCPVerificationBeforeInputReservesNoInputAndReportsOutcomes(t *testing.T) {
	client, capture, _ := openMCPFixture(t, "mcp-failed", mcpStdioServer("fx_bbbb"), mcpHTTPServer("web_aaaa"))
	outcomes, err := client.VerifyManagedMCP(context.Background())
	assertCode(t, err, domain.Unavailable)
	if len(outcomes) != 2 || outcomes[0].State != domain.MCPStartupFailed || outcomes[1].State != domain.MCPStartupReady {
		t.Fatalf("typed outcomes %v", outcomes)
	}
	if _, err := client.ReconnectManagedMCP(context.Background(), outcomes[0].Identity.DefinitionID); err != nil {
		t.Fatal(err)
	}
	// The same original input identities are still available: verification
	// before input reserved nothing, so no accepted work was replayed or lost.
	inputID, requestID := domain.NewID(), domain.NewID()
	if _, err := client.StartTurn(context.Background(), requestID, inputID, input(domain.ExecuteMode)); err != nil {
		t.Fatal(err)
	}
	methods := captureMethods(t, capture)
	if strings.Count(strings.Join(methods, ","), "turn/start") != 1 {
		t.Fatalf("native received %v", methods)
	}
	plain, _, _ := openMCPFixture(t, "mcp")
	if _, err := plain.VerifyManagedMCP(context.Background()); err == nil {
		t.Fatal("verification without a selection must not succeed vacuously")
	}
}

func TestManagedMCPElicitationResolutionAfterInterruptionIsNotAcceptance(t *testing.T) {
	client, _, _ := openMCPFixture(t, "mcp", mcpStdioServer("fx_bbbb"))
	turn, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, client, "elicitation", map[string]any{"requestId": 77, "params": map[string]any{"turnId": "active", "serverName": "fx_bbbb", "mode": "form", "message": "Write?", "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{}}}})
	requested := nextKind(t, client, InteractionRequestedEvent)
	answers := QuestionAnswers{Answers: map[string][]string{"action": {"accept"}}}
	if _, err := client.AnswerQuestions(context.Background(), domain.NewID(), requested.Interaction.ID, turn.TurnID, answers); err != nil {
		t.Fatal(err)
	}
	// The turn was interrupted before the native resolution was observed. An
	// interruption also resolves pending requests, so resolution is no longer
	// proof that the owner's transmitted response was consumed.
	client.execution.interrupt = domain.NewID()
	closed := nextKind(t, client, InteractionClosedEvent)
	if closed.InteractionState.Accepted || len(client.queuedEvents) != 0 {
		t.Fatalf("an interrupted resolution was published as acceptance: %+v", closed.InteractionState)
	}
	status, err := client.InspectInteraction(context.Background(), requested.Interaction.ID)
	if err != nil || status.Accepted || status.Delivery != QuestionTransmitted {
		t.Fatalf("response ownership changed: %+v %v", status, err)
	}
}

func TestManagedMCPSecretsJoinTheAPIShellEnvironmentExclusion(t *testing.T) {
	config := mcpConfig(mcpStdioServer("fx_bbbb"), mcpHTTPServer("web_aaaa"))
	config.API = &APIConfig{ServerOrigin: "http://127.0.0.1:1", Token: apiFixtureToken()}
	if _, err := configureManagedMCP(&config); err != nil {
		t.Fatal(err)
	}
	if _, err := configureAPI(&config); err != nil {
		t.Fatal(err)
	}
	var exclusion []string
	for i := 0; i+1 < len(config.Process.Args); i++ {
		if config.Process.Args[i] == "-c" && strings.HasPrefix(config.Process.Args[i+1], "shell_environment_policy.exclude=") {
			exclusion = nil
			if err := json.Unmarshal([]byte(strings.TrimPrefix(config.Process.Args[i+1], "shell_environment_policy.exclude=")), &exclusion); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, name := range append([]string{executionTokenEnv}, config.mcpExcluded...) {
		if !slices.Contains(exclusion, name) {
			t.Fatalf("native shell tools may inherit %s: %v", name, exclusion)
		}
	}
	if len(exclusion) < 3 {
		t.Fatalf("exclusion lost protected names: %v", exclusion)
	}
}

func TestManagedMCPLostElicitationResponseStaysUncertainAndIsNeverReplayed(t *testing.T) {
	client, _, _ := openMCPFixture(t, "mcp-blocked", mcpStdioServer("fx_bbbb"))
	turn, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, client, "elicitation", map[string]any{"requestId": 9, "params": map[string]any{"turnId": "active", "serverName": "fx_bbbb", "mode": "form", "message": "Write?", "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}}})
	requested := nextKind(t, client, InteractionRequestedEvent)
	response := domain.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status, err := client.AnswerQuestions(ctx, response, requested.Interaction.ID, turn.TurnID, QuestionAnswers{Answers: map[string][]string{"action": {"accept"}, "field:text": {strings.Repeat("x", 200<<10)}}})
	assertCode(t, err, domain.RecoveryRequired)
	if status.ResponseID != response || status.Delivery != QuestionDeliveryUncertain || status.Accepted || !client.execution.paused || !client.execution.interactions.blocksInput() {
		t.Fatalf("lost response lost ownership or unpaused input: %+v", status)
	}
	declined := QuestionAnswers{Answers: map[string][]string{"action": {"decline"}, "field:text": {}}}
	_, err = client.AnswerQuestions(context.Background(), domain.NewID(), requested.Interaction.ID, turn.TurnID, declined)
	assertCode(t, err, domain.RecoveryRequired)
	retained, err := client.InspectInteraction(context.Background(), requested.Interaction.ID)
	if err != nil || retained != status {
		t.Fatal("a replay replaced the uncertain response")
	}
	// A later native resolution cannot turn an uncertain transmission into acceptance.
	if len(client.queuedEvents) != 0 {
		t.Fatal("uncertain delivery queued acceptance")
	}
}

func TestManagedMCPExpectedHTTPOriginMatchesNativeNormalization(t *testing.T) {
	for endpoint, want := range map[string]string{
		"https://MCP.Example.test/stream":       "https://mcp.example.test",
		"https://mcp.example.test:443/stream":   "https://mcp.example.test",
		"http://127.0.0.1:8080/mcp":             "http://127.0.0.1:8080",
		"http://localhost:80/mcp":               "http://localhost",
		"http://[::1]:9000/mcp":                 "http://[::1]:9000",
		"https://mcp.example.test:8443/a?b=c#d": "https://mcp.example.test:8443",
	} {
		if got, ok := httpOrigin(endpoint); !ok || got != want {
			t.Fatalf("%s: %q %v", endpoint, got, ok)
		}
	}
	for _, endpoint := range []string{"ftp://example.test/", "https:///nohost", "::bad"} {
		if _, ok := httpOrigin(endpoint); ok {
			t.Fatalf("%s accepted", endpoint)
		}
	}
}
