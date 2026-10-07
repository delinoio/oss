package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

func openCodeInteractionFixture(t *testing.T, question bool) (*openCodeTextFixture, *OpenCodeEventPublisher, opencode.Observation) {
	f, c := newOpenCodeEventsFixture(t)
	publishOpenCodeFixtureEvent(t, f, c, f.assistant(false))
	tool := f.read(opencode.ToolPending)
	if question {
		tool = f.builtin(domain.OpenCodeQuestionTool, opencode.ToolPending)
	}
	publishOpenCodeFixtureEvent(t, f, c, tool)
	n := &opencode.NativeInteraction{ID: "per_01960dcbe1faABCDEFGHIJKLMN", SessionID: f.input.SessionID, Kind: opencode.PermissionInteraction, Tool: &opencode.InteractionTool{MessageID: textAssistantID, CallID: tool.Part.Tool.CallID}, Permission: &opencode.NativePermission{Name: "read", Patterns: []string{"original/*.env"}, Always: []string{}, Metadata: json.RawMessage(`{"exact":9007199254740993}`)}}
	o := opencode.Observation{Kind: opencode.PermissionAskedEvent, Interaction: n}
	if question {
		multiple := true
		n.ID, n.Kind, n.Permission = "que_01960dcbe1faABCDEFGHIJKLMN", opencode.QuestionInteraction, nil
		n.Questions = []opencode.NativeQuestion{{Text: "Original question", Header: "", Options: []opencode.QuestionOption{}, Multiple: &multiple}}
		o.Kind = opencode.QuestionAskedEvent
	}
	return f, c, f.observation(o)
}

func TestOpenCodeInteractionPublicationKeepsOriginalRequestAndOwnedMemory(t *testing.T) {
	for _, question := range []bool{false, true} {
		f, c, o := openCodeInteractionFixture(t, question)
		if err := c.PublishObservation(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		var event domain.ExecutionEvent
		if domain.Decode(f.rpc.events[len(f.rpc.events)-1], &event) != nil || event.Kind != domain.ExecutionInteractionRequested || event.Interaction.NativeItemID != textPartOneID || event.Interaction.NativeRequestID.Text != o.Interaction.ID || event.Interaction.OpenCode.NativeEventID != o.EventID {
			t.Fatal("native proposal identity was reinterpreted")
		}
		if question {
			*o.Interaction.Questions[0].Multiple = false
			if !*c.interactions[o.Interaction.ID].OpenCode.Questions[0].Multiple {
				t.Fatal("caller rewrote retained original flags")
			}
		} else {
			o.Interaction.Permission.Patterns[0] = "changed"
			if c.interactions[o.Interaction.ID].OpenCode.Permission.Patterns[0] != "original/*.env" {
				t.Fatal("caller rewrote retained original scope")
			}
		}
	}
}

func TestOpenCodeInteractionPublicationRejectsForeignAndUncertainProposals(t *testing.T) {
	for _, name := range []string{"session", "message", "call", "namespace", "missing-tool", "kind", "repeated", "lost-ack"} {
		t.Run(name, func(t *testing.T) {
			f, c, o := openCodeInteractionFixture(t, false)
			switch name {
			case "session":
				o.Interaction.SessionID = "ses_01960dcbe1fbABCDEFGHIJKLMN"
			case "message":
				o.Interaction.Tool.MessageID = f.input.MessageID
			case "call":
				o.Interaction.Tool.CallID = "unknown"
			case "namespace":
				o.Interaction.ID = "que_01960dcbe1faABCDEFGHIJKLMN"
			case "missing-tool":
				o.Interaction.Tool = nil
			case "kind":
				o.Kind = opencode.QuestionAskedEvent
			case "repeated":
				if err := c.PublishObservation(context.Background(), o); err != nil {
					t.Fatal(err)
				}
				o = f.observation(o)
			case "lost-ack":
				f.rpc.lose = true
			}
			if name == "session" {
				err := c.PublishObservation(context.Background(), o)
				if err != nil {
					t.Fatal("session metadata blocked observation", err)
				}
				return
			}
			if err := c.PublishObservation(context.Background(), o); err == nil || !c.blocked || !f.c.blocked {
				t.Fatal("invalid/uncertain native proposal remained publishable")
			}
		})
	}
}
