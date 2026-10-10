// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func contextImagePNG(t *testing.T, value uint8) string {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	im.Set(0, 0, color.RGBA{R: value, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestContextHistoryGeneratedImagesProveContinuationWithoutGenerationAuthority(t *testing.T) {
	c, capture, checkpoint, page := continuationFixture(t, "sleep")
	turn := page["data"].([]any)[0].(map[string]any)
	image := generationItem("completed", contextImagePNG(t, 1))
	image["savedPath"] = filepath.Join(t.TempDir(), "not-created.png")
	items := turn["items"].([]any)
	items = append(items, map[string]any{"type": "contextCompaction", "id": "original-context"}, image)
	turn["items"] = items
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	turns, err := c.compactionTurnsLocked(context.Background())
	if err != nil || len(turns) != 1 {
		t.Fatal("settled image-bearing context rejected", err)
	}
	checkpoint.Context = &ContinuationContextCheckpoint{Version: 1, TurnsCount: 1, HistoryDigest: historyDigest(turns), RolloutDigest: strings.Repeat("a", 64), RolloutPath: "/private/original", Records: []ContextRecord{{Trigger: AutomaticCompaction, TurnID: checkpoint.TurnID, LiveItemID: "original-context", HistoryItemID: "original-context"}}}
	if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ContinueAfterSuccess); err != nil {
		t.Fatal("Context-bearing continuation rejected original generated image", err)
	}
	if c.imageGeneration || len(requestsOf(t, capture, "turn/start")) != 0 || len(requestsOf(t, capture, "thread/compact/start")) != 0 {
		t.Fatal("history validation acquired generation or input authority")
	}
	image["result"] = contextImagePNG(t, 2)
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	changed, err := c.compactionTurnsLocked(context.Background())
	if err != nil || contextMatchesHistory(*checkpoint.Context, changed) == nil {
		t.Fatal("changed valid image result matched immutable history proof", err)
	}
}

func TestContextHistoryGeneratedImagesPreservePagedRevertPrefix(t *testing.T) {
	c, capture, source, ids, inputs := revertFixture(t, "ready")
	turns := []any{}
	for n, prompt := range []string{"earlier prompt", "middle prompt", "latest prompt"} {
		turn := fixtureTurn(ids[n], TurnCompleted)
		items := []any{map[string]any{"type": "userMessage", "id": string(domain.NewID()), "clientId": inputs[n].ID, "content": []any{map[string]any{"type": "text", "text": prompt, "text_elements": []any{}}}}}
		if n == 0 {
			items = append(items, generationItem("completed", contextImagePNG(t, 1)))
		} else if n == 1 {
			failure := generationItem("failed", "")
			failure["id"] = "native-original-failure"
			failure["failure"] = map[string]any{"type": "usageLimitExceeded", "limitId": "original-limit", "resetsAt": nil}
			items = append(items, failure)
		}
		turn["itemsView"], turn["items"] = "full", items
		turns = append(turns, turn)
	}
	fixtureSignal(t, c, "history", map[string]any{"page": map[string]any{"data": turns, "nextCursor": nil, "backwardsCursor": nil}})
	original, err := c.compactionTurnsLocked(context.Background())
	if err != nil || len(original) != 3 {
		t.Fatal("paged success/failure image history rejected", err)
	}
	claims := 0
	result, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[2], ids[2], func(intent RevertIntent) error {
		claims++
		if len(intent.ExpectedHistory) != 2 || historyDigest(intent.ExpectedHistory) != historyDigest(original[:2]) {
			t.Fatal("revert changed original image-bearing prefix")
		}
		return nil
	})
	if err != nil || claims != 1 || result.TurnsCount != 2 || result.HistoryDigest != historyDigest(original[:2]) || len(requestsOf(t, capture, "thread/revert")) != 1 {
		t.Fatal("revert did not retain exact success/failure image history", err)
	}
}

func TestContextHistoryGeneratedImagesRejectMalformedAndBoundedEvidence(t *testing.T) {
	c, capture, checkpoint, page := continuationFixture(t, "sleep")
	turn := page["data"].([]any)[0].(map[string]any)
	original := turn["items"].([]any)
	for _, scenario := range []string{"bad-base64", "bad-png", "missing-result", "missing-failure", "unknown-field", "oversized-id", "oversized-prompt", "running", "failed-output", "duplicate-id", "unknown-kind", "foreign-turn-shape", "turn-bound"} {
		t.Run(scenario, func(t *testing.T) {
			item := generationItem("completed", contextImagePNG(t, 1))
			switch scenario {
			case "bad-base64":
				item["result"] = "not base64"
			case "bad-png":
				item["result"] = base64.StdEncoding.EncodeToString([]byte("not PNG"))
			case "missing-result":
				delete(item, "result")
			case "missing-failure":
				delete(item, "failure")
			case "unknown-field":
				item["unknown"] = true
			case "oversized-id":
				item["id"] = strings.Repeat("x", 1025)
			case "oversized-prompt":
				item["revisedPrompt"] = strings.Repeat("x", domain.MaxPromptBytes+1)
			case "running":
				item = generationItem("in_progress", "")
			case "failed-output":
				item["status"] = "failed"
			case "unknown-kind":
				item["type"] = "foreignGeneration"
			}
			items := append(append([]any(nil), original...), item)
			if scenario == "duplicate-id" {
				items = append(items, item)
			}
			turn["items"] = items
			data := []any{turn}
			if scenario == "foreign-turn-shape" {
				turn["id"] = "foreign-invalid"
			} else {
				turn["id"] = checkpoint.TurnID
			}
			if scenario == "turn-bound" {
				// Individual one-turn pages fit the native frame bound.
				// The shared turn count still rejects the complete oversized history.
				data = nil
				for i := 0; i <= maxForkTurns; i++ {
					large := fixtureTurn(domain.NewID(), TurnCompleted)
					large["itemsView"] = "full"
					large["items"] = []any{item, map[string]any{"type": "agentMessage", "id": string(domain.NewID()), "text": "neighboring original text"}}
					data = append(data, large)
				}
			}
			// The continuation fixture emits one page. Turn-bound coverage
			// uses the paged revert fixture, whose replies contain one turn each.
			client := c
			if scenario == "turn-bound" {
				client, _, _, _, _ = revertFixture(t, "ready")
			}
			fixtureSignal(t, client, "history", map[string]any{"page": map[string]any{"data": data, "nextCursor": nil, "backwardsCursor": nil}})
			if _, err := client.compactionTurnsLocked(context.Background()); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("malformed or oversized history accepted", err)
			}
		})
	}
	if len(requestsOf(t, capture, "turn/start")) != 0 || len(requestsOf(t, capture, "thread/compact/start")) != 0 {
		t.Fatal("rejected history authorized native mutation")
	}
}

func TestContextHistoryGeneratedImagesRetainAggregateByteBound(t *testing.T) {
	c, capture, _, _, _ := revertFixture(t, "ready")
	item := generationItem("completed", contextImagePNG(t, 1))
	for i := 0; i < 18; i++ {
		turn := fixtureTurn(domain.NewID(), TurnCompleted)
		turn["itemsView"] = "full"
		turn["items"] = []any{item, map[string]any{"type": "agentMessage", "id": string(domain.NewID()), "text": strings.Repeat("x", 250000)}}
		fixtureSignal(t, c, "history", map[string]any{"append": i != 0, "page": map[string]any{"data": []any{turn}, "nextCursor": nil, "backwardsCursor": nil}})
	}
	if _, err := c.compactionTurnsLocked(context.Background()); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("valid individual image pages exceeded aggregate bound", err)
	}
	if len(requestsOf(t, capture, "thread/revert")) != 0 || len(requestsOf(t, capture, "turn/start")) != 0 {
		t.Fatal("oversized history authorized native mutation")
	}
}
