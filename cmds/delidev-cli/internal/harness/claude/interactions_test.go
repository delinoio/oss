package claude

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func interactionTool(t *testing.T, b *ExecutionBinding, name string, input map[string]any) StreamEvent {
	t.Helper()
	id := "toolu_callback"
	message := "msg_callback"
	contentStart(t, b, "", message)
	block := map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 0, "content_block": block}))
	lifecycleObserve(t, b, contentCompleted(t, b, "", message, block))
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_stop", "index": 0}))
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "message_stop"}))
	raw, _ := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": name, "tool_use_id": id, "input": input, "permission_suggestions": nil, "blocked_path": nil, "requires_user_interaction": true})
	return StreamEvent{Kind: NativeRequest, RequestID: "native_callback", ArrivalID: domain.NewID(), Body: raw}
}
func interactionEcho(event StreamEvent, raw []byte) StreamEvent {
	return StreamEvent{Kind: NativeReplyEcho, RequestID: event.RequestID, ArrivalID: event.ArrivalID, Response: StreamResponse{Result: raw}}
}
func TestInteractionMatchesCompletedToolAndOriginalReplyWithoutGrantingInputAcceptance(t *testing.T) {
	b := contentFixture(t)
	event := interactionTool(t, b, "Bash", map[string]any{"command": "private-fixture-command", "description": "fixture description"})
	observation := lifecycleObserve(t, b, event)
	request := observation.Interaction.Request
	if observation.Kind != InteractionObserved || request.RequiresUserInteraction == nil || !*request.RequiresUserInteraction || request.Kind != ToolPermission || request.ToolName != "Bash" || request.ToolID != "toolu_callback" {
		t.Fatal("original callback ownership missing")
	}
	wire, _ := json.Marshal(observation)
	if bytes.Contains(wire, []byte("private-fixture-command")) || bytes.Contains(wire, []byte("native_callback")) {
		t.Fatal("private callback entered generic output")
	}
	request.Input[0] = '['
	original, reply, err := b.PreparePermissionReply(event.ArrivalID, PermissionReply{Behavior: PermissionAllow})
	if err != nil || original.RequestID != event.RequestID || original.ArrivalID != event.ArrivalID || !bytes.Contains(reply, []byte(`"updatedInput":{"command":"private-fixture-command"`)) {
		t.Fatal("caller changed exact native reply", err)
	}
	if b.interactionBytes != 0 {
		t.Fatal("prepared callback input buffer retained")
	}
	if _, _, err = b.PreparePermissionReply(event.ArrivalID, PermissionReply{Behavior: PermissionAllow}); err == nil {
		t.Fatal("callback prepared twice")
	}
	echoed := lifecycleObserve(t, b, interactionEcho(event, reply))
	if echoed.Kind != InteractionObserved || echoed.Interaction.Kind != InteractionReplyEchoed || echoed.Interaction.Canceled || b.finished {
		t.Fatal("echo fabricated completion")
	}
	if _, err = b.Observe(interactionEcho(event, reply)); err == nil {
		t.Fatal("duplicate echo accepted")
	}
}
func TestInteractionRejectsForeignChangedAndUnownedCallbacks(t *testing.T) {
	for _, scenario := range []string{"changed-input", "changed-tool", "foreign-tool", "case-alias", "unknown-field", "duplicate-request", "uncompleted", "unprepared-echo", "wrong-echo", "foreign-arrival", "finished-tool", "capacity"} {
		t.Run(scenario, func(t *testing.T) {
			b := contentFixture(t)
			event := interactionTool(t, b, "Bash", map[string]any{"command": "fixture"})
			var fields map[string]any
			_ = json.Unmarshal(event.Body, &fields)
			switch scenario {
			case "changed-input":
				fields["input"] = map[string]any{"command": "different"}
			case "changed-tool":
				fields["tool_name"] = "Write"
			case "foreign-tool":
				fields["tool_use_id"] = "foreign"
			case "case-alias":
				fields["TOOL_NAME"] = "Bash"
			case "unknown-field":
				fields["unrecognized"] = true
			case "duplicate-request":
				lifecycleObserve(t, b, event)
				event.ArrivalID = domain.NewID()
			case "uncompleted":
				tool := b.content.tools["toolu_callback"]
				tool.input = [32]byte{}
				b.content.tools["toolu_callback"] = tool
			case "finished-tool":
				tool := b.content.tools["toolu_callback"]
				tool.finished = true
				b.content.tools["toolu_callback"] = tool
			case "capacity":
				b.interactionBytes = maxBufferedContent
			case "unprepared-echo":
				lifecycleObserve(t, b, event)
				event = interactionEcho(event, []byte(`{"behavior":"allow"}`))
			case "wrong-echo", "foreign-arrival":
				lifecycleObserve(t, b, event)
				_, reply, err := b.PreparePermissionReply(event.ArrivalID, PermissionReply{Behavior: PermissionAllow})
				if err != nil {
					t.Fatal(err)
				}
				event = interactionEcho(event, reply)
				if scenario == "wrong-echo" {
					event.Response.Result = []byte(`{"behavior":"deny","message":"different"}`)
				} else {
					event.ArrivalID = domain.NewID()
				}
			}
			event.Body, _ = json.Marshal(fields)
			if _, err := b.Observe(event); err == nil {
				t.Fatal("invalid callback accepted")
			}
			if _, err := b.Observe(lifecycleResult(t, b, Completed, false)); err == nil {
				t.Fatal("later terminal erased interaction uncertainty")
			}
		})
	}
}
func TestInteractionCancellationRemainsIndependentFromLateExactEcho(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-reply", true: "after-reply"}[prepared], func(t *testing.T) {
			b := contentFixture(t)
			event := interactionTool(t, b, "Bash", map[string]any{})
			lifecycleObserve(t, b, event)
			var reply []byte
			if prepared {
				_, raw, err := b.PreparePermissionReply(event.ArrivalID, PermissionReply{Behavior: PermissionDeny, Message: "User declined the fixture", Interrupt: true})
				if err != nil {
					t.Fatal(err)
				}
				reply = raw
			}
			canceled := lifecycleObserve(t, b, StreamEvent{Kind: NativeCancellation, RequestID: event.RequestID, ArrivalID: event.ArrivalID})
			if canceled.Interaction.Kind != InteractionCanceled || !canceled.Interaction.Canceled || b.interactionBytes != 0 {
				t.Fatal("cancellation lost original state")
			}
			if _, _, err := b.PreparePermissionReply(event.ArrivalID, PermissionReply{Behavior: PermissionAllow}); err == nil {
				t.Fatal("canceled callback answered")
			}
			if prepared {
				echoed := lifecycleObserve(t, b, interactionEcho(event, reply))
				if !echoed.Interaction.Canceled || echoed.Interaction.Kind != InteractionReplyEchoed {
					t.Fatal("late echo erased cancellation")
				}
			}
		})
	}
}
func questionInput() map[string]any {
	return map[string]any{"questions": []any{map[string]any{"question": "Original question?", "header": "Approach", "multiSelect": false, "options": []any{map[string]any{"label": "First", "description": "Use first"}, map[string]any{"label": "Second", "description": "Use second"}}}}, "metadata": map[string]any{"source": "private-fixture"}}
}
func TestInteractionQuestionReplyPreservesOriginalInputAndMissingAnswers(t *testing.T) {
	for _, answers := range []map[string]string{{"Original question?": "First"}, {}, {"Original question?": "Explicit free text"}} {
		b := contentFixture(t)
		input := questionInput()
		event := interactionTool(t, b, "AskUserQuestion", input)
		request := lifecycleObserve(t, b, event).Interaction.Request
		if request.Kind != UserQuestion || len(request.Questions) != 1 || *request.Questions[0].MultiSelect {
			t.Fatal("question options lost")
		}
		request.Questions[0].Question = "caller mutation"
		if _, _, err := b.PreparePermissionReply(event.ArrivalID, PermissionReply{Behavior: PermissionAllow, Answers: map[string]string{"foreign": "First"}}); err == nil {
			t.Fatal("foreign question answer accepted")
		}
		_, raw, err := b.PreparePermissionReply(event.ArrivalID, PermissionReply{Behavior: PermissionAllow, Answers: answers})
		if err != nil {
			t.Fatal(err)
		}
		var reply struct {
			Behavior string                     `json:"behavior"`
			Input    map[string]json.RawMessage `json:"updatedInput"`
		}
		if json.Unmarshal(raw, &reply) != nil {
			t.Fatal("invalid native reply")
		}
		expected, _ := json.Marshal(input["questions"])
		actual, _ := streamReplyDigest(reply.Input["questions"])
		want, _ := streamReplyDigest(expected)
		if actual != want || len(reply.Input["metadata"]) == 0 {
			t.Fatal("reply changed original question input")
		}
		var retained map[string]string
		_ = json.Unmarshal(reply.Input["answers"], &retained)
		if len(retained) != len(answers) {
			t.Fatal("missing answer became synthetic value")
		}
		lifecycleObserve(t, b, interactionEcho(event, raw))
	}
}
func TestInteractionPlanRetainsExactNativeArtifactAndPrivateSuggestions(t *testing.T) {
	b := contentFixture(t)
	input := map[string]any{"plan": "# Private plan\nDo the fixture.", "planFilePath": "/private/fixture/plan.md", "allowedPrompts": []any{map[string]any{"tool": "Bash", "prompt": "run fixture tests"}}}
	event := interactionTool(t, b, "ExitPlanMode", input)
	var fields map[string]any
	_ = json.Unmarshal(event.Body, &fields)
	fields["permission_suggestions"] = []any{map[string]any{"type": "addRules", "rules": []any{map[string]any{"toolName": "Bash", "ruleContent": "fixture:*"}}, "behavior": "allow", "destination": "session"}}
	event.Body, _ = json.Marshal(fields)
	request := lifecycleObserve(t, b, event).Interaction.Request
	if request.Kind != PlanApproval || request.Plan == nil || *request.Plan != input["plan"] || request.PlanPath == nil || len(request.Suggestions) != 1 {
		t.Fatal("plan or suggested scope lost")
	}
	_, raw, err := b.PreparePermissionReply(event.ArrivalID, PermissionReply{Behavior: PermissionAllow})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("updatedPermissions")) || !bytes.Contains(raw, []byte("allowedPrompts")) {
		t.Fatal("reply implicitly persisted permissions or discarded input")
	}
}
func TestInteractionRejectsMalformedQuestionAndPermissionShapes(t *testing.T) {
	for _, raw := range []string{`{"type":"unknown"}`, `{"type":"setMode","mode":"invalid"}`, `{"type":"addRules","rules":[{"ToolName":"Bash"}],"behavior":"allow"}`, `{"type":"addRules","rules":[{"toolName":"Bash"}],"behavior":"allow","mode":"plan"}`} {
		var value PermissionUpdate
		if json.Unmarshal([]byte(raw), &value) == nil {
			t.Fatal("malformed permission accepted")
		}
	}
	b := contentFixture(t)
	input := questionInput()
	input["questions"].([]any)[0].(map[string]any)["question"] = strings.Repeat("x", 4097)
	if _, err := b.Observe(interactionTool(t, b, "AskUserQuestion", input)); err == nil {
		t.Fatal("unbounded question accepted")
	}
}

func TestInteractionUnconfirmedReplyCannotBecomeSuccessfulExecution(t *testing.T) {
	b := contentFixture(t)
	event := interactionTool(t, b, "Bash", map[string]any{})
	lifecycleObserve(t, b, event)
	if _, _, err := b.PreparePermissionReply(event.ArrivalID, PermissionReply{Behavior: PermissionAllow}); err != nil {
		t.Fatal(err)
	}
	lifecycleObserve(t, b, contentResult(t, b, "", map[string]any{"type": "tool_result", "tool_use_id": "toolu_callback", "is_error": false, "content": "fixture"}))
	if _, err := b.Observe(lifecycleResult(t, b, Completed, false)); err == nil {
		t.Fatal("tool completion replaced missing exact reply evidence")
	}
}

func TestInteractionToolClosureCannotAuthorizeLateResponse(t *testing.T) {
	b := contentFixture(t)
	event := interactionTool(t, b, "Bash", map[string]any{})
	lifecycleObserve(t, b, event)
	lifecycleObserve(t, b, contentResult(t, b, "", map[string]any{"type": "tool_result", "tool_use_id": "toolu_callback", "is_error": true, "content": "fixture refusal"}))
	if _, _, err := b.PreparePermissionReply(event.ArrivalID, PermissionReply{Behavior: PermissionAllow}); err == nil {
		t.Fatal("finished tool authorized late callback reply")
	}
}
