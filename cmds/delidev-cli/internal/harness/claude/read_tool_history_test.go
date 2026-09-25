package claude

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func readContinuationFixture(t *testing.T) *APISession {
	t.Helper()
	return inlineContinuationFixture(t, inlineReadTool)
}

func inlineContinuationFixture(t *testing.T, kind inlineToolKind) *APISession {
	t.Helper()
	s, _ := continuationFixture(t)
	path := filepath.Join(s.config.Home, "projects", "delidev", string(s.config.SessionID)+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	input := map[string]any{"file_path": "/private/read-fixture.txt"}
	if kind == inlineBashTool {
		input = map[string]any{"command": "printf private-original-bash"}
	}
	inputRaw, _ := json.Marshal(input)
	inputHash, err := streamReplyDigest(inputRaw)
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]any{"type": "text", "file": map[string]any{"filePath": "/private/read-fixture.txt", "content": "Private original Read content", "numLines": 1, "startLine": 1, "totalLines": 1}}
	if kind == inlineBashTool {
		metadata = map[string]any{"stdout": "Private original Bash content", "stderr": "", "interrupted": false, "isImage": false, "noOutputExpected": false}
	}
	metadataRaw, _ := json.Marshal(metadata)
	metadataHash, valid := inlineMetadata(kind, metadataRaw)
	if !valid {
		t.Fatal("invalid fixture Read metadata")
	}
	toolID, message, resultID := "toolu_original_"+strings.ToLower(string(kind)), "msg_original_"+strings.ToLower(string(kind)), string(domain.NewID())
	tool := map[string]any{"type": "assistant", "uuid": string(domain.NewID()), "parentUuid": records[3]["parentUuid"], "sessionId": s.config.SessionID, "cwd": s.config.Workspace, "version": SupportedVersion, "isSidechain": false, "message": map[string]any{"role": "assistant", "id": message, "model": s.config.Model, "content": []any{map[string]any{"type": "tool_use", "id": toolID, "name": string(kind), "input": input}}}}
	result := map[string]any{"type": "user", "uuid": resultID, "parentUuid": tool["uuid"], "sessionId": s.config.SessionID, "cwd": s.config.Workspace, "version": SupportedVersion, "isSidechain": false, "toolUseResult": metadata, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": toolID, "is_error": false, "content": "Private original Read content"}}}}
	records[3]["parentUuid"] = resultID
	records = append(records[:3:3], append([]map[string]any{tool, result}, records[3:]...)...)
	s.history = &sessionHistory{}
	s.current.content.seen = map[string]bool{}
	for _, record := range records {
		if record["type"] != "user" && record["type"] != "assistant" {
			continue
		}
		raw, _ := json.Marshal(map[string]any{"type": record["type"], "uuid": record["uuid"], "session_id": s.config.SessionID, "parent_tool_use_id": nil, "message": record["message"]})
		if err := s.history.observe(LifecycleObservation{SessionID: s.config.SessionID, Native: &StreamEvent{Kind: NativeMessage, Type: record["type"].(string), Body: raw}}); err != nil {
			t.Fatal(err)
		}
		s.current.seen[record["uuid"].(string)] = true
		if record["type"] == "assistant" {
			s.current.content.seen["\x00"+record["message"].(map[string]any)["id"].(string)] = true
		}
	}
	s.current.content.tools = map[string]nativeToolState{toolID: {name: string(kind), ownerInput: s.current.input, ownerTurn: s.current.turnID, message: message, input: inputHash, streamed: true, finished: true, inline: &inlineToolEvidence{NativeID: resultID, Metadata: metadataHash}}}
	if err := os.WriteFile(path, historyJSONL(t, records), 0600); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReadCheckpointPreservesOriginalToolOwnershipWithoutContent(t *testing.T) {
	s := readContinuationFixture(t)
	closed, err := s.CloseForContinuation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, ref, err := closed.RetainCheckpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"/private/read-fixture.txt", "Private original Read content", s.config.Home} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("private Read content entered checkpoint")
		}
	}
	cfg := s.config
	cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
	restored, err := RestoreCheckpoint(context.Background(), cfg, raw, ref)
	if err != nil {
		t.Fatal(err)
	}
	original := s.current.content.tools["toolu_original_read"]
	actual := restored.previous.current.content.tools["toolu_original_read"]
	if actual.name != original.name || actual.ownerInput != original.ownerInput || actual.ownerTurn != original.ownerTurn || actual.message != original.message || actual.input != original.input || !actual.finished || !actual.streamed || actual.inline == nil || *actual.inline != *original.inline {
		t.Fatal("Read checkpoint lost original ownership")
	}
	clear(raw)
	if _, _, err := restored.RetainCheckpoint(context.Background()); err != nil {
		t.Fatal("restored Read evidence was not independently owned", err)
	}
}
func TestReadCheckpointRejectsChangedOrOmittedToolProvenance(t *testing.T) {
	for _, name := range []string{"omitted", "tool", "input", "turn", "provider", "index", "input-digest", "result", "metadata", "caller", "duplicate", "changed-supplement"} {
		t.Run(name, func(t *testing.T) {
			s := readContinuationFixture(t)
			closed, err := s.CloseForContinuation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			raw, ref, err := closed.RetainCheckpoint(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var cp sessionCheckpoint
			if err := json.Unmarshal(raw, &cp); err != nil {
				t.Fatal(err)
			}
			tool := &cp.ReadTools[0]
			switch name {
			case "omitted":
				cp.ReadTools = nil
			case "tool":
				tool.ID = "toolu_foreign"
			case "input":
				tool.Input = domain.NewID()
			case "turn":
				tool.Turn = string(domain.NewID())
			case "provider":
				tool.Message = "foreign-provider"
			case "index":
				tool.Index++
			case "input-digest":
				tool.InputDigest = hex.EncodeToString(bytes.Repeat([]byte{0}, 32))
			case "result":
				tool.Result = string(cp.Input)
			case "metadata":
				tool.MetadataDigest = hex.EncodeToString(bytes.Repeat([]byte{0}, 32))
			case "caller":
				tool.Caller = CodeCaller20250825
			case "duplicate":
				cp.ReadTools = append(cp.ReadTools, *tool)
			case "changed-supplement":
				path := filepath.Join(s.config.Home, "projects", "delidev", string(s.config.SessionID)+".jsonl")
				contents, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				contents = bytes.ReplaceAll(contents, []byte(`"numLines":1`), []byte(`"numLines":2`))
				if err := os.WriteFile(path, contents, 0600); err != nil {
					t.Fatal(err)
				}
			}
			raw, _ = json.Marshal(cp)
			ref.SHA256 = checkpointDigest(raw)
			cfg := s.config
			cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
			if restored, err := RestoreCheckpoint(context.Background(), cfg, raw, ref); err == nil || restored != nil {
				t.Fatal("changed Read evidence granted process replacement")
			}
		})
	}
}
func TestReadClosureRequiresSettledInlineRootObservation(t *testing.T) {
	for _, name := range []string{"pending", "other-tool", "child", "unstreamed", "missing-result", "programmatic"} {
		t.Run(name, func(t *testing.T) {
			s := readContinuationFixture(t)
			tool := s.current.content.tools["toolu_original_read"]
			switch name {
			case "pending":
				tool.finished = false
			case "other-tool":
				tool.name = "Write"
			case "child":
				tool.parent = "parent-task"
			case "unstreamed":
				tool.streamed = false
			case "missing-result":
				tool.inline = nil
			case "programmatic":
				tool.caller = NativeToolCaller{Kind: CodeCaller20250825, ToolID: "caller"}
			}
			s.current.content.tools["toolu_original_read"] = tool
			if closed, err := s.CloseForContinuation(context.Background()); err == nil || closed != nil {
				t.Fatal("unsupported tool history granted replacement")
			}
		})
	}
}
