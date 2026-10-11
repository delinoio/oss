// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"testing"
)

func TestMCPSelectionPresenceAndImportedRebinding(t *testing.T) {
	for _, raw := range []string{`{}`, `{"mcp":{"selections":[]}}`} {
		var agent Agent
		if Decode([]byte(raw), &agent) != nil {
			t.Fatal("invalid presence fixture")
		}
		if (agent.MCP == nil) != (raw == `{}`) {
			t.Fatal("omission and explicit clear collapsed")
		}
	}
	imported := MCPSelectionList{Selections: []MCPSelection{{DefinitionID: NewID(), Unresolved: true}}}
	if ValidateMCPSelections(&imported) != nil {
		t.Fatal("safe unresolved reference rejected")
	}
	imported.Selections[0].Unresolved = false
	if ValidateMCPSelections(&imported) == nil {
		t.Fatal("unbound import acquired Worker authority")
	}
}
func TestMCPClosedDocumentsAndCredentialFreeEndpoints(t *testing.T) {
	mutation := MCPMutation{Version: 1, Action: MCPDelete, DefinitionID: NewID(), ExpectedRevision: 1, Confirmed: true}
	raw, _ := json.Marshal(mutation)
	raw = append(raw[:len(raw)-1], []byte(`,"unknown":true}`)...)
	var parsed MCPMutation
	if Decode(raw, &parsed) == nil {
		t.Fatal("unknown MCP mutation field accepted")
	}
	for _, endpoint := range []string{"http://mcp.example.invalid", "https://secret@mcp.example.invalid", "https://mcp.example.invalid?token=secret"} {
		if MCPURL(endpoint) == nil {
			t.Fatal("credential-bearing or insecure endpoint accepted")
		}
	}
	if MCPURL("https://mcp.example.invalid/rpc") != nil {
		t.Fatal("safe endpoint rejected")
	}
}
