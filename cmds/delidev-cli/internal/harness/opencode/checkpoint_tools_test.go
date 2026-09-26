package opencode

import (
	"encoding/json"
	"testing"
)

func checkpointInlineToolFixture(name checkpointToolName) *NativeToolPart {
	output, end := "private original result", uint64(2)
	metadata := json.RawMessage(`{"preview":"private original result","truncated":false,"loaded":[]}`)
	if name == checkpointShellTool {
		metadata = json.RawMessage(`{"output":"private original result","exit":0,"truncated":false}`)
	}
	if name == checkpointQuestionTool {
		metadata = json.RawMessage(`{"answers":[["private original result"]],"truncated":false}`)
	}
	input := json.RawMessage(`{}`)
	if name == checkpointGlobTool {
		metadata = json.RawMessage(`{"count":1,"truncated":false}`)
	}
	if name == checkpointGrepTool {
		metadata = json.RawMessage(`{"matches":1,"truncated":false}`)
	}
	if name == checkpointTodoTool {
		input = json.RawMessage(`{"todos":[{"content":"private original result","status":"waiting","priority":"urgent"}]}`)
		metadata = json.RawMessage(`{"todos":[{"content":"private original result","status":"waiting","priority":"urgent"}],"truncated":false}`)
	}
	if name == checkpointWriteTool {
		metadata = json.RawMessage(`{"diagnostics":{},"filepath":"/private/file.txt","exists":false,"truncated":false}`)
	}
	if name == checkpointEditTool {
		metadata = json.RawMessage(`{"diagnostics":{},"diff":"private original diff","filediff":{"file":"/private/file.txt","patch":"private original patch","additions":1,"deletions":1},"truncated":false}`)
	}
	if name == checkpointApplyPatchTool {
		metadata = json.RawMessage(`{"diagnostics":{},"diff":"private original diff","files":[{"filePath":"/private/file.txt","relativePath":"file.txt","type":"update","patch":"private original patch","additions":1,"deletions":1}],"truncated":false}`)
	}
	return &NativeToolPart{Input: input, Name: string(name), State: ToolCompleted, Output: &output, Timing: &NativeTiming{Start: 1, End: &end}, Metadata: metadata}
}

func TestCheckpointInlineToolsRequireCompleteIndependentState(t *testing.T) {
	for _, name := range []checkpointToolName{checkpointReadTool, checkpointShellTool, checkpointQuestionTool, checkpointWriteTool, checkpointEditTool, checkpointApplyPatchTool} {
		for _, fault := range []string{"valid", "running", "failed", "output", "timing", "end", "compacted", "attachments", "provider", "metadata", "truncated", "interrupted", "missing-fields", "auxiliary"} {
			t.Run(string(name)+"/"+fault, func(t *testing.T) {
				tool := checkpointInlineToolFixture(name)
				var metadata map[string]any
				_ = json.Unmarshal(tool.Metadata, &metadata)
				switch fault {
				case "running":
					tool.State = ToolRunning
				case "failed":
					tool.State = ToolError
				case "output":
					tool.Output = nil
				case "timing":
					tool.Timing = nil
				case "end":
					tool.Timing.End = nil
				case "compacted":
					tool.Timing.Compacted = tool.Timing.End
				case "attachments":
					tool.Attachments = []NativePart{{}}
				case "provider":
					tool.PartMetadata = json.RawMessage(`{"providerExecuted":true}`)
				case "metadata":
					tool.PartMetadata = json.RawMessage(`{"unknown":true}`)
				case "truncated":
					metadata["truncated"] = true
				case "interrupted":
					metadata["interrupted"] = true
				case "missing-fields":
					delete(metadata, "truncated")
				case "auxiliary":
					metadata["outputPath"] = "/private/tool-output"
				}
				tool.Metadata, _ = json.Marshal(metadata)
				if checkpointInlineTool(tool) != (fault == "valid") {
					t.Fatal("replacement authority did not match independent complete tool state")
				}
			})
		}
	}
	for _, raw := range []string{`{"preview":"x","truncated":false}`, `{"preview":"x","truncated":false,"loaded":null}`} {
		tool := checkpointInlineToolFixture(checkpointReadTool)
		tool.Metadata = json.RawMessage(raw)
		if checkpointInlineTool(tool) {
			t.Fatal("absent instruction-loader observation granted restoration")
		}
	}
	for _, raw := range []string{`{"output":"x","truncated":false}`, `{"output":"x","truncated":false,"exit":null}`, `{"output":"x","truncated":false,"exit":0.5}`, `{"output":"x","truncated":false,"exit":0,"outputPath":null}`, `{"output":"x","truncated":false,"exit":9007199254740992}`} {
		tool := checkpointInlineToolFixture(checkpointShellTool)
		tool.Metadata = json.RawMessage(raw)
		if checkpointInlineTool(tool) {
			t.Fatal("unobserved exit granted restoration")
		}
	}
	tool := checkpointInlineToolFixture("future")
	if checkpointInlineTool(tool) || checkpointInlineTool(nil) {
		t.Fatal("unknown tool acquired restoration authority")
	}
}

func checkpointToolsFixture() (nativeCheckpoint, *sessionAPI) {
	value := nativeCheckpoint{Project: "global", NativeRoot: "/"}
	observer := &inputObserver{parts: make(map[string]*observedPart)}
	message := HistoryMessage{ID: "private-message"}
	for _, name := range []checkpointToolName{checkpointReadTool, checkpointShellTool} {
		id := "private-" + string(name)
		raw := []byte(`{"private":"` + string(name) + `"}`)
		message.Parts = append(message.Parts, HistoryPart{ID: id, Kind: ToolPartKind, Digest: mutationDigest(raw)})
		observer.parts[id] = &observedPart{raw: raw, value: NativePart{Tool: checkpointInlineToolFixture(name)}}
	}
	value.History.Messages = []HistoryMessage{message}
	s := &sessionAPI{observer: observer}
	value.Tools = s.checkpointToolHistory(value)
	return value, s
}

func TestCheckpointToolProofPreservesCompleteOrderedLineage(t *testing.T) {
	for _, fault := range []string{"valid", "legacy", "version", "interactions", "missing", "extra", "reorder", "duplicate", "identity", "digest", "name"} {
		t.Run(fault, func(t *testing.T) {
			value, _ := checkpointToolsFixture()
			if value.Tools == nil {
				t.Fatal("original completed inline evidence missing")
			}
			switch fault {
			case "legacy":
				value.Tools = nil
			case "version":
				value.Tools.Version++
			case "interactions":
				value.Tools.InteractionFree = false
			case "missing":
				value.Tools.Parts = value.Tools.Parts[:1]
			case "extra":
				value.Tools.Parts = append(value.Tools.Parts, value.Tools.Parts[0])
			case "reorder":
				value.Tools.Parts[0], value.Tools.Parts[1] = value.Tools.Parts[1], value.Tools.Parts[0]
			case "duplicate":
				value.Tools.Parts[1] = value.Tools.Parts[0]
			case "identity":
				value.Tools.Parts[0].ID += "changed"
			case "digest":
				value.Tools.Parts[0].Digest = mutationDigest([]byte("changed"))
			case "name":
				value.Tools.Parts[0].Name = "future"
			}
			if (checkpointReplacementProfile(value) == nil) != (fault == "valid") {
				t.Fatal("contradictory tool evidence acquired replacement authority")
			}
		})
	}
	value, s := checkpointToolsFixture()
	next := nativeCheckpoint{Project: "global", NativeRoot: "/", Previous: []HistoryObservation{value.History}}
	s.predecessor = &value
	s.observer = &inputObserver{}
	next.Tools = s.checkpointToolHistory(next)
	if next.Tools == nil || len(next.Tools.Parts) != 2 || checkpointReplacementProfile(next) != nil {
		t.Fatal("text successor lost predecessor tool evidence")
	}
	next.Tools.Parts[0].ID = "changed"
	if value.Tools.Parts[0].ID == "changed" {
		t.Fatal("successor changed original proof")
	}
}

func TestCheckpointToolCaptureCannotInventPositiveObservation(t *testing.T) {
	for _, fault := range []string{"interaction", "problem", "missing", "changed", "unsupported", "predecessor"} {
		t.Run(fault, func(t *testing.T) {
			value, s := checkpointToolsFixture()
			value.Tools = nil
			switch fault {
			case "interaction":
				s.observer.interactions = map[string]*observedInteraction{"original": {}}
			case "problem":
				s.observer.problem = sessionUncertain()
			case "missing":
				delete(s.observer.parts, value.History.Messages[0].Parts[0].ID)
			case "changed":
				s.observer.parts[value.History.Messages[0].Parts[0].ID].raw = []byte("changed")
			case "unsupported":
				s.observer.parts[value.History.Messages[0].Parts[0].ID].value.Tool.Name = "future"
			case "predecessor":
				s.predecessor = &nativeCheckpoint{Project: "foreign"}
			}
			if s.checkpointToolHistory(value) != nil {
				t.Fatal("missing original evidence was inferred")
			}
		})
	}
}
