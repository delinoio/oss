package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func (f *threadFixture) dynamicReply(id, raw json.RawMessage) bool {
	var v struct {
		Success      *bool `json:"success"`
		ContentItems []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"contentItems"`
	}
	if domain.Decode(raw, &v) != nil || v.Success == nil || *v.Success || len(v.ContentItems) != 1 || v.ContentItems[0].Type != "inputText" || v.ContentItems[0].Text != dynamicUnavailableText {
		return false
	}
	if path := os.Getenv("DELIDEV_CODEX_CAPTURE"); path != "" {
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return false
		}
		err = json.NewEncoder(file).Encode(map[string]any{"method": "dynamicReply", "params": map[string]any{"id": id, "result": json.RawMessage(raw)}})
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return false
		}
	}
	if f.mode != "thread-turn-dynamic-no-resolution" {
		f.notify("serverRequest/resolved", map[string]any{"threadId": f.thread["id"], "requestId": id})
	}
	return true
}
func dynamicItem(status ToolStatus) map[string]any {
	return map[string]any{"type": "dynamicToolCall", "id": "dynamic-call", "namespace": nil, "tool": "fixture_tool", "arguments": map[string]any{}, "status": status, "contentItems": nil, "success": nil, "durationMs": nil}
}
func ownedDynamicFixture(t *testing.T, id any) (*Client, string, domain.ID, Event) {
	t.Helper()
	c, capture, _, _ := boundTurnFixture(t, "dynamic")
	turn, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, c, "approval", map[string]any{"requestId": id, "method": "item/tool/call", "params": map[string]any{"callId": "dynamic-call", "namespace": nil, "tool": "fixture_tool", "arguments": map[string]any{}}})
	return c, capture, turn.TurnID, nextKind(t, c, DynamicRequestedEvent)
}
func TestDynamicUnavailableReplyOwnsExactlyOneTypedOriginalArrival(t *testing.T) {
	for _, id := range []any{7, "7"} {
		t.Run("id-"+string(mustJSON(t, id)), func(t *testing.T) {
			c, capture, turn, event := ownedDynamicFixture(t, id)
			request := event.DynamicRequest
			originalID := request.NativeID
			if request.CallID != "dynamic-call" || request.Namespace != nil || request.Tool != "fixture_tool" || request.ID.Validate() != nil {
				t.Fatal("lost original request")
			}
			if request.NativeID.Number != nil {
				*request.NativeID.Number = 99
			}
			stages := []DynamicReplyStage{}
			reply, err := c.ReplyDynamicUnavailable(context.Background(), request.ID, func(state DynamicReplyState) error {
				stages = append(stages, state.Stage)
				if state.Request.NativeID.Kind == NumberRequestID && *state.Request.NativeID.Number != 7 {
					t.Fatal("caller replaced native ID")
				}
				return nil
			})
			if err != nil || len(stages) != 2 || stages[0] != DynamicSendIntent || stages[1] != DynamicTransmitted || reply.Closure != InteractionOpen {
				t.Fatal(reply, stages, err)
			}
			_, err = c.ReplyDynamicUnavailable(context.Background(), request.ID, func(DynamicReplyState) error { t.Fatal("repeated intent"); return nil })
			assertCode(t, err, domain.Conflict)
			resolved := nextKind(t, c, DynamicResolvedEvent)
			if resolved.DynamicReply.Stage != DynamicTransmitted || resolved.DynamicReply.Closure != InteractionNativeClosed || resolved.TurnID != turn || !resolved.Correlated {
				t.Fatal("resolution fabricated tool/root outcome")
			}
			replies := requestsOf(t, capture, "dynamicReply")
			if len(replies) == 1 {
				if originalID.Kind == NumberRequestID && replies[0]["id"] != float64(7) || originalID.Kind == TextRequestID && replies[0]["id"] != "7" {
					t.Fatal("changed exact wire request identity")
				}
			}
			if originalID.Kind != reply.Request.NativeID.Kind || len(replies) != 1 {
				t.Fatal("wrong reply count or wire kind")
			}
			inspected, err := c.InspectDynamicReply(context.Background(), request.ID)
			if err != nil || inspected.Stage != DynamicTransmitted || inspected.Closure != InteractionNativeClosed {
				t.Fatal(inspected, err)
			}
		})
	}
}
func TestDynamicIntentFailureAndCancellationCannotRepeat(t *testing.T) {
	for _, scenario := range []string{"before-intent", "intent-failure", "after-intent", "delivery-journal-failure"} {
		t.Run(scenario, func(t *testing.T) {
			c, capture, _, event := ownedDynamicFixture(t, 7)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			if scenario == "before-intent" {
				cancel()
			}
			_, err := c.ReplyDynamicUnavailable(ctx, event.DynamicRequest.ID, func(state DynamicReplyState) error {
				calls++
				if scenario == "intent-failure" || scenario == "delivery-journal-failure" && state.Stage == DynamicTransmitted {
					return errors.New("fixture journal failure")
				}
				if scenario == "after-intent" {
					cancel()
				}
				return nil
			})
			if err == nil {
				t.Fatal("expected retained failure")
			}
			state, inspectionErr := c.InspectDynamicReply(context.Background(), event.DynamicRequest.ID)
			if inspectionErr != nil {
				t.Fatal(inspectionErr)
			}
			if scenario != "before-intent" {
				if state.Stage == DynamicObserved {
					t.Fatal("forgot claimed attempt")
				}
				_, again := c.ReplyDynamicUnavailable(context.Background(), event.DynamicRequest.ID, func(DynamicReplyState) error { t.Fatal("intent repeated"); return nil })
				assertCode(t, again, domain.Conflict)
			}
			if scenario == "before-intent" && calls != 0 || scenario == "intent-failure" && len(requestsOf(t, capture, "dynamicReply")) != 0 {
				t.Fatal("pre-wire failure sent reply")
			}
		})
	}
}
func TestDynamicLifecycleClosedShapesPreserveFalseAndInertMedia(t *testing.T) {
	started := dynamicItem(ToolRunning)
	completed := dynamicItem(ToolCompleted)
	completed["success"] = false
	completed["durationMs"] = 17
	completed["contentItems"] = []any{map[string]any{"type": "inputText", "text": "native false"}, map[string]any{"type": "inputImage", "imageUrl": "data:image/png;base64,private"}, map[string]any{"type": "inputAudio", "audioUrl": "https://private.invalid/audio"}}
	for _, v := range []map[string]any{started, completed} {
		raw := mustJSON(t, v)
		tool, err := decodeDynamicTool(raw, v["status"] != ToolRunning)
		if err != nil {
			t.Fatal(err)
		}
		projection, err := tool.Dynamic.Projection(tool.Status)
		if err != nil {
			t.Fatal(err)
		}
		if tool.Status == ToolCompleted {
			if projection.Success == nil || *projection.Success || len(projection.ContentItems) != 3 || projection.DurationMS == nil || *projection.DurationMS != 17 {
				t.Fatal(projection)
			}
			public := string(mustJSON(t, projection))
			if strings.Contains(public, "private.invalid") || strings.Contains(public, "base64,private") {
				t.Fatal("media source escaped")
			}
		}
	}
	for _, bad := range []string{`{"type":"dynamicToolCall","id":"a","namespace":null,"tool":"t","arguments":{},"status":"completed","contentItems":null,"success":null}`, strings.Replace(string(mustJSON(t, completed)), `"tool":"fixture_tool"`, `"tool":"a","tool":"b"`, 1), strings.Replace(string(mustJSON(t, completed)), `"durationMs":17`, `"durationMs":-1`, 1)} {
		if _, err := decodeDynamicTool([]byte(bad), true); err == nil {
			t.Fatal("accepted malformed item")
		}
	}
	if !managedForkItem(mustJSON(t, completed), "dynamicToolCall") || !settledForkTool(mustJSON(t, completed), "dynamicToolCall") || settledForkTool(mustJSON(t, started), "dynamicToolCall") {
		t.Fatal("wrong settled history eligibility")
	}
}
func TestDynamicForeignMalformedAndDuplicateRequestsNeverOwnReply(t *testing.T) {
	c, _, _, _ := boundTurnFixture(t, "dynamic")
	turn, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"threadId": c.thread, "turnId": turn.TurnID, "callId": "dynamic-call", "namespace": nil, "tool": "fixture_tool", "arguments": map[string]any{}}
	for _, scenario := range []string{"namespace", "thread", "turn", "missing", "duplicate", "excessive"} {
		t.Run(scenario, func(t *testing.T) {
			params := map[string]any{}
			for key, value := range base {
				params[key] = value
			}
			switch scenario {
			case "namespace":
				params["namespace"] = "foreign"
			case "thread":
				params["threadId"] = domain.NewID()
			case "turn":
				params["turnId"] = domain.NewID()
			case "missing":
				delete(params, "namespace")
			case "excessive":
				params["arguments"] = strings.Repeat("a", domain.MaxMessageText)
			}
			raw := mustJSON(t, params)
			if scenario == "duplicate" {
				raw = []byte(strings.Replace(string(raw), `"tool":"fixture_tool"`, `"tool":"a","tool":"b"`, 1))
			}
			event, err := c.observeDynamicRequestLocked(nativewire.Event{Kind: nativewire.ServerRequest, Token: domain.NewID(), ID: json.RawMessage(`7`), Method: "item/tool/call", Params: raw})
			if err == nil && event.Kind != NativeExtensionEvent {
				t.Fatal("foreign/malformed request gained authority")
			}
		})
	}
}

func TestDynamicHistoryPreservesCompleteNativeJSONAndForkDigest(t *testing.T) {
	item := dynamicItem(ToolCompleted)
	item["success"] = false
	item["contentItems"] = []any{map[string]any{"type": "inputText", "text": "original"}}
	turn := fixtureTurn(domain.NewID(), TurnCompleted)
	turn["itemsView"] = "full"
	turn["items"] = []any{item}
	turns := []json.RawMessage{mustJSON(t, turn)}
	source := &ForkSource{turns: turns}
	checkpoint := source.Checkpoint()
	if checkpoint.DynamicHistory == nil || !checkpoint.DynamicHistory.matches(turns) || !hasDynamicHistory(turns) {
		t.Fatal("missing complete original dynamic history")
	}
	changed := []json.RawMessage{json.RawMessage(strings.Replace(string(turns[0]), `"original"`, `"changed"`, 1))}
	if checkpoint.DynamicHistory.matches(changed) {
		t.Fatal("changed output retained original digest")
	}
	c := &Client{dynamicItems: map[string]dynamicItemIdentity{}}
	started, err := decodeDynamicTool(mustJSON(t, dynamicItem(ToolRunning)), false)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := decodeDynamicTool(mustJSON(t, item), true)
	if err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	if c.observeDynamicIdentity(id, started, false) != nil || c.observeDynamicIdentity(id, completed, true) != nil {
		t.Fatal("valid lifecycle rejected")
	}
	prior := c.dynamicItems[string(id)+"/"+completed.ID]
	completed.Dynamic.ContentItems = []json.RawMessage{json.RawMessage(`{"type":"inputText","text":"changed"}`)}
	if prior.completedDigest == dynamicToolDigest(completed) {
		t.Fatal("changed completed native content escaped immutable comparison")
	}
}

func TestDynamicLostNativeResolutionRetainsOneOriginalAttempt(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "dynamic-no-resolution")
	turn, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, c, "approval", map[string]any{"requestId": 7, "method": "item/tool/call", "params": map[string]any{"callId": "dynamic-call", "namespace": nil, "tool": "fixture_tool", "arguments": map[string]any{}}})
	event := nextKind(t, c, DynamicRequestedEvent)
	calls := 0
	state, err := c.ReplyDynamicUnavailable(context.Background(), event.DynamicRequest.ID, func(DynamicReplyState) error { calls++; return nil })
	if err != nil || state.Stage != DynamicTransmitted || state.Closure != InteractionOpen || calls != 2 {
		t.Fatal("lost resolution became accepted completion", state, err)
	}
	_, err = c.ReplyDynamicUnavailable(context.Background(), event.DynamicRequest.ID, func(DynamicReplyState) error { t.Fatal("lost resolution repeated send"); return nil })
	assertCode(t, err, domain.Conflict)
	// A control round trip waits for the fixture to consume the preceding reply.
	fixtureSignal(t, c, "notify", map[string]any{"method": "warning", "params": map[string]any{"message": "fixture reply consumed"}})
	if len(requestsOf(t, capture, "dynamicReply")) != 1 {
		t.Fatal("reply repeated after missing native resolution")
	}
	retained, err := c.InspectDynamicReply(context.Background(), event.DynamicRequest.ID)
	if err != nil || retained.Request.TurnID != turn.TurnID || retained.Closure != InteractionOpen || !c.dynamicBlocksInput() {
		t.Fatal("lost original outstanding request", retained, err)
	}
}
