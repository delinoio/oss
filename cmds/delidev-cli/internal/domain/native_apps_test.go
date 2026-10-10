// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func appFixture() (SessionNativeAppSelection, NativeAppInventory) {
	scope := NativeAppScope{SessionID: NewID(), MachineID: NewID(), AccountID: NewID(), ConnectionID: NewID(), ConfigurationDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	inventory := NativeAppInventory{Scope: scope, InventoryID: NewID(), Discovered: []NativeAppDiscovery{{ID: "connector-original", Name: "Original App", Accessible: true, Enabled: true}}, Installed: []NativeAppInstalled{{ID: "connector-original", Enabled: true, Callable: true}}}
	selection := SessionNativeAppSelection{Scope: scope, InventoryID: inventory.InventoryID, Revision: 1, AppIDs: []string{"connector-original"}}
	return selection, inventory
}
func TestSessionNativeAppsOriginalCallAdmission(t *testing.T) {
	selection, inventory := appFixture()
	if AdmitNativeAppCall(selection, selection, inventory, "connector-original") != nil {
		t.Fatal("original explicitly selected callable app denied")
	}
	tests := []struct {
		name   string
		mutate func(*SessionNativeAppSelection, *NativeAppInventory)
		app    string
	}{
		{"unselected", func(s *SessionNativeAppSelection, _ *NativeAppInventory) { s.AppIDs = []string{} }, "connector-original"},
		{"revoked", func(s *SessionNativeAppSelection, _ *NativeAppInventory) { s.Revision++; s.AppIDs = []string{} }, "connector-original"},
		{"later regrant", func(s *SessionNativeAppSelection, _ *NativeAppInventory) { s.Revision += 2 }, "connector-original"},
		{"account changed", func(s *SessionNativeAppSelection, _ *NativeAppInventory) { s.Scope.AccountID = NewID() }, "connector-original"},
		{"connection changed", func(s *SessionNativeAppSelection, _ *NativeAppInventory) { s.Scope.ConnectionID = NewID() }, "connector-original"},
		{"configuration changed", func(s *SessionNativeAppSelection, _ *NativeAppInventory) {
			s.Scope.ConfigurationDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}, "connector-original"},
		{"installed without callability", func(_ *SessionNativeAppSelection, v *NativeAppInventory) { v.Installed[0].Callable = false }, "connector-original"},
		{"disabled", func(_ *SessionNativeAppSelection, v *NativeAppInventory) {
			v.Installed[0].Callable = false
			v.Installed[0].Enabled = false
		}, "connector-original"},
		{"discovery only", func(_ *SessionNativeAppSelection, v *NativeAppInventory) { v.Installed = []NativeAppInstalled{} }, "connector-original"},
		{"unavailable", func(_ *SessionNativeAppSelection, v *NativeAppInventory) { v.Discovered[0].Accessible = false }, "connector-original"},
		{"foreign inventory", func(_ *SessionNativeAppSelection, v *NativeAppInventory) { v.Scope.SessionID = NewID() }, "connector-original"},
		{"foreign call", func(_ *SessionNativeAppSelection, _ *NativeAppInventory) {}, "connector-foreign"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			frozen, inv := appFixture()
			current := frozen
			test.mutate(&current, &inv)
			if AdmitNativeAppCall(frozen, current, inv, test.app) == nil {
				t.Fatal("unproved or revoked authority admitted")
			}
		})
	}
}
func TestSessionNativeAppsContradictoryOrDuplicateSnapshot(t *testing.T) {
	_, inventory := appFixture()
	inventory.Installed[0].Enabled = false
	if inventory.Validate() == nil {
		t.Fatal("callable disabled app accepted")
	}
	_, inventory = appFixture()
	inventory.Installed = append(inventory.Installed, inventory.Installed[0])
	if inventory.Validate() == nil {
		t.Fatal("duplicate installed identity accepted")
	}
	_, inventory = appFixture()
	inventory.Discovered = nil
	if inventory.Validate() == nil {
		t.Fatal("missing discovery mistaken for complete empty inventory")
	}
}
