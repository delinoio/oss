package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestOpenCodeBuiltinPublicationRetainsJSONAndSharesCallOwnership(t *testing.T) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	raw, title, output, metadata := "", "original", "original result", `{"exact":9007199254740993,"diagnostics":{}}`
	end := uint64(200)
	started := domain.ToolSnapshot{Kind: domain.OpenCodeBuiltinTool, Status: domain.ToolPending, Builtin: &domain.OpenCodeBuiltinObservation{Name: domain.OpenCodeWrite, CallID: "original", InputJSON: `{}`, Raw: &raw}}
	update := &domain.ExecutionToolUpdate{ID: domain.NewID(), NativeID: "prt_01960dcbe1fbABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1fbABCDEFGHIJKLMN", Snapshot: &started}
	e := f.event(domain.ExecutionToolStarted, 3)
	e.Tool = update
	f.publish(t, e)
	duplicate := *update
	duplicate.ID, duplicate.NativeID = domain.NewID(), "prt_01960dcbe1fcABCDEFGHIJKLMN"
	duplicate.Snapshot = &domain.ToolSnapshot{Kind: domain.OpenCodeReadTool, Status: domain.ToolPending, Read: &domain.OpenCodeReadObservation{CallID: "original", Raw: &raw}}
	e.Tool, e.Sequence = &duplicate, 4
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("native call acquired a second tool kind")
	}
	running := domain.ToolSnapshot{Kind: domain.OpenCodeBuiltinTool, Status: domain.ToolRunning, Builtin: &domain.OpenCodeBuiltinObservation{Name: domain.OpenCodeWrite, CallID: "original", InputJSON: `{"filePath":"original","number":1.000}`, Timing: &domain.OpenCodeToolTiming{Start: 100}}}
	update.Snapshot, e.Tool, e.Kind = &running, update, domain.ExecutionToolUpdated
	f.publish(t, e)
	completed := *running.Builtin
	completed.Timing, completed.Title, completed.Output, completed.MetadataJSON = &domain.OpenCodeToolTiming{Start: 100, End: &end}, &title, &output, &metadata
	update.Snapshot = &domain.ToolSnapshot{Kind: domain.OpenCodeBuiltinTool, Status: domain.ToolCompleted, Builtin: &completed}
	e.Kind, e.Sequence = domain.ExecutionToolCompleted, 5
	completed.Name = domain.OpenCodeEdit
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("original native tool changed within its part")
	}
	completed.Name = domain.OpenCodeWrite
	request := f.publish(t, e)
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("original builtin completion lost its receipt")
	}
	record, _ := f.service.Store.Get(context.Background(), domain.MessageKind, update.ID)
	message, err := store.Decode[domain.ExecutionMessage](record)
	if err != nil || message.State != domain.MessageComplete || message.Tool.Completed.Builtin.InputJSON != running.Builtin.InputJSON || *message.Tool.Completed.Builtin.MetadataJSON != metadata {
		t.Fatal("stored original JSON was normalized or dropped")
	}
}

func TestOpenCodeWorkspaceNotificationsRetainIndependentEventReceipts(t *testing.T) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	e := f.event(domain.ExecutionProgressObserved, 3)
	e.Progress = &domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.OpenCodeWorkspaceProgressKind, Workspace: &domain.OpenCodeWorkspaceEvent{Kind: domain.OpenCodeFileEdited, NativeEventID: "evt_01960dcbe1fbABCDEFGHIJKLMN", File: "/original/file"}}}
	request := f.publish(t, e)
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("original file notification lost its receipt")
	}
	id, native := e.Progress.ID, e.Progress.Progress.Workspace.NativeEventID
	e.Progress.ID, e.Sequence = domain.NewID(), 4
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("native workspace event was duplicated")
	}
	e.Progress.Progress = domain.NativeProgress{Kind: domain.OpenCodeChangesProgressKind, Changes: &domain.OpenCodeChanges{Source: domain.OpenCodeSessionDiff, NativeEventID: native, Diffs: []domain.OpenCodeFileDiff{}}}
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("native event changed progress kind")
	}
	record, _ := f.service.Store.Get(context.Background(), domain.MessageKind, id)
	message, _ := store.Decode[domain.ExecutionMessage](record)
	if message.NativeID != "" || message.NativeParentID != "" || message.Progress.Workspace.File != "/original/file" {
		t.Fatal("instance notification invented message/tool ownership")
	}
	record, _ = f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	session, _ := store.Decode[domain.Session](record)
	if session.Execution.LatestWorkspaceEventID != id || session.Execution.LastSequence != 3 {
		t.Fatal("rejected notification altered atomic latest reference")
	}
}
