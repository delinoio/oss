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

func TestSessionNativeAppsOriginalCancelNeverAdmitsEffectAfterRevocation(t *testing.T) {
	scope := NativeAppScope{SessionID: NewID(), MachineID: NewID(), AccountID: NewID(), ConnectionID: NewID(), ConfigurationDigest: strings.Repeat("a", 64)}
	inventory := NativeAppInventory{Scope: scope, InventoryID: NewID(), Discovered: []NativeAppDiscovery{{ID: "original", Name: "Original App", Accessible: true}}, Installed: []NativeAppInstalled{{ID: "original", Enabled: true, Callable: true}}}
	selection := SessionNativeAppSelection{Scope: scope, InventoryID: inventory.InventoryID, Revision: 1, AppIDs: []string{"original"}}
	proof := NativeAppCallProof{Scope: scope, Selection: selection, Inventory: inventory, NativeItemID: "original-call", AppID: "original"}
	questionID := "mcp_tool_call_approval_original-call"
	questions := &QuestionRequest{Blocking: true, Questions: []Question{{ID: questionID, Header: "Approve app tool call?", Text: "Allow the original App?", Options: []QuestionOption{{Label: "Allow", Description: "Run the tool and continue."}, {Label: "Cancel", Description: "Cancel this tool call."}}}}}
	value := ExecutionInteraction{NativeApps: &proof, NativeItemID: proof.NativeItemID, Type: UserQuestionInteraction, Questions: questions, Response: &QuestionResponse{Input: QuestionResponseInput{Answers: map[string][]string{questionID: {"Cancel"}}}}}
	if admits, err := NativeAppsResponseAdmitsEffect(value); err != nil || admits {
		t.Fatal("original cancellation borrowed effect authority", err)
	}
	revoked := selection
	revoked.Revision++
	revoked.AppIDs = []string{}
	if AdmitNativeAppCall(selection, revoked, inventory, "original") == nil {
		t.Fatal("revoked Allow was admitted")
	}
	for _, answer := range []string{"Allow for this session", "allow", "", "foreign"} {
		value.Response.Input.Answers = map[string][]string{questionID: {answer}}
		if admits, err := NativeAppsResponseAdmitsEffect(value); err == nil || admits {
			t.Fatal("unknown or remembered response admitted", answer)
		}
	}
	value.Response.Input.Answers = map[string][]string{questionID: {"Allow"}}
	if admits, err := NativeAppsResponseAdmitsEffect(value); err != nil || !admits {
		t.Fatal("exact original one-call Allow lost", err)
	}
}
