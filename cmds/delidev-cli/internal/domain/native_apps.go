// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/hex"
	"slices"
	"strings"
)

const MaxSessionNativeApps = 256

// NativeAppScope is original account/configuration ownership, never an account
// selected later by routing or a connector name supplied by a product client.
type NativeAppScope struct {
	SessionID           ID     `json:"session_id"`
	MachineID           ID     `json:"machine_id"`
	AccountID           ID     `json:"account_id"`
	ConnectionID        ID     `json:"connection_id"`
	ConfigurationDigest string `json:"configuration_digest"`
}

func nativeAppDigestValid(v string) bool {
	decoded, err := hex.DecodeString(v)
	return err == nil && len(decoded) == 32 && strings.ToLower(v) == v
}
func (s NativeAppScope) Validate() error {
	if s.SessionID.Validate() != nil || s.MachineID.Validate() != nil || s.AccountID.Validate() != nil || s.ConnectionID.Validate() != nil || !nativeAppDigestValid(s.ConfigurationDigest) {
		return NativeAppsUnavailable()
	}
	return nil
}

type NativeAppDiscovery struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Accessible bool   `json:"accessible"`
	Enabled    bool   `json:"enabled"`
}
type NativeAppInstalled struct {
	ID       string `json:"id"`
	Enabled  bool   `json:"enabled"`
	Callable bool   `json:"callable"`
}
type NativeAppInventory struct {
	Scope       NativeAppScope       `json:"scope"`
	InventoryID ID                   `json:"inventory_id"`
	Discovered  []NativeAppDiscovery `json:"discovered"`
	Installed   []NativeAppInstalled `json:"installed"`
}

func NativeAppIDValid(id string) bool {
	return Text(id, "native app identity", 1024, true) == nil && strings.TrimSpace(id) == id
}
func NativeAppsUnavailable() error {
	return Fail(Unavailable, "Native Apps are unavailable in this session.", "Refresh the original account's Apps inventory in an eligible session.")
}
func (v NativeAppInventory) Validate() error {
	if v.Scope.Validate() != nil || v.InventoryID.Validate() != nil || v.Discovered == nil || v.Installed == nil || len(v.Discovered) > MaxSessionNativeApps || len(v.Installed) > MaxSessionNativeApps {
		return NativeAppsUnavailable()
	}
	seen := map[string]bool{}
	for _, app := range v.Discovered {
		if !NativeAppIDValid(app.ID) || Text(app.Name, "native app name", 1024, true) != nil || seen[app.ID] {
			return NativeAppsUnavailable()
		}
		seen[app.ID] = true
	}
	seen = map[string]bool{}
	for _, app := range v.Installed {
		if !NativeAppIDValid(app.ID) || seen[app.ID] || app.Callable && !app.Enabled {
			return NativeAppsUnavailable()
		}
		seen[app.ID] = true
	}
	return nil
}

type SessionNativeAppSelection struct {
	Scope       NativeAppScope `json:"scope"`
	InventoryID ID             `json:"inventory_id"`
	Revision    uint64         `json:"revision"`
	AppIDs      []string       `json:"app_ids"`
}

func (s SessionNativeAppSelection) Validate() error {
	if s.Scope.Validate() != nil || s.InventoryID.Validate() != nil || s.Revision == 0 || s.AppIDs == nil || len(s.AppIDs) > MaxSessionNativeApps {
		return NativeAppsUnavailable()
	}
	seen := map[string]bool{}
	for _, id := range s.AppIDs {
		if !NativeAppIDValid(id) || seen[id] {
			return NativeAppsUnavailable()
		}
		seen[id] = true
	}
	return nil
}

// AdmitNativeAppCall checks both the frozen original selection and current
// selection. Revocation can remove authority but cannot lend a later selection
// or account generation to an old call. Installed metadata alone grants nothing.
func AdmitNativeAppCall(frozen, current SessionNativeAppSelection, inventory NativeAppInventory, appID string) error {
	if frozen.Validate() != nil || current.Validate() != nil || inventory.Validate() != nil || !NativeAppIDValid(appID) || frozen.Scope != current.Scope || frozen.Scope != inventory.Scope || current.Revision != frozen.Revision || !slices.Contains(frozen.AppIDs, appID) || !slices.Contains(current.AppIDs, appID) {
		return NativeAppsUnavailable()
	}
	discovered := false
	for _, app := range inventory.Discovered {
		if app.ID == appID && app.Accessible {
			discovered = true
			break
		}
	}
	if !discovered {
		return NativeAppsUnavailable()
	}
	for _, app := range inventory.Installed {
		if app.ID == appID && app.Enabled && app.Callable {
			return nil
		}
	}
	return NativeAppsUnavailable()
}
