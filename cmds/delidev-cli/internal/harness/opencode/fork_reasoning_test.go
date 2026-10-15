// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const (
	reasoningForkChildID  = "ses_01960dcbe1fcABCDEFGHIJKLMN"
	reasoningSourcePartID = "prt_01960dcbe1f01234567890ABCD"
	reasoningChildPartID  = "prt_01960dcbe1ff1234567890ABCD"
)

// reasoningForkHistories builds a complete source history and its byte-faithful
// clone. The assistant message gets one extra plain timed reasoning part before
// its original parts, in both histories, so ordering is preserved.
func reasoningForkHistories(t *testing.T, mutate func(part map[string]any)) (source, child []json.RawMessage) {
	t.Helper()
	f := newHistoryFixture(t)
	mapping := map[string]string{}
	for index, id := range f.o.messageOrder {
		var info map[string]any
		if json.Unmarshal(f.o.messages[id].raw, &info) != nil {
			t.Fatal("invalid fixture message")
		}
		parts := []any{}
		for _, partID := range f.o.messages[id].parts {
			var part map[string]any
			_ = json.Unmarshal(f.o.parts[partID].raw, &part)
			parts = append(parts, part)
		}
		if index > 0 {
			reasoning := map[string]any{"id": reasoningSourcePartID, "sessionID": fixtureSessionID, "messageID": id, "type": "reasoning", "text": "private reasoning", "time": map[string]any{"start": 1000, "end": 1001}}
			if mutate != nil {
				mutate(reasoning)
			}
			parts = append([]any{reasoning}, parts...)
		}
		raw, _ := json.Marshal(map[string]any{"info": info, "parts": parts})
		source = append(source, raw)
		nextID := "msg_01960dcbe1fc1234567890ABCD"
		if index > 0 {
			nextID = "msg_01960dcbe1fd1234567890ABCD"
		}
		mapping[id] = nextID
		info["id"], info["sessionID"] = nextID, reasoningForkChildID
		if parent, ok := info["parentID"].(string); ok {
			info["parentID"] = mapping[parent]
		}
		childIDs := []string{"prt_01960dcbe1fc1234567890ABCD", "prt_01960dcbe1fd1234567890ABCD", "prt_01960dcbe1fe1234567890ABCD"}
		clones := []any{}
		for i, part := range parts {
			// Clone through JSON so source and child never alias one map.
			encoded, _ := json.Marshal(part)
			var clone map[string]any
			_ = json.Unmarshal(encoded, &clone)
			if clone["type"] == "reasoning" {
				clone["id"] = reasoningChildPartID
			} else {
				clone["id"] = childIDs[i]
			}
			clone["messageID"], clone["sessionID"] = nextID, reasoningForkChildID
			clones = append(clones, clone)
		}
		raw, _ = json.Marshal(map[string]any{"info": info, "parts": clones})
		child = append(child, raw)
	}
	return source, child
}

func TestForkHistoryClonesReasoningOnlyUnderProfileV2(t *testing.T) {
	source, child := reasoningForkHistories(t, nil)
	ids, history, err := compareForkHistory(source, child, fixtureSessionID, reasoningForkChildID, true)
	if err != nil || len(ids) != 2 || len(history) != 2 {
		t.Fatal("reasoning clone rejected by profile v2", err)
	}
	assistant := ids[1]
	if len(assistant.Parts) != 3 || assistant.Parts[0].Source != reasoningSourcePartID || assistant.Parts[0].Child != reasoningChildPartID {
		t.Fatalf("reasoning keeps its own identity and original order: %#v", assistant.Parts)
	}
	if history[1].Parts[0].Kind != ReasoningPartKind {
		t.Fatal("reasoning kind was not preserved in the child history")
	}
	if _, _, err := compareForkHistory(source, child, fixtureSessionID, reasoningForkChildID, false); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("profile v1 adopted reasoning", err)
	}
	if err := checkForkSourceParts(source, true); err != nil {
		t.Fatal("complete reasoning source rejected before Fork", err)
	}
	if err := checkForkSourceParts(source, false); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("profile v1 source with reasoning passed the pre-fork gate", err)
	}
}

func TestForkHistoryRejectsIneligibleReasoningBeforeAnyChild(t *testing.T) {
	for name, mutate := range map[string]func(part map[string]any){
		"metadata":  func(part map[string]any) { part["metadata"] = map[string]any{"signature": "x"} },
		"synthetic": func(part map[string]any) { part["synthetic"] = true },
		"ignored":   func(part map[string]any) { part["ignored"] = false },
		"untimed":   func(part map[string]any) { part["time"] = map[string]any{"start": 1000} },
	} {
		t.Run(name, func(t *testing.T) {
			source, child := reasoningForkHistories(t, mutate)
			if err := checkForkSourceParts(source, true); domain.SafeError(err).Code != domain.Unsupported {
				t.Fatal("ineligible reasoning passed the pre-fork gate", err)
			}
			// Synthetic/ignored reasoning is already outside the strict native
			// decoder, so only the error class differs from the field checks.
			if _, _, err := compareForkHistory(source, child, fixtureSessionID, reasoningForkChildID, true); err == nil {
				t.Fatal("ineligible reasoning was compared")
			}
		})
	}
}

func TestForkHistoryRejectsChangedReasoningClone(t *testing.T) {
	for name, mutate := range map[string]func(part map[string]any){
		"text":     func(part map[string]any) { part["text"] = "foreign reasoning" },
		"time":     func(part map[string]any) { part["time"] = map[string]any{"start": 1000, "end": 2000} },
		"metadata": func(part map[string]any) { part["metadata"] = map[string]any{"signature": "x"} },
		"kind":     func(part map[string]any) { part["type"] = "text" },
	} {
		t.Run(name, func(t *testing.T) {
			source, child := reasoningForkHistories(t, nil)
			var value map[string]any
			_ = json.Unmarshal(child[1], &value)
			parts := value["parts"].([]any)
			mutate(parts[0].(map[string]any))
			child[1], _ = json.Marshal(value)
			if _, _, err := compareForkHistory(source, child, fixtureSessionID, reasoningForkChildID, true); err == nil {
				t.Fatal("changed reasoning clone adopted")
			}
		})
	}
}

func TestForkPartKindAllowListGatesReasoningByProfile(t *testing.T) {
	for _, kind := range []PartKind{TextPartKind, StepStartPartKind, StepFinishPartKind} {
		if !forkPartKindAllowed(kind, false) || !forkPartKindAllowed(kind, true) {
			t.Fatalf("%s must stay allowed in both profiles", kind)
		}
	}
	if forkPartKindAllowed(ReasoningPartKind, false) || !forkPartKindAllowed(ReasoningPartKind, true) {
		t.Fatal("reasoning belongs to profile v2 only")
	}
	for _, kind := range []PartKind{ToolPartKind, FilePartKind, SnapshotPartKind, PatchPartKind, AgentPartKind, RetryPartKind, CompactionPartKind, SubtaskPartKind} {
		if forkPartKindAllowed(kind, true) {
			t.Fatalf("%s must stay rejected", kind)
		}
	}
}

func TestForkCheckpointProofRecordsProfileOnlyForV2(t *testing.T) {
	v1, _ := json.Marshal(nativeCheckpointFork{Version: 1})
	v2, _ := json.Marshal(nativeCheckpointFork{Version: 1, Reasoning: true})
	if string(v1) == string(v2) {
		t.Fatal("profile marker missing")
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(v1, &raw)
	if _, present := raw["reasoning"]; present {
		t.Fatal("profile v1 bytes changed")
	}
}
