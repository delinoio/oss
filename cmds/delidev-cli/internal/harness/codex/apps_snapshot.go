// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"slices"
	"sort"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Join only complete original-account observations. Directory rows describe
// discovery; installed rows independently supply native enablement/callability.
// A retained selection absent from both remains visible without fabricated
// discovery, installation or execution support.
func joinAppsSnapshot(selection domain.CodexAppConfiguration, directory []domain.CodexApp, installed []installedAppRow, descriptions map[string]appReadRow) ([]domain.CodexApp, error) {
	if selection.Validate() != nil || directory == nil || installed == nil || descriptions == nil {
		return nil, incompatible()
	}
	rows := map[string]domain.CodexApp{}
	for _, app := range directory {
		if app.Validate() != nil || !app.Discovered || app.Installed || app.Callable || app.Selected {
			return nil, incompatible()
		}
		if _, exists := rows[app.ID]; exists {
			return nil, incompatible()
		}
		rows[app.ID] = app
	}
	seen := map[string]bool{}
	for _, app := range installed {
		if seen[app.ID] || domain.Text(app.ID, "native installed app identity", 1024, true) != nil || app.Enabled == nil || app.Callable == nil || *app.Callable && !*app.Enabled {
			return nil, incompatible()
		}
		seen[app.ID] = true
		row := rows[app.ID]
		row.ID, row.Installed, row.Enabled, row.Callable = app.ID, true, *app.Enabled, *app.Callable
		rows[app.ID] = row
	}
	for _, id := range selection.AppIDs {
		row := rows[id]
		row.ID, row.Selected = id, true
		if description, exists := descriptions[id]; exists {
			if description.ID != id || domain.Text(description.Name, "native app name", 4096, true) != nil {
				return nil, incompatible()
			}
			row.Name = description.Name
		}
		rows[id] = row
	}
	for id := range descriptions {
		if !slices.Contains(selection.AppIDs, id) {
			return nil, incompatible()
		}
	}
	if len(rows) > domain.MaxCodexAppInventory {
		return nil, incompatible()
	}
	result := make([]domain.CodexApp, 0, len(rows))
	for _, row := range rows {
		if row.Validate() != nil {
			return nil, incompatible()
		}
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// This predicate is eligible only after a successful original-thread forced
// refresh. app/installed propagates refresh errors instead of returning its old
// cache. The native refreshed tool catalog invalidates every earlier prepared
// call before its irreversible preparation. A config write acknowledgment alone
// cannot supply that barrier, especially when native user reload was rejected.
func appsRemovalObserved(previous, next domain.CodexAppConfiguration, fresh []installedAppRow) bool {
	if !previous.RemovalOnly(next) || fresh == nil || len(fresh) > domain.MaxCodexAppInventory {
		return false
	}
	seen := map[string]bool{}
	for _, app := range fresh {
		if seen[app.ID] || domain.Text(app.ID, "native installed app identity", 1024, true) != nil || app.Enabled == nil || app.Callable == nil || *app.Callable && !*app.Enabled {
			return false
		}
		seen[app.ID] = true
		if !slices.Contains(next.AppIDs, app.ID) && (*app.Enabled || *app.Callable) {
			return false
		}
	}
	return true
}
