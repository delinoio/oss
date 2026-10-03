// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"reflect"
	"testing"
)

func TestSubagentParentToolOwnershipIsUniqueAcrossBatchAndRetainedChildren(t *testing.T) {
	root := "original-root"
	a := SubagentObservation{ID: NewID(), NativeID: "first-child", ParentID: root, ParentToolID: "original-agent-tool", Source: ClaudeTaskSource, SourceID: "first-start", Status: SubagentRunning}
	b := a
	b.ID, b.NativeID, b.SourceID = NewID(), "second-child", "second-start"
	if _, err := ApplySubagents(nil, root, []SubagentObservation{a, b}); err == nil {
		t.Fatal("complete batch admitted two children for one original parent tool")
	}
	for _, status := range []SubagentStatus{SubagentRunning, SubagentCompleted} {
		a.Status = status
		state, err := ApplySubagents(nil, root, []SubagentObservation{a})
		if err != nil {
			t.Fatal(err)
		}
		before := SubagentState{a.NativeID: state[a.NativeID]}
		if _, err := ApplySubagents(state, root, []SubagentObservation{b}); err == nil || !reflect.DeepEqual(state, before) {
			t.Fatal("duplicate parent tool reused retained ownership or changed prior state")
		}
		distinct := b
		distinct.ParentToolID = "another-original-agent-tool"
		if next, err := ApplySubagents(state, root, []SubagentObservation{distinct}); err != nil || len(next) != 2 {
			t.Fatal("distinct original tools could not own independent children", err)
		}
	}
}

func TestSubagentToolsRequireVerifiedContentForNewAndRetainedEntries(t *testing.T) {
	root := "original-root"
	tool := SubagentTool{NativeID: "nested-agent-tool", Name: "Agent"}
	child := SubagentObservation{
		ID:           NewID(),
		NativeID:     "child",
		ParentID:     root,
		ParentToolID: "original-agent-tool",
		Source:       ClaudeContentSource,
		SourceID:     "content-report",
		Status:       SubagentRunning,
		Tools:        []SubagentTool{tool},
	}
	state, err := ApplySubagents(nil, root, []SubagentObservation{child})
	if err != nil {
		t.Fatal("verified content could not introduce a nested tool", err)
	}

	repeated := child
	repeated.Source, repeated.SourceID = ClaudeHistorySource, "history-report"
	if _, err := ApplySubagents(state, root, []SubagentObservation{repeated}); err != nil {
		t.Fatal("history could not repeat a retained nested tool", err)
	}

	for _, source := range []SubagentSource{ClaudeTaskSource, ClaudeHistorySource} {
		bad := repeated
		bad.Source, bad.SourceID = source, string(source)+"-new-tool"
		bad.Tools = append(append([]SubagentTool(nil), tool), SubagentTool{NativeID: "invented-agent-tool", Name: "Agent"})
		if _, err := ApplySubagents(state, root, []SubagentObservation{bad}); err == nil {
			t.Fatalf("%s report introduced an unretained nested tool", source)
		}
	}
}
