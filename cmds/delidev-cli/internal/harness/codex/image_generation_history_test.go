// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func contextGenerationPNG(t *testing.T, changed bool) string {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if changed {
		im.Pix[0], im.Pix[3] = 255, 255
	}
	var out bytes.Buffer
	if err := png.Encode(&out, im); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(out.Bytes())
}

func imageContextFixture(t *testing.T) (*Client, string, ContinuationCheckpoint, []domain.ID, []HistoricalInput, []any) {
	t.Helper()
	c, capture := openThreadFixture(t, "thread-revert-ready")
	bound, err := c.ResumeThread(context.Background(), domain.NewID(), domain.NewID(), threadSettings(t))
	if err != nil {
		t.Fatal(err)
	}
	ids, inputs, turns := []domain.ID{}, []HistoricalInput{}, []any{}
	for _, prompt := range []string{"earlier prompt", "middle prompt", "latest prompt"} {
		id := domain.NewID()
		original := input(domain.ExecuteMode)
		original.Prompt = prompt
		in := HistoricalInput{ID: domain.NewID(), PromptDigest: original.InputDigest()}
		ids, inputs = append(ids, id), append(inputs, in)
		turn := fixtureTurn(id, TurnCompleted)
		turn["itemsView"] = "full"
		turn["items"] = []any{map[string]any{"type": "userMessage", "id": string(domain.NewID()), "clientId": in.ID, "content": []any{map[string]any{"type": "text", "text": prompt, "text_elements": []any{}}}}}
		turns = append(turns, turn)
	}
	success := generationItem("completed", contextGenerationPNG(t, false))
	// An observed saved path grants no access: this path need not exist.
	success["savedPath"] = "/unopened/original/generated.png"
	first := turns[0].(map[string]any)
	first["items"] = append(first["items"].([]any), success, map[string]any{"type": "contextCompaction", "id": "original-context"})
	failed := generationItem("failed", "")
	failed["id"] = "original-failed-image"
	failed["failure"] = map[string]any{"type": "usageLimitExceeded", "limitId": "original-limit", "resetsAt": nil}
	second := turns[1].(map[string]any)
	command := commandFixture()
	command["status"], command["exitCode"], command["aggregatedOutput"] = "completed", 0, "original tool output"
	second["items"] = append(second["items"].([]any), failed, command)
	last := turns[2].(map[string]any)
	last["items"] = append(last["items"].([]any), map[string]any{"type": "agentMessage", "id": "original-assistant", "text": "original text"})
	source := ContinuationCheckpoint{ThreadID: bound.Thread.ID, SessionID: bound.Thread.SessionID, TurnID: ids[2], Status: TurnCompleted, Mode: domain.ExecuteMode, Effective: *bound.Effective, Inputs: []HistoricalInput{inputs[2]}}
	path := filepath.Join(c.home, "sessions", "fixture.jsonl")
	if err := security.PrivateDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("original image history rollout"), 0600); err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, c, "metadata", map[string]any{"path": path})
	setImageContextHistory(t, c, &source, turns)
	return c, capture, source, ids, inputs, turns
}

func setImageContextHistory(t *testing.T, c *Client, source *ContinuationCheckpoint, turns []any) []json.RawMessage {
	t.Helper()
	normalized := make([]json.RawMessage, 0, len(turns))
	for _, turn := range turns {
		raw, err := json.Marshal(turn)
		if err != nil {
			t.Fatal(err)
		}
		normalized = append(normalized, raw)
	}
	first := turns[0].(map[string]any)
	source.Context = &ContinuationContextCheckpoint{Version: 1, TurnsCount: uint32(len(turns)), HistoryDigest: historyDigest(normalized), RolloutPath: filepath.Join(c.home, "sessions", "fixture.jsonl"), RolloutDigest: imageContextRolloutDigest(), Records: []ContextRecord{{Trigger: AutomaticCompaction, TurnID: first["id"].(domain.ID), LiveItemID: "original-context", HistoryItemID: "original-context"}}}
	fixtureSignal(t, c, "history", map[string]any{"page": map[string]any{"data": turns, "nextCursor": nil, "backwardsCursor": nil}})
	return normalized
}

func TestGeneratedImageContextHistoryPreservesPagedProofAndContinuation(t *testing.T) {
	c, capture, source, _, _, turns := imageContextFixture(t)
	expected := setImageContextHistory(t, c, &source, turns)
	got, err := c.compactionTurnsLocked(context.Background())
	if err != nil || historyDigest(got) != historyDigest(expected) || len(got) != 3 {
		t.Fatal("original paged image history changed", err)
	}
	for n := range got {
		if !bytes.Equal(got[n], expected[n]) {
			t.Fatal("original native JSON changed")
		}
	}
	if contextMatchesHistory(*source.Context, got) != nil {
		t.Fatal("image-bearing context proof rejected")
	}
	if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), source, ContinueAfterSuccess); err != nil {
		t.Fatal("context-bearing continuation rejected", err)
	}
	if len(requestsOf(t, capture, "turn/start")) != 0 || len(requestsOf(t, capture, "thread/revert")) != 0 {
		t.Fatal("history validation sent native mutation")
	}
}

func TestGeneratedImageRevertRetainsOriginalPrefix(t *testing.T) {
	c, capture, source, ids, inputs, turns := imageContextFixture(t)
	expected := setImageContextHistory(t, c, &source, turns)
	if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), source, ContinueAfterSuccess); err != nil {
		t.Fatal(err)
	}
	claims := 0
	proof, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[2], ids[2], func(intent RevertIntent) error {
		claims++
		if historyDigest(intent.ExpectedHistory) != historyDigest(expected[:2]) {
			t.Fatal("image-bearing retained prefix changed")
		}
		return nil
	})
	if err != nil || claims != 1 || proof.HistoryDigest != historyDigest(expected[:2]) || len(requestsOf(t, capture, "thread/revert")) != 1 {
		t.Fatal("original image history did not retain exact Revert proof", err)
	}
}

func TestGeneratedImageContextHistoryRejectsMalformedAndUnknownItems(t *testing.T) {
	cases := []struct {
		name   string
		change func(map[string]any, map[string]any)
	}{
		{"base64", func(item, _ map[string]any) { item["result"] = "!" }},
		{"png", func(item, _ map[string]any) { item["result"] = base64.StdEncoding.EncodeToString([]byte("not PNG")) }},
		{"missing", func(item, _ map[string]any) { delete(item, "failure") }},
		{"unknown-field", func(item, _ map[string]any) { item["unexpected"] = true }},
		{"unknown-kind", func(item, _ map[string]any) { item["type"] = "unknownImage" }},
		{"unfinished", func(item, _ map[string]any) { item["status"] = "in_progress" }},
		{"duplicate-id", func(item, turn map[string]any) { item["id"] = turn["items"].([]any)[0].(map[string]any)["id"] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, capture, source, ids, inputs, turns := imageContextFixture(t)
			if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), source, ContinueAfterSuccess); err != nil {
				t.Fatal(err)
			}
			reads := len(requestsOf(t, capture, "thread/turns/list"))
			first := turns[0].(map[string]any)
			tc.change(first["items"].([]any)[1].(map[string]any), first)
			setImageContextHistory(t, c, &source, turns)
			claims := 0
			_, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[2], ids[2], func(RevertIntent) error { claims++; return nil })
			if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || claims != 0 || len(requestsOf(t, capture, "thread/revert")) != 0 || len(requestsOf(t, capture, "thread/turns/list")) <= reads {
				t.Fatal("invalid image history authorized mutation", err)
			}
		})
	}
}

func TestGeneratedImageContextRejectsChangedResultAndForeignCheckpoint(t *testing.T) {
	for _, mode := range []string{"changed-result", "foreign-checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			c, capture, source, _, _, turns := imageContextFixture(t)
			if mode == "changed-result" {
				originalContext := source.Context
				turns[0].(map[string]any)["items"].([]any)[1].(map[string]any)["result"] = contextGenerationPNG(t, true)
				setImageContextHistory(t, c, &source, turns)
				source.Context = originalContext
			} else {
				source.ThreadID = domain.NewID()
			}
			_, err := c.VerifyContinuation(context.Background(), domain.NewID(), source, ContinueAfterSuccess)
			if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || len(requestsOf(t, capture, "turn/start")) != 0 {
				t.Fatal("changed or foreign history authorized input", err)
			}
		})
	}
}

func imageContextRolloutDigest() string {
	digest := sha256.Sum256([]byte("original image history rollout"))
	return hex.EncodeToString(digest[:])
}

// Expand only this synthetic fixture after its small control request, so the
// aggregate proof crosses 4 MiB without crossing the native frame bound.
func expandImageContextHistory(raw json.RawMessage) json.RawMessage {
	var page struct {
		Data []json.RawMessage `json:"data"`
		Next *string           `json:"nextCursor"`
		Back *string           `json:"backwardsCursor"`
	}
	if json.Unmarshal(raw, &page) != nil {
		panic("invalid image context fixture")
	}
	prefix := []json.RawMessage{}
	for n := 0; n < 9; n++ {
		turn := fixtureTurn(domain.NewID(), TurnCompleted)
		turn["itemsView"] = "full"
		turn["items"] = []any{map[string]any{"type": "agentMessage", "id": string(domain.NewID()), "text": strings.Repeat("x", 500<<10)}}
		item, _ := json.Marshal(turn)
		prefix = append(prefix, item)
	}
	page.Data = append(prefix, page.Data...)
	result, _ := json.Marshal(page)
	return result
}

func TestGeneratedImageContextHistoryPreservesAggregateAndTurnBounds(t *testing.T) {
	for _, mode := range []string{"aggregate-bytes", "turn-count"} {
		t.Run(mode, func(t *testing.T) {
			c, capture, source, ids, inputs, turns := imageContextFixture(t)
			if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), source, ContinueAfterSuccess); err != nil {
				t.Fatal(err)
			}
			reads := len(requestsOf(t, capture, "thread/turns/list"))
			if mode == "aggregate-bytes" {
				fixtureSignal(t, c, "history", map[string]any{"page": map[string]any{"data": turns, "nextCursor": nil, "backwardsCursor": nil}, "expandImageContext": true})
			} else {
				for n := 0; n < maxForkTurns; n++ {
					turn := fixtureTurn(domain.NewID(), TurnCompleted)
					turn["itemsView"] = "full"
					turn["items"] = []any{map[string]any{"type": "agentMessage", "id": string(domain.NewID()), "text": "bounded text"}}
					turns = append(turns, turn)
				}
				fixtureSignal(t, c, "history", map[string]any{"page": map[string]any{"data": turns, "nextCursor": nil, "backwardsCursor": nil}})
			}
			claims := 0
			_, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[2], ids[2], func(RevertIntent) error { claims++; return nil })
			if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || claims != 0 || len(requestsOf(t, capture, "thread/revert")) != 0 || len(requestsOf(t, capture, "thread/turns/list")) <= reads {
				t.Fatal("history bounds authorized mutation", err)
			}
		})
	}
}

func TestGeneratedImageContextDecoderRejectsDuplicateFields(t *testing.T) {
	raw, _ := json.Marshal(generationItem("failed", ""))
	duplicate := json.RawMessage(strings.TrimSuffix(string(raw), "}") + `,"status":"failed"}`)
	if _, err := decodeImageGeneration(duplicate, true); err == nil {
		t.Fatal("duplicate native image field accepted")
	}
}
