package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

func (f *openCodeTextFixture) shell(state opencode.ToolState) opencode.Observation {
	o := f.read(state)
	o.Part.Tool.Name, o.Part.Tool.CallID = "bash", "call_original_shell"
	if state != opencode.ToolPending {
		o.Part.Tool.Input = json.RawMessage(`{"command":"original command","timeout":1000}`)
	}
	if state == opencode.ToolRunning {
		o.Part.Tool.Metadata = json.RawMessage(`{"output":"first preview"}`)
	}
	if state == opencode.ToolCompleted {
		o.Part.Tool.Metadata = json.RawMessage(`{"output":"last preview","exit":7,"truncated":true,"outputPath":"/native/output"}`)
	}
	return o
}

func TestOpenCodeShellRetainsSnapshotsAndNullableExit(t *testing.T) {
	for _, result := range []opencode.ToolState{opencode.ToolCompleted, opencode.ToolError} {
		f := newOpenCodeTextFixture(t)
		f.user(t)
		f.publish(t, f.assistant(false))
		f.publish(t, f.shell(opencode.ToolPending))
		f.publish(t, f.shell(opencode.ToolRunning))
		rolling := f.shell(opencode.ToolRunning)
		rolling.Part.Tool.Metadata = json.RawMessage(`{"output":"different tail"}`)
		f.publish(t, rolling)
		f.publish(t, rolling)
		f.publish(t, f.shell(result))
		f.publish(t, f.assistant(true))
		if len(f.rpc.events) != 8 {
			t.Fatal("native rolling snapshots were appended, dropped or duplicated")
		}
		var event domain.ExecutionEvent
		if domain.Decode(f.rpc.events[7], &event) != nil || event.Kind != domain.ExecutionToolCompleted || event.Tool.Snapshot.Kind != domain.OpenCodeShellTool || event.Tool.Snapshot.Shell.Input.Workdir != nil {
			t.Fatal("native Shell lifecycle or omitted directory was lost")
		}
	}
	f := newOpenCodeTextFixture(t)
	for _, exit := range []string{"null", "0", "-1"} {
		o := f.shell(opencode.ToolCompleted)
		o.Part.Tool.Metadata = json.RawMessage(`{"output":"","exit":` + exit + `,"truncated":false}`)
		s, err := openCodeShellSnapshot(o.Part.Tool)
		if err != nil || !s.Shell.Metadata.ExitObserved || (s.Shell.Metadata.Exit == nil) != (exit == "null") {
			t.Fatal("original explicit exit observation was lost")
		}
	}
}

func TestOpenCodeShellRejectsChangedOrUnsupportedEvidence(t *testing.T) {
	for _, name := range []string{"call", "parent", "command", "workdir", "timeout", "start", "kind", "unknown-input", "unknown-metadata", "missing-exit", "fractional-exit", "imprecise-exit", "bad-output-path", "attachment", "provider-executed", "duplicate-cross-tool-call", "early-final", "lost-ack"} {
		t.Run(name, func(t *testing.T) {
			f := newOpenCodeTextFixture(t)
			f.user(t)
			f.publish(t, f.assistant(false))
			f.publish(t, f.shell(opencode.ToolPending))
			f.publish(t, f.shell(opencode.ToolRunning))
			o := f.shell(opencode.ToolCompleted)
			switch name {
			case "call":
				o.Part.Tool.CallID = "changed"
			case "parent":
				o.Part.MessageID = f.input.MessageID
			case "command":
				o.Part.Tool.Input = json.RawMessage(`{"command":"changed","timeout":1000}`)
			case "workdir":
				o.Part.Tool.Input = json.RawMessage(`{"command":"original command","timeout":1000,"workdir":"/changed"}`)
			case "timeout":
				o.Part.Tool.Input = json.RawMessage(`{"command":"original command","timeout":2}`)
			case "start":
				o.Part.Tool.Timing.Start++
			case "kind":
				o = f.read(opencode.ToolCompleted)
			case "unknown-input":
				o.Part.Tool.Input = json.RawMessage(`{"command":"original command","description":"unhandled"}`)
			case "unknown-metadata":
				o.Part.Tool.Metadata = json.RawMessage(`{"unhandled":true}`)
			case "missing-exit":
				o.Part.Tool.Metadata = json.RawMessage(`{"output":"","truncated":false}`)
			case "fractional-exit":
				o.Part.Tool.Metadata = json.RawMessage(`{"output":"","truncated":false,"exit":0.5}`)
			case "imprecise-exit":
				o.Part.Tool.Metadata = json.RawMessage(`{"output":"","truncated":false,"exit":9007199254740992}`)
			case "bad-output-path":
				o.Part.Tool.Metadata = json.RawMessage(`{"output":"","truncated":false,"exit":0,"outputPath":"/unproven"}`)
			case "attachment":
				o.Part.Tool.Attachments = []opencode.NativePart{{Kind: opencode.FilePartKind}}
			case "provider-executed":
				o.Part.Tool.PartMetadata = json.RawMessage(`{"providerExecuted":true}`)
			case "duplicate-cross-tool-call":
				o = f.read(opencode.ToolPending)
				o.Part.ID = textPartTwoID
				o.Part.Tool.CallID = "call_original_shell"
			case "early-final":
				o = f.assistant(true)
			case "lost-ack":
				f.rpc.lose = true
			}
			if _, err := f.c.PublishObservation(context.Background(), f.observation(o)); err == nil {
				t.Fatal("changed or unsupported Shell evidence was accepted")
			}
			if _, err := f.c.PublishObservation(context.Background(), f.observation(f.shell(opencode.ToolCompleted))); err == nil {
				t.Fatal("uncertain Shell publication resumed")
			}
		})
	}
}
