package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

func (f *openCodeTextFixture) todo(state opencode.ToolState) opencode.Observation {
	o := f.read(state)
	o.Part.Tool.Name, o.Part.Tool.CallID = "todowrite", "call_original_todo"
	if state != opencode.ToolPending {
		o.Part.Tool.Input = json.RawMessage(`{"todos":[]}`)
	}
	if state == opencode.ToolCompleted {
		o.Part.Tool.Metadata = json.RawMessage(`{"todos":[],"truncated":false}`)
	}
	return o
}

func TestTodoToolsRetainClearingAndRejectChangedAppliedLists(t *testing.T) {
	for _, state := range []opencode.ToolState{opencode.ToolCompleted, opencode.ToolError} {
		f := newOpenCodeTextFixture(t)
		f.user(t)
		f.publish(t, f.assistant(false))
		for _, s := range []opencode.ToolState{opencode.ToolPending, opencode.ToolRunning, state} {
			f.publish(t, f.todo(s))
			f.publish(t, f.todo(s))
		}
		if len(f.rpc.events) != 7 {
			t.Fatal("todo lifecycle was dropped or duplicated")
		}
		var event domain.ExecutionEvent
		if domain.Decode(f.rpc.events[6], &event) != nil || event.Tool.Snapshot.Todo.Input.Todos == nil {
			t.Fatal("explicit clear became an omitted proposal")
		}
	}
	for _, name := range []string{"input", "result", "call", "start", "cross-tool-call", "unknown-metadata", "lost-ack"} {
		t.Run(name, func(t *testing.T) {
			f := newOpenCodeTextFixture(t)
			f.user(t)
			f.publish(t, f.assistant(false))
			f.publish(t, f.todo(opencode.ToolPending))
			f.publish(t, f.todo(opencode.ToolRunning))
			o := f.todo(opencode.ToolCompleted)
			switch name {
			case "input":
				o.Part.Tool.Input = json.RawMessage(`{"todos":[{"content":"changed","status":"pending","priority":"high"}]}`)
			case "result":
				o.Part.Tool.Metadata = json.RawMessage(`{"todos":null,"truncated":false}`)
			case "call":
				o.Part.Tool.CallID = "changed"
			case "start":
				o.Part.Tool.Timing.Start++
			case "cross-tool-call":
				o = f.read(opencode.ToolPending)
				o.Part.ID = textPartTwoID
				o.Part.Tool.CallID = "call_original_todo"
			case "unknown-metadata":
				o.Part.Tool.Metadata = json.RawMessage(`{"todos":[],"truncated":false,"unknown":true}`)
			case "lost-ack":
				f.rpc.lose = true
			}
			if _, err := f.c.PublishObservation(context.Background(), f.observation(o)); err == nil {
				t.Fatal("changed todo evidence was accepted")
			}
			if _, err := f.c.PublishObservation(context.Background(), f.observation(f.todo(opencode.ToolCompleted))); err == nil {
				t.Fatal("uncertain todo publication resumed")
			}
		})
	}
}

func TestTodoProgressPreservesIndependentEventsAndUncertainPublication(t *testing.T) {
	f, c := newOpenCodeEventsFixture(t)
	for i := 0; i < 2; i++ {
		o := f.observation(opencode.Observation{Kind: opencode.TodoUpdatedEvent, Todo: &opencode.NativeTodoUpdate{SessionID: f.input.SessionID, Todos: []domain.OpenCodeTodo{}}})
		if err := c.PublishObservation(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		var event domain.ExecutionEvent
		if domain.Decode(f.rpc.events[len(f.rpc.events)-1], &event) != nil || event.Progress == nil || event.Progress.Progress.Todo.NativeEventID != o.EventID || event.Progress.Progress.Todo.Todos == nil {
			t.Fatal("original independent event identity or empty list lost")
		}
	}
	for _, name := range []string{"foreign", "missing", "repeated", "lost-ack"} {
		t.Run(name, func(t *testing.T) {
			f, c := newOpenCodeEventsFixture(t)
			o := f.observation(opencode.Observation{Kind: opencode.TodoUpdatedEvent, Todo: &opencode.NativeTodoUpdate{SessionID: f.input.SessionID, Todos: []domain.OpenCodeTodo{}}})
			switch name {
			case "foreign":
				o.Todo.SessionID = "foreign"
			case "missing":
				o.Todo.Todos = nil
			case "repeated":
				if err := c.PublishObservation(context.Background(), o); err != nil {
					t.Fatal(err)
				}
			case "lost-ack":
				f.rpc.lose = true
			}
			if name == "foreign" {
				err := c.PublishObservation(context.Background(), o)
				if err != nil {
					t.Fatal("session metadata blocked observation", err)
				}
				return
			}
			if err := c.PublishObservation(context.Background(), o); err == nil {
				t.Fatal("invalid todo event was published")
			}
			if !c.blocked || !f.c.blocked {
				t.Fatal("uncertain original stream remained usable")
			}
		})
	}
}
