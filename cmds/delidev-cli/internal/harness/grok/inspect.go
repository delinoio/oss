package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// A private HOME alone does not disable machine-managed Grok configuration.
// Native inspect is a read-only command. Validate its original observations
// before starting ACP, and never activate inherited policies or extensions.
func inspect(ctx context.Context, config process.Config) error {
	bounded, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := inspectionOutput{cancel: cancel, capture: true}
	stderr := inspectionOutput{cancel: cancel}
	config.Args = []string{"--no-auto-update", "inspect", "--json"}
	config.Stdout, config.Stderr = &stdout, &stderr
	err := process.Run(bounded, config)
	if err != nil && domain.SafeError(err).Code == domain.RecoveryRequired {
		return err
	}
	if stdout.overflow || stderr.overflow {
		return domain.Fail(domain.ResourceExhausted, "Grok Build configuration inspection exceeded its bound.", "Use an isolated supported native installation and refresh discovery.")
	}
	if err != nil {
		return probeUnavailable()
	}
	return validateInspection(stdout.buffer.Bytes(), config.Cwd)
}

type inspectionOutput struct {
	buffer   bytes.Buffer
	count    int
	capture  bool
	overflow bool
	cancel   context.CancelFunc
}

func (w *inspectionOutput) Write(data []byte) (int, error) {
	remaining := max(0, maxProbeStderr-w.count)
	if w.capture {
		_, _ = w.buffer.Write(data[:min(remaining, len(data))])
	}
	if len(data) > remaining {
		w.overflow = true
		w.cancel()
	}
	w.count = min(maxProbeStderr, w.count+min(maxProbeStderr, len(data)))
	return len(data), nil
}

func validateInspection(raw []byte, cwd string) error {
	var report struct {
		Version             string          `json:"grokVersion"`
		Channel             string          `json:"channel"`
		Cwd                 string          `json:"cwd"`
		ProjectRoot         json.RawMessage `json:"projectRoot"`
		ProjectTrusted      bool            `json:"projectTrusted"`
		ProjectInstructions json.RawMessage `json:"projectInstructions"`
		Permissions         struct {
			Sources                    json.RawMessage `json:"sources"`
			Loaded                     uint32          `json:"loaded"`
			Skipped                    json.RawMessage `json:"skipped"`
			MCPServerAllowlist         json.RawMessage `json:"mcpServerAllowlist"`
			MCPLockdownSources         json.RawMessage `json:"mcpLockdownSources"`
			MCPManagedServersOnly      string          `json:"mcpManagedServersOnly"`
			MarketplaceAllowlist       json.RawMessage `json:"marketplaceAllowlist"`
			MarketplaceLockdownSources json.RawMessage `json:"marketplaceLockdownSources"`
			ManagedMarketplaces        json.RawMessage `json:"managedMarketplaces"`
			ManagedSettingsPath        string          `json:"managedSettingsPath"`
			ManagedSettingsExists      bool            `json:"managedSettingsExists"`
			ManagedSettingsActive      bool            `json:"managedSettingsActive"`
			ClaudeBypassLockAdvisory   bool            `json:"claudeBypassLockAdvisory"`
		} `json:"permissions"`
		LoginPolicy struct {
			DisableAPIKeyAuth  json.RawMessage `json:"disableApiKeyAuth"`
			ForceLoginTeamUUID json.RawMessage `json:"forceLoginTeamUuid"`
			APIKeyAuthDisabled bool            `json:"apiKeyAuthDisabled"`
		} `json:"loginPolicy"`
		Hooks  json.RawMessage `json:"hooks"`
		Skills json.RawMessage `json:"skills"`
		Agents []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Source      struct {
				Type string `json:"type"`
			} `json:"source"`
		} `json:"agents"`
		Plugins       json.RawMessage `json:"plugins"`
		Marketplaces  json.RawMessage `json:"marketplaces"`
		MCPServers    json.RawMessage `json:"mcpServers"`
		LSPServers    json.RawMessage `json:"lspServers"`
		ConfigSources struct {
			Layers json.RawMessage `json:"layers"`
		} `json:"configSources"`
		ExternalCompat struct {
			RemoteSettingsLoaded bool `json:"remoteSettingsLoaded"`
			Cells                []struct {
				Vendor  string `json:"vendor"`
				Surface string `json:"surface"`
				Enabled bool   `json:"enabled"`
				Source  string `json:"source"`
			} `json:"cells"`
		} `json:"externalCompat"`
	}
	if decode(raw, &report) != nil || report.Version != SupportedVersion || !text(report.Channel, 64) || report.Cwd != cwd || !isNull(report.ProjectRoot) || !report.ProjectTrusted ||
		report.Permissions.Loaded != 0 || report.Permissions.MCPManagedServersOnly != "off" || !text(report.Permissions.ManagedSettingsPath, 8192) ||
		report.Permissions.ManagedSettingsExists || report.Permissions.ManagedSettingsActive || report.Permissions.ClaudeBypassLockAdvisory ||
		!isNull(report.LoginPolicy.DisableAPIKeyAuth) || !isNull(report.LoginPolicy.ForceLoginTeamUUID) || report.LoginPolicy.APIKeyAuthDisabled || report.ExternalCompat.RemoteSettingsLoaded {
		return incompatible()
	}
	for _, list := range []json.RawMessage{report.ProjectInstructions, report.Hooks, report.Skills, report.Plugins, report.Marketplaces, report.MCPServers, report.LSPServers, report.ConfigSources.Layers,
		report.Permissions.Sources, report.Permissions.Skipped, report.Permissions.MCPServerAllowlist, report.Permissions.MCPLockdownSources, report.Permissions.MarketplaceAllowlist, report.Permissions.MarketplaceLockdownSources, report.Permissions.ManagedMarketplaces} {
		if !emptyArray(list) {
			return incompatible()
		}
	}
	agents := map[string]bool{"general-purpose": false, "explore": false, "plan": false}
	if len(report.Agents) != len(agents) {
		return incompatible()
	}
	for _, agent := range report.Agents {
		seen, known := agents[agent.Name]
		if !known || seen || agent.Source.Type != "builtin" || !text(agent.Description, 16<<10) {
			return incompatible()
		}
		agents[agent.Name] = true
	}
	// Session import scanners are advertised but never invoked. Their homes
	// are empty and private; other compatibility scanners must be disabled.
	cells := map[string]bool{}
	for _, vendor := range []string{"cursor", "claude"} {
		for _, surface := range []string{"skills", "rules", "agents", "mcps", "hooks", "sessions"} {
			cells[vendor+"/"+surface] = false
		}
	}
	cells["codex/sessions"] = false
	if len(report.ExternalCompat.Cells) != len(cells) {
		return incompatible()
	}
	for _, cell := range report.ExternalCompat.Cells {
		key := cell.Vendor + "/" + cell.Surface
		seen, known := cells[key]
		session := strings.HasSuffix(key, "/sessions")
		if !known || seen || cell.Enabled != session || (!session && cell.Source != "env") || (session && cell.Source != "default") {
			return incompatible()
		}
		cells[key] = true
	}
	return nil
}
