// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func generationPNG(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 600, 600))
	var n uint32 = 1
	for i := 0; i < len(im.Pix); i++ {
		n = n*1664525 + 1013904223
		im.Pix[i] = byte(n >> 24)
	}
	im.Set(0, 0, color.RGBA{1, 2, 3, 255})
	var out bytes.Buffer
	if png.Encode(&out, im) != nil {
		t.Fatal("encode")
	}
	return out.Bytes()
}
func generationItem(status string, result string) map[string]any {
	return map[string]any{"type": "imageGeneration", "id": "native-original-call", "status": status, "revisedPrompt": nil, "result": result, "failure": nil}
}
func TestNativeImageGenerationShapesAndOriginalProvider(t *testing.T) {
	png := generationPNG(t)
	if len(png) < 1<<20 {
		t.Fatal("fixture must prove large native output")
	}
	cases := []struct {
		name             string
		item             map[string]any
		completed, valid bool
	}{
		{"started", generationItem("in_progress", ""), false, true},
		{"completed", generationItem("completed", base64.StdEncoding.EncodeToString(png)), true, true},
		{"failed", generationItem("failed", ""), true, true},
		{"empty success", generationItem("completed", ""), true, false},
		{"failure with output", generationItem("failed", base64.StdEncoding.EncodeToString(png)), true, false},
		{"unknown status", generationItem("partial", ""), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(tc.item)
			got, err := decodeImageGeneration(raw, tc.completed)
			if (err == nil) != tc.valid {
				t.Fatalf("validation:%v", err)
			}
			if tc.valid && tc.item["status"] == "completed" && !bytes.Equal(got.Bytes, png) {
				t.Fatal("original bytes changed")
			}
		})
	}
	for _, field := range []string{"backendGenerationId", "usage", "unknown"} {
		item := generationItem("failed", "")
		item[field] = "private"
		raw, _ := json.Marshal(item)
		if _, err := decodeImageGeneration(raw, true); err == nil {
			t.Fatal("unknown native field admitted")
		}
	}
	for _, key := range []string{"type", "id", "status", "revisedPrompt", "result", "failure"} {
		item := generationItem("failed", "")
		delete(item, key)
		raw, _ := json.Marshal(item)
		if _, err := decodeImageGeneration(raw, true); err == nil {
			t.Fatal("required native field absent", key)
		}
	}
	for _, key := range []string{"transparentBackground", "savedPath"} {
		item := generationItem("failed", "")
		item[key] = nil
		raw, _ := json.Marshal(item)
		if _, err := decodeImageGeneration(raw, true); err == nil {
			t.Fatal("nullable optional native field", key)
		}
	}
	item := generationItem("failed", "")
	item["failure"] = map[string]any{"type": "usageLimitExceeded", "limitId": "original"}
	missingReset, _ := json.Marshal(item)
	if _, err := decodeImageGeneration(missingReset, true); err == nil {
		t.Fatal("missing nullable reset field admitted")
	}
	thread, turn := domain.NewID(), domain.NewID()
	raw, _ := json.Marshal(generationItem("in_progress", ""))
	c := &Client{thread: thread, imageRoot: t.TempDir(), imageMachine: domain.NewID(), execution: &executionState{active: turn, turns: map[domain.ID]trackedTurn{turn: {Turn: Turn{ID: turn, Status: TurnRunning}}}}}
	if _, err := c.observeImageGeneration(nativewire.Event{Method: "item/started"}, turn, raw); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("API/feature flag granted generation")
	}
	c.managedHome = t.TempDir()
	event, err := c.observeImageGeneration(nativewire.Event{Method: "item/started"}, turn, raw)
	if err != nil || event.ItemID != "native-original-call" || !event.Correlated {
		t.Fatal("original managed observation rejected", err)
	}
	c.sidechat = ReadOnlySidechatV1
	if _, err = c.observeImageGeneration(nativewire.Event{Method: "item/started"}, turn, raw); err == nil {
		t.Fatal("Sidechat granted image generation")
	}
}
func TestNativeImageGenerationUsageLimitAndNoInventedUsage(t *testing.T) {
	item := generationItem("failed", "")
	item["failure"] = map[string]any{"type": "usageLimitExceeded", "limitId": "native-limit", "resetsAt": int64(123)}
	raw, _ := json.Marshal(item)
	v, err := decodeImageGeneration(raw, true)
	if err != nil || v.Observation.Failure.LimitID != "native-limit" || *v.Observation.Failure.ResetsAt != 123 || len(v.Bytes) != 0 {
		t.Fatal("original native failure lost", err)
	}
	raw, _ = json.Marshal(v)
	if bytes.Contains(raw, []byte(`"usage":`)) {
		t.Fatal("fabricated usage")
	}
}
