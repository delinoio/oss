package claude

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func contentFixture(t *testing.T) *ExecutionBinding {
	t.Helper()
	b, _ := lifecycleFixture(t)
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandQueued))
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandStarted))
	init := lifecycleInit(t, b)
	init = lifecycleChange(t, init, "tools", []string{"Bash", "Read", "Write", "AskUserQuestion", "EnterPlanMode", "ExitPlanMode", "Task"})
	lifecycleObserve(t, b, init)
	lifecycleObserve(t, b, lifecycleReplay(t, b))
	return b
}

func contentPartial(t *testing.T, b *ExecutionBinding, parent string, fields map[string]any) StreamEvent {
	t.Helper()
	var owner *string
	if parent != "" {
		owner = &parent
	}
	return lifecycleMessage(t, b, "stream_event", map[string]any{"parent_tool_use_id": owner, "event": fields})
}

func contentMessage(id string, blocks ...any) map[string]any {
	if blocks == nil {
		blocks = []any{}
	}
	return map[string]any{"id": id, "type": "message", "role": "assistant", "model": "resolved-fixture-model", "content": blocks, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 3, "output_tokens": 0}}
}

func contentStart(t *testing.T, b *ExecutionBinding, parent, id string) {
	t.Helper()
	lifecycleObserve(t, b, contentPartial(t, b, parent, map[string]any{"type": "message_start", "message": contentMessage(id)}))
}

func contentCompleted(t *testing.T, b *ExecutionBinding, parent, id string, block any) StreamEvent {
	t.Helper()
	var owner *string
	if parent != "" {
		owner = &parent
	}
	return lifecycleMessage(t, b, "assistant", map[string]any{"timestamp": "2026-09-25T01:00:00Z", "parent_tool_use_id": owner, "message": contentMessage(id, block)})
}

func contentTool(t *testing.T, b *ExecutionBinding, parent, id, name string) {
	t.Helper()
	message := "msg_" + id
	block := map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}
	contentStart(t, b, parent, message)
	lifecycleObserve(t, b, contentPartial(t, b, parent, map[string]any{"type": "content_block_start", "index": 0, "content_block": block}))
	lifecycleObserve(t, b, contentCompleted(t, b, parent, message, block))
	lifecycleObserve(t, b, contentPartial(t, b, parent, map[string]any{"type": "content_block_stop", "index": 0}))
	lifecycleObserve(t, b, contentPartial(t, b, parent, map[string]any{"type": "message_stop"}))
}

func contentResult(t *testing.T, b *ExecutionBinding, parent string, blocks ...any) StreamEvent {
	t.Helper()
	var owner *string
	if parent != "" {
		owner = &parent
	}
	return lifecycleMessage(t, b, "user", map[string]any{"timestamp": "2026-09-25T01:00:00Z", "parent_tool_use_id": owner, "message": map[string]any{"role": "user", "content": blocks}})
}

func TestContentPreservesMultipleBlocksAndNativeUsage(t *testing.T) {
	b := contentFixture(t)
	contentStart(t, b, "", "msg_multiple")
	for i, text := range []string{"First native block.", "Second native block."} {
		started := lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "text", "text": ""}}))
		*started.Content[0].Block.Text = "caller mutation"
		changed := lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "text_delta", "text": text}}))
		if changed.Kind != ContentObserved || *changed.Content[0].Delta != text || *changed.Content[0].Index != uint32(i) {
			t.Fatal("native block delta lost its index/content")
		}
		completed := lifecycleObserve(t, b, contentCompleted(t, b, "", "msg_multiple", map[string]any{"type": "text", "text": text})).Content[0]
		if completed.Kind != ContentCompleted || completed.MessageID != "msg_multiple" || *completed.Block.Text != text || *completed.Index != uint32(i) || completed.Usage == nil || *completed.Usage.Input != 3 || b.content.bufferedBytes != 0 {
			t.Fatal("completed block was flattened, changed, or retained unnecessarily")
		}
		lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_stop", "index": i}))
	}
	updated := lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 11}})).Content[0]
	if updated.Kind != ProviderMessageUpdated || updated.Usage.Input != nil || *updated.Usage.Output != 11 || updated.StopReason == nil || *updated.StopReason != "end_turn" {
		t.Fatal("native usage update was merged or stop reason discarded")
	}
	finished := lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "message_stop"})).Content[0]
	if finished.Kind != ProviderMessageFinished || finished.Usage != nil {
		t.Fatal("message stop fabricated usage")
	}
	lifecycleObserve(t, b, lifecycleResult(t, b, Completed, false))
}

func TestContentThinkingSignaturesRemainPrivate(t *testing.T) {
	b := contentFixture(t)
	contentStart(t, b, "", "msg_thinking")
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}}))
	for _, delta := range []map[string]any{{"type": "thinking_delta", "thinking": "Visible native reasoning."}, {"type": "signature_delta", "signature": "private-native-signature"}} {
		observation := lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_delta", "index": 0, "delta": delta}))
		raw, err := json.Marshal(observation)
		if err != nil || strings.Contains(string(raw), "private-native-signature") {
			t.Fatal("native thinking signature escaped")
		}
	}
	observation := lifecycleObserve(t, b, contentCompleted(t, b, "", "msg_thinking", map[string]any{"type": "thinking", "thinking": "Visible native reasoning.", "signature": "private-native-signature"}))
	raw, err := json.Marshal(observation)
	if err != nil || strings.Contains(string(raw), "private-native-signature") || !strings.Contains(string(raw), "Visible native reasoning.") || b.content.bufferedBytes != 0 {
		t.Fatal("reasoning/signature boundary changed")
	}
}

func TestContentPreservesNativeEnrichedToolAndOrderedResults(t *testing.T) {
	b := contentFixture(t)
	contentStart(t, b, "", "msg_plan")
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "tool_plan", "name": "ExitPlanMode", "input": map[string]any{}}}))
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": "{}"}}))
	native := json.RawMessage(`{"plan":"The exact native plan.","planFilePath":"/private/fixture-plan.md"}`)
	completion := lifecycleObserve(t, b, contentCompleted(t, b, "", "msg_plan", map[string]any{"type": "tool_use", "id": "tool_plan", "name": "ExitPlanMode", "input": native})).Content[0]
	if string(completion.Block.Tool.ProposedInput) != "{}" || string(completion.Block.Tool.Input) != string(native) {
		t.Fatal("native enriched input replaced the proposal or lost its original fields")
	}
	digest, _ := streamReplyDigest(native)
	completion.Block.Tool.Input[0] = '['
	completion.Block.Tool.Name = "caller mutation"
	if b.content.tools["tool_plan"].input != digest || b.content.tools["tool_plan"].name != "ExitPlanMode" {
		t.Fatal("returned tool metadata changed retained ownership")
	}
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_stop", "index": 0}))
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "message_stop"}))
	result := contentResult(t, b, "", map[string]any{"type": "tool_result", "tool_use_id": "tool_plan", "content": []any{map[string]any{"type": "text", "text": "first"}, map[string]any{"type": "text", "text": "second"}}})
	result = lifecycleChange(t, result, "tool_use_result", map[string]any{"plan": "The exact native plan."})
	observed := lifecycleObserve(t, b, result).Content[0].ToolResult
	if observed.ID != "tool_plan" || observed.Name != "ExitPlanMode" || observed.Error != nil || observed.Text != nil || len(observed.Blocks) != 2 || *observed.Blocks[0].Text != "first" || *observed.Blocks[1].Text != "second" || len(observed.Structured) == 0 || b.content.openTools != 0 {
		t.Fatal("tool result lost native identity, null state, or ordered content")
	}
	if _, err := b.Observe(contentResult(t, b, "", map[string]any{"type": "tool_result", "tool_use_id": "tool_plan", "content": "duplicate"})); err == nil {
		t.Fatal("completed tool accepted another result")
	}
}

func TestContentRejectsContradictionsAndLatchesRecovery(t *testing.T) {
	for _, change := range []string{"gap", "stop-before-completion", "wrong-delta", "duplicate-delta-field", "changed-completion", "foreign-message", "changed-model", "duplicate-block", "early-message-stop", "unknown-block", "success-with-open-message", "nested-case-alias", "unknown-stop"} {
		t.Run(change, func(t *testing.T) {
			b := contentFixture(t)
			contentStart(t, b, "", "msg_current")
			start := map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": "observed"}}
			var bad StreamEvent
			switch change {
			case "gap":
				start["index"] = 1
				bad = contentPartial(t, b, "", start)
			case "unknown-block":
				start["content_block"] = map[string]any{"type": "future-native-kind"}
				bad = contentPartial(t, b, "", start)
			default:
				lifecycleObserve(t, b, contentPartial(t, b, "", start))
				switch change {
				case "stop-before-completion":
					bad = contentPartial(t, b, "", map[string]any{"type": "content_block_stop", "index": 0})
				case "wrong-delta":
					bad = contentPartial(t, b, "", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": "foreign"}})
				case "duplicate-delta-field":
					bad = contentPartial(t, b, "", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "append", "Text": "override"}})
				case "changed-completion":
					bad = contentCompleted(t, b, "", "msg_current", map[string]any{"type": "text", "text": "different"})
				case "foreign-message":
					bad = contentCompleted(t, b, "", "msg_foreign", map[string]any{"type": "text", "text": "observed"})
				case "changed-model":
					message := contentMessage("msg_current", map[string]any{"type": "text", "text": "observed"})
					message["model"] = "different"
					bad = lifecycleChange(t, contentCompleted(t, b, "", "msg_current", nil), "message", message)
				case "duplicate-block":
					bad = contentPartial(t, b, "", start)
				case "early-message-stop":
					bad = contentPartial(t, b, "", map[string]any{"type": "message_stop"})
				case "success-with-open-message":
					bad = lifecycleResult(t, b, Completed, false)
				case "nested-case-alias":
					bad = contentPartial(t, b, "", map[string]any{"type": "message_delta", "delta": map[string]any{"Stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 0}})
				case "unknown-stop":
					bad = contentPartial(t, b, "", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "future-reason"}, "usage": map[string]any{"output_tokens": 0}})
				}
			}
			if _, err := b.Observe(bad); err == nil {
				t.Fatal("contradictory native content accepted")
			}
			if _, err := b.Observe(lifecycleResult(t, b, Completed, false)); err == nil || b.problem == nil {
				t.Fatal("later success cleared content uncertainty")
			}
		})
	}
}

func TestContentToolResultsValidateAllOwnershipBeforeCompletion(t *testing.T) {
	for _, change := range []string{"foreign", "duplicate", "parent", "role-alias", "structured-multiple", "rich-unsupported"} {
		t.Run(change, func(t *testing.T) {
			b := contentFixture(t)
			contentTool(t, b, "", "tool_first", "Write")
			contentTool(t, b, "", "tool_second", "Write")
			first := map[string]any{"type": "tool_result", "tool_use_id": "tool_first", "content": "first"}
			second := map[string]any{"type": "tool_result", "tool_use_id": "tool_second", "content": "second"}
			var bad StreamEvent
			switch change {
			case "foreign":
				second["tool_use_id"] = "tool_foreign"
			case "duplicate":
				second["tool_use_id"] = "tool_first"
			case "rich-unsupported":
				second["content"] = []any{map[string]any{"type": "image", "source": map[string]any{}}}
			}
			bad = contentResult(t, b, "", first, second)
			switch change {
			case "parent":
				bad = lifecycleChange(t, bad, "parent_tool_use_id", "foreign-parent")
			case "role-alias":
				bad = lifecycleChange(t, bad, "message", map[string]any{"Role": "user", "content": []any{first, second}})
			case "structured-multiple":
				bad = lifecycleChange(t, bad, "tool_use_result", map[string]any{"result": "ambiguous"})
			}
			if _, err := b.Observe(bad); err == nil || b.content.openTools != 2 || b.content.tools["tool_first"].finished || b.content.tools["tool_second"].finished {
				t.Fatal("failed result partially completed tools")
			}
		})
	}
}

func TestContentChildOwnershipAndAggregateBound(t *testing.T) {
	t.Run("interleaved-tool-limit", func(t *testing.T) {
		b := contentFixture(t)
		contentTool(t, b, "", "parent_tool", "Agent")
		contentStart(t, b, "parent_tool", "pending_message")
		block := map[string]any{"type": "tool_use", "id": "pending_tool", "name": "Write", "input": map[string]any{}}
		lifecycleObserve(t, b, contentPartial(t, b, "parent_tool", map[string]any{"type": "content_block_start", "index": 0, "content_block": block}))
		for i := 0; i < 127; i++ {
			contentTool(t, b, "", fmt.Sprintf("root_tool_%d", i), "Write")
		}
		if _, err := b.Observe(contentCompleted(t, b, "parent_tool", "pending_message", block)); err == nil || b.content.openTools != 128 || b.content.tools["pending_tool"].name != "" {
			t.Fatal("interleaved completion bypassed the open-tool bound")
		}
	})
	t.Run("active-child", func(t *testing.T) {
		b := contentFixture(t)
		contentTool(t, b, "", "parent_tool", "Agent")
		contentStart(t, b, "parent_tool", "child_message")
		if _, err := b.Observe(contentResult(t, b, "", map[string]any{"type": "tool_result", "tool_use_id": "parent_tool", "content": "done"})); err == nil || b.content.tools["parent_tool"].finished {
			t.Fatal("parent result abandoned its active child")
		}
	})
	t.Run("buffered-descendants", func(t *testing.T) {
		b := contentFixture(t)
		text := strings.Repeat("x", domain.MaxMessageText)
		for i := 0; i <= maxBufferedContent/domain.MaxMessageText; i++ {
			contentTool(t, b, "", fmt.Sprintf("parent_%d", i), "Agent")
		}
		for i := 0; i <= maxBufferedContent/domain.MaxMessageText; i++ {
			parent := fmt.Sprintf("parent_%d", i)
			contentStart(t, b, parent, "child_message")
			_, err := b.Observe(contentPartial(t, b, parent, map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": text}}))
			if i < maxBufferedContent/domain.MaxMessageText && err != nil {
				t.Fatal(err)
			}
			if i == maxBufferedContent/domain.MaxMessageText && (err == nil || b.content.bufferedBytes > maxBufferedContent) {
				t.Fatal("concurrent child content exceeded aggregate memory bound")
			}
		}
	})
}
