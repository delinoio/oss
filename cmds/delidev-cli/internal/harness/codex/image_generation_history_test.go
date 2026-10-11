// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func historyGenerationItem(t *testing.T, value uint8) map[string]any {
	t.Helper()
	pixels := image.NewRGBA(image.Rect(0, 0, 2, 2))
	pixels.Set(0, 0, color.RGBA{R: value, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, pixels); err != nil {
		t.Fatal(err)
	}
	return generationItem("completed", base64.StdEncoding.EncodeToString(encoded.Bytes()))
}

func generatedHistoryTurns(t *testing.T, ids []domain.ID, inputs []HistoricalInput, item any) []any {
	t.Helper()
	turns := make([]any, len(ids))
	for n, text := range []string{"earlier prompt", "middle prompt", "latest prompt"} {
		turn := fixtureTurn(ids[n], TurnCompleted)
		turn["itemsView"] = "full"
		turn["items"] = []any{
			map[string]any{"type": "userMessage", "id": fmt.Sprintf("original-user-%d", n), "clientId": inputs[n].ID, "content": []any{map[string]any{"type": "text", "text": text, "text_elements": []any{}}}},
			map[string]any{"type": "sleep", "id": fmt.Sprintf("original-sleep-%d", n), "durationMs": uint64(7)},
			uniqueHistoryItem(t, item, fmt.Sprintf("original-image-%d", n)),
			map[string]any{"type": "agentMessage", "id": fmt.Sprintf("original-assistant-%d", n), "text": "original response"},
		}
		turns[n] = turn
	}
	return turns
}

func uniqueHistoryItem(t *testing.T, item any, id string) map[string]any {
	t.Helper()

	source, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("unexpected history item type %T", item)
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	cloned["id"] = id
	return cloned
}

func setGeneratedHistory(t *testing.T, c *Client, turns []any) []json.RawMessage {
	t.Helper()
	fixtureSignal(t, c, "history", map[string]any{"page": map[string]any{"data": turns, "nextCursor": nil, "backwardsCursor": nil}})
	result := make([]json.RawMessage, len(turns))
	for n, turn := range turns {
		raw, err := json.Marshal(turn)
		if err != nil {
			t.Fatal(err)
		}
		result[n] = raw
	}
	return result
}

func TestImageGenerationPagedContextAndRevertPreserveOriginalHistory(t *testing.T) {
	c, capture, source, ids, inputs := revertFixture(t, "ready")
	success := historyGenerationItem(t, 1)
	// A valid absolute descriptor is retained privately, never opened by history.
	success["savedPath"] = "/nonexistent/private-generated-image.png"
	success["transparentBackground"] = true
	turns := generatedHistoryTurns(t, ids, inputs, success)
	failure := generationItem("failed", "")
	failure["failure"] = map[string]any{"type": "usageLimitExceeded", "limitId": "original-limit", "resetsAt": nil}
	turns[1].(map[string]any)["items"].([]any)[2] = failure
	expected := setGeneratedHistory(t, c, turns)
	settings, execution := c.execution.settings, c.execution
	for _, direction := range []string{"asc", "desc"} {
		observed, err := c.contextTurnsLocked(context.Background(), direction, nil, false)
		if err != nil || !reflect.DeepEqual(observed, expected) || historyDigest(observed) != historyDigest(expected) {
			t.Fatal("paged generated-image history changed or was refused", direction, err)
		}
	}
	if c.execution != execution || !reflect.DeepEqual(c.execution.settings, settings) || c.imageGeneration || c.problem != nil {
		t.Fatal("history validation changed execution or enabled generation")
	}
	raw, _ := json.Marshal(success)
	if managedForkItem(raw, "imageGeneration") {
		t.Fatal("context history expanded managed Fork eligibility")
	}
	if len(requestsOf(t, capture, "thread/revert")) != 0 || len(requestsOf(t, capture, "thread/compact/start")) != 0 || len(requestsOf(t, capture, "turn/start")) != 0 {
		t.Fatal("history observation caused native mutation")
	}
	claims := 0
	result, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[2], ids[2], func(intent RevertIntent) error {
		claims++
		if !reflect.DeepEqual(intent.ExpectedHistory, expected[:2]) {
			t.Fatal("revert claim lost original generated-image bytes")
		}
		return nil
	})
	if err != nil || claims != 1 || result.TurnsCount != 2 || result.HistoryDigest != historyDigest(expected[:2]) || len(requestsOf(t, capture, "thread/revert")) != 1 {
		t.Fatal("revert failed to prove original image-bearing prefix", err)
	}
}

func TestImageGenerationMalformedHistoryCannotClaimRevert(t *testing.T) {
	valid := historyGenerationItem(t, 1)
	raw, _ := json.Marshal(valid)
	cases := map[string]func(map[string]any){
		"base64": func(item map[string]any) { item["result"] = "private-invalid-base64" },
		"PNG": func(item map[string]any) {
			item["result"] = base64.StdEncoding.EncodeToString([]byte("private-invalid-PNG"))
		},
		"running":          func(item map[string]any) { item["status"] = "in_progress"; item["result"] = "" },
		"unknown-kind":     func(item map[string]any) { item["type"] = "futureImageGeneration" },
		"unknown-field":    func(item map[string]any) { item["privateUnknown"] = true },
		"oversized-prompt": func(item map[string]any) { item["revisedPrompt"] = strings.Repeat("x", domain.MaxPromptBytes+1) },
	}
	for _, key := range []string{"type", "id", "status", "revisedPrompt", "result", "failure"} {
		cases["missing-"+key] = func(item map[string]any) { delete(item, key) }
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			var item map[string]any
			if json.Unmarshal(raw, &item) != nil {
				t.Fatal("invalid fixture")
			}
			change(item)
			assertImageHistoryRefusesRevert(t, item, false)
		})
	}
	t.Run("duplicate-ID", func(t *testing.T) { assertImageHistoryRefusesRevert(t, valid, true) })
}

func assertImageHistoryRefusesRevert(t *testing.T, item any, duplicate bool) {
	t.Helper()
	c, capture, source, ids, inputs := revertFixture(t, "ready")
	turns := generatedHistoryTurns(t, ids, inputs, item)
	if duplicate {
		first := turns[0].(map[string]any)
		items := first["items"].([]any)
		first["items"] = append(items, items[2])
	}
	setGeneratedHistory(t, c, turns)
	claims := 0
	_, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[2], ids[2], func(RevertIntent) error { claims++; return nil })
	assertCode(t, err, domain.RecoveryRequired)
	if claims != 0 || c.execution.revertAction != "" || len(requestsOf(t, capture, "thread/revert")) != 0 || len(requestsOf(t, capture, "turn/start")) != 0 || len(requestsOf(t, capture, "turn/steer")) != 0 {
		t.Fatal("invalid image history acquired a mutation claim")
	}
	if strings.Contains(domain.SafeError(err).Error(), "private-") {
		t.Fatal("native image content escaped into diagnostics")
	}
}

func TestImageGenerationContextCheckpointBindsExactResult(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint("changed-", changed), func(t *testing.T) {
			c, capture, checkpoint, page := continuationFixture(t, "sleep")
			turn := page["data"].([]any)[0].(map[string]any)
			items := append(turn["items"].([]any), historyGenerationItem(t, 1), generationItem("failed", ""), map[string]any{"type": "contextCompaction", "id": "original-context"})
			items[len(items)-2].(map[string]any)["id"] = "original-failed-image"
			turn["items"] = items
			raw, _ := json.Marshal(turn)
			checkpoint.Context = &ContinuationContextCheckpoint{Version: 1, TurnsCount: 1, HistoryDigest: historyDigest([]json.RawMessage{raw}), RolloutPath: "/private/original", RolloutDigest: strings.Repeat("0", 64), Records: []ContextRecord{{Trigger: AutomaticCompaction, TurnID: checkpoint.TurnID, LiveItemID: "original-live-context", HistoryItemID: "original-context"}}}
			if changed {
				items[len(items)-3] = historyGenerationItem(t, 2)
			}
			fixtureSignal(t, c, "history", map[string]any{"page": page})
			observed, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ContinueAfterSuccess)
			if changed {
				assertCode(t, err, domain.RecoveryRequired)
				_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
				assertCode(t, err, domain.RecoveryRequired)
			} else if err != nil || observed.ID != checkpoint.TurnID || !reflect.DeepEqual(c.execution.contextBase, checkpoint.Context) {
				t.Fatal("original image-bearing context was not verified", err)
			}
			if c.imageGeneration || len(requestsOf(t, capture, "turn/start")) != 0 || len(requestsOf(t, capture, "turn/steer")) != 0 {
				t.Fatal("context proof enabled generation or replayed input")
			}
		})
	}
}

func TestImageGenerationContextKeepsAggregateHistoryBound(t *testing.T) {
	c, capture, source, ids, inputs := revertFixture(t, "image-bounds")
	turns := generatedHistoryTurns(t, ids, inputs, generationItem("failed", ""))
	// The fixture expands each image's prompt on read, keeping request frames
	// small while exercising the aggregate bound across valid paginated turns.
	for len(turns) < 17 {
		turn := fixtureTurn(domain.NewID(), TurnCompleted)
		turn["itemsView"] = "full"
		turn["items"] = []any{generationItem("failed", "")}
		turns = append(turns, turn)
	}
	setGeneratedHistory(t, c, turns)
	reads := len(requestsOf(t, capture, "thread/turns/list"))
	claims := 0
	_, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[2], ids[2], func(RevertIntent) error { claims++; return nil })
	assertCode(t, err, domain.RecoveryRequired)
	// Sixteen maximum-length prompts plus their original turn envelopes exceed
	// 4 MiB, although every page and native item is independently within bounds.
	if claims != 0 || len(requestsOf(t, capture, "thread/revert")) != 0 || len(requestsOf(t, capture, "thread/turns/list"))-reads != 16 {
		t.Fatal("aggregate history bound was bypassed or not exercised")
	}
}
