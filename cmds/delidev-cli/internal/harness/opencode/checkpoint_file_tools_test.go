package opencode

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCheckpointFileToolsPreserveOriginalResultsAndLineage(t *testing.T) {
	for _, name := range []checkpointToolName{checkpointWriteTool, checkpointEditTool, checkpointApplyPatchTool} {
		t.Run(string(name), func(t *testing.T) {
			value, s := checkpointToolsFixture()
			part := &value.History.Messages[0].Parts[0]
			s.observer.parts[part.ID].value.Tool = checkpointInlineToolFixture(name)
			value.Tools = s.checkpointToolHistory(value)
			if value.Tools == nil || value.Tools.Version != 7 || !value.Tools.InteractionFree || checkpointReplacementProfile(value) != nil {
				t.Fatal("original file result lost replacement eligibility")
			}
			original, _ := json.Marshal(value)
			s.predecessor, s.observer = &value, &inputObserver{}
			next := nativeCheckpoint{Project: "global", NativeRoot: fixtureNativeRoot(), Previous: []HistoryObservation{copyHistoryObservation(value.History)}}
			next.Tools = s.checkpointToolHistory(next)
			if next.Tools == nil || next.Tools.Version != 7 || checkpointReplacementProfile(next) != nil {
				t.Fatal("text successor dropped original file history")
			}
			next.Tools.Parts[0].Digest = "changed"
			after, _ := json.Marshal(value)
			if !bytes.Equal(original, after) {
				t.Fatal("successor mutated original file evidence")
			}
			for version := uint32(1); version <= 6; version++ {
				value.Tools.Version = version
				if validCheckpointTools(value) {
					t.Fatal("legacy proof gained file tool authority", version)
				}
			}
		})
	}
}

func TestCheckpointFileMetadataRetainsOpaqueDiagnosticsAndRejectsMissingState(t *testing.T) {
	for _, name := range []checkpointToolName{checkpointWriteTool, checkpointEditTool, checkpointApplyPatchTool} {
		tool := checkpointInlineToolFixture(name)
		// Diagnostic extensions are original inert JSON, not product diagnostics.
		tool.Metadata = bytes.Replace(tool.Metadata, []byte(`"diagnostics":{}`), []byte(`"diagnostics":{"/private/file.txt":[{"message":"original","extension":9007199254740993}]}`), 1)
		original := bytes.Clone(tool.Metadata)
		if !checkpointInlineTool(tool) || !bytes.Equal(original, tool.Metadata) {
			t.Fatal("opaque original diagnostic content was dropped or rounded", name)
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(original, &fields)
		for key := range fields {
			missing := make(map[string]json.RawMessage)
			_ = json.Unmarshal(original, &missing)
			delete(missing, key)
			tool.Metadata, _ = json.Marshal(missing)
			if checkpointInlineTool(tool) {
				t.Fatal("missing required native metadata accepted", name, key)
			}
		}
	}
	for _, kind := range []string{"add", "update", "delete", "move"} {
		raw := []byte(`{"filePath":"/private/file","relativePath":"file","type":"` + kind + `","patch":"","additions":0,"deletions":0,"movePath":"/private/moved"}`)
		if !checkpointFileDiff(raw, true) {
			t.Fatal("native patch operation lost", kind)
		}
		if checkpointFileDiff(bytes.Replace(raw, []byte(`"additions":0`), []byte(`"additions":-1`), 1), true) {
			t.Fatal("invalid patch count accepted")
		}
	}
}

func TestCheckpointFileToolOncePermissionRetainsExactClaim(t *testing.T) {
	for _, name := range []checkpointToolName{checkpointWriteTool, checkpointEditTool, checkpointApplyPatchTool} {
		value, s, interaction := checkpointPermissionFixture(t)
		part := s.observer.parts[interaction.attempt.claim.PartID]
		part.value.Tool.Name = string(name)
		part.value.Tool.Metadata = checkpointInlineToolFixture(name).Metadata
		value.Tools = s.checkpointToolHistory(value)
		if value.Tools == nil || value.Tools.Version != 7 || value.Tools.InteractionFree || len(value.Tools.Once) != 1 || value.Tools.Once[0] != interaction.attempt.claim || checkpointReplacementProfile(value) != nil {
			t.Fatal("file tool lost original one-time permission", name)
		}
		interaction.attempt.receipt.NativeAccepted = false
		if s.checkpointToolHistory(value) != nil {
			t.Fatal("file result fabricated permission acceptance", name)
		}
	}
}

func TestCheckpointFileToolComposesWithOriginalTodoLineage(t *testing.T) {
	value, s := checkpointToolsFixture()
	part := &value.History.Messages[0].Parts[0]
	s.observer.parts[part.ID].value.Tool = checkpointInlineToolFixture(checkpointTodoTool)
	value.History.Todo = &TodoHistoryObservation{EventID: "evt_01960dcbe299ABCDEFGHIJKLMN", Digest: mutationDigest([]byte(`[]`))}
	observed := *value.History.Todo
	s.observer.todo = &observed
	value.Tools = s.checkpointToolHistory(value)
	if value.Tools == nil || value.Tools.Version != 6 {
		t.Fatal("original Todo checkpoint missing")
	}
	original, _ := json.Marshal(value)
	next, nextSession := checkpointToolsFixture()
	next.Previous = []HistoryObservation{copyHistoryObservation(value.History)}
	next.History.Messages[0].Parts = next.History.Messages[0].Parts[:1]
	current := &next.History.Messages[0].Parts[0]
	current.ID = "private-file-part"
	nextSession.observer.parts[current.ID] = &observedPart{raw: []byte(`{"private":"read"}`), value: NativePart{Tool: checkpointInlineToolFixture(checkpointWriteTool)}}
	nextSession.predecessor = &value
	next.Tools = nextSession.checkpointToolHistory(next)
	if next.Tools == nil || next.Tools.Version != 7 || latestCheckpointTodo(next) == nil || *latestCheckpointTodo(next) != observed || checkpointReplacementProfile(next) != nil {
		t.Fatal("new file result lost earlier native Todo state")
	}
	after, _ := json.Marshal(value)
	if !bytes.Equal(original, after) {
		t.Fatal("file continuation rewrote original Todo evidence")
	}
}
