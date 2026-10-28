// SPDX-License-Identifier: Apache-2.0
package cursor

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func prepareRootFixture(t *testing.T, messages []any, saved *Continuation) ([]rootCandidate, *Session, message) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"messages": messages})
	request, s, err := Prepare(context.Background(), input(string(body)), saved)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := ReadFrame(bytes.NewReader(request.Body))
	if err != nil {
		t.Fatal(err)
	}
	outer, _ := parse(frame.Payload)
	run, _ := outer.sub(1)
	state, _ := run.sub(1)
	var roots []rootCandidate
	for _, f := range state {
		if f.number == 1 {
			data, ok := s.blobs[hex.EncodeToString(f.data)]
			if !ok {
				t.Fatal("missing blob")
			}
			var value map[string]any
			if json.Unmarshal(data, &value) != nil {
				t.Fatal("bad JSON")
			}
			text, _ := textContent(value["content"])
			roots = append(roots, rootCandidate{role: str(value["role"]), text: text, data: data})
		}
	}
	if len(roots) > MaxRoots || rootBytes(roots) > MaxRootBytes {
		t.Fatal("root envelope exceeded")
	}
	return roots, s, state
}
func TestRootPruningKeepsRecentCompleteTurns(t *testing.T) {
	messages := []any{map[string]any{"role": "system", "content": "never remove this instruction"}}
	for i := 0; i < 120; i++ {
		messages = append(messages, map[string]any{"role": "user", "content": fmt.Sprintf("user-%03d", i)}, map[string]any{"role": "assistant", "content": fmt.Sprintf("answer-%03d", i)})
	}
	messages = append(messages, map[string]any{"role": "user", "content": "active"})
	roots, _, _ := prepareRootFixture(t, messages, nil)
	if len(roots) != 191 || roots[0].text != "never remove this instruction" || roots[1].text != "user-025" || roots[len(roots)-1].text != "answer-119" {
		t.Fatalf("retained roots=%d first=%q last=%q", len(roots), roots[1].text, roots[len(roots)-1].text)
	}
	for i := 1; i < len(roots); i += 2 {
		if roots[i].role != "user" || roots[i+1].role != "assistant" {
			t.Fatal("partial turn retained")
		}
	}
}
func toolBatch(n, size int) []any {
	messages := []any{map[string]any{"role": "user", "content": "inspect all requested objects"}}
	var calls []any
	for i := 0; i < n; i++ {
		calls = append(calls, map[string]any{"id": fmt.Sprint(i), "type": "function", "function": map[string]any{"name": "inspect", "arguments": fmt.Sprintf("{\"object\":%d}", i)}})
	}
	messages = append(messages, map[string]any{"role": "assistant", "tool_calls": calls})
	for i := 0; i < n; i++ {
		messages = append(messages, map[string]any{"role": "tool", "tool_call_id": fmt.Sprint(i), "content": fmt.Sprintf("answer-%d:", i) + strings.Repeat("雪\"\n", size)})
	}
	return messages
}
func TestRootPruningSharesResultsAndRecoversInitiator(t *testing.T) {
	roots, _, _ := prepareRootFixture(t, toolBatch(3, 35000), nil)
	seen := 0
	for _, r := range roots {
		if strings.Contains(r.text, "\noutput:\n") {
			seen++
			if !strings.Contains(r.text, "answer-") {
				t.Fatal("result lost its output")
			}
		}
	}
	if seen != 3 || roots[1].role != "user" {
		t.Fatalf("results=%d initiator=%s", seen, roots[1].role)
	}
	if !strings.Contains(roots[len(roots)-1].text, replayTruncation) {
		t.Fatal("truncation not disclosed")
	}
}
func TestCheckpointPressureFallsBackBeforeLosingSiblingResult(t *testing.T) {
	var old []any
	for i := 0; i < 94; i++ {
		old = append(old, map[string]any{"role": "user", "content": fmt.Sprintf("old-%d", i)}, map[string]any{"role": "assistant", "content": "observed"})
	}
	old = append(old, map[string]any{"role": "assistant", "content": "extra narration"}, map[string]any{"role": "user", "content": "inspect all requested objects"})
	_, s, state := prepareRootFixture(t, old, nil)
	// Preserve the fixture's actual roots and an unmistakable checkpoint-only field.
	var checkpoint []byte
	for _, f := range state {
		if f.number == 1 {
			checkpoint = append(checkpoint, pb(1, f.data)...)
		}
	}
	s.checkpoint = append(checkpoint, ps(10, "checkpoint-only")...)
	saved, err := s.Snapshot(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	batch := toolBatch(3, 3)
	next := append(append([]any(nil), old...), batch[1:]...)
	roots, _, rebuilt := prepareRootFixture(t, next, saved)
	if rebuilt.get(10).number != 0 {
		t.Fatal("exhausted checkpoint was retained")
	}
	for i := 0; i < 3; i++ {
		found := false
		for _, r := range roots {
			found = found || strings.Contains(r.text, fmt.Sprintf("answer-%d:", i))
		}
		if !found {
			t.Fatalf("lost sibling result %d", i)
		}
	}
}
func TestRootInstructionOverflowStillRefuses(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "system", "content": strings.Repeat("x", MaxRootBytes)}, map[string]any{"role": "user", "content": "active"}}})
	_, _, err := Prepare(context.Background(), input(string(body)), nil)
	requireCode(t, err, "cursor_root_envelope_limit")
}
func TestInvocationRefundDoesNotRestoreTruncatedOutput(t *testing.T) {
	history := []historyMessage{{Role: "user", Text: "inspect"}, {Role: "assistant", Calls: []historyCall{{ID: "c", Name: "inspect", Arguments: strings.Repeat("x", 4000)}}}, {Role: "tool", CallID: "c", Text: strings.Repeat("y", 9000)}}
	_, roots, err := rootCandidates(history, -1, 0, "gpt-5.4")
	if err != nil {
		t.Fatal(err)
	}
	small := roots[len(roots)-1]
	shrunk, ok := shrinkRoot(small, 5000)
	if !ok || shrunk.elided {
		t.Fatal("fixture")
	}
	widened := refundInvocationSpace([]rootCandidate{shrunk}, 0)[0]
	if !strings.Contains(widened.text, strings.Repeat("x", 4000)) || !strings.Contains(widened.text, replayTruncation) || strings.Contains(widened.text, strings.Repeat("y", 9000)) {
		t.Fatal("refund changed selected tool output")
	}
}

func TestRootRepetitionNoteReservesBudget(t *testing.T) {
	history := []historyMessage{{Role: "user", Text: "inspect"}}
	for i := 0; i < 3; i++ {
		history = append(history, historyMessage{Role: "assistant", Text: "checking", Calls: []historyCall{{ID: fmt.Sprint(i), Name: "inspect", Arguments: "{}"}}}, historyMessage{Role: "tool", CallID: fmt.Sprint(i), Text: strings.Repeat("result", 40000)})
	}
	systems, roots, err := rootCandidates(history, -1, 0, "gpt-5.4")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(roots[1].text, "produced 3 times") || roots[len(roots)-1].index != -1 {
		t.Fatal("missing pinned repetition evidence")
	}
	selected, required := pruneRoots(systems, roots, 0, 0, false)
	if len(required) != 3 || rootBytes(selected) > MaxRootBytes || !strings.Contains(selected[len(selected)-1].text, "same tool call repeated 3 times") {
		t.Fatal("synthetic reservation lost active results or exceeded budget")
	}
	for _, index := range required {
		found := false
		for _, r := range selected {
			found = found || r.index == index && !r.elided
		}
		if !found {
			t.Fatalf("lost output %d", index)
		}
	}
	// A single free slot belongs to the tool result, never the synthetic note.
	selected, _ = pruneRoots(nil, roots, MaxRoots-1, 0, true)
	if len(selected) != 1 || selected[0].role != "tool" {
		t.Fatal("note displaced sole result slot")
	}
}

func TestCheckpointSuffixKeepsCoveredTurnEntries(t *testing.T) {
	history := []historyMessage{{Role: "user", Text: "covered initiator"}}
	for i := 0; i < 4; i++ {
		history = append(history, historyMessage{Role: "assistant", Text: fmt.Sprintf("observation-%d", i), Calls: []historyCall{{ID: fmt.Sprint(i), Name: "inspect", Arguments: fmt.Sprintf("{\"index\":%d}", i)}}}, historyMessage{Role: "tool", CallID: fmt.Sprint(i), Text: "result"})
	}
	_, roots, err := rootCandidates(history, -1, 1, "gpt-5.4")
	if err != nil {
		t.Fatal(err)
	}
	selected, _ := pruneRoots(nil, roots, 186, 0, true)
	if len(selected) != 6 || selected[0].index != 3 || selected[len(selected)-1].index != 8 {
		t.Fatal("suffix must retain recent individual entries of covered turn", selected)
	}
}
