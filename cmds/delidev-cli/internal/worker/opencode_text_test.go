package worker

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

type openCodeTextFixture struct {
	c      *OpenCodeTextPublisher
	rpc    *openCodeBindingRPC
	input  opencode.SessionClaim
	events int
}

func newOpenCodeTextFixture(t *testing.T) *openCodeTextFixture {
	t.Helper()
	b, journal, claims, rpc := newOpenCodeBindingFixture(t)
	ctx := context.Background()
	for _, claim := range claims[:2] {
		if err := journal.Claim(ctx, claim); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.BindSession(ctx, claims[0].RequestID, claims[1].SessionID, openCodeBindingObservation(b)); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptInput(ctx, openCodeBindingReceipt(claims[1])); err != nil {
		t.Fatal(err)
	}
	c, err := OpenOpenCodeTextPublisher(b)
	if err != nil {
		t.Fatal(err)
	}
	return &openCodeTextFixture{c: c, rpc: rpc, input: claims[1]}
}

func (f *openCodeTextFixture) observation(value opencode.Observation) opencode.Observation {
	f.events++
	value.EventID = fmt.Sprintf("evt_%012xabcdefghijklmn", f.events)
	return value
}

func (f *openCodeTextFixture) publish(t *testing.T, value opencode.Observation) {
	t.Helper()
	if handled, err := f.c.PublishObservation(context.Background(), f.observation(value)); err != nil || !handled {
		t.Fatalf("text observation was not retained: %v", err)
	}
}

func (f *openCodeTextFixture) user(t *testing.T) {
	t.Helper()
	s := f.c.binding.requested.Session
	f.publish(t, opencode.Observation{Kind: opencode.MessageUpdatedEvent, Message: &opencode.NativeMessage{ID: f.input.MessageID, SessionID: f.input.SessionID, Role: opencode.UserMessageRole, User: &opencode.NativeUserMessage{Agent: string(s.Agent), Provider: s.Provider, Model: s.Model}}})
	f.publish(t, opencode.Observation{Kind: opencode.MessagePartUpdatedEvent, Part: &opencode.NativePart{ID: f.input.PartID, SessionID: f.input.SessionID, MessageID: f.input.MessageID, Kind: opencode.TextPartKind, Text: &opencode.NativeTextPart{Text: f.c.binding.publisher.input.Input.Prompt}}})
}

const textAssistantID = "msg_01960dcbe1fbABCDEFGHIJKLMN"
const textPartOneID = "prt_01960dcbe1fbABCDEFGHIJKLMN"
const textPartTwoID = "prt_01960dcbe1fcABCDEFGHIJKLMN"

func (f *openCodeTextFixture) assistant(finalized bool) opencode.Observation {
	s := f.c.binding.requested.Session
	completed := int64(1234)
	return opencode.Observation{Kind: opencode.MessageUpdatedEvent, MessageFinalized: finalized, Message: &opencode.NativeMessage{ID: textAssistantID, SessionID: f.input.SessionID, Role: opencode.AssistantMessageRole, Assistant: &opencode.NativeAssistantMessage{ParentID: f.input.MessageID, Model: s.Model, Provider: s.Provider, Agent: string(s.Agent), Mode: string(s.Agent), Completed: &completed}}}
}

func (f *openCodeTextFixture) part(id, text string, ended bool) opencode.Observation {
	value := &opencode.NativeTextPart{Text: text, Timing: &opencode.NativeTiming{Start: 1000}}
	if ended {
		end := uint64(1234)
		value.Timing.End = &end
	}
	return opencode.Observation{Kind: opencode.MessagePartUpdatedEvent, Part: &opencode.NativePart{ID: id, SessionID: f.input.SessionID, MessageID: textAssistantID, Kind: opencode.TextPartKind, Text: value}}
}

func TestOpenCodeTextRetainsOrderedPartsAndValidatedMessageClosure(t *testing.T) {
	f := newOpenCodeTextFixture(t)
	f.user(t)
	f.publish(t, f.assistant(false)) // Early completed metadata is provisional.
	f.publish(t, f.part(textPartOneID, "", false))
	f.publish(t, opencode.Observation{Kind: opencode.MessagePartDeltaEvent, Delta: &opencode.NativeTextDelta{MessageID: textAssistantID, PartID: textPartOneID, Text: "첫째"}})
	f.publish(t, f.part(textPartOneID, "첫째\nsecond", false))
	f.publish(t, f.part(textPartTwoID, " separate part ", true))
	f.publish(t, f.part(textPartOneID, "첫째\nsecond", true))
	if len(f.rpc.events) != 8 || f.c.parts[textPartOneID].complete || f.c.parts[textPartTwoID].complete {
		t.Fatal("native text end or provisional metadata completed the assistant")
	}
	f.publish(t, f.assistant(true))
	if len(f.rpc.events) != 10 {
		t.Fatal("native message closure lost an original text part")
	}
	for index, id := range []string{textPartOneID, textPartTwoID} {
		var event domain.ExecutionEvent
		if domain.Decode(f.rpc.events[8+index], &event) != nil || event.Kind != domain.ExecutionMessageCompleted || event.Message.NativeID != id || event.Message.NativeParentID != textAssistantID || event.Message.Phase != nil || event.NativeTurnID != f.input.MessageID || event.Message.Text != f.c.parts[id].message.Text {
			t.Fatal("text completion flattened original parts or fabricated message phase/turn identity")
		}
	}
	f.publish(t, f.part(textPartOneID, "첫째\nsecond", true))
	f.publish(t, f.assistant(true))
	if len(f.rpc.events) != 10 {
		t.Fatal("repeated native snapshots duplicated transcript content")
	}
	if _, err := OpenOpenCodeTextPublisher(f.c.binding); err == nil {
		t.Fatal("another mapper adopted the original native stream")
	}
}

func TestOpenCodeTextRefusesChangedIdentityContentAndPrematureClosure(t *testing.T) {
	for _, name := range []string{"parent", "session", "regression", "synthetic", "ignored", "oversized", "model", "final-before-text-end", "duplicate-arrival", "delta-after-end", "closed-journal"} {
		t.Run(name, func(t *testing.T) {
			f := newOpenCodeTextFixture(t)
			f.user(t)
			f.publish(t, f.assistant(false))
			initial := f.observation(f.part(textPartOneID, "original", false))
			if _, err := f.c.PublishObservation(context.Background(), initial); err != nil {
				t.Fatal(err)
			}
			bad := f.observation(f.part(textPartOneID, "original", false))
			flag := true
			switch name {
			case "parent":
				bad.Part.MessageID = f.input.MessageID
			case "session":
				bad.Part.SessionID = "ses_01960dcbe1fbabcdefghijklmn"
			case "regression":
				bad.Part.Text.Text = "changed"
			case "synthetic":
				bad.Part.Text.Synthetic = &flag
			case "ignored":
				bad.Part.Text.Ignored = &flag
			case "oversized":
				bad.Part.Text.Text = "original" + strings.Repeat("x", domain.MaxMessageText)
			case "model":
				bad = f.observation(f.assistant(false))
				bad.Message.Assistant.Model = "foreign"
			case "final-before-text-end":
				bad = f.observation(f.assistant(true))
			case "duplicate-arrival":
				bad = initial
			case "delta-after-end":
				f.publish(t, f.part(textPartOneID, "original", true))
				bad = f.observation(opencode.Observation{Kind: opencode.MessagePartDeltaEvent, Delta: &opencode.NativeTextDelta{MessageID: textAssistantID, PartID: textPartOneID, Text: "late"}})
			case "closed-journal":
				if err := f.c.binding.journal.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before := len(f.rpc.events)
			if _, err := f.c.PublishObservation(context.Background(), bad); err == nil {
				t.Fatal("contradictory text observation was published")
			}
			if _, err := f.c.PublishObservation(context.Background(), f.observation(f.part(textPartOneID, "original", true))); err == nil || len(f.rpc.events) != before {
				t.Fatal("later valid text erased original publication uncertainty")
			}
		})
	}
}

func TestOpenCodeTextDoesNotReinterpretOtherNativeFamilies(t *testing.T) {
	f := newOpenCodeTextFixture(t)
	f.user(t)
	f.publish(t, f.assistant(false))
	for _, kind := range []opencode.PartKind{opencode.ReasoningPartKind, opencode.ToolPartKind, opencode.StepFinishPartKind, opencode.CompactionPartKind} {
		observation := f.part(textPartOneID, "private unhandled content", false)
		observation.Part.Kind = kind
		if handled, err := f.c.PublishObservation(context.Background(), f.observation(observation)); err != nil || handled {
			t.Fatal("other native family was normalized as assistant text")
		}
	}
	if len(f.rpc.events) != 4 {
		t.Fatal("unhandled native content entered the text transcript")
	}
}

func TestOpenCodeTextLostAcknowledgmentRetainsFactWithoutMapperReplay(t *testing.T) {
	f := newOpenCodeTextFixture(t)
	f.user(t)
	f.publish(t, f.assistant(false))
	f.rpc.lose = true
	if _, err := f.c.PublishObservation(context.Background(), f.observation(f.part(textPartOneID, "original response", false))); err == nil {
		t.Fatal("expected lost original text acknowledgement")
	}
	last := len(f.rpc.events) - 1
	f.rpc.lose = false
	if err := f.c.binding.publisher.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.rpc.requests[last] != f.rpc.requests[last+1] || string(f.rpc.events[last]) != string(f.rpc.events[last+1]) {
		t.Fatal("outbox replay changed the original text event")
	}
	if _, err := f.c.PublishObservation(context.Background(), f.observation(f.part(textPartOneID, "original response", true))); err == nil || len(f.rpc.events) != last+2 {
		t.Fatal("acknowledged outbox fact reconstructed a blocked native mapper")
	}
}
