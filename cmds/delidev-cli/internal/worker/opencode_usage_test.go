package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

const usageStartID = "prt_01960dcbe2fbABCDEFGHIJKLMN"
const usageFinishID = "prt_01960dcbe3fbABCDEFGHIJKLMN"

func usageStep(f *openCodeTextFixture, finish bool) opencode.Observation {
	part := &opencode.NativePart{SessionID: f.input.SessionID, MessageID: textAssistantID, ID: usageStartID, Kind: opencode.StepStartPartKind, Step: &opencode.NativeStepPart{}}
	if finish {
		cost := json.Number("0.000001e+0")
		reason := opencode.FinishStop
		part.ID, part.Kind = usageFinishID, opencode.StepFinishPartKind
		part.Step = &opencode.NativeStepPart{Cost: &cost, Reason: &reason, Usage: &opencode.NativeUsage{Input: 12, Output: 7, Reasoning: 3, CacheRead: 8, CacheWrite: 2}}
	}
	return opencode.Observation{Kind: opencode.MessagePartUpdatedEvent, Part: part}
}

func newUsageFixture(t *testing.T) (*openCodeTextFixture, *OpenCodeUsagePublisher) {
	t.Helper()
	f := newOpenCodeTextFixture(t)
	u, err := OpenOpenCodeUsagePublisher(f.c)
	if err != nil {
		t.Fatal(err)
	}
	f.user(t)
	f.publish(t, f.assistant(false))
	return f, u
}

func usagePublish(t *testing.T, f *openCodeTextFixture, u *OpenCodeUsagePublisher, o opencode.Observation) {
	t.Helper()
	o = f.observation(o)
	if _, err := f.c.PublishObservation(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if handled, err := u.PublishObservation(context.Background(), o); err != nil || !handled {
		t.Fatalf("original usage was not retained: %v", err)
	}
}

func TestOpenCodeUsagePreservesOverlappingSourceSemanticsWithoutTotals(t *testing.T) {
	f, u := newUsageFixture(t)
	start, finish := usageStep(f, false), usageStep(f, true)
	usagePublish(t, f, u, start)
	usagePublish(t, f, u, finish)
	usagePublish(t, f, u, finish)
	usagePublish(t, f, u, start) // A repeated start cannot reopen the finished step.
	message := f.assistant(true)
	message.Message.Assistant.Cost = *finish.Part.Step.Cost
	message.Message.Assistant.Usage = *finish.Part.Step.Usage
	usagePublish(t, f, u, message)
	usagePublish(t, f, u, message)
	if len(f.rpc.events) != 6 {
		t.Fatal("overlapping sources were omitted or duplicated")
	}
	for i, source := range []domain.OpenCodeUsageSource{domain.OpenCodeStepUsage, domain.OpenCodeMessageUsage} {
		var event domain.ExecutionEvent
		if domain.Decode(f.rpc.events[4+i], &event) != nil || event.Kind != domain.ExecutionOpenCodeUsageObserved || event.Usage != nil || event.ResponseUsage != nil || event.OpenCodeUsage.Source != source || event.NativeTurnID != f.input.MessageID {
			t.Fatal("native observations entered another usage profile")
		}
		v := event.OpenCodeUsage
		if v.Counts.Input != "12" || v.Counts.Output != "7" || v.Counts.Reasoning != "3" || v.Counts.CacheRead != "8" || v.Counts.CacheWrite != "2" || v.Counts.Total != nil || v.NativeEstimate != "0.000001e+0" {
			t.Fatal("native categories, missing total or exact estimate spelling changed")
		}
	}
	if _, err := OpenOpenCodeUsagePublisher(f.c); err == nil {
		t.Fatal("another usage mapper adopted original observations")
	}
}

func TestOpenCodeUsageRejectsMissingChangedOrUnownedEvidence(t *testing.T) {
	for _, name := range []string{"no-start", "parent", "session", "changed-source", "duplicate-arrival", "mixed-part", "changed-final-counts", "premature-final", "lost-ack", "closed-writer"} {
		t.Run(name, func(t *testing.T) {
			f, u := newUsageFixture(t)
			if name != "no-start" {
				usagePublish(t, f, u, usageStep(f, false))
			}
			bad := usageStep(f, true)
			switch name {
			case "parent":
				bad.Part.MessageID = f.input.MessageID
			case "session":
				bad.Part.SessionID = "ses_01960dcbe1fbabcdefghijklmn"
			case "changed-source":
				usagePublish(t, f, u, bad)
				bad.Part.Step.Usage.Output++
			case "duplicate-arrival":
				bad = f.observation(bad)
				if _, err := u.PublishObservation(context.Background(), bad); err != nil {
					t.Fatal(err)
				}
			case "mixed-part":
				bad.Part.ID = usageStartID
			case "changed-final-counts":
				usagePublish(t, f, u, bad)
				bad = f.assistant(true)
				bad.Message.Assistant.Cost = "0"
				if _, err := f.c.PublishObservation(context.Background(), f.observation(bad)); err != nil {
					t.Fatal(err)
				}
			case "premature-final":
				bad = f.assistant(true)
				bad.Message.Assistant.Cost = "0"
			case "lost-ack":
				f.rpc.lose = true
			case "closed-writer":
				if err := f.c.binding.journal.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if name != "duplicate-arrival" {
				bad = f.observation(bad)
			}
			if _, err := u.PublishObservation(context.Background(), bad); err == nil {
				t.Fatal("invalid usage acquired publication authority")
			}
			if _, err := f.c.PublishObservation(context.Background(), f.observation(f.part(textPartOneID, "later text", true))); err == nil {
				t.Fatal("usage uncertainty did not block its original transcript")
			}
		})
	}
}

func TestOpenCodeUsageNativeErrorClosesAnUnfinishedStepWithoutInventingStepUsage(t *testing.T) {
	f, u := newUsageFixture(t)
	usagePublish(t, f, u, usageStep(f, false))
	message := f.assistant(true)
	message.Message.Assistant.Cost = "0"
	message.Message.Assistant.Error = &opencode.NativeError{Kind: opencode.APIErrorKind}
	usagePublish(t, f, u, message)
	if len(u.steps) != 0 || len(u.starts) != 1 || len(u.values) != 1 || len(f.rpc.events) != 5 || u.values[textAssistantID].Source != domain.OpenCodeMessageUsage {
		t.Fatal("native error fabricated completed-step usage or lost its original step")
	}
}
