package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func nativeFilePatchFixture() []any {
	return []any{map[string]any{"oldStart": 1, "oldLines": 1, "newStart": 1, "newLines": 1, "lines": []any{"-Private original file", "+Private replacement file"}}}
}
func inlineFileFixtureValues(kind inlineToolKind) (map[string]any, map[string]any) {
	path := "/private/original-file-fixture.txt"
	if kind == inlineWriteTool {
		return map[string]any{"file_path": path, "content": "Private written content"}, map[string]any{"type": "create", "filePath": path, "content": "Private written content", "originalFile": nil, "structuredPatch": []any{}, "userModified": false}
	}
	return map[string]any{"file_path": path, "old_string": "Private original file", "new_string": "Private replacement file", "replace_all": false}, map[string]any{"filePath": path, "oldString": "Private original file", "newString": "Private replacement file", "originalFile": "Private original file", "structuredPatch": nativeFilePatchFixture(), "userModified": false, "replaceAll": false}
}

func TestFileToolMetadataPreservesCompletePinnedShapes(t *testing.T) {
	for _, kind := range []inlineToolKind{inlineWriteTool, inlineEditTool} {
		for _, name := range []string{"valid", "unknown-field", "missing-path", "null-content", "original-file", "missing-modified", "user-modified", "null-patch", "null-hunk", "missing-hunk-field", "null-line", "alias-hunk-field", "wrong-family", "negative-coordinate"} {
			t.Run(string(kind)+"/"+name, func(t *testing.T) {
				_, value := inlineFileFixtureValues(kind)
				// Exercise nonempty replacement hunks for both Write and Edit.
				if kind == inlineWriteTool {
					value["type"], value["originalFile"], value["structuredPatch"] = "update", "Private original file", nativeFilePatchFixture()
				}
				switch name {
				case "unknown-field":
					value["newFlag"] = true
				case "missing-path":
					delete(value, "filePath")
				case "null-content":
					if kind == inlineWriteTool {
						value["content"] = nil
					} else {
						value["newString"] = nil
					}
				case "original-file":
					value["originalFile"] = nil
				case "missing-modified":
					delete(value, "userModified")
				case "user-modified":
					value["userModified"] = true
				case "null-patch":
					value["structuredPatch"] = nil
				case "null-hunk":
					value["structuredPatch"] = []any{nil}
				case "missing-hunk-field":
					delete(value["structuredPatch"].([]any)[0].(map[string]any), "oldStart")
				case "null-line":
					value["structuredPatch"].([]any)[0].(map[string]any)["lines"] = []any{nil}
				case "alias-hunk-field":
					hunk := value["structuredPatch"].([]any)[0].(map[string]any)
					delete(hunk, "oldLines")
					hunk["OLDLINES"] = 1
				case "wrong-family":
					if kind == inlineWriteTool {
						value["type"] = "replacement"
					} else {
						value["replaceAll"] = "false"
					}
				case "negative-coordinate":
					value["structuredPatch"].([]any)[0].(map[string]any)["newStart"] = -1
				}
				raw, _ := json.Marshal(value)
				_, valid := inlineMetadata(kind, raw)
				if valid != (name == "valid") {
					t.Fatal("file tool metadata accepted an unproved shape")
				}
			})
		}
	}
	for _, name := range []string{"valid-create", "missing-original", "nonempty-original", "nonempty-patch"} {
		t.Run(name, func(t *testing.T) {
			_, value := inlineFileFixtureValues(inlineWriteTool)
			switch name {
			case "missing-original":
				delete(value, "originalFile")
			case "nonempty-original":
				value["originalFile"] = "original"
			case "nonempty-patch":
				value["structuredPatch"] = nativeFilePatchFixture()
			}
			raw, _ := json.Marshal(value)
			_, valid := inlineWriteMetadata(raw)
			if valid != (name == "valid-create") {
				t.Fatal("creation replaced original update evidence")
			}
		})
	}
}

func TestFileToolCheckpointRetainsExactFamilyAndOriginalResult(t *testing.T) {
	for _, kind := range []inlineToolKind{inlineWriteTool, inlineEditTool} {
		t.Run(string(kind), func(t *testing.T) {
			s := inlineContinuationFixture(t, kind)
			closed, err := s.CloseForContinuation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			raw, ref, err := closed.RetainCheckpoint(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{"/private/original-file-fixture.txt", "Private written content", "Private original file", "Private replacement file"} {
				if bytes.Contains(raw, []byte(private)) {
					t.Fatal("private file content entered checkpoint")
				}
			}
			cfg := s.config
			cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
			restored, err := RestoreCheckpoint(context.Background(), cfg, raw, ref)
			if err != nil {
				t.Fatal(err)
			}
			tool := restored.previous.current.content.tools["toolu_original_"+strings.ToLower(string(kind))]
			if tool.name != string(kind) || tool.ownerInput != s.current.input || tool.inline == nil {
				t.Fatal("file ownership changed")
			}
			if _, _, err := restored.RetainCheckpoint(context.Background()); err != nil {
				t.Fatal(err)
			}
			var cp sessionCheckpoint
			if json.Unmarshal(raw, &cp) != nil {
				t.Fatal("invalid checkpoint")
			}
			for _, change := range []string{"omitted", "family", "metadata", "duplicate"} {
				changed := cp
				switch change {
				case "omitted":
					changed.WriteTools, changed.EditTools = nil, nil
				case "family":
					changed.WriteTools, changed.EditTools = cp.EditTools, cp.WriteTools
				case "metadata":
					changed.WriteTools = append([]checkpointInlineTool(nil), cp.WriteTools...)
					changed.EditTools = append([]checkpointInlineTool(nil), cp.EditTools...)
					for _, group := range changed.inlineTools().groups() {
						if len(group.Items) != 0 {
							group.Items[0].MetadataDigest = strings.Repeat("0", 64)
						}
					}
				case "duplicate":
					changed.BashTools = append(append([]checkpointInlineTool(nil), cp.WriteTools...), cp.EditTools...)
				}
				altered, _ := json.Marshal(changed)
				alteredRef := ref
				alteredRef.SHA256 = checkpointDigest(altered)
				if actual, err := RestoreCheckpoint(context.Background(), cfg, altered, alteredRef); err == nil || actual != nil {
					t.Fatal("changed file-tool proof accepted", change)
				}
			}
		})
	}
}

func TestInlineReadMetadataRejectsNestedAliases(t *testing.T) {
	raw := []byte(`{"type":"text","file":{"filePath":"/private/fixture.txt","content":"fixture","numLines":1,"startLine":1,"totalLines":1}}`)
	if _, valid := inlineReadMetadata(raw); !valid {
		t.Fatal("valid native Read result refused")
	}
	for _, field := range []string{"filePath", "content", "numLines", "startLine", "totalLines"} {
		changed := bytes.Replace(raw, []byte(`"`+field+`"`), []byte(`"`+strings.ToUpper(field)+`"`), 1)
		if _, valid := inlineReadMetadata(changed); valid {
			t.Fatal("nested native field alias accepted", field)
		}
	}
}
