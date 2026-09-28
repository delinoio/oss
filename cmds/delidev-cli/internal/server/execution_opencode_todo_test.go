package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func TestOpenCodeTodoPublicationKeepsOriginalLifecycleAndCallOwnership(t *testing.T) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	raw, output, title := "", "original todo output", "original"
	end, truncated := uint64(200), false
	started := domain.ToolSnapshot{Kind: domain.OpenCodeTodoTool, Status: domain.ToolPending, Todo: &domain.OpenCodeTodoObservation{CallID: "call-original", Raw: &raw}}
	running := domain.ToolSnapshot{Kind: domain.OpenCodeTodoTool, Status: domain.ToolRunning, Todo: &domain.OpenCodeTodoObservation{CallID: "call-original", Input: domain.OpenCodeTodoInput{Todos: []domain.OpenCodeTodo{}}, Timing: &domain.OpenCodeToolTiming{Start: 100}}}
	completed := domain.ToolSnapshot{Kind: domain.OpenCodeTodoTool, Status: domain.ToolCompleted, Todo: &domain.OpenCodeTodoObservation{CallID: "call-original", Input: running.Todo.Input, Timing: &domain.OpenCodeToolTiming{Start: 100, End: &end}, Title: &title, Output: &output, Metadata: &domain.OpenCodeTodoMetadata{Todos: []domain.OpenCodeTodo{}, Truncated: &truncated}}}
	update := domain.ExecutionToolUpdate{ID: domain.NewID(), NativeID: "prt_01960dcbe1fbABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1fbABCDEFGHIJKLMN", Snapshot: &started}
	makeEvent := func(kind domain.ExecutionEventKind, sequence uint64, value domain.ExecutionToolUpdate) domain.ExecutionEvent {
		e := f.event(kind, sequence)
		e.Tool = &value
		return e
	}
	request := f.publish(t, makeEvent(domain.ExecutionToolStarted, 3, update))
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("Todo proposal replay duplicated publication")
	}
	duplicate := update
	duplicate.ID = domain.NewID()
	duplicate.NativeID = "prt_01960dcbe1fcABCDEFGHIJKLMN"
	if _, err := f.call(f.requestEvent(t, makeEvent(domain.ExecutionToolStarted, 4, duplicate))); err == nil {
		t.Fatal("one native call acquired a second part")
	}
	duplicate.Snapshot = &domain.ToolSnapshot{Kind: domain.OpenCodeReadTool, Status: domain.ToolPending, Read: &domain.OpenCodeReadObservation{CallID: "call-original", Raw: &raw}}
	if _, err := f.call(f.requestEvent(t, makeEvent(domain.ExecutionToolStarted, 4, duplicate))); err == nil {
		t.Fatal("native call acquired a second part across tool kinds")
	}
	update.Snapshot = &completed
	if _, err := f.call(f.requestEvent(t, makeEvent(domain.ExecutionToolCompleted, 4, update))); err == nil {
		t.Fatal("pending proposal fabricated successful execution")
	}
	update.Snapshot = &running
	request = f.publish(t, makeEvent(domain.ExecutionToolUpdated, 4, update))
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("Todo running replay duplicated publication")
	}
	for _, name := range []string{"parent", "call", "input", "start", "kind"} {
		bad := update
		body, _ := json.Marshal(completed)
		var snapshot domain.ToolSnapshot
		if domain.Decode(body, &snapshot) != nil {
			t.Fatal("invalid fixture")
		}
		bad.Snapshot = &snapshot
		switch name {
		case "parent":
			bad.NativeParentID = "msg_01960dcbe1fcABCDEFGHIJKLMN"
		case "call":
			snapshot.Todo.CallID = "changed"
		case "input":
			snapshot.Todo.Input.Todos = []domain.OpenCodeTodo{{Content: "changed", Status: "pending", Priority: "high"}}
			snapshot.Todo.Metadata.Todos = snapshot.Todo.Input.Todos
		case "start":
			snapshot.Todo.Timing.Start++
		case "kind":
			snapshot.Kind = domain.CommandTool
		}
		if _, err := f.call(f.requestEvent(t, makeEvent(domain.ExecutionToolCompleted, 5, bad))); err == nil {
			t.Fatal("Todo completion substituted original ownership", name)
		}
	}
	update.Snapshot = &completed
	f.publish(t, makeEvent(domain.ExecutionToolCompleted, 5, update))
	update.Snapshot = &running
	if _, err := f.call(f.requestEvent(t, makeEvent(domain.ExecutionToolUpdated, 6, update))); err == nil {
		t.Fatal("completed Todo returned to running")
	}
	r, err := f.service.Store.Get(context.Background(), domain.MessageKind, update.ID)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil || value.State != domain.MessageComplete || value.NativeParentID != update.NativeParentID || value.Tool == nil || len(value.Tool.States) != 1 || value.Tool.States[0].Sequence != 4 || value.Tool.Started.Todo.Raw == nil || value.Tool.Started.Todo.Input.Todos != nil || value.Tool.Completed == nil || *value.Tool.Completed.Todo.Output != output {
		t.Fatal("retained Todo lost original proposal, applied input or result")
	}
}

func TestOpenCodeTodoProgressRetainsOriginalEventAndExactReceipt(t *testing.T) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	event := f.event(domain.ExecutionProgressObserved, 3)
	event.Progress = &domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.OpenCodeTodoProgressKind, Todo: &domain.OpenCodeTodoProgress{NativeEventID: "evt_01960dcbe1faABCDEFGHIJKLMN", Todos: []domain.OpenCodeTodo{}}}}
	request := f.publish(t, event)
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("exact todo receipt was not replayed")
	}
	original := event.Progress.ID
	event.Progress.ID = domain.NewID()
	event.Sequence = 4
	if _, err := f.call(f.requestEvent(t, event)); err == nil {
		t.Fatal("one native event acquired a new progress record")
	}
	event.Progress.Progress.Todo.NativeEventID = "evt_01960dcbe1fbABCDEFGHIJKLMN"
	f.publish(t, event)
	record, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	retained, _ := store.Decode[domain.Session](record)
	if retained.Execution.LatestTodoID != event.Progress.ID || retained.Execution.LatestPlanID != "" || retained.Execution.Outcome != domain.ExecutionRunning {
		t.Fatal("todo progress invented a plan or execution outcome")
	}
	record, _ = f.service.Store.Get(context.Background(), domain.MessageKind, original)
	value, err := store.Decode[domain.ExecutionMessage](record)
	if err != nil || value.NativeID != "" || value.NativeParentID != "" || value.Progress.Todo.Todos == nil {
		t.Fatal("todo event was assigned a fabricated native item or lost its explicit clear")
	}
	// Cross-harness progress cannot reinterpret a Codex plan as native Todo.
	event.Sequence = 5
	event.Progress.ID = domain.NewID()
	event.Progress.Progress = domain.NativeProgress{Kind: domain.PlanProgress, Plan: &domain.NativePlan{Steps: []domain.PlanStep{}}}
	if _, err := f.call(f.requestEvent(t, event)); err == nil {
		t.Fatal("OpenCode accepted a foreign plan format")
	}
	input := f.input
	input.Configuration.Harness = domain.Codex
	event.Progress.Progress = domain.NativeProgress{Kind: domain.OpenCodeTodoProgressKind, Todo: &domain.OpenCodeTodoProgress{NativeEventID: "evt_01960dcbe1fcABCDEFGHIJKLMN", Todos: []domain.OpenCodeTodo{}}}
	if validateNativeMessageOrigin(input, event) == nil {
		t.Fatal("Codex accepted OpenCode todo evidence")
	}
}
