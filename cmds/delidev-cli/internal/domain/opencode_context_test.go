// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestOpenCodeContextRetainsOriginalMetadataWithoutInventingLegacyDefaults(t *testing.T) {
	for _, source := range []EvidenceSource{Known, UserDeclared} {
		limit := uint64(80000)
		modelID, providerID, accountID := NewID(), NewID(), NewID()
		agent := Agent{Name: "Original", Harness: OpenCode, ModelID: modelID, Accounts: []WeightedAccount{{ID: accountID, Weight: 1}}, Options: AgentOptions{Permission: PermissionDefault}}
		model := Model{Name: "Original", NativeID: "original-model", ProviderID: providerID, Harnesses: []Harness{OpenCode}, ContextLimit: &limit, MetadataSource: source}
		snapshot, err := ResolveExecutionConfiguration(NewID(), 1, agent, 1, model, Priority, nil)
		if err != nil || snapshot.Validate() != nil || snapshot.OpenCodeContext == nil || snapshot.OpenCodeContext.Tokens != limit || snapshot.OpenCodeContext.Source != source || snapshot.OpenCodeContext.Policy != OpenCodeNativeContextV1 {
			t.Fatal("context metadata lost original selection", err)
		}
		limit = 90000
		if snapshot.OpenCodeContext.Tokens != 80000 {
			t.Fatal("later model metadata rewrote immutable native context")
		}
		snapshot.Harness = Codex
		if snapshot.Validate() == nil {
			t.Fatal("OpenCode context acquired another harness authority")
		}
		model.ContextLimit = nil
		legacy, err := ResolveExecutionConfiguration(NewID(), 1, agent, 1, model, Priority, nil)
		raw, _ := json.Marshal(legacy)
		if err != nil || legacy.OpenCodeContext != nil || bytes.Contains(raw, []byte("opencode_context")) {
			t.Fatal("legacy snapshot acquired a fabricated context default", err)
		}
	}
	for _, value := range []OpenCodeModelContext{{Tokens: 1023, Source: Known}, {Tokens: 1_000_000_001, Source: Known}, {Tokens: 80000, Source: Unknown}, {Tokens: 80000, Source: Known, Policy: "unrecognized"}} {
		if value.Validate() == nil {
			t.Fatal("unavailable or unsupported context accepted")
		}
	}
}
