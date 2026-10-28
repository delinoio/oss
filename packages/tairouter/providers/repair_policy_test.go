// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"encoding/json"
	"testing"
)

func TestTypedResponseRepairPolicy(t *testing.T) {
	var p Preset
	raw := []byte(`{"responsesItemIdRepair":{"message":["msg_"],"reasoning":[],"repairInvalidIds":false,"repairMissingTerminalIds":true},"showThinkingSummary":false,"escapeBuiltinToolNames":true,"unrecognizedNumber":9007199254740993}`)
	if e := json.Unmarshal(raw, &p); e != nil {
		t.Fatal(e)
	}
	r := p.ResponsesItemIDRepair
	if r == nil || len(r.Message) != 1 || r.Reasoning == nil || r.RepairInvalidIDs == nil || *r.RepairInvalidIDs || r.RepairMissingTerminalIDs == nil || !*r.RepairMissingTerminalIDs || p.ShowThinkingSummary == nil || *p.ShowThinkingSummary || p.EscapeBuiltinToolNames == nil || !*p.EscapeBuiltinToolNames {
		t.Fatalf("optional object lost %+v", p)
	}
	if e := json.Unmarshal([]byte(`{"responsesItemIdRepair":true}`), &p); e == nil {
		t.Fatal("scalar repair policy accepted")
	}
	for _, row := range Catalog() {
		if row.ResponsesItemIDRepair != nil {
			var original map[string]json.RawMessage
			if e := json.Unmarshal(row.Metadata, &original); e != nil {
				t.Fatal(e)
			}
			if len(original["responsesItemIdRepair"]) == 0 {
				t.Fatal("missing raw repair metadata")
			}
			*row.ResponsesItemIDRepair.RepairInvalidIDs = false
			fresh, _ := Lookup(row.ID)
			if fresh.ResponsesItemIDRepair.RepairInvalidIDs == nil || !*fresh.ResponsesItemIDRepair.RepairInvalidIDs {
				t.Fatal("shared mutable nested policy")
			}
			return
		}
	}
	t.Fatal("pinned repair policy missing")
}
