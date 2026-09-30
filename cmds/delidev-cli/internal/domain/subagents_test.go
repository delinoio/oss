// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"reflect"
	"testing"
)

func TestSubagentTreeRejectsForeignCyclicAndReusedIdentitiesAtomically(t *testing.T) {
	root, first, second := string(NewID()), string(NewID()), string(NewID())
	a := SubagentObservation{ID: NewID(), NativeID: first, ParentID: root, Source: CodexCollaborationSource, SourceID: "spawn-one", Status: SubagentRunning}
	b := SubagentObservation{ID: NewID(), NativeID: second, ParentID: first, Source: CodexCollaborationSource, SourceID: "spawn-two", Status: SubagentRunning}
	state, err := ApplySubagents(nil, root, []SubagentObservation{b, a})
	if err != nil || state.Closed() {
		t.Fatal("nested tree lost live ownership", err)
	}
	before := SubagentState{}
	for key, owner := range state {
		before[key] = owner
	}
	for _, scenario := range []string{"foreign", "cycle", "reuse", "reparent", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			changed := b
			batch := []SubagentObservation{changed}
			switch scenario {
			case "foreign":
				changed.ParentID = string(NewID())
			case "cycle":
				changed.ParentID = second
			case "reuse":
				changed.ID = a.ID
			case "reparent":
				changed.ParentID = root
			case "duplicate":
				batch = append(batch, b)
			}
			batch[0] = changed
			if _, err := ApplySubagents(state, root, batch); err == nil {
				t.Fatal("invalid ownership accepted")
			}
			if !reflect.DeepEqual(state, before) {
				t.Fatal("failed batch changed retained state")
			}
		})
	}
	a.Status = SubagentCompleted
	state, err = ApplySubagents(state, root, []SubagentObservation{a})
	if err != nil || state.Closed() {
		t.Fatal("parent completed its live descendant", err)
	}
	b.Status = SubagentCompleted
	state, err = ApplySubagents(state, root, []SubagentObservation{b})
	if err != nil || !state.Closed() {
		t.Fatal("late child completion lost", err)
	}
	b.Status = SubagentShutdown
	state, err = ApplySubagents(state, root, []SubagentObservation{b})
	if err != nil || !state.Closed() {
		t.Fatal("native shutdown after completion was rejected", err)
	}
	b.Status = SubagentCompleted
	if _, err := ApplySubagents(state, root, []SubagentObservation{b}); err == nil {
		t.Fatal("shutdown lifecycle was reopened")
	}
	b.Status = SubagentRunning
	if _, err := ApplySubagents(state, root, []SubagentObservation{b}); err == nil {
		t.Fatal("terminal regression accepted")
	}
}

func TestSubagentUsageKeepsUnavailableAndExactOverlappingCounters(t *testing.T) {
	total := "18446744073709551615"
	model := "requested-only"
	v := SubagentObservation{ID: NewID(), NativeID: "child", ParentID: "root", Status: SubagentRunning, Source: ClaudeTaskSource, SourceID: "task-start", RequestedModel: &model, Usage: &SubagentUsage{Scope: SubagentCumulativeUsage, Total: &total, NativeReport: `{"total_tokens":18446744073709551615,"tool_uses":0,"duration_ms":1}`}}
	if v.Validate() != nil || v.ObservedModel != nil || v.Output != nil || v.Usage.Input != nil {
		t.Fatal("missing observations were fabricated")
	}
	bad := "01"
	v.Usage.Input = &bad
	if v.Validate() == nil {
		t.Fatal("noncanonical counter accepted")
	}
}
