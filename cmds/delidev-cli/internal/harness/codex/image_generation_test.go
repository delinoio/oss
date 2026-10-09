// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"
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
	c.imageGeneration = true
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

func TestNativeImageGenerationActivationRequiresOriginalManagedAssignment(t *testing.T) {
	valid := Config{EnableImageGeneration: true, Mode: ThreadProtocol, ManagedAuthentication: true, ImageRoot: t.TempDir(), ImageMachineID: domain.NewID()}
	if err := configureImageGeneration(&valid); err != nil {
		t.Fatal(err)
	}
	if len(valid.Process.Args) != 2 || valid.Process.Args[1] != "features.image_generation=true" {
		t.Fatal("native feature not requested")
	}
	for _, kind := range []string{"proxy", "Sidechat", "no-auth", "probe", "no-root", "no-machine"} {
		config := valid
		config.Process.Args = nil
		switch kind {
		case "proxy":
			config.API = &APIConfig{}
		case "Sidechat":
			config.Sidechat = ReadOnlySidechatV1
		case "no-auth":
			config.ManagedAuthentication = false
		case "probe":
			config.Mode = ProbeProtocol
		case "no-root":
			config.ImageRoot = ""
		case "no-machine":
			config.ImageMachineID = ""
		}
		if configureImageGeneration(&config) == nil {
			t.Fatal("unsupported activation", kind)
		}
	}
	disabled := valid
	disabled.EnableImageGeneration = false
	disabled.Process.Args = nil
	if configureImageGeneration(&disabled) != nil || disabled.Process.Args[1] != "features.image_generation=false" {
		t.Fatal("historical assignment gained generation")
	}
	readonly := Client{managedHome: t.TempDir(), sidechat: ReadOnlySidechatV1, imageRoot: t.TempDir()}
	if readonly.nativeFrameLimit() != 16<<20 {
		t.Fatal("original image-bearing history truncated for read-only Sidechat")
	}
}

func TestNativeImageGenerationRequiresObservedEffectiveFeatureBeforeInput(t *testing.T) {
	for _, mode := range []string{"thread-managed-image-enabled", "thread-managed-image-disabled", "thread-managed-ready"} {
		t.Run(mode, func(t *testing.T) {
			config := fixtureConfig(t, mode)
			config.Mode, config.ManagedAuthentication, config.EnableImageGeneration = ThreadProtocol, true, true
			config.ImageRoot, config.ImageMachineID = t.TempDir(), domain.NewID()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client, err := Open(ctx, config)
			if mode == "thread-managed-image-enabled" {
				if err != nil {
					t.Fatal("original effective feature rejected", err)
				}
				if err = client.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				client.Close()
				t.Fatal("unobserved native feature authorized input")
			}
		})
	}
}
