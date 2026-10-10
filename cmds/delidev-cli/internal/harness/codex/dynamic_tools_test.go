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

func dynamicItemFixture() map[string]any {
	return map[string]any{"type": "dynamicToolCall", "id": "dynamic-call", "namespace": nil, "tool": "unavailable", "arguments": map[string]any{"secret": "retained-private", "large": json.Number("9007199254740993")}, "status": "completed", "contentItems": []any{map[string]any{"type": "inputText", "text": "Original text\nsecond line"}, map[string]any{"type": "inputImage", "imageUrl": "https://private.example/credential?token=secret"}, map[string]any{"type": "inputAudio", "audioUrl": "data:audio/private"}}, "success": false, "durationMs": 3}
}
func TestDynamicToolDecoderRetainsOriginalOrderedHistoryWithInertProjection(t *testing.T) {
	raw := mustJSON(t, dynamicItemFixture())
	item, err := DecodeDynamicTool(raw, "")
	if err != nil || string(item.OriginalJSON) != string(raw) || item.Observation.Namespace != nil || item.Observation.Success == nil || *item.Observation.Success || *item.Observation.DurationMS != 3 || item.Observation.Arguments.Type != domain.DynamicObject || len(item.Observation.ContentItems) != 3 || item.Observation.ContentItems[1].Type != domain.DynamicImage || item.Observation.ContentItems[2].Type != domain.DynamicAudio {
		t.Fatal("original dynamic history was rewritten", err)
	}
	public := string(mustJSON(t, item.Observation))
	for _, secret := range []string{"retained-private", "9007199254740993", "private.example", "data:audio", "token=secret"} {
		if strings.Contains(public, secret) {
			t.Fatal("private original escaped projection", secret)
		}
	}
	for _, args := range []string{`null`, `false`, `1`, `"text"`, `[]`, `{}`} {
		item := dynamicItemFixture()
		item["arguments"] = json.RawMessage(args)
		if _, err := DecodeDynamicTool(mustJSON(t, item), ""); err != nil {
			t.Fatal("valid original argument type rejected", args, err)
		}
	}
}
func TestDynamicToolDecoderRejectsAmbiguousUnboundedAndMissingFields(t *testing.T) {
	for _, bad := range []string{"missing-namespace", "missing-arguments", "missing-content", "unknown-field", "unknown-content", "content-null", "content-field", "negative-duration", "fraction-duration", "null-status", "null-tool", "oversize-output", "nested-duplicate"} {
		t.Run(bad, func(t *testing.T) {
			item := dynamicItemFixture()
			switch bad {
			case "missing-namespace":
				delete(item, "namespace")
			case "missing-arguments":
				delete(item, "arguments")
			case "missing-content":
				delete(item, "contentItems")
			case "unknown-field":
				item["handler"] = "dispatch"
			case "unknown-content":
				item["contentItems"] = []any{map[string]any{"type": "encrypted", "data": "secret"}}
			case "content-null":
				item["contentItems"] = []any{map[string]any{"type": "inputText", "text": nil}}
			case "content-field":
				item["contentItems"] = []any{map[string]any{"type": "inputImage", "imageUrl": "private", "dispatch": true}}
			case "negative-duration":
				item["durationMs"] = -1
			case "fraction-duration":
				item["durationMs"] = 0.5
			case "null-status":
				item["status"] = nil
			case "null-tool":
				item["tool"] = nil
			case "oversize-output":
				item["contentItems"] = []any{map[string]any{"type": "inputText", "text": strings.Repeat("x", domain.MaxMessageText+1)}}
			case "nested-duplicate":
				item["arguments"] = json.RawMessage(`{"a":{"secret":1,"secret":2}}`)
			}
			raw, err := json.Marshal(item)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeDynamicTool(raw, ""); err == nil {
				t.Fatal("ambiguous native item accepted")
			}
		})
	}
}
func TestDynamicToolOriginalRequestOnceOnlyAndResolutionIndependent(t *testing.T) {
	c, _, _, _ := boundTurnFixture(t, "dynamic")
	turn, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
	if err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, c, "dynamic", map[string]any{"requestId": 7})
	event := nextKind(t, c, DynamicToolRequestedEvent)
	arrival := event.DynamicTool.Observation.ArrivalID
	if event.DynamicTool.Observation.RequestID.Kind != domain.InteractionNumberID || *event.DynamicTool.Observation.RequestID.Number != 7 {
		t.Fatal("request id kind was rewritten")
	}
	// Editing returned metadata cannot edit the privately retained descriptor.
	event.DynamicTool.Observation.Tool = "adopted"
	event.DynamicTool.Observation.RequestID.Kind = domain.InteractionTextID
	response := domain.NewID()
	result, err := c.RejectDynamicTool(context.Background(), arrival, response, turn.TurnID)
	if err != nil || result.Observation.Tool != "unavailable" || result.Observation.Delivery != domain.DynamicTransmitted || *result.Observation.RequestResolved || result.Observation.NegativeOutcome == nil || *result.Observation.NegativeOutcome {
		t.Fatal("fixed negative response lost independent delivery/outcome", err)
	}
	if _, err := c.RejectDynamicTool(context.Background(), arrival, domain.NewID(), turn.TurnID); err == nil {
		t.Fatal("original reply repeated")
	}
	resolved := nextKind(t, c, DynamicToolObservedEvent)
	if resolved.DynamicTool.Observation.Stage != domain.DynamicToolResolved || !*resolved.DynamicTool.Observation.RequestResolved || resolved.DynamicTool.Observation.ResponseID != response || c.execution.turns[turn.TurnID].Turn.Status != TurnRunning {
		t.Fatal("resolution manufactured root completion")
	}
}
func TestDynamicToolRequestRejectsForeignRestrictedAndReplayedOwnership(t *testing.T) {
	for _, bad := range []string{"foreign-thread", "foreign-turn", "sidechat", "observation", "title", "replay", "changed-call-request", "null-arguments-missing", "fraction-id"} {
		t.Run(bad, func(t *testing.T) {
			c, turn := observationClient()
			c.mode = ThreadProtocol
			c.execution.paused = false
			params := map[string]any{"threadId": c.thread, "turnId": turn, "callId": "dynamic-call", "namespace": nil, "tool": "unavailable", "arguments": nil}
			native := nativewire.Event{Kind: nativewire.ServerRequest, Method: "item/tool/call", ID: json.RawMessage(`7`), Token: domain.NewID()}
			switch bad {
			case "foreign-thread":
				params["threadId"] = domain.NewID()
			case "foreign-turn":
				params["turnId"] = domain.NewID()
			case "sidechat":
				c.sidechat = ReadOnlySidechatV1
			case "observation":
				c.modelObservation = "model"
			case "title":
				c.api = &apiBinding{title: true}
			case "null-arguments-missing":
				delete(params, "arguments")
			case "fraction-id":
				native.ID = json.RawMessage(`7.5`)
			}
			native.Params = mustJSON(t, params)
			if bad == "replay" || bad == "changed-call-request" {
				if _, err := c.observeEventLocked(native); err != nil {
					t.Fatal(err)
				}
				native.Token = domain.NewID()
				if bad == "changed-call-request" {
					native.ID = json.RawMessage(`"7"`)
				}
			}
			if _, err := c.observeEventLocked(native); err == nil {
				t.Fatal("unowned or replayed request acquired reply authority")
			}
		})
	}
}
func TestDynamicToolHistoryUsesSharedDecoderWithoutReplyAuthority(t *testing.T) {
	c, _, _, page := continuationFixture(t, "sleep")
	page["nextCursor"] = nil
	page["backwardsCursor"] = nil
	turns := page["data"].([]any)
	turn := turns[0].(map[string]any)
	items := turn["items"].([]any)
	original := dynamicItemFixture()
	turn["items"] = append(items, original)
	raw := mustJSON(t, page)
	if _, _, err := decodeLatestTurnInputs(raw); err != nil {
		t.Fatal("valid dynamic continuation history rejected", err)
	}
	if !managedForkItem(mustJSON(t, original), "dynamicToolCall") {
		t.Fatal("managed Fork rewrote or rejected original dynamic history")
	}
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	history, err := c.contextTurnsLocked(context.Background(), "asc", nil, false)
	if err != nil || len(history) != 1 || !strings.Contains(string(history[0]), "retained-private") || !strings.Contains(string(history[0]), "9007199254740993") || !strings.Contains(string(history[0]), "private.example") {
		t.Fatal("context history lost original dynamic fields", err)
	}
	if len(c.dynamicState().arrivals) != 0 {
		t.Fatal("historical item acquired live reply authority")
	}
	delete(original, "success")
	turn["items"] = append(items, original)
	if _, _, err := decodeLatestTurnInputs(mustJSON(t, page)); err == nil || managedForkItem(mustJSON(t, original), "dynamicToolCall") {
		t.Fatal("malformed dynamic history bypassed shared decoder")
	}
}

func TestDynamicToolCanceledOriginalReplyDoesNotWriteOrResolve(t *testing.T) {
	c, _, _, _ := boundTurnFixture(t, "dynamic")
	turn, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
	if err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, c, "dynamic", map[string]any{"requestId": "7"})
	event := nextKind(t, c, DynamicToolRequestedEvent)
	if event.DynamicTool.Observation.RequestID.Kind != domain.InteractionTextID || event.DynamicTool.Observation.RequestID.Text != "7" {
		t.Fatal("text native id became integer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.RejectDynamicTool(ctx, event.DynamicTool.Observation.ArrivalID, domain.NewID(), turn.TurnID); err == nil {
		t.Fatal("canceled attempt wrote a negative reply")
	}
	inspection, err := c.InspectDynamicTool(context.Background(), event.DynamicTool.Observation.ArrivalID)
	if err != nil || inspection.DynamicTool.Observation.Delivery != domain.DynamicNotSent || *inspection.DynamicTool.Observation.RequestResolved || c.execution.turns[turn.TurnID].Turn.Status != TurnRunning {
		t.Fatal("cancellation fabricated reply, resolution or root completion", err)
	}
}
