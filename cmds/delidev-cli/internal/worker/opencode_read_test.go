package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

func (f *openCodeTextFixture) read(state opencode.ToolState) opencode.Observation {
	tool := &opencode.NativeToolPart{CallID: "call_original_read", Name: "read", State: state, Input: json.RawMessage(`{"filePath":"/fixture/한글"}`)}
	end := uint64(1200)
	switch state {
	case opencode.ToolPending:
		raw := ""
		tool.Raw = &raw
		tool.Input = json.RawMessage(`{}`)
	case opencode.ToolRunning:
		tool.Timing = &opencode.NativeTiming{Start: 1000}
	case opencode.ToolCompleted:
		title, output := "한글", " original\n<script>text</script>🙂 "
		tool.Title, tool.Output = &title, &output
		tool.Timing = &opencode.NativeTiming{Start: 1000, End: &end}
		tool.Metadata = json.RawMessage(`{"preview":"original","truncated":false,"loaded":[],"display":{"type":"file","path":"/fixture/한글","text":"","lineStart":1,"lineEnd":0,"totalLines":0,"truncated":false}}`)
	case opencode.ToolError:
		message := "Original native read failed."
		tool.Error = &message
		tool.Timing = &opencode.NativeTiming{Start: 1000, End: &end}
	}
	return opencode.Observation{Kind: opencode.MessagePartUpdatedEvent, Part: &opencode.NativePart{ID: textPartOneID, MessageID: textAssistantID, SessionID: f.input.SessionID, Kind: opencode.ToolPartKind, Tool: tool}}
}

func TestOpenCodeReadRetainsProposalAppliedInputAndResult(t *testing.T) {
	for _, result := range []opencode.ToolState{opencode.ToolCompleted, opencode.ToolError} {
		t.Run(string(result), func(t *testing.T) {
			f := newOpenCodeTextFixture(t)
			f.user(t)
			f.publish(t, f.assistant(false))
			for _, state := range []opencode.ToolState{opencode.ToolPending, opencode.ToolRunning, result} {
				f.publish(t, f.read(state))
				f.publish(t, f.read(state))
			}
			if len(f.rpc.events) != 7 {
				t.Fatal("Read snapshots were omitted or duplicated")
			}
			for index, kind := range []domain.ExecutionEventKind{domain.ExecutionToolStarted, domain.ExecutionToolUpdated, domain.ExecutionToolCompleted} {
				var event domain.ExecutionEvent
				if domain.Decode(f.rpc.events[index+4], &event) != nil || event.Kind != kind || event.Tool.NativeID != textPartOneID || event.Tool.NativeParentID != textAssistantID || event.NativeTurnID != f.input.MessageID || event.Tool.Snapshot.Read.CallID != "call_original_read" {
					t.Fatal("original Read lifecycle or identity was replaced")
				}
			}
			f.publish(t, f.assistant(true))
			if len(f.rpc.events) != 7 {
				t.Fatal("Read closure invented assistant text or execution completion")
			}
		})
	}
}

func TestOpenCodeReadFailureMayClosePendingWithoutInventedRunning(t *testing.T) {
	f := newOpenCodeTextFixture(t)
	f.user(t)
	f.publish(t, f.assistant(false))
	f.publish(t, f.read(opencode.ToolPending))
	f.publish(t, f.read(opencode.ToolError))
	if len(f.rpc.events) != 6 || f.c.reads[textPartOneID].latest.Status != domain.ToolFailed {
		t.Fatal("pending failure fabricated a running observation")
	}
}

func TestOpenCodeReadRejectsChangedAndUnsupportedEvidence(t *testing.T) {
	for _, name := range []string{"parent", "call", "input", "start", "late-update", "early-final", "unknown-input", "unknown-metadata", "attachment", "provider-executed", "oversized", "text-conversion", "duplicate-call", "lost-ack"} {
		t.Run(name, func(t *testing.T) {
			f := newOpenCodeTextFixture(t)
			f.user(t)
			f.publish(t, f.assistant(false))
			f.publish(t, f.read(opencode.ToolPending))
			f.publish(t, f.read(opencode.ToolRunning))
			value := f.read(opencode.ToolCompleted)
			switch name {
			case "parent":
				value.Part.MessageID = f.input.MessageID
			case "call":
				value.Part.Tool.CallID = "changed"
			case "input":
				value.Part.Tool.Input = json.RawMessage(`{"filePath":"changed"}`)
			case "start":
				value.Part.Tool.Timing.Start++
			case "late-update":
				f.publish(t, value)
				value = f.read(opencode.ToolRunning)
			case "early-final":
				value = f.assistant(true)
			case "unknown-input":
				value.Part.Tool.Input = json.RawMessage(`{"filePath":"/fixture/한글","extra":true}`)
			case "unknown-metadata":
				value.Part.Tool.Metadata = json.RawMessage(`{"secretExtension":"private"}`)
			case "attachment":
				value.Part.Tool.Attachments = []opencode.NativePart{{Kind: opencode.FilePartKind}}
			case "provider-executed":
				value.Part.Tool.PartMetadata = json.RawMessage(`{"providerExecuted":true}`)
			case "oversized":
				output := strings.Repeat("x", domain.MaxMessageText+1)
				value.Part.Tool.Output = &output
			case "text-conversion":
				value = f.part(textPartOneID, "replacement", true)
			case "duplicate-call":
				value = f.read(opencode.ToolPending)
				value.Part.ID = textPartTwoID
			case "lost-ack":
				f.rpc.lose = true
			}
			before := len(f.rpc.events)
			if _, err := f.c.PublishObservation(context.Background(), f.observation(value)); err == nil {
				t.Fatal("invalid Read publication succeeded")
			}
			if name != "lost-ack" && len(f.rpc.events) != before {
				t.Fatal("invalid Read evidence changed the outbox")
			}
			if _, err := f.c.PublishObservation(context.Background(), f.observation(f.read(opencode.ToolCompleted))); err == nil {
				t.Fatal("failed mapper regained authority")
			}
		})
	}
}

func TestOpenCodeReadOwnsRetainedSnapshotMemory(t *testing.T) {
	f := newOpenCodeTextFixture(t)
	f.user(t)
	f.publish(t, f.assistant(false))
	f.publish(t, f.read(opencode.ToolPending))
	value := f.read(opencode.ToolRunning)
	f.publish(t, value)
	value.Part.Tool.Timing.Start = 999
	if f.c.reads[textPartOneID].latest.Read.Timing.Start != 1000 {
		t.Fatal("caller changed retained original evidence")
	}
	f.publish(t, f.read(opencode.ToolCompleted))
}
