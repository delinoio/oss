// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestSubagentClaudeCreationRequiresOriginalTaskAtomically(t *testing.T) {
	for _, test := range []struct {
		source domain.SubagentSource
		task   bool
	}{
		{domain.ClaudeContentSource, false}, {domain.ClaudeHistorySource, false},
		{domain.ClaudeContentSource, true}, {domain.ClaudeHistorySource, true},
		{domain.ClaudeTaskSource, false},
	} {
		name := string(test.source)
		if test.task {
			name += "/with-task-metadata"
		} else {
			name += "/without-task-metadata"
		}
		t.Run(name, func(t *testing.T) {
			f, children, sequence := claudeSubagentPublicationFixture(t)
			children[1].Source = test.source
			if test.task {
				children[1].Task = &domain.SubagentTask{}
			} else {
				children[1].Task = nil
			}
			if test.source != domain.ClaudeTaskSource {
				children[1].Output = &domain.SubagentOutput{NativeMessageID: "unowned-child-content", Text: "Unowned child output", Partial: true}
			}
			ctx := context.Background()
			before, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			event := f.event(domain.ExecutionSubagentObserved, sequence+1)
			// The valid task-backed sibling precedes the invalid new child.
			event.Subagents = children
			if _, err := f.call(f.requestEvent(t, event)); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("unowned initial child did not reject the complete batch", err)
			}
			after, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			if err != nil || after.Revision != before.Revision {
				t.Fatal("unowned initial child advanced session ownership", err)
			}
			for _, child := range children {
				if _, err := f.service.Store.Get(ctx, domain.SubagentKind, child.ID); domain.SafeError(err).Code != domain.NotFound {
					t.Fatal("unowned initial child partially published its batch", err)
				}
			}
			// Rejection does not consume the original sequence or valid sibling.
			event.Subagents = children[:1]
			f.publish(t, event)
		})
	}
}

func TestSubagentClaudeTaskCreationAllowsOwnedContentHistoryAndNestedTask(t *testing.T) {
	f, children, sequence := claudeSubagentPublicationFixture(t)
	parent := children[0]
	event := f.event(domain.ExecutionSubagentObserved, sequence+1)
	event.Subagents = []domain.SubagentObservation{parent}
	f.publish(t, event)
	parent.Source, parent.SourceID = domain.ClaudeContentSource, string(domain.NewID())
	parent.Tools = []domain.SubagentTool{{NativeID: "original-nested-agent", Name: "Agent"}}
	parent.Output = &domain.SubagentOutput{NativeMessageID: "owned-parent-content", Text: "Owned child output", Partial: true}
	event.Sequence++
	event.Subagents = []domain.SubagentObservation{parent}
	f.publish(t, event)
	nested := domain.SubagentObservation{ID: domain.NewID(), NativeID: "original-nested-child", ParentID: parent.NativeID, ParentToolID: parent.Tools[0].NativeID, Source: domain.ClaudeTaskSource, SourceID: string(domain.NewID()), Status: domain.SubagentRunning, Task: &domain.SubagentTask{}}
	event.Sequence++
	event.Subagents = []domain.SubagentObservation{nested}
	f.publish(t, event)
	parent.Source, parent.SourceID, parent.Tools = domain.ClaudeHistorySource, string(domain.NewID()), nil
	nested.Source, nested.SourceID = domain.ClaudeContentSource, string(domain.NewID())
	nested.Output = &domain.SubagentOutput{NativeMessageID: "owned-nested-content", Text: "Owned nested output", Partial: true}
	event.Sequence++
	event.Subagents = []domain.SubagentObservation{parent, nested}
	req := f.requestEvent(t, event)
	if _, err := f.call(req); err != nil {
		t.Fatal(err)
	}
	before, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.call(req); err != nil {
		t.Fatal("exact owned content receipt did not replay", err)
	}
	after, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil || after.Revision != before.Revision {
		t.Fatal("replayed owned content advanced the session", err)
	}
	for _, child := range []domain.SubagentObservation{parent, nested} {
		row, err := f.service.Store.Get(context.Background(), domain.SubagentKind, child.ID)
		if err != nil {
			t.Fatal(err)
		}
		retained, err := store.Decode[domain.SubagentRecord](row)
		if err != nil || retained.Sources[0].Source != domain.ClaudeTaskSource || retained.Observation.Output == nil || retained.Observation.Output.NativeMessageID != child.Output.NativeMessageID {
			t.Fatal("task-backed child lost original ownership or content", err)
		}
	}
}
