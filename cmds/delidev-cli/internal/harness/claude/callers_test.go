package claude

import (
	"encoding/json"
	"testing"
)

func programmaticFixtures(version NativeCallerKind) (map[string]any, map[string]any, map[string]any, map[string]any) {
	code := map[string]any{"type": ServerToolUseBlock, "id": "code_original", "name": ServerCodeExecution, "input": map[string]any{"code": "private provider program"}}
	called, result := serverFixtureBlocks(ServerWebSearch)
	called["caller"] = map[string]any{"type": version, "tool_id": "code_original"}
	result["caller"] = called["caller"]
	finished := map[string]any{"type": CodeExecutionResultBlock, "tool_use_id": "code_original", "content": map[string]any{"type": ServerCodeResult, "return_code": 0, "stdout": "private provider output", "stderr": "", "content": []any{}}}
	return code, called, result, finished
}

func finishProviderMessage(t *testing.T, b *ExecutionBinding, stop NativeStopReason) {
	t.Helper()
	observation := lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 0}}))
	*observation.Content[0].StopReason = "end_turn"
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "message_stop"}))
}

func TestProgrammaticServerAncestryAndOriginalResult(t *testing.T) {
	for _, version := range []NativeCallerKind{CodeCaller20250825, CodeCaller20260120} {
		for _, omitResultCaller := range []bool{false, true} {
			b := contentFixture(t)
			contentStart(t, b, "", "program")
			code, called, result, finished := programmaticFixtures(version)
			if omitResultCaller {
				delete(result, "caller")
			}
			for index, block := range []map[string]any{code, called, result, finished} {
				serverRootBlock(t, b, "program", index, block)
			}
			if b.content.serverTools["server_fixture"].caller != (NativeToolCaller{Kind: version, ToolID: "code_original"}) || b.content.openTools != 0 || len(b.content.tools) != 0 {
				t.Fatal("programmatic provider call lost ancestry or acquired local authority")
			}
		}
	}
}

func TestProgrammaticCallerRejectsForeignFinishedAndChangedAncestry(t *testing.T) {
	for _, name := range []string{"unknown", "self", "local-parent", "wrong-server-kind", "finished", "changed-result", "changed-version", "parent-before-child", "local-pending", "changed-local-caller", "foreign-agent"} {
		t.Run(name, func(t *testing.T) {
			b := contentFixture(t)
			code, called, result, finished := programmaticFixtures(CodeCaller20260120)
			if name == "local-parent" {
				contentTool(t, b, "", "code_original", "Read")
			}
			contentStart(t, b, "", "program")
			index := 0
			if name != "unknown" && name != "local-parent" && name != "self" {
				if name == "wrong-server-kind" {
					code["name"] = ServerBashExecution
				}
				serverRootBlock(t, b, "program", index, code)
				index++
			}
			switch name {
			case "finished":
				serverRootBlock(t, b, "program", index, finished)
				index++
			case "self":
				called["id"] = "code_original"
			case "foreign-agent":
				tool := b.content.serverTools["code_original"]
				tool.parent = "foreign"
				b.content.serverTools["code_original"] = tool
			}
			bad := contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": index, "content_block": called})
			if name == "changed-result" || name == "changed-version" || name == "parent-before-child" {
				serverRootBlock(t, b, "program", index, called)
				index++
				if name == "changed-result" {
					result["caller"] = map[string]any{"type": DirectCaller}
				}
				if name == "changed-version" {
					result["caller"] = map[string]any{"type": CodeCaller20250825, "tool_id": "code_original"}
				}
				if name == "parent-before-child" {
					result = finished
				}
				bad = contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": index, "content_block": result})
			}
			if name == "local-pending" || name == "changed-local-caller" {
				local := map[string]any{"type": ToolUseBlock, "id": "local", "name": "Read", "input": map[string]any{}, "caller": called["caller"]}
				if name == "local-pending" {
					serverRootBlock(t, b, "program", index, local)
					index++
					bad = contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": index, "content_block": finished})
				} else {
					lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": index, "content_block": local}))
					delete(local, "caller")
					bad = contentCompleted(t, b, "", "program", local)
				}
			}
			before := b.content.openTools
			if _, err := b.Observe(bad); err == nil || b.problem == nil || before != b.content.openTools {
				t.Fatal("invalid ancestry changed retained ownership or escaped recovery")
			}
		})
	}
}

func TestProviderContinuationRequiresOriginalStopAndSettledLocalChildren(t *testing.T) {
	for _, stop := range []NativeStopReason{"tool_use", "pause_turn", "end_turn", "max_tokens"} {
		for _, returned := range []bool{false, true} {
			t.Run(string(stop)+"/returned_"+map[bool]string{false: "false", true: "true"}[returned], func(t *testing.T) {
				b := contentFixture(t)
				code, called, _, finished := programmaticFixtures(CodeCaller20260120)
				contentStart(t, b, "", "original")
				serverRootBlock(t, b, "original", 0, code)
				serverRootBlock(t, b, "original", 1, map[string]any{"type": ToolUseBlock, "id": "local", "name": "Read", "input": map[string]any{}, "caller": called["caller"]})
				finishProviderMessage(t, b, stop)
				if returned {
					lifecycleObserve(t, b, contentResult(t, b, "", map[string]any{"type": "tool_result", "tool_use_id": "local", "content": "original output"}))
				}
				start := contentPartial(t, b, "", map[string]any{"type": "message_start", "message": contentMessage("continued")})
				if !returned || (stop != "tool_use" && stop != "pause_turn") {
					if _, err := b.Observe(start); err == nil {
						t.Fatal("unproven provider continuation accepted")
					}
					return
				}
				lifecycleObserve(t, b, start)
				serverRootBlock(t, b, "continued", 0, finished)
				tool := b.content.serverTools["code_original"]
				if !tool.finished || tool.message != "original" || tool.activeMessage != "continued" || b.content.openTools != 0 {
					t.Fatal("continuation replaced original call identity")
				}
			})
		}
	}
}

func TestProviderPauseCanContinueRepeatedlyWithoutInventingToolCalls(t *testing.T) {
	b := contentFixture(t)
	call, result := serverFixtureBlocks(ServerWebSearch)
	contentStart(t, b, "", "original")
	serverRootBlock(t, b, "original", 0, call)
	finishProviderMessage(t, b, "pause_turn")
	contentStart(t, b, "", "continuation_one")
	finishProviderMessage(t, b, "pause_turn")
	contentStart(t, b, "", "continuation_two")
	serverRootBlock(t, b, "continuation_two", 0, result)
	if len(b.content.serverTools) != 1 || b.content.serverTools["server_fixture"].message != "original" || b.content.openTools != 0 {
		t.Fatal("pause synthesized another call")
	}
}

func TestProgrammaticChildSnapshotValidatesOriginalOrderAtomically(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		b := taskFixture(t)
		code, called, result, finished := programmaticFixtures(CodeCaller20260120)
		blocks := []any{code, called, result, finished}
		if invalid {
			blocks = []any{called, code, result, finished}
		}
		event := lifecycleChange(t, contentCompleted(t, b, "parent", "child", nil), "message", contentMessage("child", blocks...))
		_, err := b.Observe(event)
		if invalid {
			if err == nil || len(b.content.serverTools) != 0 {
				t.Fatal("invalid child published partial ancestry")
			}
		} else if err != nil || len(b.content.serverTools) != 2 || b.content.openTools != 1 {
			t.Fatal("valid original child ancestry was lost", err)
		}
	}
}

func TestNativeCallerClosedUnion(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"type":"direct","tool_id":null}`, `{"type":"direct","extra":true}`, `{"type":"code_execution_20260120"}`, `{"type":"code_execution_20260120","tool_id":null}`, `{"type":"code_execution_20260120","tool_id":""}`, `{"type":"code_execution_20260521","tool_id":"original"}`} {
		if _, err := decodeToolCaller(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid native caller union accepted")
		}
	}
}
