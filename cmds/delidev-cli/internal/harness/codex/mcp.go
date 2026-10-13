// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// ManagedMCPServer is one execution-frozen Worker-owned MCP generation. The
// protected values exist only in memory and process environment: never in a
// native argument, log, journal or published observation.
type ManagedMCPServer struct {
	Identity    domain.MCPServerIdentity
	Transport   domain.MCPTransport
	Command     string
	Arguments   []string
	Cwd         string
	Endpoint    string
	Environment map[string]string `json:"-"`
	Headers     map[string]string `json:"-"`
	BearerToken string            `json:"-"`
}

// MCPServerOutcome is the typed native startup classification of a selected server.
type MCPServerOutcome struct {
	Identity domain.MCPServerIdentity
	State    domain.MCPStartupOutcome
}

// mcpBinding is the private expected native configuration of one selection.
type mcpBinding struct {
	server         ManagedMCPServer
	name           string
	environment    []string // names inherited by a stdio child
	headerEnv      map[string]string
	bearerEnv      string
	expectedOrigin string
}

var (
	mcpEnvName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	mcpServerName  = regexp.MustCompile(`^[a-z0-9_]{1,128}$`)
	mcpReservedEnv = []string{"PATH", "HOME", "USER", "SHELL", "TMPDIR", "TEMP", "TMP", "CODEX_", "DELIDEV_", "LD_", "DYLD_", "OPENAI_", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "SSL_", "NODE_OPTIONS", "PYTHONPATH", "XDG_"}
)

func mcpUnavailable() *domain.Error {
	return domain.Fail(domain.Unsupported, "The selected MCP generations could not be loaded into this native profile.", "Keep the selection and use an installed Codex profile that supports managed MCP; no input was sent.")
}

func mcpMismatch(field string) *domain.Error {
	e := mcpUnavailable()
	e.Guidance = "Reconcile the native MCP " + field + " observation with the original selection without changing managed policy; no input was sent."
	return e
}

func tomlString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\u%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func tomlStrings(values []string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = tomlString(v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// httpOrigin is the scheme, lowercase host and non-default port of an endpoint,
// the same normalized origin the native URL layer reports for its configuration.
func httpOrigin(endpoint string) (string, bool) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := parsed.Port()
	if port == "80" && parsed.Scheme == "http" || port == "443" && parsed.Scheme == "https" {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return parsed.Scheme + "://" + host, true
}

// configureManagedMCP adds the selected servers to the launch before the native
// process exists. Secrets travel only through the private process environment
// and are named by reference in native configuration; MCP tables merge across
// layers, so verifyManagedMCP also proves the effective inventory afterwards.
func configureManagedMCP(config *Config) ([]mcpBinding, error) {
	if len(config.ManagedMCP) == 0 {
		return nil, nil
	}
	if config.Mode != ThreadProtocol || config.Sidechat != "" || config.CodeReviewModel != "" || config.ModelObservation || len(config.ManagedMCP) > 64 {
		return nil, mcpUnavailable()
	}
	claimed := map[string]string{}
	// Windows environment names are case-insensitive, so a collision between
	// differently cased names is compared exactly like the host environment.
	claimedFolded := map[string]string{}
	taken := func(name string) bool {
		for _, entry := range config.Process.Env {
			key, _, _ := strings.Cut(entry, "=")
			if strings.EqualFold(key, name) {
				return true
			}
		}
		return false
	}
	servers := slices.Clone(config.ManagedMCP)
	sort.Slice(servers, func(i, j int) bool { return servers[i].Identity.Server < servers[j].Identity.Server })
	var bindings []mcpBinding
	seenName, seenID := map[string]bool{}, map[domain.ID]bool{}
	var exclusions []string
	for index, server := range servers {
		if server.Identity.Validate() != nil || !mcpServerName.MatchString(server.Identity.Server) || seenName[server.Identity.Server] || seenID[server.Identity.DefinitionID] {
			return nil, mcpUnavailable()
		}
		seenName[server.Identity.Server], seenID[server.Identity.DefinitionID] = true, true
		binding := mcpBinding{server: server, name: server.Identity.Server}
		// The literal reflection guard rejects every frame that contains a protected
		// value, so a short value would break the native connection.
		if shortMCPSecret(server) {
			return nil, mcpUnavailable()
		}
		var fields []string
		switch server.Transport {
		case domain.MCPStdio:
			if server.Command == "" || server.Endpoint != "" || len(server.Headers) != 0 || server.BearerToken != "" || domain.Text(server.Cwd, "MCP cwd", 4096, true) != nil {
				return nil, mcpUnavailable()
			}
			fields = append(fields, "command="+tomlString(server.Command), "args="+tomlStrings(server.Arguments), "cwd="+tomlString(server.Cwd))
			names := make([]string, 0, len(server.Environment))
			for name := range server.Environment {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				value := server.Environment[name]
				if !mcpEnvName.MatchString(name) || reservedMCPEnvironment(name) || value == "" {
					return nil, mcpUnavailable()
				}
				if prior, exists := claimed[name]; exists && prior != value {
					// Two servers cannot share one inherited name with different values.
					return nil, mcpUnavailable()
				} else if !exists && taken(name) {
					return nil, mcpUnavailable()
				}
				if other, exists := claimedFolded[strings.ToUpper(name)]; exists && other != name {
					// A differently cased name could reach another server's child on a
					// case-insensitive host; fail closed instead of sharing a value.
					return nil, mcpUnavailable()
				}
				claimedFolded[strings.ToUpper(name)] = name
				claimed[name] = value
				binding.environment = append(binding.environment, name)
			}
			if len(binding.environment) > 0 {
				fields = append(fields, "env_vars="+tomlStrings(binding.environment))
			}
		case domain.MCPStreamableHTTP:
			origin, ok := httpOrigin(server.Endpoint)
			if server.Endpoint == "" || !ok || server.Command != "" || len(server.Arguments) != 0 || len(server.Environment) != 0 || domain.ValidateMCPURL(server.Endpoint) != nil {
				return nil, mcpUnavailable()
			}
			binding.expectedOrigin = origin
			fields = append(fields, "url="+tomlString(server.Endpoint))
			if server.BearerToken != "" {
				env := fmt.Sprintf("DELIDEV_MCP_%d_BEARER", index)
				claimed[env] = server.BearerToken
				binding.bearerEnv = env
				fields = append(fields, "bearer_token_env_var="+tomlString(env))
			}
			if len(server.Headers) > 0 {
				names := make([]string, 0, len(server.Headers))
				for name := range server.Headers {
					names = append(names, name)
				}
				sort.Strings(names)
				binding.headerEnv = map[string]string{}
				parts := []string{}
				// HTTP field names are case-insensitive: two spellings would target one header.
				folded := map[string]bool{}
				for position, name := range names {
					value := server.Headers[name]
					if folded[strings.ToLower(name)] {
						return nil, mcpUnavailable()
					}
					folded[strings.ToLower(name)] = true
					if name == "" || strings.ContainsAny(name, "\r\n: \x00\"\\") || value == "" || (server.BearerToken != "" && strings.EqualFold(name, "authorization")) {
						return nil, mcpUnavailable()
					}
					env := fmt.Sprintf("DELIDEV_MCP_%d_H%d", index, position)
					claimed[env] = value
					binding.headerEnv[name] = env
					parts = append(parts, tomlString(name)+"="+tomlString(env))
				}
				fields = append(fields, "env_http_headers={"+strings.Join(parts, ",")+"}")
			}
		default:
			return nil, mcpUnavailable()
		}
		fields = append(fields, "startup_timeout_sec=30", "tool_timeout_sec=120")
		config.Process.Args = append(config.Process.Args, "-c", "mcp_servers."+binding.name+"={"+strings.Join(fields, ",")+"}")
		// The retained binding keeps only non-secret expectations.
		binding.server.Environment, binding.server.Headers, binding.server.BearerToken = nil, nil, ""
		bindings = append(bindings, binding)
	}
	names := make([]string, 0, len(claimed))
	for name := range claimed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		config.Process.Env = append(slices.Clone(config.Process.Env), name+"="+claimed[name])
		config.Process.ProtectedValues = append(slices.Clone(config.Process.ProtectedValues), claimed[name])
		exclusions = append(exclusions, name)
	}
	config.mcpExcluded = exclusions
	return bindings, nil
}

func reservedMCPEnvironment(name string) bool {
	upper := strings.ToUpper(name)
	for _, reserved := range mcpReservedEnv {
		if upper == reserved || strings.HasSuffix(reserved, "_") && strings.HasPrefix(upper, reserved) {
			return true
		}
	}
	return false
}

func inertMCPField(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	switch trimmed {
	case "", "null", "{}", "[]", "false", `""`:
		return true
	}
	return false
}

// mcpOverrideFields change process identity, requests, authority or tool policy.
// Native layers merge by key, so any non-inert lower-layer value is a mismatch.
var mcpOverrideFields = []string{"env", "http_headers", "http_headers_helper", "oauth", "auth", "oauth_resource", "scopes", "environment_id_override", "enabled_tools", "disabled_tools", "omit_tools_from", "default_tools_approval_mode", "tools", "name"}

func (c *Client) validateMCPConfig(config map[string]json.RawMessage) error {
	var servers map[string]map[string]json.RawMessage
	if len(config["mcp_servers"]) == 0 || string(config["mcp_servers"]) == "null" {
		servers = map[string]map[string]json.RawMessage{}
	} else if json.Unmarshal(config["mcp_servers"], &servers) != nil || servers == nil {
		return mcpMismatch("configuration")
	}
	if len(servers) != len(c.managedMCP) {
		return mcpMismatch("inventory")
	}
	// Plugin-provided servers are outside the selected inventory.
	if raw := config["plugins"]; !inertMCPField(raw) {
		var plugins map[string]json.RawMessage
		if json.Unmarshal(raw, &plugins) != nil || len(plugins) != 0 {
			return mcpMismatch("plugins")
		}
	}
	for _, binding := range c.managedMCP {
		fields, present := servers[binding.name]
		if !present {
			return mcpMismatch("inventory")
		}
		for _, field := range mcpOverrideFields {
			if !inertMCPField(fields[field]) {
				return mcpMismatch("override")
			}
		}
		if raw := fields["enabled"]; len(raw) != 0 && string(raw) != "true" && string(raw) != "null" {
			return mcpMismatch("enablement")
		}
		text := func(key string) (string, bool) {
			var value *string
			if len(fields[key]) == 0 {
				return "", true
			}
			if json.Unmarshal(fields[key], &value) != nil {
				return "", false
			}
			if value == nil {
				return "", true
			}
			return *value, true
		}
		// A lower layer can set environment_id; a non-local value routes the server
		// outside the selected Worker's owned process tree.
		if environment, ok := text("environment_id"); !ok || environment != "local" {
			return mcpMismatch("environment")
		}
		command, ok1 := text("command")
		cwd, ok2 := text("cwd")
		endpoint, ok3 := text("url")
		bearer, ok4 := text("bearer_token_env_var")
		if !ok1 || !ok2 || !ok3 || !ok4 {
			return mcpMismatch("transport")
		}
		var arguments []string
		var envVars []json.RawMessage
		var headerEnv map[string]string
		if (len(fields["args"]) != 0 && string(fields["args"]) != "null" && json.Unmarshal(fields["args"], &arguments) != nil) || (len(fields["env_vars"]) != 0 && string(fields["env_vars"]) != "null" && json.Unmarshal(fields["env_vars"], &envVars) != nil) || (len(fields["env_http_headers"]) != 0 && string(fields["env_http_headers"]) != "null" && json.Unmarshal(fields["env_http_headers"], &headerEnv) != nil) {
			return mcpMismatch("transport")
		}
		server := binding.server
		switch server.Transport {
		case domain.MCPStdio:
			if command != server.Command || endpoint != "" || bearer != "" || len(headerEnv) != 0 || !nativePathEqual(cwd, server.Cwd) || !slices.Equal(arguments, server.Arguments) || len(envVars) != len(binding.environment) {
				return mcpMismatch("stdio")
			}
			for i, raw := range envVars {
				var name string
				if json.Unmarshal(raw, &name) != nil || name != binding.environment[i] {
					return mcpMismatch("environment")
				}
			}
		case domain.MCPStreamableHTTP:
			if endpoint != server.Endpoint || command != "" || len(arguments) != 0 || len(envVars) != 0 || bearer != binding.bearerEnv || len(headerEnv) != len(binding.headerEnv) {
				return mcpMismatch("http")
			}
			for name, env := range binding.headerEnv {
				if headerEnv[name] != env {
					return mcpMismatch("headers")
				}
			}
		}
	}
	return nil
}

type mcpStatusEntry struct {
	Name          string          `json:"name"`
	RuntimeStatus *string         `json:"runtimeStatus"`
	PluginID      *string         `json:"pluginId"`
	HTTPOrigin    *string         `json:"httpOrigin"`
	ToolsError    *string         `json:"toolsError"`
	Tools         json.RawMessage `json:"tools"`
	ServerInfo    json.RawMessage `json:"serverInfo"`
	Capabilities  json.RawMessage `json:"serverCapabilities"`
	Resources     json.RawMessage `json:"resources"`
	Templates     json.RawMessage `json:"resourceTemplates"`
	AuthStatus    string          `json:"authStatus"`
}

// mcpStartupWait is the overall bound for every selected server to start.
var mcpStartupWait = 45 * time.Second

// listMCPStatus asks for each selected server separately. With a thread and a
// server name the installed app-server reuses the thread's own connection; an
// unscoped listing would start a second copy of every configured server.
func (c *Client) listMCPStatus(ctx context.Context) ([]mcpStatusEntry, error) {
	var entries []mcpStatusEntry
	for _, binding := range c.managedMCP {
		response, err := c.wire.Call(ctx, domain.NewID(), "mcpServerStatus/list", struct {
			Limit      uint32    `json:"limit"`
			ThreadID   domain.ID `json:"threadId"`
			ServerName string    `json:"serverName"`
		}{16, c.thread, binding.name})
		if err != nil {
			return nil, err
		}
		var result struct {
			Data []mcpStatusEntry `json:"data"`
			Next *string          `json:"nextCursor"`
		}
		if response.ErrorCode != nil || len(response.Result) > 8<<20 || json.Unmarshal(response.Result, &result) != nil || result.Data == nil || result.Next != nil || len(result.Data) != 1 {
			return nil, mcpMismatch("status")
		}
		entries = append(entries, result.Data...)
	}
	return entries, nil
}

func classifyMCP(entry mcpStatusEntry) domain.MCPStartupOutcome {
	status := ""
	if entry.RuntimeStatus != nil {
		status = *entry.RuntimeStatus
	}
	// A connected server whose tool discovery failed exposes no usable catalog.
	if status == "connected" && entry.ToolsError != nil {
		return domain.MCPStartupFailed
	}
	switch status {
	case "connected":
		return domain.MCPStartupReady
	case "authenticationRequired":
		return domain.MCPStartupAuthenticationRequired
	case "cancelled":
		return domain.MCPStartupCancelled
	case "failed", "disabled":
		return domain.MCPStartupFailed
	case "starting", "notStarted":
		return domain.MCPStartupPending
	}
	if entry.ToolsError != nil {
		return domain.MCPStartupFailed
	}
	return domain.MCPStartupPending
}

// verifyManagedMCP proves the exact selected native inventory and startup state
// for the original thread before any input. Unselected, plugin, project or
// foreign servers fail closed; a failed selected server never silently degrades.
func (c *Client) verifyManagedMCP(ctx context.Context, cwd string) (returned error) {
	if len(c.managedMCP) == 0 {
		return nil
	}
	defer func() {
		if returned != nil && c.logger != nil {
			c.logger.WarnContext(ctx, "Codex managed MCP inventory rejected", "owner_id", c.ownerID, "code", domain.SafeError(returned).Code)
		}
	}()
	response, err := c.wire.Call(ctx, domain.NewID(), "config/read", struct {
		Cwd           string `json:"cwd"`
		IncludeLayers bool   `json:"includeLayers"`
	}{Cwd: cwd})
	if err != nil {
		return err
	}
	var config struct {
		Config map[string]json.RawMessage `json:"config"`
	}
	if response.ErrorCode != nil || len(response.Result) > 8<<20 || json.Unmarshal(response.Result, &config) != nil || config.Config == nil {
		return mcpMismatch("configuration")
	}
	if err := c.validateMCPConfig(config.Config); err != nil {
		return err
	}
	deadline := time.Now().Add(mcpStartupWait)
	// Every status call is bounded by the overall startup deadline: a thread-scoped
	// listing waits for its selected server, so serial calls could otherwise exceed it.
	statusCtx, cancelStatus := context.WithDeadline(ctx, deadline)
	defer cancelStatus()
	for {
		entries, err := c.listMCPStatus(statusCtx)
		if err != nil {
			if ctx.Err() == nil && statusCtx.Err() != nil {
				return domain.Fail(domain.Unavailable, "A selected MCP server did not finish starting.", "Reconnect the original selected definition; no input was sent.")
			}
			return err
		}
		outcomes, pending, err := c.matchMCPInventory(entries)
		if err != nil {
			return err
		}
		c.recordMCPOutcomes(outcomes)
		if !pending {
			for _, outcome := range outcomes {
				if outcome.State != domain.MCPStartupReady {
					if c.logger != nil {
						c.logger.WarnContext(ctx, "Codex managed MCP startup incomplete", "owner_id", c.ownerID, "definition_id", outcome.Identity.DefinitionID, "revision", outcome.Identity.Revision, "state", outcome.State)
					}
					// Typed per-server outcomes remain available from ManagedMCPOutcomes; the
					// native diagnostic wrapper intentionally replaces error text.
					return domain.Fail(domain.Unavailable, "A selected MCP server did not start.", "Reconnect or re-authenticate the original selected definition; the saved selection and every call were left unchanged and no input was sent.")
				}
			}
			if c.logger != nil {
				c.logger.InfoContext(ctx, "Codex managed MCP inventory verified", "owner_id", c.ownerID, "servers", len(outcomes))
			}
			return nil
		}
		if time.Now().After(deadline) {
			return domain.Fail(domain.Unavailable, "A selected MCP server did not finish starting.", "Reconnect the original selected definition; no input was sent.")
		}
		select {
		case <-ctx.Done():
			return domain.SafeError(ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (c *Client) matchMCPInventory(entries []mcpStatusEntry) ([]MCPServerOutcome, bool, error) {
	byName := map[string]mcpStatusEntry{}
	for _, entry := range entries {
		if domain.Text(entry.Name, "native MCP server", 1024, true) != nil || byName[entry.Name].Name != "" {
			return nil, false, mcpMismatch("status")
		}
		byName[entry.Name] = entry
	}
	for name, entry := range byName {
		selected := slices.ContainsFunc(c.managedMCP, func(b mcpBinding) bool { return b.name == name })
		// The built-in ChatGPT connector keeps its separate passive boundary.
		if !selected && !(name == "codex_apps" && c.managedHome != "") || entry.PluginID != nil {
			return nil, false, mcpMismatch("inventory")
		}
	}
	outcomes := make([]MCPServerOutcome, 0, len(c.managedMCP))
	pending := false
	for _, binding := range c.managedMCP {
		entry, present := byName[binding.name]
		if !present {
			return nil, false, mcpMismatch("inventory")
		}
		switch binding.server.Transport {
		case domain.MCPStdio:
			if entry.HTTPOrigin != nil {
				return nil, false, mcpMismatch("transport")
			}
		case domain.MCPStreamableHTTP:
			if entry.HTTPOrigin == nil || *entry.HTTPOrigin != binding.expectedOrigin {
				return nil, false, mcpMismatch("transport")
			}
		}
		state := classifyMCP(entry)
		if state == domain.MCPStartupPending {
			pending = true
		}
		outcomes = append(outcomes, MCPServerOutcome{Identity: binding.server.Identity, State: state})
	}
	return outcomes, pending, nil
}

func (c *Client) recordMCPOutcomes(outcomes []MCPServerOutcome) {
	for _, outcome := range outcomes {
		c.setMCPOutcome(outcome.Identity.Server, outcome.State)
	}
}

func (c *Client) setMCPOutcome(name string, state domain.MCPStartupOutcome) {
	c.mcpMu.Lock()
	defer c.mcpMu.Unlock()
	if c.mcpOutcomes == nil {
		c.mcpOutcomes = map[string]domain.MCPStartupOutcome{}
	}
	c.mcpOutcomes[name] = state
}

func (c *Client) mcpOutcome(name string) domain.MCPStartupOutcome {
	c.mcpMu.Lock()
	defer c.mcpMu.Unlock()
	if state, known := c.mcpOutcomes[name]; known {
		return state
	}
	return domain.MCPStartupPending
}

// ManagedMCPOutcomes returns the latest typed startup state for each selected
// server in stable identity order. It never contains native diagnostics.
func (c *Client) ManagedMCPOutcomes() []MCPServerOutcome {
	result := make([]MCPServerOutcome, 0, len(c.managedMCP))
	for _, binding := range c.managedMCP {
		result = append(result, MCPServerOutcome{Identity: binding.server.Identity, State: c.mcpOutcome(binding.name)})
	}
	return result
}

func (c *Client) mcpBinding(name string) (mcpBinding, bool) {
	for _, binding := range c.managedMCP {
		if binding.name == name {
			return binding, true
		}
	}
	return mcpBinding{}, false
}

// VerifyManagedMCP proves the exact selected inventory and returns each selected
// generation's typed startup outcome before any input is reserved or sent.
func (c *Client) VerifyManagedMCP(ctx context.Context) (outcomes []MCPServerOutcome, returned error) {
	defer c.recordFailure(ctx, domain.CodexExecution, &returned)
	if err := c.acquireControl(ctx); err != nil {
		return nil, err
	}
	defer func() { <-c.control }()
	if c.execution == nil || len(c.managedMCP) == 0 || c.problem != nil {
		return nil, mcpUnavailable()
	}
	err := c.verifyManagedMCP(ctx, c.execution.settings.Cwd)
	return c.ManagedMCPOutcomes(), err
}

// ReconnectManagedMCP reloads the frozen native configuration for a selected
// definition that is not ready. The installed native reload has no server
// selector, so it refreshes every selected server of this execution and healthy
// ones may restart; the typed outcomes of all selected servers are returned. It
// cannot add, edit or substitute a definition, resend any call or elicitation,
// or run while a turn owns the thread.
func (c *Client) ReconnectManagedMCP(ctx context.Context, definition domain.ID) (outcomes []MCPServerOutcome, returned error) {
	defer c.recordFailure(ctx, domain.CodexExecution, &returned)
	if err := definition.Validate(); err != nil {
		return nil, err
	}
	if err := c.acquireControl(ctx); err != nil {
		return nil, err
	}
	defer func() { <-c.control }()
	if c.execution == nil || len(c.managedMCP) == 0 || c.problem != nil {
		return nil, mcpUnavailable()
	}
	selected := false
	ready := false
	for _, binding := range c.managedMCP {
		if binding.server.Identity.DefinitionID == definition {
			selected = true
			ready = c.mcpOutcome(binding.name) == domain.MCPStartupReady
		}
	}
	if !selected {
		return nil, domain.Fail(domain.Conflict, "That MCP definition is not part of this execution.", "Reconnect only a definition selected when this execution started.")
	}
	if ready {
		// The native reload cannot target one server, so never run it for a healthy one.
		return nil, domain.Fail(domain.Conflict, "That MCP definition is already ready.", "Reconnect only a selected definition that failed to start; a reload can restart every selected server.")
	}
	state := c.execution
	if state.paused || state.active != "" || state.interrupt != "" || state.interactions.blocksInput() {
		return nil, turnConflict()
	}
	response, err := c.wire.Call(ctx, domain.NewID(), "config/mcpServer/reload", nativewire.OmittedParams{})
	if err != nil {
		return nil, err
	}
	if response.ErrorCode != nil {
		return nil, mcpUnavailable()
	}
	if err := c.verifyManagedMCP(ctx, state.settings.Cwd); err != nil {
		return c.ManagedMCPOutcomes(), err
	}
	if c.logger != nil {
		c.logger.InfoContext(ctx, "Codex managed MCP reconnect verified", "owner_id", c.ownerID, "definition_id", definition)
	}
	return c.ManagedMCPOutcomes(), nil
}

func mcpStartupState(status nativeMCPStartupState, reason *nativeMCPFailureReason) domain.MCPStartupOutcome {
	switch status {
	case nativeMCPReady:
		return domain.MCPStartupReady
	case nativeMCPCancelled:
		return domain.MCPStartupCancelled
	case nativeMCPFailed:
		if reason != nil && *reason == nativeMCPReauthentication {
			return domain.MCPStartupAuthenticationRequired
		}
		return domain.MCPStartupFailed
	default:
		return domain.MCPStartupPending
	}
}

func shortMCPSecret(server ManagedMCPServer) bool {
	short := func(value string) bool { return value != "" && len(value) < domain.MinMCPSecretBytes }
	if short(server.BearerToken) {
		return true
	}
	for _, value := range server.Environment {
		if short(value) {
			return true
		}
	}
	for _, value := range server.Headers {
		if short(value) {
			return true
		}
	}
	return false
}
