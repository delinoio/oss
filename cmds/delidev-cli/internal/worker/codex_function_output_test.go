// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"testing"
)

func functionOutputPublicationFixture() *codex.FunctionOutput {
	text := "original inert result"
	namespace := "original namespace"
	return &codex.FunctionOutput{ID: "original-function-output", Observation: domain.FunctionOutputObservation{Name: "tool", Namespace: &namespace, Variant: domain.FunctionOutputStructured, Parts: []domain.FunctionOutputPart{{Kind: domain.FunctionOutputText, Text: &text}, {Kind: domain.FunctionOutputImage, Reference: domain.FunctionOutputFileID}, {Kind: domain.FunctionOutputEncrypted}, {Kind: domain.FunctionOutputAudio, Reference: domain.FunctionOutputAudioURL}}}}
}
func TestFunctionOutputWorkerPublishesOrderedInertObservationWithoutCompletingTurn(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	native := functionOutputPublicationFixture()
	event := codex.Event{Kind: codex.FunctionOutputStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: native.ID, Correlated: true, FunctionOutput: native}
	for _, kind := range []codex.EventKind{codex.FunctionOutputStartedEvent, codex.FunctionOutputCompletedEvent} {
		event.Kind = kind
		if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil || c.finished || c.blocked {
			t.Fatal("inert output became terminal evidence", err)
		}
	}
	var started, completed domain.ExecutionEvent
	if len(rpc.events) != 4 || domain.Decode(rpc.events[2], &started) != nil || domain.Decode(rpc.events[3], &completed) != nil {
		t.Fatal("missing typed durable output")
	}
	if started.Artifact.ID != completed.Artifact.ID || completed.Kind != domain.ExecutionArtifactCompleted || completed.Outcome != "" || completed.Artifact.Snapshot.FunctionOutput.Parts[2].Kind != domain.FunctionOutputEncrypted {
		t.Fatal("one durable typed item lost original order/identity")
	}
}
func TestFunctionOutputWorkerRejectsForeignChangedAndRepeatedLifecycle(t *testing.T) {
	for _, fault := range []string{"thread", "turn", "uncorrelated", "late", "name", "namespace", "variant", "duplicate", "delta"} {
		t.Run(fault, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			native := functionOutputPublicationFixture()
			event := codex.Event{Kind: codex.FunctionOutputStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: native.ID, Correlated: true, FunctionOutput: native}
			if _, err := c.PublishCore(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			event.Kind = codex.FunctionOutputCompletedEvent
			switch fault {
			case "thread":
				event.ThreadID = domain.NewID()
			case "turn":
				event.TurnID = domain.NewID()
			case "uncorrelated":
				event.Correlated = false
			case "late":
				event.Late = true
			case "name":
				native.Observation.Name = "changed"
			case "namespace":
				native.Observation.Namespace = nil
			case "variant":
				native.Observation.Variant = domain.FunctionOutputString
				native.Observation.Text = new(string)
				native.Observation.Parts = nil
			case "duplicate":
				if _, err := c.PublishCore(context.Background(), event); err != nil {
					t.Fatal(err)
				}
			case "delta":
				event.Kind = codex.ArtifactDeltaEvent
				event.ArtifactDelta = &codex.ArtifactDelta{Kind: codex.PlanTextDelta, Text: "not a function output delta"}
			}
			before := len(rpc.events)
			if _, err := c.PublishCore(context.Background(), event); err == nil || !c.blocked || len(rpc.events) != before {
				t.Fatal("invalid output observation was published")
			}
		})
	}
}
