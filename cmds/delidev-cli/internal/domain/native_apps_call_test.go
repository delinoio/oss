// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
)

func TestSessionNativeAppsCallProofRejectsForeignAndUnverifiedOriginalCalls(t *testing.T) {
	scope := NativeAppScope{SessionID: NewID(), MachineID: NewID(), AccountID: NewID(), ConnectionID: NewID(), ConfigurationDigest: strings.Repeat("a", 64)}
	inventory := NativeAppInventory{Scope: scope, InventoryID: NewID(), Discovered: []NativeAppDiscovery{{ID: "original", Name: "Original App", Accessible: true, Enabled: true}}, Installed: []NativeAppInstalled{{ID: "original", Enabled: true, Callable: true}}}
	selection := SessionNativeAppSelection{Scope: scope, InventoryID: inventory.InventoryID, Revision: 1, AppIDs: []string{"original"}}
	proof := NativeAppCallProof{Scope: scope, Selection: selection, Inventory: inventory, NativeItemID: "native-original-call", AppID: "original"}
	for _, scenario := range []string{"original", "foreign-scope", "foreign-item", "foreign-app", "unselected", "not-callable"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := proof
			switch scenario {
			case "foreign-scope":
				candidate.Scope.AccountID = NewID()
			case "foreign-item":
				candidate.NativeItemID = ""
			case "foreign-app":
				candidate.AppID = "foreign"
			case "unselected":
				candidate.Selection.AppIDs = []string{}
			case "not-callable":
				candidate.Inventory.Installed = []NativeAppInstalled{{ID: "original", Enabled: true, Callable: false}}
			}
			if (candidate.Validate() == nil) != (scenario == "original") {
				t.Fatal("foreign or unverified effect proof admitted")
			}
		})
	}
}
