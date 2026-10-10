// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestGoalHistoryRejectsActiveForeignAndIncompleteNativeSnapshots(t *testing.T) {
	thread, turn := domain.NewID(), domain.NewID()
	native := goalFixture(thread)
	native["status"] = "paused"
	raw, _ := json.Marshal(native)
	p := GoalHistoryCheckpoint{Version: 1, Goal: raw, TurnsCount: 1, HistoryDigest: strings.Repeat("a", 64), RolloutDigest: strings.Repeat("b", 64), RolloutPath: "/original/private/sessions/source.jsonl", InputTurnID: turn}
	if p.validate(thread) != nil {
		t.Fatal("non-active original goal rejected")
	}
	if p.validate(domain.NewID()) == nil {
		t.Fatal("foreign native goal admitted")
	}
	native["status"] = "active"
	p.Goal, _ = json.Marshal(native)
	if p.validate(thread) == nil {
		t.Fatal("active goal admitted for Resume")
	}
	p.Goal = nil
	if p.validate(thread) == nil {
		t.Fatal("unknown goal converted to absence")
	}
	p.Goal = json.RawMessage("null")
	if p.validate(thread) != nil {
		t.Fatal("observed native absence rejected")
	}
	p.OwnedTurns = []domain.ID{turn, turn}
	if p.validate(thread) == nil {
		t.Fatal("repeated native goal turn admitted")
	}
}
func TestGoalHistoryNeverManufacturesProductInputsFromNativeContext(t *testing.T) {
	turn := domain.NewID()
	raw, _ := json.Marshal(map[string]any{"id": turn, "rootTurnId": nil, "itemsView": "full", "status": "completed", "error": nil, "startedAt": nil, "completedAt": nil, "durationMs": nil, "items": []any{map[string]any{"type": "userMessage", "id": "original-native-context", "clientId": nil, "content": []any{map[string]any{"type": "text", "text": "native-owned goal context", "text_elements": []any{}}}}}})
	_, inputs, err := decodeGoalTurnInputs(raw, func(parts []json.RawMessage) (domain.SessionInput, error) {
		t.Fatal("native context was treated as product input")
		return domain.SessionInput{}, nil
	})
	if err != nil || len(inputs) != 0 {
		t.Fatal("native context manufactured an input", err)
	}
	if _, _, err := decodeLatestTurnInputs(marshalForkPage([]json.RawMessage{raw})); err == nil {
		t.Fatal("legacy profile accepted unattributed goal context")
	}
}
func TestNativeGoalToolOutputsRemainClosedPrivateObservations(t *testing.T) {
	for _, name := range []string{"create_goal", "get_goal", "update_goal"} {
		raw, _ := json.Marshal(map[string]any{"type": "functionCallOutput", "id": "native-original-output", "name": name, "namespace": nil, "output": "{\"goal\":null}"})
		if !nativeGoalToolOutput(raw) {
			t.Fatal("closed native output rejected")
		}
	}
	for _, item := range []map[string]any{{"type": "functionCallOutput", "id": "native-original-output", "name": "run_commands", "namespace": nil, "output": "{}"}, {"type": "functionCallOutput", "id": "native-original-output", "name": "get_goal", "namespace": "foreign", "output": "{}"}, {"type": "functionCallOutput", "id": "native-original-output", "name": "get_goal", "namespace": nil, "output": []any{}}} {
		raw, _ := json.Marshal(item)
		if nativeGoalToolOutput(raw) {
			t.Fatal("foreign native tool adopted")
		}
	}
}

func TestGoalRootIdentityIsOriginalPerTurnMetadata(t *testing.T) {
	id, root := domain.NewID(), domain.NewID()
	a := Turn{ID: id, RootTurnID: &root}
	if !sameRootTurn(a, a) || sameRootTurn(a, Turn{ID: id}) {
		t.Fatal("original root identity was replaced")
	}
	changed := domain.NewID()
	if sameRootTurn(a, Turn{ID: id, RootTurnID: &changed}) {
		t.Fatal("foreign root admitted on original turn")
	}
	if !sameRootTurn(Turn{ID: id}, Turn{ID: id}) {
		t.Fatal("legacy nullable root rejected")
	}
}

func TestGoalHistoryContentDoesNotReclassifyGoalFreeInputs(t *testing.T) {
	input := domain.NewID()
	turn := map[string]any{"id": domain.NewID(), "rootTurnId": nil, "itemsView": "full", "status": "completed", "error": nil, "items": []any{map[string]any{"type": "userMessage", "id": "original-input", "clientId": input, "content": []any{}}}}
	raw, _ := json.Marshal(turn)
	if nativeGoalHistoryContent(raw) {
		t.Fatal("ordinary input became goal-bearing")
	}
	item := turn["items"].([]any)[0].(map[string]any)
	item["clientId"] = nil
	raw, _ = json.Marshal(turn)
	if !nativeGoalHistoryContent(raw) {
		t.Fatal("source-owned native annotation lost")
	}
}
