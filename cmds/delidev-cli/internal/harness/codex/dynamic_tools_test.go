// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"strings"
	"testing"
)

func (f *threadFixture) dynamicReply(id, raw json.RawMessage) bool {
	var response struct {
		Success      bool                        `json:"success"`
		ContentItems []domain.DynamicToolContent `json:"contentItems"`
	}
	if domain.Decode(raw, &response) != nil || response.Success || len(response.ContentItems) != 1 || response.ContentItems[0].Type != domain.DynamicText || response.ContentItems[0].Text == nil || *response.ContentItems[0].Text != "This dynamic tool is unavailable in DeliDev." {
		return false
	}
	f.notify("serverRequest/resolved", map[string]any{"threadId": f.thread["id"], "requestId": id})
	return true
}
func TestDynamicUnavailableOriginalOnceOnlyReceiptAndResolution(t *testing.T) {
	c, _, _, _ := boundTurnFixture(t, "dynamic")
	var states []DynamicReplyState
	c.dynamicRecorder = func(_ context.Context, v DynamicReplyState) error { states = append(states, v); return nil }
	_, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, c, "dynamic", map[string]any{"requestId": 7, "params": map[string]any{"callId": "dynamic-call", "namespace": nil, "tool": "fixture_tool", "arguments": map[string]any{}}})
	e := nextKind(t, c, MetadataEvent)
	for e.Metadata != DynamicUnavailableResponded {
		e = nextKind(t, c, MetadataEvent)
	}
	if len(states) != 2 || states[0].Delivery != DynamicReplyIntent || states[1].Delivery != DynamicReplyTransmitted || states[0].Resolved || len(c.execution.interactions.arrivals) != 0 {
		t.Fatal("dynamic reply changed question ownership or receipt order")
	}
	owned := c.dynamicReplies["n:7"]
	for _, native := range []nativewire.Event{owned.native, func() nativewire.Event { copy := owned.native; copy.Token = domain.NewID(); return copy }()} {
		if _, err := c.answerDynamicUnavailableLocked(context.Background(), native); err == nil {
			t.Fatal("dynamic request replay/replacement replied again")
		}
	}
	if len(states) != 2 {
		t.Fatal("replay altered durable intent")
	}
	e = nextKind(t, c, MetadataEvent)
	if e.Metadata != DynamicRequestResolved || len(states) != 3 || !states[2].Resolved || states[2].Delivery != DynamicReplyTransmitted || c.execution.active == "" {
		t.Fatal("resolution fabricated item/turn completion")
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	nextKind(t, c, TurnCompletedEvent)
}
func TestDynamicToolClosedLifecycleAndInertOrderedContent(t *testing.T) {
	c, turn := observationClient()
	args := json.RawMessage(`{"original":true}`)
	start := map[string]any{"type": "dynamicToolCall", "id": "dynamic-call", "namespace": "original", "tool": "fixture_tool", "arguments": args, "status": "inProgress", "contentItems": nil, "success": nil, "durationMs": nil}
	e, err := observeFixture(c, "item/started", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 1, "item": start})
	if err != nil || e.Kind != ToolStartedEvent {
		t.Fatal(err)
	}
	start["status"], start["success"], start["durationMs"] = "completed", false, 23
	start["contentItems"] = []any{map[string]any{"type": "inputText", "text": "original"}, map[string]any{"type": "inputImage", "imageUrl": "https://inert.invalid/private"}, map[string]any{"type": "inputAudio", "audioUrl": "data:audio/inert"}}
	e, err = observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": start})
	if err != nil || e.Kind != ToolCompletedEvent || e.Tool.Dynamic.Success == nil || *e.Tool.Dynamic.Success || len(*e.Tool.Dynamic.ContentItems) != 3 || (*e.Tool.Dynamic.ContentItems)[1].Type != domain.DynamicImage || c.execution.active != turn {
		t.Fatal("dynamic lifecycle lost original outcome/content/order", err)
	}
	start["namespace"] = "foreign"
	if _, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": start}); err == nil {
		t.Fatal("dynamic namespace substituted")
	}
	for _, raw := range []string{`{"type":"dynamicToolCall","id":"call","tool":"fixture","arguments":{},"status":"completed","contentItems":[{"type":"inputText","text":"original","imageUrl":"foreign"}]}`, strings.Repeat(" ", 512<<10) + `{}`} {
		if _, err := decodeDynamicTool(json.RawMessage(raw), true); err == nil {
			t.Fatal("malformed/oversized lifecycle admitted")
		}
	}
}
func TestDynamicUnavailableRecorderFailureFencesResendAndAuxiliary(t *testing.T) {
	c, _, _, _ := boundTurnFixture(t, "dynamic")
	_, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	c.dynamicRecorder = func(_ context.Context, v DynamicReplyState) error {
		calls++
		if v.Delivery == DynamicReplyTransmitted {
			return interactionUncertain()
		}
		return nil
	}
	fixtureSignal(t, c, "dynamic", map[string]any{"requestId": 7, "params": map[string]any{"callId": "dynamic-call", "namespace": nil, "tool": "fixture_tool", "arguments": map[string]any{}}})
	for {
		_, err := c.NextEvent(context.Background())
		if err != nil {
			if domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal(err)
			}
			break
		}
	}
	if c.problem == nil || !c.execution.paused || calls != 2 {
		t.Fatal("lost durable delivery did not retain recovery")
	}
	native := c.dynamicReplies["n:7"].native
	if _, err := c.answerDynamicUnavailableLocked(context.Background(), native); err == nil || calls != 2 {
		t.Fatal("uncertain reply repeated")
	}
	c.dynamicRecorder = nil
	native.ID = json.RawMessage(`8`)
	if _, err := c.answerDynamicUnavailableLocked(context.Background(), native); err == nil {
		t.Fatal("auxiliary profile acquired unavailable response authority")
	}
	if err := c.Close(); err != nil {
		t.Fatal("independent original cleanup failed", err)
	}
}

func TestDynamicRequestRejectsMalformedForeignAndCanceledBeforeIntent(t *testing.T) {
	c, turn := observationClient()
	c.mode = ThreadProtocol
	records := 0
	c.dynamicRecorder = func(context.Context, DynamicReplyState) error { records++; return nil }
	params := map[string]any{"threadId": c.thread, "turnId": turn, "callId": "dynamic-call", "namespace": nil, "tool": "fixture_tool", "arguments": map[string]any{}}
	native := nativewire.Event{Kind: nativewire.ServerRequest, Method: "item/tool/call", ID: json.RawMessage(`7`), Token: domain.NewID()}
	raw, _ := json.Marshal(params)
	for _, bad := range []string{`{}`, `{"threadId":null}`, string(raw[:len(raw)-1]) + `,"tool":"duplicate"}`, string(raw[:len(raw)-1]) + `,"unknown":true}`} {
		native.Params = json.RawMessage(bad)
		if _, err := c.answerDynamicUnavailableLocked(context.Background(), native); err == nil {
			t.Fatal("malformed dynamic request admitted")
		}
	}
	for _, field := range []string{"threadId", "turnId"} {
		copy := map[string]any{}
		for k, v := range params {
			copy[k] = v
		}
		copy[field] = domain.NewID()
		native.Params, _ = json.Marshal(copy)
		if _, err := c.answerDynamicUnavailableLocked(context.Background(), native); err == nil {
			t.Fatal("foreign dynamic scope admitted")
		}
	}
	native.Params = raw
	native.Kind = nativewire.Notification
	if _, err := c.answerDynamicUnavailableLocked(context.Background(), native); err == nil {
		t.Fatal("notification became request authority")
	}
	native.Kind = nativewire.ServerRequest
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.answerDynamicUnavailableLocked(ctx, native); err == nil {
		t.Fatal("canceled request reached intent")
	}
	if records != 0 || len(c.dynamicReplies) != 0 || len(c.execution.interactions.arrivals) != 0 {
		t.Fatal("refused dynamic request changed ownership")
	}
}
func TestDynamicToolFullHistoryRetainsItemsWithoutDispatch(t *testing.T) {
	turn := fixtureTurn(domain.NewID(), TurnCompleted)
	turn["itemsView"] = "full"
	dynamic := map[string]any{"type": "dynamicToolCall", "id": "dynamic-call", "namespace": nil, "tool": "fixture_tool", "arguments": map[string]any{"original": true}, "status": "completed", "contentItems": []any{}, "success": false, "durationMs": 3}
	user := map[string]any{"type": "userMessage", "id": "original-user", "clientId": domain.NewID(), "content": []any{map[string]any{"type": "text", "text": "Original prompt", "text_elements": []any{}}}}
	turn["items"] = []any{user, dynamic}
	raw, _ := json.Marshal(turn)
	if _, inputs, err := decodeLatestTurnInputs(marshalForkPage([]json.RawMessage{raw})); err != nil || len(inputs) != 1 {
		t.Fatal("closed dynamic history rejected", err)
	}
	dynamic["contentItems"] = []any{map[string]any{"type": "foreign", "text": "original"}}
	raw, _ = json.Marshal(turn)
	if _, _, err := decodeLatestTurnInputs(marshalForkPage([]json.RawMessage{raw})); err == nil {
		t.Fatal("malformed retained dynamic history admitted")
	}
}
