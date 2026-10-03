// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
)

func TestContinuationContextRequiresCompleteUnchangedHistory(t *testing.T) {
	turn := domain.NewID()
	wire := fixtureTurn(turn, TurnCompleted)
	wire["itemsView"] = "full"
	wire["items"] = []any{map[string]any{"type": "contextCompaction", "id": "item-1"}}
	raw, _ := json.Marshal(wire)
	turns := []json.RawMessage{raw}
	proof := ContinuationContextCheckpoint{Version: 1, TurnsCount: 1, HistoryDigest: historyDigest(turns), RolloutDigest: strings.Repeat("0", 64), RolloutPath: "/private/original", Records: []ContextRecord{{Trigger: ManualCompaction, ActionID: domain.NewID(), TurnID: turn, LiveItemID: "original-live-item", HistoryItemID: "item-1"}}}
	if contextMatchesHistory(proof, turns) != nil {
		t.Fatal("complete original context rejected")
	}
	for _, change := range []func(*ContinuationContextCheckpoint){func(p *ContinuationContextCheckpoint) { p.Records[0].TurnID = domain.NewID() }, func(p *ContinuationContextCheckpoint) { p.Records[0].HistoryItemID = "item-2" }, func(p *ContinuationContextCheckpoint) { p.Records[0].Trigger = "unknown" }, func(p *ContinuationContextCheckpoint) { p.HistoryDigest = strings.Repeat("1", 64) }, func(p *ContinuationContextCheckpoint) { p.Records = nil }, func(p *ContinuationContextCheckpoint) { p.TurnsCount = 2 }, func(p *ContinuationContextCheckpoint) { p.Records[0].Trigger = AutomaticCompaction }} {
		p := cloneContinuationContext(&proof)
		change(p)
		if contextMatchesHistory(*p, turns) == nil {
			t.Fatal("foreign, missing or changed context accepted")
		}
	}
	clone := cloneContinuationContext(&proof)
	clone.Records[0].LiveItemID = "changed"
	if proof.Records[0].LiveItemID != "original-live-item" {
		t.Fatal("retained context shared caller mutation")
	}
}
