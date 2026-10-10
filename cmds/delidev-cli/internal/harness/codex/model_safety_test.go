// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func safetyFixture(c *Client, turn domain.ID, method string) map[string]any {
	p := map[string]any{"threadId": c.thread, "turnId": turn}
	switch method {
	case "model/verification":
		p["verifications"] = []string{"trustedAccessForCyber"}
	case "model/safetyBuffering/updated":
		p["model"], p["useCases"], p["reasons"], p["showBufferingUi"], p["fasterModel"] = "fixture-model", []string{"private-use-case-sentinel"}, []string{"private-reason-sentinel"}, true, "private-faster-model-sentinel"
	case "turn/moderationMetadata":
		p["metadata"] = map[string]any{"private-moderation-sentinel": []any{true, nil, 1, "private-content-sentinel"}}
	case "model/rerouted":
		p["fromModel"], p["toModel"], p["reason"] = "fixture-model", "private-safety-model-sentinel", "highRiskCyberActivity"
	}
	return p
}
func TestModelSafetyPassiveEvidencePreservesExecutionAndRedaction(t *testing.T) {
	c, turn := observationClient()
	c.execution.paused = false
	for _, method := range []string{"model/verification", "model/safetyBuffering/updated", "turn/moderationMetadata"} {
		event, err := observeFixture(c, method, safetyFixture(c, turn, method))
		if err != nil || event.Metadata != ModelSafetyObserved || event.ModelSafety == nil || event.TurnID != turn || !event.Correlated || c.execution.paused || c.problem != nil || c.execution.active != turn || c.execution.settings.Model != "fixture-model" {
			t.Fatal("passive evidence changed execution", err)
		}
		for _, value := range []any{event, event.ModelSafety} {
			raw, _ := json.Marshal(value)
			if strings.Contains(string(raw), "sentinel") || strings.Contains(string(raw), "trustedAccessForCyber") {
				t.Fatal("private safety content serialized")
			}
		}
		if event.ModelSafety.Buffering != nil {
			event.ModelSafety.Buffering.Reasons[0] = "mutated"
		}
		if event.ModelSafety.Moderation != nil {
			event.ModelSafety.Moderation[0] = '['
		}
	}
	if len(c.execution.modelSafety) != 3 || c.execution.modelSafety[1].Buffering.Reasons[0] == "mutated" || c.execution.modelSafety[2].Moderation[0] != '{' {
		t.Fatal("returned evidence mutated retained observation")
	}
	for _, show := range []bool{false, true, false} {
		p := safetyFixture(c, turn, "model/safetyBuffering/updated")
		p["showBufferingUi"] = show
		if _, err := observeFixture(c, "model/safetyBuffering/updated", p); err != nil {
			t.Fatal(err)
		}
	}
	usage, err := observeFixture(c, "thread/tokenUsage/updated", map[string]any{"threadId": c.thread, "turnId": turn, "tokenUsage": map[string]any{"total": usageCounts(18), "last": usageCounts(18), "modelContextWindow": nil}})
	if err != nil || usage.Kind != UsageEvent || *usage.Usage.Last.Total != 18 {
		t.Fatal("buffering replaced usage", err)
	}
	terminal, err := observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": map[string]any{"id": turn, "items": []any{}, "status": "completed", "error": nil}})
	if err != nil || terminal.Kind != TurnCompletedEvent || terminal.Turn.Status != TurnCompleted || !terminal.Correlated {
		t.Fatal("buffering lost original completion", err)
	}
}
func TestModelRerouteRetainsTypedEvidenceBeforeSendFence(t *testing.T) {
	c, turn := observationClient()
	c.mode = ThreadProtocol
	if _, err := observeFixture(c, "model/rerouted", safetyFixture(c, turn, "model/rerouted")); err == nil {
		t.Fatal("reroute satisfied selected model")
	}
	if len(c.execution.modelSafety) != 1 || c.execution.modelSafety[0].Reroute.Reason != HighRiskCyberActivity || !c.execution.paused || c.problem == nil || c.execution.settings.Model != "fixture-model" || c.execution.turns[turn].Turn.Status != TurnRunning {
		t.Fatal("reroute evidence or fence lost")
	}
	if c.eligibleTurnLocked(false) == nil {
		t.Fatal("reroute admitted new input")
	}
	if _, err := observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": map[string]any{"id": turn, "items": []any{}, "status": "completed", "error": nil}}); err == nil {
		t.Fatal("rerouted terminal proved selected model success")
	}
	raw, _ := json.Marshal(c.execution.modelSafety[0])
	if strings.Contains(string(raw), "sentinel") {
		t.Fatal("reroute serialized")
	}
}
func TestModelSafetyRejectsMalformedForeignAndUnknownTurn(t *testing.T) {
	for _, method := range []string{"model/verification", "model/safetyBuffering/updated", "turn/moderationMetadata", "model/rerouted"} {
		t.Run(method, func(t *testing.T) {
			for _, bad := range []string{"unknown-field", "missing", "null", "wrong-type", "unknown-turn", "oversize"} {
				c, turn := observationClient()
				p := safetyFixture(c, turn, method)
				key := map[string]string{"model/verification": "verifications", "model/safetyBuffering/updated": "showBufferingUi", "turn/moderationMetadata": "metadata", "model/rerouted": "reason"}[method]
				switch bad {
				case "unknown-field":
					p["extra"] = true
				case "missing":
					delete(p, key)
				case "null":
					if method == "turn/moderationMetadata" {
						p["turnId"] = nil
					} else {
						p[key] = nil
					}
				case "wrong-type":
					p[key] = map[string]any{"unexpected": true}
					if method == "turn/moderationMetadata" {
						p["threadId"] = false
					}
				case "unknown-turn":
					p["turnId"] = domain.NewID()
				case "oversize":
					p[key] = strings.Repeat("x", 65<<10)
				}
				if _, err := observeFixture(c, method, p); err == nil || len(c.execution.modelSafety) != 0 {
					t.Fatal("malformed observation retained", bad)
				}
			}
			c, turn := observationClient()
			p := safetyFixture(c, turn, method)
			p["threadId"] = domain.NewID()
			c.execution.paused = false
			event, err := observeFixture(c, method, p)
			if err != nil || event.Kind != NativeExtensionEvent || len(c.execution.modelSafety) != 0 || c.execution.paused {
				t.Fatal("foreign observation affected root", err)
			}
		})
	}
	c, turn := observationClient()
	for _, p := range []map[string]any{{"threadId": c.thread, "turnId": turn, "verifications": []string{"unknown"}}, {"threadId": c.thread, "turnId": turn, "fromModel": "fixture-model", "toModel": "fixture-other", "reason": "unknown"}} {
		method := "model/verification"
		if p["reason"] != nil {
			method = "model/rerouted"
		}
		if _, err := observeFixture(c, method, p); err == nil {
			t.Fatal("unknown enum accepted")
		}
	}
	raw := json.RawMessage(`{"threadId":"` + string(c.thread) + `","turnId":"` + string(turn) + `","metadata":{"duplicate":1,"duplicate":2}}`)
	if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "turn/moderationMetadata", Params: raw}); err == nil {
		t.Fatal("ambiguous moderation accepted")
	}
}
func TestModelSafetyJsonValuesLateEvidenceAndRetentionLimit(t *testing.T) {
	c, turn := observationClient()
	for _, v := range []any{nil, []any{}, "text", false, 1} {
		p := safetyFixture(c, turn, "turn/moderationMetadata")
		p["metadata"] = v
		if _, err := observeFixture(c, "turn/moderationMetadata", p); err != nil {
			t.Fatal("pinned JsonValue rejected", err)
		}
	}
	tracked := c.execution.turns[turn]
	tracked.Turn.Status = TurnCompleted
	c.execution.turns[turn] = tracked
	event, err := observeFixture(c, "model/verification", safetyFixture(c, turn, "model/verification"))
	if err != nil || !event.Late || !event.ModelSafety.Late {
		t.Fatal("terminal evidence revived turn", err)
	}
	c.execution.modelSafety = make([]ModelSafetyObservation, 256)
	if _, err := observeFixture(c, "model/verification", safetyFixture(c, turn, "model/verification")); err == nil || len(c.execution.modelSafety) != 256 {
		t.Fatal("retention bound evicted original evidence")
	}
}
