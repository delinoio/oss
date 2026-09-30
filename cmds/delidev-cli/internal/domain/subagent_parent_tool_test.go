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
