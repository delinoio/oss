// SPDX-License-Identifier: Apache-2.0
package domain

import "slices"

const MaxCodexAppSelections = 100
const MaxCodexAppInventory = 1000

// CodexAppConfiguration is an immutable selection for one original account.
// An inventory or installed plugin cannot create this authority.
type CodexAppConfiguration struct {
	Version    int      `json:"version"`
	SessionID  ID       `json:"session_id"`
	AccountID  ID       `json:"account_id"`
	Generation ID       `json:"generation"`
	AppIDs     []string `json:"app_ids"`
}

func (c CodexAppConfiguration) Validate() error {
	if c.Version != 1 || c.SessionID.Validate() != nil || c.AccountID.Validate() != nil || c.Generation.Validate() != nil || c.AppIDs == nil || len(c.AppIDs) > MaxCodexAppSelections {
		return invalidCodexApps()
	}
	seen := map[string]bool{}
	for _, id := range c.AppIDs {
		if id == "_default" || Text(id, "Codex app identity", 1024, true) != nil || seen[id] {
			return invalidCodexApps()
		}
		seen[id] = true
	}
	return nil
}

func (c CodexAppConfiguration) Clone() CodexAppConfiguration {
	c.AppIDs = slices.Clone(c.AppIDs)
	return c
}

// RemovalOnly prevents a live controller from gaining new app authority.
func (c CodexAppConfiguration) RemovalOnly(next CodexAppConfiguration) bool {
	if c.Validate() != nil || next.Validate() != nil || c.SessionID != next.SessionID || c.AccountID != next.AccountID || c.Generation == next.Generation {
		return false
	}
	for _, id := range next.AppIDs {
		if !slices.Contains(c.AppIDs, id) {
			return false
		}
	}
	return true
}

// These states are independent. Callable is an original native runtime fact,
// not a consequence of discovery, installation, selection or enablement alone.
type CodexApp struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Discovered bool   `json:"discovered"`
	Accessible bool   `json:"accessible"`
	Installed  bool   `json:"installed"`
	Enabled    bool   `json:"enabled"`
	Callable   bool   `json:"callable"`
	Selected   bool   `json:"selected"`
}

func (a CodexApp) Validate() error {
	if Text(a.ID, "Codex app identity", 1024, true) != nil || Text(a.Name, "Codex app name", 4096, false) != nil || a.Callable && (!a.Installed || !a.Enabled) {
		return invalidCodexApps()
	}
	return nil
}

func invalidCodexApps() error {
	return Fail(InvalidArgument, "Invalid original Codex app configuration.", "Preserve the original session, account, immutable generation and bounded unique app selection.")
}
