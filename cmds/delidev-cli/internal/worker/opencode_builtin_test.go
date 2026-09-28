package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

func (f *openCodeTextFixture) builtin(name domain.OpenCodeBuiltinName, state opencode.ToolState) opencode.Observation {
	o := f.read(state)
	o.Part.Tool.Name, o.Part.Tool.CallID = string(name), "call_original_builtin"
	if state != opencode.ToolPending {
		o.Part.Tool.Input = json.RawMessage(`{"nativeField":"original","number":9007199254740993}`)
	}
	if state == opencode.ToolCompleted {
		o.Part.Tool.Metadata = json.RawMessage(`{"diagnostics":{"original":{"extension":true}},"number":1.000}`)
	}
	return o
}

func TestOpenCodeBuiltinsPreserveCompleteObjectsAndOriginalFailure(t *testing.T) {
	for _, name := range []domain.OpenCodeBuiltinName{domain.OpenCodeWrite, domain.OpenCodeEdit, domain.OpenCodeApplyPatch, domain.OpenCodeGlob, domain.OpenCodeGrep} {
		for _, terminal := range []opencode.ToolState{opencode.ToolCompleted, opencode.ToolError} {
			f := newOpenCodeTextFixture(t)
			f.user(t)
			f.publish(t, f.assistant(false))
			for _, state := range []opencode.ToolState{opencode.ToolPending, opencode.ToolRunning, terminal} {
				f.publish(t, f.builtin(name, state))
				f.publish(t, f.builtin(name, state))
			}
			var event domain.ExecutionEvent
			if len(f.rpc.events) != 7 || domain.Decode(f.rpc.events[6], &event) != nil || event.Tool.Snapshot.Builtin.InputJSON != string(f.builtin(name, terminal).Part.Tool.Input) {
				t.Fatal("original builtin input dropped, rounded or duplicated")
			}
			b := event.Tool.Snapshot.Builtin
			if terminal == opencode.ToolCompleted && (b.MetadataJSON == nil || *b.MetadataJSON != string(f.builtin(name, terminal).Part.Tool.Metadata)) || terminal == opencode.ToolError && (b.Error == nil || b.Output != nil) {
				t.Fatal("native outcome or complete metadata changed")
			}
		}
	}
}

func TestOpenCodeBuiltinsBlockChangedIdentityAndUncertainPublication(t *testing.T) {
	for _, name := range []string{"input", "name", "call", "start", "cross-tool-call", "unknown-part-metadata", "lost-ack"} {
		t.Run(name, func(t *testing.T) {
			f := newOpenCodeTextFixture(t)
			f.user(t)
			f.publish(t, f.assistant(false))
			f.publish(t, f.builtin(domain.OpenCodeWrite, opencode.ToolPending))
			f.publish(t, f.builtin(domain.OpenCodeWrite, opencode.ToolRunning))
			o := f.builtin(domain.OpenCodeWrite, opencode.ToolCompleted)
			switch name {
			case "input":
				o.Part.Tool.Input = json.RawMessage(`{"changed":true}`)
			case "name":
				o.Part.Tool.Name = "edit"
			case "call":
				o.Part.Tool.CallID = "changed"
			case "start":
				o.Part.Tool.Timing.Start++
			case "cross-tool-call":
				o = f.read(opencode.ToolPending)
				o.Part.ID, o.Part.Tool.CallID = textPartTwoID, "call_original_builtin"
			case "unknown-part-metadata":
				o.Part.Tool.PartMetadata = json.RawMessage(`{"unknown":true}`)
			case "lost-ack":
				f.rpc.lose = true
			}
			if _, err := f.c.PublishObservation(context.Background(), f.observation(o)); err == nil || !f.c.blocked {
				t.Fatal("changed or uncertain tool remained publishable")
			}
		})
	}
}

func TestOpenCodeWorkspaceEventsKeepOriginalScopeAndRejectUncertainPublication(t *testing.T) {
	f, c := newOpenCodeEventsFixture(t)
	for i := 0; i < 2; i++ {
		o := f.observation(opencode.Observation{Kind: opencode.FileEditedEvent})
		o.WorkspaceEvent = &domain.OpenCodeWorkspaceEvent{Kind: domain.OpenCodeFileEdited, NativeEventID: o.EventID, File: "/original/path"}
		if err := c.PublishObservation(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		var event domain.ExecutionEvent
		if domain.Decode(f.rpc.events[len(f.rpc.events)-1], &event) != nil || event.Progress.Progress.Workspace == nil || *event.Progress.Progress.Workspace != *o.WorkspaceEvent {
			t.Fatal("independent original file notification changed")
		}
	}
	for _, name := range []string{"missing", "mismatched-id", "mismatched-kind", "repeated", "lost-ack"} {
		t.Run(name, func(t *testing.T) {
			f, c := newOpenCodeEventsFixture(t)
			o := f.observation(opencode.Observation{Kind: opencode.FileEditedEvent})
			o.WorkspaceEvent = &domain.OpenCodeWorkspaceEvent{Kind: domain.OpenCodeFileEdited, NativeEventID: o.EventID, File: "/original/path"}
			switch name {
			case "missing":
				o.WorkspaceEvent = nil
			case "mismatched-id":
				o.WorkspaceEvent.NativeEventID = "evt_01960dcbe1ffabcdefghijklmn"
			case "mismatched-kind":
				o.WorkspaceEvent.Kind = domain.OpenCodeFileAdded
			case "repeated":
				if err := c.PublishObservation(context.Background(), o); err != nil {
					t.Fatal(err)
				}
			case "lost-ack":
				f.rpc.lose = true
			}
			if err := c.PublishObservation(context.Background(), o); err == nil || !c.blocked || !f.c.blocked {
				t.Fatal("uncertain workspace event remained publishable")
			}
		})
	}
}
