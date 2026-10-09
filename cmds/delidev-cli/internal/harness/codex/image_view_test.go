// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestNativeImageViewClosedLifecycle(t *testing.T) {
	c, turn := observationClient()
	item := map[string]any{"type": "imageView", "id": "original-image-call", "path": "/fixture/image.png"}
	params := map[string]any{"threadId": c.thread, "turnId": turn, "item": item, "startedAtMs": 1}
	event, err := observeFixture(c, "item/started", params)
	if err != nil || event.Kind != ToolStartedEvent || event.Tool == nil || event.Tool.Kind != ImageViewTool || event.Tool.Status != ToolRunning || event.Tool.ImagePath != item["path"] || !event.Correlated {
		t.Fatalf("missing original image view: %v", err)
	}
	delete(params, "startedAtMs")
	params["completedAtMs"] = 2
	event, err = observeFixture(c, "item/completed", params)
	if err != nil || event.Tool == nil || event.Tool.Status != ToolCompleted || c.execution.turns[turn].Turn.Status != TurnRunning {
		t.Fatalf("image view changed root outcome: %v", err)
	}
	for _, field := range []string{"status", "url", "data", "sha256", "environmentId"} {
		item[field] = "unsupported"
		if _, err := observeFixture(c, "item/completed", params); err == nil {
			t.Fatalf("accepted extra %s", field)
		}
		delete(item, field)
	}
	params["threadId"] = domain.NewID()
	event, err = observeFixture(c, "item/completed", params)
	if err != nil || event.Kind != NativeExtensionEvent || event.Tool != nil {
		t.Fatal("foreign thread became original image view")
	}
}

func TestNativeImageViewHistoryIsMetadataOnly(t *testing.T) {
	raw := json.RawMessage(`{"type":"imageView","id":"original-image-call","path":"/fixture/missing.png"}`)
	observed, err := decodeTool(raw, "imageView", true)
	if err != nil || observed.Status != ToolCompleted || observed.ImagePath != "/fixture/missing.png" {
		t.Fatalf("history attempted a new file observation: %v", err)
	}
}
