// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexSafetyMetadataDoesNotPublishOrReplaceUsageAndTerminal(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	before := len(rpc.events)
	input := c.acceptedInputs[0]
	for _, kind := range []codex.ModelSafetyKind{codex.ModelVerificationObserved, codex.ModelBufferingObserved, codex.ModelModerationObserved} {
		event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.ModelSafetyObserved, ThreadID: c.thread, TurnID: c.turn, Correlated: true, ModelSafety: &codex.ModelSafetyObservation{Kind: kind, ThreadID: c.thread, TurnID: c.turn}}
		if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil || len(rpc.events) != before || c.finished || c.blocked || c.acceptedInputs[0] != input {
			t.Fatal("safety metadata gained product authority", err)
		}
	}
	total := int64(10)
	usage := &domain.NativeTokenUsage{Total: domain.NativeTokenCounts{Input: &total, Cached: &total, Output: &total, Reasoning: &total, Total: &total}, Last: domain.NativeTokenCounts{Input: &total, Cached: &total, Output: &total, Reasoning: &total, Total: &total}}
	if handled, err := c.PublishCore(context.Background(), codex.Event{Kind: codex.UsageEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Usage: usage}); !handled || err != nil {
		t.Fatal("original usage rejected", err)
	}
	terminal := codex.Event{Kind: codex.TurnCompletedEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
	if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished || len(rpc.events) != before+2 {
		t.Fatal("original completion lost", err)
	}
}
func TestCodexSafetyMetadataPreservesForeignLateAndRerouteFences(t *testing.T) {
	for _, bad := range []string{"missing", "foreign", "unscoped", "late", "reroute"} {
		c, rpc := codexTerminalStatusFixture(t)
		before := len(rpc.events)
		observation := &codex.ModelSafetyObservation{Kind: codex.ModelModerationObserved, ThreadID: c.thread, TurnID: c.turn}
		event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.ModelSafetyObserved, ThreadID: c.thread, TurnID: c.turn, Correlated: true, ModelSafety: observation}
		switch bad {
		case "missing":
			event.ModelSafety = nil
		case "foreign":
			observation.TurnID = domain.NewID()
		case "unscoped":
			event.TurnID = ""
		case "late":
			event.Late = true
		case "reroute":
			observation.Reroute = &codex.ModelRerouteObservation{Reason: codex.HighRiskCyberActivity}
		}
		if handled, err := c.PublishCore(context.Background(), event); handled || err == nil || len(rpc.events) != before {
			t.Fatal("unsafe observation granted authority", bad)
		}
	}
}
