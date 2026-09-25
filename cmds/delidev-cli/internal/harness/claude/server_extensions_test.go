package claude

import (
	"bytes"
	"encoding/json"
	"testing"
)

type serverExtensionFixture struct {
	id      string
	name    ServerToolName
	kind    ContentBlockKind
	content json.RawMessage
}

func serverExtensionFixtures() []serverExtensionFixture {
	return []serverExtensionFixture{
		{"code", ServerCodeExecution, CodeExecutionResultBlock, json.RawMessage(`{"type":"code_execution_result","stdout":"private provider stdout","stderr":"","return_code":0,"content":[{"type":"code_execution_output","file_id":"private-provider-file"}]}`)},
		{"encrypted-code", ServerCodeExecution, CodeExecutionResultBlock, json.RawMessage(`{"type":"encrypted_code_execution_result","encrypted_stdout":"private encrypted stdout","stderr":"private provider stderr","return_code":13,"content":[]}`)},
		{"bash", ServerBashExecution, BashExecutionResultBlock, json.RawMessage(`{"type":"bash_code_execution_result","stdout":"private provider output","stderr":"","return_code":-1,"content":[{"type":"bash_code_execution_output","file_id":"private-bash-file"}]}`)},
		{"editor-create", ServerEditorExecution, EditorExecutionResultBlock, json.RawMessage(`{"type":"text_editor_code_execution_create_result","is_file_update":false}`)},
		{"editor-replace", ServerEditorExecution, EditorExecutionResultBlock, json.RawMessage(`{"type":"text_editor_code_execution_str_replace_result","lines":["private provider line"],"new_lines":1,"new_start":0,"old_lines":null,"old_start":null}`)},
		{"editor-view", ServerEditorExecution, EditorExecutionResultBlock, json.RawMessage(`{"type":"text_editor_code_execution_view_result","content":"private provider file contents","file_type":"text","num_lines":1,"start_line":0,"total_lines":null}`)},
		{"advisor", ServerAdvisor, AdvisorResultBlock, json.RawMessage(`{"type":"advisor_result","text":"private provider advice","stop_reason":"max_tokens"}`)},
		{"redacted-advisor", ServerAdvisor, AdvisorResultBlock, json.RawMessage(`{"type":"advisor_redacted_result","encrypted_content":"private encrypted advice","stop_reason":null}`)},
		{"tool-search-regex", ServerToolSearchRegex, ToolSearchResultBlock, json.RawMessage(`{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"UninstalledProviderTool"}]}`)},
		{"tool-search-bm25", ServerToolSearchBM25, ToolSearchResultBlock, json.RawMessage(`{"type":"tool_search_tool_search_result","tool_references":[]}`)},
		{"code-error", ServerCodeExecution, CodeExecutionResultBlock, json.RawMessage(`{"type":"code_execution_tool_result_error","error_code":"execution_time_exceeded"}`)},
		{"bash-error", ServerBashExecution, BashExecutionResultBlock, json.RawMessage(`{"type":"bash_code_execution_tool_result_error","error_code":"output_file_too_large"}`)},
		{"editor-error", ServerEditorExecution, EditorExecutionResultBlock, json.RawMessage(`{"type":"text_editor_code_execution_tool_result_error","error_code":"file_not_found","error_message":"private provider diagnostic"}`)},
		{"advisor-error", ServerAdvisor, AdvisorResultBlock, json.RawMessage(`{"type":"advisor_tool_result_error","error_code":"model_not_found"}`)},
		{"tool-search-error", ServerToolSearchRegex, ToolSearchResultBlock, json.RawMessage(`{"type":"tool_search_tool_result_error","error_code":"unavailable","error_message":"private provider diagnostic"}`)},
	}
}

func TestServerExtensionsPreserveProviderSemanticsWithoutLocalAuthority(t *testing.T) {
	for _, fixture := range serverExtensionFixtures() {
		t.Run(fixture.id, func(t *testing.T) {
			b := contentFixture(t)
			contentStart(t, b, "", "msg_extensions")
			use := map[string]any{"type": ServerToolUseBlock, "id": "srv_extension", "name": fixture.name, "input": map[string]any{"original": "private provider input"}}
			serverRootBlock(t, b, "msg_extensions", 0, use)
			block := map[string]any{"type": fixture.kind, "tool_use_id": "srv_extension", "content": fixture.content}
			completed := serverRootBlock(t, b, "msg_extensions", 1, block)
			result := completed.Block.ServerResult
			if result == nil || result.Kind != fixture.kind || result.Name != fixture.name || result.ID != "srv_extension" || b.content.openTools != 0 || len(b.content.tools) != 0 || !b.content.serverTools["srv_extension"].finished || b.advertisedTools["UninstalledProviderTool"] {
				t.Fatal("provider result acquired local tool or inventory authority")
			}
			var stored struct {
				Content json.RawMessage `json:"content"`
			}
			_ = json.Unmarshal(result.Native, &stored)
			want, _ := streamReplyDigest(fixture.content)
			got, _ := streamReplyDigest(stored.Content)
			if want != got {
				t.Fatal("provider extension content was rewritten")
			}
			if result.Execution != nil && result.Execution.Kind == ServerEncryptedCodeResult && result.Execution.Stdout != nil {
				t.Fatal("encrypted stdout became readable text")
			}
			if result.Advice != nil && result.Advice.Kind == ServerAdvisorRedactedResult && result.Advice.Text != nil {
				t.Fatal("redacted advice became readable text")
			}
			published, _ := json.Marshal(completed)
			if bytes.Contains(published, []byte("private provider")) || bytes.Contains(published, []byte("private encrypted")) {
				t.Fatal("provider data bypassed private publication boundary")
			}
			lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "message_stop"}))
			if !lifecycleObserve(t, b, lifecycleResult(t, b, Completed, false)).Result.Successful() {
				t.Fatal("provider subprocess status replaced native input outcome")
			}
		})
	}
}

func TestServerExtensionsRejectMixedMissingAndMalformedShapes(t *testing.T) {
	cases := []struct {
		kind    ContentBlockKind
		content string
	}{
		{CodeExecutionResultBlock, `{"type":"code_execution_result","stdout":"","stderr":"","content":[]}`},
		{CodeExecutionResultBlock, `{"type":"code_execution_result","stdout":"","stderr":"","return_code":0.5,"content":[]}`},
		{CodeExecutionResultBlock, `{"type":"code_execution_result","stdout":"","stderr":"","return_code":0,"content":null}`},
		{CodeExecutionResultBlock, `{"type":"code_execution_result","stdout":"","stderr":"","return_code":0,"encrypted_stdout":null,"content":[]}`},
		{CodeExecutionResultBlock, `{"type":"encrypted_code_execution_result","stdout":null,"stderr":"","return_code":0,"encrypted_stdout":"opaque","content":[]}`},
		{CodeExecutionResultBlock, `{"type":"code_execution_result","stdout":"","stderr":"","return_code":0,"content":[{"type":"bash_code_execution_output","file_id":"foreign"}]}`},
		{BashExecutionResultBlock, `{"type":"code_execution_result","stdout":"","stderr":"","return_code":0,"content":[]}`},
		{CodeExecutionResultBlock, `{"type":"code_execution_tool_result_error","error_code":"unavailable","error_message":"unverified"}`},
		{EditorExecutionResultBlock, `{"type":"text_editor_code_execution_create_result","is_file_update":null}`},
		{EditorExecutionResultBlock, `{"type":"text_editor_code_execution_create_result","is_file_update":false,"content":"mixed"}`},
		{EditorExecutionResultBlock, `{"type":"text_editor_code_execution_str_replace_result","new_start":-1}`},
		{EditorExecutionResultBlock, `{"type":"text_editor_code_execution_str_replace_result","lines":[null]}`},
		{EditorExecutionResultBlock, `{"type":"text_editor_code_execution_view_result","content":"","file_type":"unknown"}`},
		{EditorExecutionResultBlock, `{"type":"text_editor_code_execution_view_result","content":null,"file_type":"text"}`},
		{AdvisorResultBlock, `{"type":"advisor_result","text":"","encrypted_content":null}`},
		{AdvisorResultBlock, `{"type":"advisor_redacted_result","text":null,"encrypted_content":"opaque"}`},
		{AdvisorResultBlock, `{"type":"advisor_result","text":"","stop_reason":"unverified"}`},
		{AdvisorResultBlock, `{"type":"advisor_tool_result_error","error_code":"invalid_tool_input"}`},
		{ToolSearchResultBlock, `{"type":"tool_search_tool_search_result","tool_references":null}`},
		{ToolSearchResultBlock, `{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"Bash","install":true}]}`},
		{ToolSearchResultBlock, `{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":""}]}`},
	}
	for index, item := range cases {
		raw, _ := json.Marshal(map[string]any{"type": item.kind, "tool_use_id": "srv_extension", "content": json.RawMessage(item.content)})
		if _, err := decodeContentBlock(raw); err == nil {
			t.Fatal("unverified provider extension became a typed result", index)
		}
	}
}

func TestToolSearchFamilyCannotCompleteAnotherServerOperation(t *testing.T) {
	b := contentFixture(t)
	contentStart(t, b, "", "msg_wrong_search")
	serverRootBlock(t, b, "msg_wrong_search", 0, map[string]any{"type": ServerToolUseBlock, "id": "srv_extension", "name": ServerCodeExecution, "input": map[string]any{}})
	block := map[string]any{"type": ToolSearchResultBlock, "tool_use_id": "srv_extension", "content": map[string]any{"type": "tool_search_tool_search_result", "tool_references": []any{}}}
	if _, err := b.Observe(contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 1, "content_block": block})); err == nil || b.content.openTools != 1 || b.content.serverTools["srv_extension"].finished {
		t.Fatal("ambiguous search algorithm adopted a different operation family")
	}
}
