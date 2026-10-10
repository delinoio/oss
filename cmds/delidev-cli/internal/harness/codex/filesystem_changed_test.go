// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestFilesystemChangedDiscardsOnlyBoundedNotifications(t *testing.T) {
	first, second := filepath.Join(t.TempDir(), "not-created-a.txt"), filepath.Join(t.TempDir(), "not-created-b.txt")
	for _, paths := range [][]string{{}, {first}, {second, first}, {first, first}} {
		raw, _ := json.Marshal(map[string]any{"watchId": "foreign-watch", "changedPaths": paths})
		c, turn := observationClient()
		settings, paused := c.execution.settings, c.execution.paused
		for i := 0; i < 3; i++ {
			event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "fs/changed", Params: raw})
			if err != nil || event.Kind != MetadataEvent || event.Metadata != FilesystemChangedDiscarded || !event.Correlated || event.ThreadID != c.thread || event.TurnID != "" || event.Native != nil {
				t.Fatal("valid passive filesystem metadata rejected or exposed", event, err)
			}
			encoded, _ := json.Marshal(event)
			if strings.Contains(string(encoded), first) || strings.Contains(string(encoded), second) || strings.Contains(string(encoded), "foreign-watch") {
				t.Fatal("private watch payload escaped")
			}
			if c.execution.active != turn || c.execution.paused != paused || !reflect.DeepEqual(c.execution.settings, settings) || c.problem != nil {
				t.Fatal("passive filesystem metadata changed original execution")
			}
		}
	}
	valid, _ := json.Marshal(map[string]any{"watchId": "fixture-watch", "changedPaths": []string{first}})
	c, _ := observationClient()
	event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: "fs/changed", Params: valid})
	if err == nil && event.Kind != NativeExtensionEvent {
		t.Fatal("server request acquired notification authority", event)
	}
	unbound := &Client{}
	event, err = unbound.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "fs/changed", Params: valid})
	if err != nil || event.Kind != NativeExtensionEvent {
		t.Fatal("unbound notification gained execution authority", event, err)
	}
	if validationStage("fs/changed") != validationFilesystem {
		t.Fatal("filesystem failure lacks a redacted classification")
	}
}

func TestFilesystemChangedRejectsMalformedMetadata(t *testing.T) {
	oversized := make([]string, 1001)
	root := t.TempDir()
	for i := range oversized {
		oversized[i] = filepath.Join(root, "file")
	}
	tooMany, _ := json.Marshal(map[string]any{"watchId": "watch", "changedPaths": oversized})
	payloads := []string{
		"", "null", "[]", "{}", `{"watchId":"watch"}`, `{"changedPaths":[]}`,
		`{"watchId":null,"changedPaths":[]}`, `{"watchId":"watch","changedPaths":null}`,
		`{"watchId":"watch","changedPaths":[null]}`, `{"watchId":"watch","changedPaths":[1]}`,
		`{"watchId":"watch","changedPaths":["relative/file"]}`, `{"watchId":"","changedPaths":[]}`,
		`{"watchId":"watch","changedPaths":[],"unknown":true}`,
		`{"watchId":"watch","watchId":"other","changedPaths":[]}`,
		`{"watchId":"watch","changedPaths":[],"changedPaths":[]}`,
		`{"watchId":"watch","changedPaths":["/nul\u0000path"]}`,
		string(tooMany),
	}
	longWatch, _ := json.Marshal(map[string]any{"watchId": strings.Repeat("x", 1025), "changedPaths": []string{}})
	longPath, _ := json.Marshal(map[string]any{"watchId": "watch", "changedPaths": []string{string(filepath.Separator) + strings.Repeat("x", 4096)}})
	payloads = append(payloads, string(longWatch), string(longPath))
	for index, raw := range payloads {
		c, turn := observationClient()
		if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "fs/changed", Params: json.RawMessage(raw)}); err == nil {
			t.Fatalf("invalid payload %d accepted", index)
		}
		if c.execution.active != turn {
			t.Fatal("rejection replaced original input")
		}
	}
}

func TestFilesystemChangedPreservesOriginalAcceptedTurn(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "normal")
	settings := c.execution.settings
	accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	for i := 0; i < 3; i++ {
		fixtureSignal(t, c, "notify", map[string]any{"method": "fs/changed", "params": map[string]any{"watchId": "unsolicited", "changedPaths": []string{filepath.Join(t.TempDir(), "absent")}}})
		event := nextKind(t, c, MetadataEvent)
		if event.Metadata != FilesystemChangedDiscarded || event.Native != nil || c.problem != nil || c.execution.active != accepted.TurnID || !reflect.DeepEqual(c.execution.settings, settings) {
			t.Fatal("notification changed original ownership", event, c.problem)
		}
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	terminal := nextKind(t, c, TurnCompletedEvent)
	nextKind(t, c, ThreadStatusEvent)
	if terminal.TurnID != accepted.TurnID || c.execution.active != "" || c.problem != nil {
		t.Fatal("ordinary original turn did not complete", terminal, c.problem)
	}
	if len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "fs/watch")) != 0 || len(requestsOf(t, capture, "fs/readFile")) != 0 {
		t.Fatal("passive notification triggered native work")
	}
}
