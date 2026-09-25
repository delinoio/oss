package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func serverFixtureBlocks(name ServerToolName) (map[string]any, map[string]any) {
	use := map[string]any{"type": "server_tool_use", "id": "server_fixture", "name": name, "input": map[string]any{"query": "private provider query"}}
	result := map[string]any{"type": "web_search_tool_result", "tool_use_id": "server_fixture", "content": []any{map[string]any{"type": "web_search_result", "url": "https://fixture.invalid", "title": "Original title", "page_age": nil, "encrypted_content": "private encrypted search content"}}}
	if name == ServerWebFetch {
		use["input"] = map[string]any{"url": "https://fixture.invalid"}
		result["type"] = WebFetchResultBlock
		result["content"] = map[string]any{"type": "web_fetch_result", "url": "https://fixture.invalid", "retrieved_at": nil, "content": map[string]any{"type": "document", "source": map[string]any{"type": "text", "media_type": "text/plain", "data": "Private fetched document."}, "title": nil, "citations": nil}}
	}
	return use, result
}

func serverRootBlock(t *testing.T, b *ExecutionBinding, message string, index int, block map[string]any) ContentEvent {
	t.Helper()
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": index, "content_block": block}))
	result := lifecycleObserve(t, b, contentCompleted(t, b, "", message, block)).Content[0]
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_stop", "index": index}))
	return result
}

func TestServerToolResultPreservesOwnershipWithoutLocalAuthority(t *testing.T) {
	for _, name := range []ServerToolName{ServerWebSearch, ServerWebFetch} {
		for _, failed := range []bool{false, true} {
			b := contentFixture(t)
			contentStart(t, b, "", "msg_server")
			use, result := serverFixtureBlocks(name)
			if failed {
				kind := "web_search_tool_result_error"
				if name == ServerWebFetch {
					kind = "web_fetch_tool_result_error"
				}
				result["content"] = map[string]any{"type": kind, "error_code": "unavailable"}
			}
			started := serverRootBlock(t, b, "msg_server", 0, use)
			if started.Block.ServerTool == nil || b.content.tools["server_fixture"].name != "" || b.content.openTools != 1 {
				t.Fatal("server call acquired local tool ownership")
			}
			started.Block.ServerTool.Input[0] = '['
			completed := serverRootBlock(t, b, "msg_server", 1, result)
			if completed.Block.ServerResult == nil || completed.Block.ServerResult.Name != name || b.content.openTools != 0 || !b.content.serverTools["server_fixture"].finished || (completed.Block.ServerResult.Problem != "") != failed || b.content.bufferedBytes != 0 {
				t.Fatal("server result changed original ownership or failure scope")
			}
			raw, _ := json.Marshal(completed)
			if bytes.Contains(raw, []byte("private encrypted")) || bytes.Contains(raw, []byte("Private fetched")) {
				t.Fatal("private server output bypassed publication")
			}
			lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "message_stop"}))
			terminal := lifecycleObserve(t, b, lifecycleResult(t, b, Completed, false))
			if !terminal.Result.Successful() {
				t.Fatal("individual tool failure replaced native original outcome")
			}
		}
	}
}

func TestServerToolRejectsLocalForeignDuplicateAndChangedResultOwnership(t *testing.T) {
	for _, name := range []string{"local-result", "client-collision", "server-collision", "unknown-result", "wrong-family", "foreign-message", "duplicate-result", "changed-result", "changed-input", "changed-caller", "pending-success"} {
		t.Run(name, func(t *testing.T) {
			b := contentFixture(t)
			use, result := serverFixtureBlocks(ServerWebSearch)
			if name == "client-collision" {
				contentTool(t, b, "", "server_fixture", "Read")
			}
			contentStart(t, b, "", "msg_server")
			if name == "client-collision" {
				if _, err := b.Observe(contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 0, "content_block": use})); err == nil {
					t.Fatal("server call reused a local tool identity")
				}
				return
			}
			if name != "unknown-result" && name != "changed-input" && name != "changed-caller" {
				serverRootBlock(t, b, "msg_server", 0, use)
			}
			var bad StreamEvent
			switch name {
			case "local-result":
				bad = contentResult(t, b, "", map[string]any{"type": "tool_result", "tool_use_id": "server_fixture", "content": "injected local result"})
			case "server-collision":
				bad = contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 1, "content_block": use})
			case "unknown-result":
				bad = contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 0, "content_block": result})
			case "wrong-family":
				_, result = serverFixtureBlocks(ServerWebFetch)
				bad = contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 1, "content_block": result})
			case "foreign-message":
				lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "message_stop"}))
				contentStart(t, b, "", "msg_foreign")
				bad = contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 0, "content_block": result})
			case "duplicate-result":
				serverRootBlock(t, b, "msg_server", 1, result)
				bad = contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 2, "content_block": result})
			case "changed-result":
				lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 1, "content_block": result}))
				result["content"] = []any{}
				bad = contentCompleted(t, b, "", "msg_server", result)
			case "changed-input", "changed-caller":
				lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 0, "content_block": use}))
				if name == "changed-input" {
					use["input"] = map[string]any{"query": "different"}
				} else {
					use["caller"] = map[string]any{"type": "direct"}
				}
				bad = contentCompleted(t, b, "", "msg_server", use)
			case "pending-success":
				lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "message_stop"}))
				bad = lifecycleResult(t, b, Completed, false)
			}
			if _, err := b.Observe(bad); err == nil || b.problem == nil {
				t.Fatal("invalid server ownership failed to retain recovery")
			}
		})
	}
}

func TestServerToolChildSnapshotsCommitAtomicallyAndBlockParentClosure(t *testing.T) {
	for _, name := range []string{"complete", "invalid-sibling", "mixed-collision", "parent-result", "task-terminal"} {
		t.Run(name, func(t *testing.T) {
			b := taskFixture(t)
			use, result := serverFixtureBlocks(ServerWebSearch)
			blocks := []any{use}
			if name == "complete" {
				blocks = append(blocks, result)
			}
			if name == "invalid-sibling" {
				changed := map[string]any{"type": "web_search_tool_result", "tool_use_id": "foreign", "content": []any{}}
				blocks = append(blocks, changed)
			}
			if name == "mixed-collision" {
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": "server_fixture", "name": "Read", "input": map[string]any{}})
			}
			event := lifecycleChange(t, contentCompleted(t, b, "parent", "msg_child_server", nil), "message", contentMessage("msg_child_server", blocks...))
			observation, err := b.Observe(event)
			if name == "invalid-sibling" || name == "mixed-collision" {
				if err == nil || len(b.content.serverTools) != 0 || b.content.openTools != 1 || len(b.content.tools) != 1 {
					t.Fatal("invalid snapshot committed partial local/server ownership")
				}
				return
			}
			if err != nil || observation.Content[0].Kind != ProviderMessageSnapshot || observation.Content[0].Index != nil {
				t.Fatal("native child server block gained synthetic streaming identity", err)
			}
			if name == "complete" {
				if b.content.openTools != 1 || !b.content.serverTools["server_fixture"].finished {
					t.Fatal("child server result did not settle only its own call")
				}
				return
			}
			var bad StreamEvent
			if name == "parent-result" {
				bad = contentResult(t, b, "", map[string]any{"type": "tool_result", "tool_use_id": "parent", "content": "premature parent return"})
			} else {
				bad = taskEvent(t, b, TaskUpdated, map[string]any{"task_id": "task", "patch": map[string]any{"status": "completed"}})
			}
			if _, err := b.Observe(bad); err == nil || b.content.openTools != 2 || b.content.serverTools["server_fixture"].finished {
				t.Fatal("parent abandoned pending server work")
			}
		})
	}
}

func TestServerToolParserRejectsUnverifiedFamiliesAndMalformedResults(t *testing.T) {
	for _, name := range []string{"unknown-name", "code-caller", "null-caller", "mixed-caller", "missing-input", "null-result", "unknown-error", "wrong-error-kind", "missing-encrypted", "changed-source-shape", "invalid-time", "duplicate-key"} {
		t.Run(name, func(t *testing.T) {
			use, result := serverFixtureBlocks(ServerWebSearch)
			block := result
			switch name {
			case "unknown-name":
				use["name"] = "unknown"
				block = use
			case "code-caller":
				use["caller"] = map[string]any{"type": "code_execution_20260120", "tool_id": "unowned"}
				block = use
			case "null-caller":
				use["caller"] = nil
				block = use
			case "mixed-caller":
				use["caller"] = map[string]any{"type": "direct", "tool_id": "unowned"}
				block = use
			case "missing-input":
				delete(use, "input")
				block = use
			case "null-result":
				result["content"] = nil
			case "unknown-error":
				result["content"] = map[string]any{"type": "web_search_tool_result_error", "error_code": "unknown"}
			case "wrong-error-kind":
				result["content"] = map[string]any{"type": "web_fetch_tool_result_error", "error_code": "unavailable"}
			case "missing-encrypted":
				delete(result["content"].([]any)[0].(map[string]any), "encrypted_content")
			case "changed-source-shape":
				result["content"].([]any)[0].(map[string]any)["provider_extension"] = true
			case "invalid-time":
				_, block = serverFixtureBlocks(ServerWebFetch)
				block["content"].(map[string]any)["retrieved_at"] = "yesterday"
			}
			raw, _ := json.Marshal(block)
			if name == "duplicate-key" {
				raw = append(raw[:len(raw)-1], []byte(`,"tool_use_id":"server_fixture"}`)...)
			}
			if _, err := decodeContentBlock(raw); err == nil {
				t.Fatal("unverified server content became a supported typed block")
			}
		})
	}
}

func TestServerAndLocalToolsShareBoundsAndProcessWideIdentities(t *testing.T) {
	for _, bound := range []string{"open", "identity"} {
		b := contentFixture(t)
		contentStart(t, b, "", "msg_limit")
		b.content.serverTools = map[string]serverToolState{}
		count := 128
		if bound == "identity" {
			count = 4096
		}
		for index := 0; index < count; index++ {
			b.content.serverTools[fmt.Sprint(index)] = serverToolState{name: ServerWebSearch, finished: bound == "identity"}
		}
		if bound == "open" {
			b.content.openTools = count
		}
		use, _ := serverFixtureBlocks(ServerWebSearch)
		if _, err := b.Observe(contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 0, "content_block": use})); err == nil || len(b.content.serverTools) != count {
			t.Fatal("server tool exceeded shared identity/work bounds")
		}
	}
}

func TestServerToolCannotSupplyLocalPermissionCallbackAuthority(t *testing.T) {
	b := contentFixture(t)
	contentStart(t, b, "", "msg_server_permission")
	use, _ := serverFixtureBlocks(ServerWebSearch)
	serverRootBlock(t, b, "msg_server_permission", 0, use)
	raw, _ := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": ServerWebSearch, "tool_use_id": "server_fixture", "input": use["input"], "permission_suggestions": nil, "blocked_path": nil, "requires_user_interaction": true})
	if _, err := b.Observe(StreamEvent{Kind: NativeRequest, RequestID: "server_callback", ArrivalID: domain.NewID(), Body: raw}); err == nil || len(b.interactions) != 0 {
		t.Fatal("provider server call became a local approval request")
	}
}
