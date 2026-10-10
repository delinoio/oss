// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Source: openai/codex c1382380de69521303b416720a52f42d51af6248,
// app-server-protocol AppInfo, InstalledApp and ConnectorMetadata. Display-only
// native branding and directory metadata stay private and grant no tool scope.
type appDirectoryRow struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Description         *string           `json:"description"`
	LogoURL             *string           `json:"logoUrl"`
	LogoURLDark         *string           `json:"logoUrlDark"`
	IconAssets          map[string]string `json:"iconAssets"`
	IconDarkAssets      map[string]string `json:"iconDarkAssets"`
	DistributionChannel *string           `json:"distributionChannel"`
	Branding            json.RawMessage   `json:"branding"`
	AppMetadata         json.RawMessage   `json:"appMetadata"`
	Labels              map[string]string `json:"labels"`
	InstallURL          *string           `json:"installUrl"`
	Accessible          *bool             `json:"isAccessible"`
	Enabled             *bool             `json:"isEnabled"`
	PluginDisplayNames  []string          `json:"pluginDisplayNames"`
}

type installedAppRow struct {
	ID          string  `json:"id"`
	RuntimeName *string `json:"runtimeName"`
	Enabled     *bool   `json:"enabled"`
	Callable    *bool   `json:"callable"`
}

func validAppDirectoryRow(a appDirectoryRow) bool {
	if domain.Text(a.ID, "native app identity", 1024, true) != nil || domain.Text(a.Name, "native app name", 4096, true) != nil || a.Accessible == nil || a.Enabled == nil || a.PluginDisplayNames == nil || len(a.PluginDisplayNames) > 100 {
		return false
	}
	for _, value := range []*string{a.Description, a.LogoURL, a.LogoURLDark, a.DistributionChannel, a.InstallURL} {
		if value != nil && domain.Text(*value, "private app metadata", 64<<10, false) != nil {
			return false
		}
	}
	for _, names := range []map[string]string{a.IconAssets, a.IconDarkAssets, a.Labels} {
		if len(names) > 100 {
			return false
		}
		for k, v := range names {
			if domain.Text(k, "private app metadata key", 1024, true) != nil || domain.Text(v, "private app metadata value", 64<<10, false) != nil {
				return false
			}
		}
	}
	for _, name := range a.PluginDisplayNames {
		if domain.Text(name, "private app plugin name", 4096, true) != nil {
			return false
		}
	}
	for _, raw := range []json.RawMessage{a.Branding, a.AppMetadata} {
		if len(raw) == 0 {
			return false
		}
		if string(raw) != "null" {
			var object map[string]json.RawMessage
			if domain.DecodeBounded(raw, &object, 256<<10) != nil || object == nil {
				return false
			}
		}
	}
	return true
}

// The caller holds the original controller. Cursors and private metadata never
// cross its lifetime; a complete bounded snapshot is required for publication.
func (c *Client) readAppsDirectoryLocked(ctx context.Context) ([]domain.CodexApp, error) {
	rows := []domain.CodexApp{}
	seen := map[string]bool{}
	cursors := map[string]bool{}
	var cursor *string
	total := 0
	for pages := 0; pages < 11; pages++ {
		response, err := c.wire.Call(ctx, domain.NewID(), "app/list", struct {
			Cursor       *string   `json:"cursor"`
			Limit        uint32    `json:"limit"`
			ThreadID     domain.ID `json:"threadId"`
			ForceRefetch bool      `json:"forceRefetch"`
		}{cursor, 100, c.thread, true})
		if err != nil || response.ErrorCode != nil {
			return nil, incompatible()
		}
		total += len(response.Result)
		if total > 4<<20 {
			return nil, incompatible()
		}
		var page struct {
			Data       []appDirectoryRow `json:"data"`
			NextCursor *string           `json:"nextCursor"`
		}
		if domain.DecodeBounded(response.Result, &page, 4<<20) != nil || page.Data == nil || len(page.Data) > 100 {
			return nil, incompatible()
		}
		for _, a := range page.Data {
			if !validAppDirectoryRow(a) || seen[a.ID] || len(rows) >= domain.MaxCodexAppInventory {
				return nil, incompatible()
			}
			seen[a.ID] = true
			rows = append(rows, domain.CodexApp{ID: a.ID, Name: a.Name, Discovered: true, Accessible: *a.Accessible, Enabled: *a.Enabled})
		}
		if page.NextCursor == nil {
			return rows, nil
		}
		if domain.Text(*page.NextCursor, "private app cursor", 4096, true) != nil || cursors[*page.NextCursor] {
			return nil, incompatible()
		}
		cursors[*page.NextCursor] = true
		cursor = page.NextCursor
	}
	return nil, incompatible()
}

func decodeInstalledApps(raw json.RawMessage) ([]installedAppRow, error) {
	var result struct {
		Apps []installedAppRow `json:"apps"`
	}
	if domain.DecodeBounded(raw, &result, 4<<20) != nil || result.Apps == nil || len(result.Apps) > domain.MaxCodexAppInventory {
		return nil, incompatible()
	}
	seen := map[string]bool{}
	for _, a := range result.Apps {
		if domain.Text(a.ID, "native installed app identity", 1024, true) != nil || seen[a.ID] || a.Enabled == nil || a.Callable == nil || *a.Callable && !*a.Enabled || a.RuntimeName != nil && domain.Text(*a.RuntimeName, "private app runtime name", 4096, false) != nil {
			return nil, incompatible()
		}
		seen[a.ID] = true
	}
	return result.Apps, nil
}

func (c *Client) readInstalledAppsLocked(ctx context.Context) ([]installedAppRow, error) {
	response, err := c.wire.Call(ctx, domain.NewID(), "app/installed", struct {
		ThreadID     domain.ID `json:"threadId"`
		ForceRefresh bool      `json:"forceRefresh"`
	}{c.thread, true})
	if err != nil || response.ErrorCode != nil {
		return nil, incompatible()
	}
	return decodeInstalledApps(response.Result)
}

type appToolSummary struct {
	Name           string  `json:"name"`
	Title          *string `json:"title"`
	Description    string  `json:"description"`
	Enabled        *bool   `json:"isEnabled"`
	DisabledReason *string `json:"disabledReason"`
	ReadOnly       *bool   `json:"isReadOnly"`
}

type appReadRow struct {
	ID                  string           `json:"id"`
	Name                string           `json:"name"`
	Description         *string          `json:"description"`
	IconURL             *string          `json:"iconUrl"`
	IconURLDark         *string          `json:"iconUrlDark"`
	DistributionChannel *string          `json:"distributionChannel"`
	InstallURL          *string          `json:"installUrl"`
	PluginDisplayNames  []string         `json:"pluginDisplayNames"`
	ToolSummaries       []appToolSummary `json:"toolSummaries"`
}

// app/read summaries are display evidence only. The refreshed installed catalog
// and the original call binding independently decide execution eligibility.
func decodeAppRead(raw json.RawMessage, requested []string) (map[string]appReadRow, error) {
	var response struct {
		Apps          []appReadRow `json:"apps"`
		MissingAppIDs []string     `json:"missingAppIds"`
	}
	if domain.DecodeBounded(raw, &response, 4<<20) != nil || response.Apps == nil || response.MissingAppIDs == nil || len(requested) > domain.MaxCodexAppSelections || len(response.Apps)+len(response.MissingAppIDs) != len(requested) {
		return nil, incompatible()
	}
	remaining := map[string]bool{}
	for _, id := range requested {
		if remaining[id] || domain.Text(id, "requested app identity", 1024, true) != nil {
			return nil, incompatible()
		}
		remaining[id] = true
	}
	result := map[string]appReadRow{}
	for _, a := range response.Apps {
		if !remaining[a.ID] || domain.Text(a.Name, "native app name", 4096, true) != nil || a.PluginDisplayNames == nil || len(a.PluginDisplayNames) > 100 || len(a.ToolSummaries) > 1000 {
			return nil, incompatible()
		}
		delete(remaining, a.ID)
		for _, s := range []*string{a.Description, a.IconURL, a.IconURLDark, a.DistributionChannel, a.InstallURL} {
			if s != nil && domain.Text(*s, "private app metadata", 64<<10, false) != nil {
				return nil, incompatible()
			}
		}
		for _, name := range a.PluginDisplayNames {
			if domain.Text(name, "private app plugin name", 4096, true) != nil {
				return nil, incompatible()
			}
		}
		tools := map[string]bool{}
		for _, tool := range a.ToolSummaries {
			if domain.Text(tool.Name, "native app tool name", 1024, true) != nil || tools[tool.Name] || domain.Text(tool.Description, "private app tool description", 64<<10, false) != nil || tool.Enabled == nil || tool.ReadOnly == nil {
				return nil, incompatible()
			}
			tools[tool.Name] = true
			for _, s := range []*string{tool.Title, tool.DisabledReason} {
				if s != nil && domain.Text(*s, "private app tool metadata", 64<<10, false) != nil {
					return nil, incompatible()
				}
			}
		}
		result[a.ID] = a
	}
	for _, id := range response.MissingAppIDs {
		if !remaining[id] {
			return nil, incompatible()
		}
		delete(remaining, id)
	}
	if len(remaining) != 0 {
		return nil, incompatible()
	}
	return result, nil
}

func (c *Client) readSelectedAppsLocked(ctx context.Context, ids []string) (map[string]appReadRow, error) {
	if len(ids) == 0 {
		return map[string]appReadRow{}, nil
	}
	response, err := c.wire.Call(ctx, domain.NewID(), "app/read", struct {
		AppIDs       []string  `json:"appIds"`
		ThreadID     domain.ID `json:"threadId"`
		IncludeTools bool      `json:"includeTools"`
	}{ids, c.thread, true})
	if err != nil || response.ErrorCode != nil {
		return nil, incompatible()
	}
	return decodeAppRead(response.Result, ids)
}
