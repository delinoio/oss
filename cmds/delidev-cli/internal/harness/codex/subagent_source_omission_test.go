// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSubagentCollaborationOmitsPriorTelemetryFromCurrentSource(t *testing.T) {
	for _, tool := range []string{"sendInput", "wait", "closeAgent"} {
		t.Run(tool, func(t *testing.T) {
			c, turn := observationClient()
			child := domain.NewID()
			model, total := "observed-child-model", "18"
			prior := domain.SubagentObservation{ID: domain.NewID(), NativeID: string(child), ParentID: string(c.thread), Status: domain.SubagentCompleted, Source: domain.CodexHistorySource, SourceID: "prior-history", ObservedModel: &model, Output: &domain.SubagentOutput{NativeMessageID: "prior-message", Text: "Prior native output", Partial: true}, Usage: &domain.SubagentUsage{Scope: domain.SubagentCumulativeUsage, Total: &total}}
			c.subagents = map[string]domain.SubagentObservation{string(child): prior}
			status := "completed"
			if tool == "closeAgent" {
				status = "shutdown"
			}
			item := map[string]any{"type": "collabAgentToolCall", "id": "current-collaboration", "tool": tool, "status": "completed", "senderThreadId": c.thread, "receiverThreadIds": []domain.ID{child}, "agentsStates": map[string]any{string(child): map[string]any{"status": status, "message": nil}}}
			event, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": item})
			if err != nil || len(event.Subagents) != 1 {
				t.Fatal("original collaboration observation was lost", err)
			}
			current := event.Subagents[0]
			if current.SourceID != "current-collaboration" || current.Output != nil || current.ObservedModel != nil || current.Usage != nil {
				t.Fatal("current receipt inherited another source's telemetry")
			}
			retained := c.subagents[string(child)]
			if retained.Output != prior.Output || retained.ObservedModel != prior.ObservedModel || retained.Usage != prior.Usage {
				t.Fatal("source omission erased retained available telemetry")
			}
			// A new completed message belongs to this source; it replaces only
			// output while the old observed model remains absent from the event.
			if tool == "wait" {
				item["id"] = "new-output-collaboration"
				item["agentsStates"] = map[string]any{string(child): map[string]any{"status": "completed", "message": "New native output"}}
				raw, _ := json.Marshal(item)
				next, err := c.observeCollaboration(raw, c.thread, turn)
				if err != nil || next.Subagents[0].Output == nil || next.Subagents[0].Output.Text != "New native output" || next.Subagents[0].ObservedModel != nil || next.Subagents[0].Usage != nil {
					t.Fatal("fresh native message lost current-source coverage", err)
				}
			}
		})
	}
}

func TestSubagentNativeMetadataDoesNotRepublishOtherSourceTelemetry(t *testing.T) {
	for _, method := range []string{"thread/started", "thread/settings/updated", "turn/completed", "item/completed", "thread/tokenUsage/updated"} {
		t.Run(method, func(t *testing.T) {
			c, _ := observationClient()
			child := domain.NewID()
			model := "prior-observed-model"
			prior := domain.SubagentObservation{ID: domain.NewID(), NativeID: string(child), ParentID: string(c.thread), Status: domain.SubagentRunning, Source: domain.CodexHistorySource, SourceID: "prior-history", ObservedModel: &model, Output: &domain.SubagentOutput{NativeMessageID: "prior-message", Text: "Prior output", Partial: true}}
			c.subagents = map[string]domain.SubagentObservation{string(child): prior}
			params := map[string]any{"threadId": child}
			switch method {
			case "thread/started":
				params = map[string]any{"thread": threadWire{ID: child, ParentThreadID: &c.thread, SessionID: c.execution.thread.SessionID, CLIVersion: SupportedVersion, ModelProvider: c.execution.settings.Provider, Status: ThreadStatus{Type: ThreadIdle}}}
			case "thread/settings/updated":
				params["threadSettings"] = map[string]any{"model": "fresh-observed-model", "modelProvider": c.execution.settings.Provider}
			case "turn/completed":
				params["turn"] = fixtureTurn(domain.NewID(), TurnCompleted)
			case "item/completed":
				params["turnId"] = domain.NewID()
				params["item"] = map[string]any{"type": "agentMessage", "id": "fresh-message", "text": "Fresh output", "phase": "final_answer"}
			case "thread/tokenUsage/updated":
				params["turnId"] = domain.NewID()
				params["tokenUsage"] = map[string]any{"total": usageCounts(18), "last": usageCounts(18), "modelContextWindow": nil}
			}
			event, err := observeFixture(c, method, params)
			if err != nil || len(event.Subagents) != 1 {
				t.Fatal("original child observation was lost", err)
			}
			current := event.Subagents[0]
			if method != "thread/settings/updated" && current.ObservedModel != nil || method != "item/completed" && current.Output != nil || method != "thread/tokenUsage/updated" && current.Usage != nil {
				t.Fatal("child receipt inherited another source's telemetry")
			}
			if method == "thread/settings/updated" && (current.ObservedModel == nil || *current.ObservedModel != "fresh-observed-model") || method == "item/completed" && (current.Output == nil || current.Output.Text != "Fresh output") || method == "thread/tokenUsage/updated" && current.Usage == nil {
				t.Fatal("child receipt discarded freshly supplied telemetry")
			}
			retained := c.subagents[string(child)]
			if method != "thread/settings/updated" && retained.ObservedModel != prior.ObservedModel || method != "item/completed" && retained.Output != prior.Output {
				t.Fatal("metadata omission erased retained facts")
			}
		})
	}
}
