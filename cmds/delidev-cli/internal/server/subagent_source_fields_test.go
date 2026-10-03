// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestSubagentSourceFieldMatrixRejectsWholePublication(t *testing.T) {
	for _, test := range []struct {
		source domain.SubagentSource
		field  string
	}{
		{domain.ClaudeTaskSource, "output"}, {domain.ClaudeTaskSource, "model"},
		{domain.CodexActivitySource, "output"}, {domain.CodexActivitySource, "model"}, {domain.CodexActivitySource, "requested"},
		{domain.CodexCollaborationSource, "model"}, {domain.CodexCollaborationSource, "blocks"},
	} {
		t.Run(string(test.source)+"/"+test.field, func(t *testing.T) {
			var f *publicationFixture
			var children []domain.SubagentObservation
			sequence := uint64(2)
			if test.source == domain.ClaudeTaskSource {
				f, children, sequence = claudeSubagentPublicationFixture(t)
			} else {
				f = newPublicationFixture(t)
				f.registerGrant(t)
				f.publish(t, f.event(domain.ExecutionThreadBound, 1))
				f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
				for i := 0; i < 2; i++ {
					children = append(children, domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(f.thread), Status: domain.SubagentRunning, Source: domain.CodexHistorySource, SourceID: string(domain.NewID())})
				}
			}
			children[1].Source = test.source
			model, text := "source_cannot_supply_model", "source cannot supply content"
			switch test.field {
			case "model":
				children[1].ObservedModel = &model
			case "requested":
				children[1].RequestedModel = &model
			case "output":
				children[1].Output = &domain.SubagentOutput{NativeMessageID: "native_output", Text: text, Partial: true}
			case "blocks":
				children[1].Output = &domain.SubagentOutput{NativeMessageID: "native_output", Partial: true, Blocks: []domain.SubagentOutputBlock{{Kind: "thinking", Text: &text}}}
			}
			ctx := context.Background()
			before, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			event := f.event(domain.ExecutionSubagentObserved, sequence+1)
			event.Subagents = children
			if _, err := f.call(f.requestEvent(t, event)); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("unavailable native source field did not reject the batch", err)
			}
			after, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			if err != nil || after.Revision != before.Revision {
				t.Fatal("invalid source field advanced session progress", err)
			}
			for _, child := range children {
				if _, err := f.service.Store.Get(ctx, domain.SubagentKind, child.ID); domain.SafeError(err).Code != domain.NotFound {
					t.Fatal("invalid source field partially retained a child", err)
				}
			}
		})
	}
}

func TestSubagentTaskOmissionRetainsContentWithoutAttributingItToTask(t *testing.T) {
	f, children, sequence := claudeSubagentPublicationFixture(t)
	child := children[0]
	event := f.event(domain.ExecutionSubagentObserved, sequence+1)
	event.Subagents = []domain.SubagentObservation{child}
	f.publish(t, event)
	model := "observed_child_model"
	child.Source, child.ObservedModel = domain.ClaudeContentSource, &model
	child.Output = &domain.SubagentOutput{NativeMessageID: "original_child_message", Text: "Original child content", Partial: true}
	event.Sequence++
	event.Subagents = []domain.SubagentObservation{child}
	f.publish(t, event)
	child.Source, child.SourceID, child.Output, child.ObservedModel = domain.ClaudeTaskSource, string(domain.NewID()), nil, nil
	child.Status = domain.SubagentCompleted
	event.Sequence++
	event.Subagents = []domain.SubagentObservation{child}
	f.publish(t, event)
	row, err := f.service.Store.Get(context.Background(), domain.SubagentKind, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := store.Decode[domain.SubagentRecord](row)
	if err != nil || retained.Observation.Source != domain.ClaudeTaskSource || retained.Observation.Output == nil || retained.Observation.ObservedModel == nil || *retained.Observation.ObservedModel != model || len(retained.Sources) != 3 || retained.Sources[0].Source != domain.ClaudeTaskSource || retained.Sources[1].Source != domain.ClaudeContentSource || retained.Sources[2].Source != domain.ClaudeTaskSource {
		t.Fatal("source validation erased or reattributed retained content", err)
	}
}

func TestSubagentTaskMetadataRequiresAcknowledgedTaskSource(t *testing.T) {
	f, children, sequence := claudeSubagentPublicationFixture(t)
	child := children[0]
	agentType, description := "general-purpose", "Original child task"
	depth := uint32(1)
	backgrounded, skipTranscript, ambient := false, true, false
	child.Task = &domain.SubagentTask{AgentType: &agentType, Description: &description, Depth: &depth, Backgrounded: &backgrounded, SkipTranscript: &skipTranscript, Ambient: &ambient}
	event := f.event(domain.ExecutionSubagentObserved, sequence+1)
	event.Subagents = []domain.SubagentObservation{child}
	f.publish(t, event)

	// A content report may omit the task, but must retain the acknowledged task
	// rather than allowing a missing report to erase its history safeguards.
	child.Source, child.SourceID, child.Task = domain.ClaudeContentSource, string(domain.NewID()), nil
	child.Output = &domain.SubagentOutput{NativeMessageID: "original-child-content", Text: "Original child content", Partial: true}
	observedModel := "observed-child-model"
	child.ObservedModel = &observedModel
	event.Sequence++
	event.Subagents = []domain.SubagentObservation{child}
	f.publish(t, event)

	// Content cannot smuggle a changed task into the retained resource.
	forgedDescription := "forged"
	forged := child
	forged.SourceID = string(domain.NewID())
	forged.Task = &domain.SubagentTask{AgentType: &agentType, Description: &forgedDescription, Depth: &depth, Backgrounded: &backgrounded, SkipTranscript: &skipTranscript, Ambient: &ambient}
	event.Sequence++
	event.Subagents = []domain.SubagentObservation{forged}
	if _, err := f.call(f.requestEvent(t, event)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("content source patched retained task metadata", err)
	}

	// Acknowledged task telemetry may patch mutable fields while preserving the
	// immutable agent/depth values and sticky skip-transcript flag.
	patchedDescription, patchedBackgrounded := "Patched task description", true
	child.Source, child.SourceID, child.Task = domain.ClaudeTaskSource, string(domain.NewID()), &domain.SubagentTask{
		AgentType: &agentType, Description: &patchedDescription, Depth: &depth, Backgrounded: &patchedBackgrounded, SkipTranscript: &skipTranscript, Ambient: &ambient,
	}
	child.Output, child.ObservedModel = nil, nil
	event.Subagents = []domain.SubagentObservation{child}
	f.publish(t, event)

	record, err := f.service.Store.Get(context.Background(), domain.SubagentKind, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := store.Decode[domain.SubagentRecord](record)
	if err != nil || retained.Observation.Task == nil || retained.Observation.Task.Description == nil || *retained.Observation.Task.Description != patchedDescription || retained.Observation.Task.Backgrounded == nil || !*retained.Observation.Task.Backgrounded || retained.Observation.Task.SkipTranscript == nil || !*retained.Observation.Task.SkipTranscript {
		t.Fatal("acknowledged task patch was not retained", err)
	}
}
