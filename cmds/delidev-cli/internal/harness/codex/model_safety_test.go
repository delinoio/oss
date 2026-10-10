// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
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
		p["model"], p["useCases"], p["reasons"], p["showBufferingUi"], p["fasterModel"] = "private-model-sentinel", []string{"private-use-sentinel"}, []string{"private-reason-sentinel"}, true, "private-faster-sentinel"
	case "turn/moderationMetadata":
		p["metadata"] = map[string]any{"private": "private-moderation-sentinel"}
	case "model/rerouted":
		p["fromModel"], p["toModel"], p["reason"] = "private-from-sentinel", "private-to-sentinel", "highRiskCyberActivity"
	}
	return p
}

func TestModelSafetyObservationsRetainPrivateDataWithoutAuthority(t *testing.T) {
	c, turn := observationClient()
	settings := c.execution.settings
	for _, method := range []string{"model/verification", "model/safetyBuffering/updated", "turn/moderationMetadata"} {
		event, err := observeFixture(c, method, safetyFixture(c, turn, method))
		if err != nil || event.Kind != MetadataEvent || event.Metadata != ModelSafetyObserved || event.TurnID != turn || !event.Correlated || event.Native != nil || event.Late {
			t.Fatal("valid safety metadata rejected", method, err)
		}
		raw, _ := json.Marshal(event)
		if strings.Contains(string(raw), "sentinel") || strings.Contains(string(raw), "trustedAccessForCyber") {
			t.Fatal("native safety content serialized")
		}
	}
	state := c.execution.turns[turn].safety
	if state == nil || len(state.verification) != 1 || state.verification[0] != trustedAccessForCyber || state.buffering.model != "private-model-sentinel" || !state.buffering.showUI || string(state.moderation) != `{"private":"private-moderation-sentinel"}` {
		t.Fatal("private typed evidence missing")
	}
	raw, _ := json.Marshal(state)
	if string(raw) != "{}" {
		t.Fatal("private retained evidence serialized")
	}
	if !reflect.DeepEqual(settings, c.execution.settings) || c.execution.active != turn || c.execution.paused || c.problem != nil {
		t.Fatal("metadata changed selected settings/input authority")
	}
	// Native transitions replace only private telemetry, never native/model settings.
	p := safetyFixture(c, turn, "model/safetyBuffering/updated")
	p["showBufferingUi"], p["fasterModel"], p["useCases"], p["reasons"] = false, nil, []string{}, []string{}
	if _, err := observeFixture(c, "model/safetyBuffering/updated", p); err != nil {
		t.Fatal(err)
	}
	if c.execution.turns[turn].safety.buffering.showUI || string(c.execution.turns[turn].safety.buffering.fasterModel) != "null" {
		t.Fatal("buffering transition lost")
	}
	delete(p, "fasterModel")
	if _, err := observeFixture(c, "model/safetyBuffering/updated", p); err != nil || len(c.execution.turns[turn].safety.buffering.fasterModel) != 0 {
		t.Fatal("omitted fasterModel collapsed", err)
	}
	usage, err := observeFixture(c, "thread/tokenUsage/updated", map[string]any{"threadId": c.thread, "turnId": turn, "tokenUsage": map[string]any{"total": usageCounts(30), "last": usageCounts(20), "modelContextWindow": nil}})
	if err != nil || usage.Kind != UsageEvent || usage.Usage.Total.Total == nil || *usage.Usage.Total.Total != 30 {
		t.Fatal("safety telemetry changed original usage", err)
	}
	completed, err := observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnCompleted)})
	if err != nil || completed.Kind != TurnCompletedEvent || !completed.Correlated || c.execution.active != "" || c.execution.paused {
		t.Fatal("safety telemetry prevented original completion", err)
	}
}

func TestModelSafetyClosedSchemasAndOwnership(t *testing.T) {
	for _, method := range []string{"model/verification", "model/safetyBuffering/updated", "turn/moderationMetadata", "model/rerouted"} {
		required := map[string][]string{
			"model/verification":            {"threadId", "turnId", "verifications"},
			"model/safetyBuffering/updated": {"threadId", "turnId", "model", "useCases", "reasons", "showBufferingUi"},
			"turn/moderationMetadata":       {"threadId", "turnId", "metadata"},
			"model/rerouted":                {"threadId", "turnId", "fromModel", "toModel", "reason"},
		}
		for _, key := range required[method] {
			c, turn := observationClient()
			p := safetyFixture(c, turn, method)
			delete(p, key)
			if _, err := observeFixture(c, method, p); err == nil {
				t.Fatal("missing required field accepted", method, key)
			}
		}
		for _, kind := range []string{"foreign", "unknown-turn", "unknown-field", "wrong-type", "duplicate", "server-request", "terminal"} {
			t.Run(method+"/"+kind, func(t *testing.T) {
				c, turn := observationClient()
				p := safetyFixture(c, turn, method)
				switch kind {
				case "foreign":
					p["threadId"] = domain.NewID()
				case "unknown-turn":
					p["turnId"] = domain.NewID()
				case "unknown-field":
					p["unknown"] = true
				case "wrong-type":
					p["turnId"] = true
				case "terminal":
					prior := c.execution.turns[turn]
					prior.Turn.Status = TurnCompleted
					c.execution.turns[turn] = prior
					c.execution.active = ""
				}
				raw, _ := json.Marshal(p)
				native := nativewire.Event{Kind: nativewire.Notification, Method: method, Params: raw}
				if kind == "duplicate" {
					native.Params = json.RawMessage(strings.Replace(string(raw), `"threadId":`, `"threadId":"duplicate","threadId":`, 1))
				}
				if kind == "server-request" {
					native.Kind = nativewire.ServerRequest
				}
				event, err := c.observeEventLocked(native)
				switch kind {
				case "foreign":
					if err != nil || event.Kind != NativeExtensionEvent {
						t.Fatal("foreign evidence attributed", err)
					}
				case "server-request":
					if err == nil && event.Kind == MetadataEvent {
						t.Fatal("request acquired notification authority")
					}
				case "terminal":
					if err != nil || !event.Late || c.execution.turns[turn].safety != nil || c.problem != nil || c.execution.active != "" {
						t.Fatal("late observation changed original outcome", err)
					}
				default:
					if err == nil {
						t.Fatal("invalid safety envelope accepted")
					}
				}
			})
		}
	}
	c, turn := observationClient()
	for _, mutation := range []struct {
		key   string
		value any
	}{
		{"model", nil}, {"model", 1}, {"useCases", nil}, {"reasons", nil}, {"showBufferingUi", nil}, {"showBufferingUi", "true"}, {"reasons", []any{1}}, {"model", strings.Repeat("x", 1025)},
	} {
		p := safetyFixture(c, turn, "model/safetyBuffering/updated")
		p[mutation.key] = mutation.value
		if _, err := observeFixture(c, "model/safetyBuffering/updated", p); err == nil {
			t.Fatal("malformed buffering accepted", mutation.key)
		}
	}
	for _, body := range []string{
		`{"threadId":"` + string(c.thread) + `","turnId":"` + string(turn) + `","metadata":{"private":1,"private":2}}`,
		`{"threadId":"` + string(c.thread) + `","turnId":"` + string(turn) + `","metadata":` + strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66) + `}`,
		`{"threadId":"` + string(c.thread) + `","turnId":"` + string(turn) + `","metadata":{]}`,
	} {
		if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "turn/moderationMetadata", Params: json.RawMessage(body)}); err == nil {
			t.Fatal("malformed/deep/ambiguous moderation accepted")
		}
	}
	for _, p := range []map[string]any{
		{"threadId": c.thread, "turnId": turn, "verifications": []string{"futureVerification"}},
		{"threadId": c.thread, "turnId": turn, "verifications": nil},
	} {
		if _, err := observeFixture(c, "model/verification", p); err == nil {
			t.Fatal("unknown/null verification accepted")
		}
	}
	for _, value := range []any{nil, []any{}, true, "futureReason"} {
		p := safetyFixture(c, turn, "model/rerouted")
		p["reason"] = value
		if _, err := observeFixture(c, "model/rerouted", p); err == nil {
			t.Fatal("invalid reroute reason accepted")
		}
	}
	for _, value := range []any{true, 1, []any{}, map[string]any{}} {
		p := safetyFixture(c, turn, "model/safetyBuffering/updated")
		p["fasterModel"] = value
		if _, err := observeFixture(c, "model/safetyBuffering/updated", p); err == nil {
			t.Fatal("malformed faster model accepted")
		}
	}
	for _, value := range []any{nil, "private-scalar", 1, true, []any{"private-array"}, map[string]any{}} {
		p := safetyFixture(c, turn, "turn/moderationMetadata")
		p["metadata"] = value
		if _, err := observeFixture(c, "turn/moderationMetadata", p); err != nil {
			t.Fatal("official JSON value narrowed", err)
		}
	}
	p := safetyFixture(c, turn, "turn/moderationMetadata")
	p["metadata"] = strings.Repeat("x", maxModerationBytes)
	if _, err := observeFixture(c, "turn/moderationMetadata", p); err == nil {
		t.Fatal("oversized moderation accepted")
	}
	p = safetyFixture(c, turn, "model/safetyBuffering/updated")
	p["reasons"] = make([]string, maxSafetyDescriptors+1)
	if _, err := observeFixture(c, "model/safetyBuffering/updated", p); err == nil {
		t.Fatal("unbounded descriptors accepted")
	}
}

func TestModelSafetyRerouteRetainsEvidenceAndOriginalRecoveryFence(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "ready")
	defer c.Close()
	result, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, TurnStartedEvent)
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	raw, _ := json.Marshal(safetyFixture(c, result.TurnID, "model/rerouted"))
	c.pendingEvent = &nativewire.Event{Kind: nativewire.Notification, Method: "model/rerouted", Params: raw}
	_, err = c.NextEvent(context.Background())
	assertCode(t, err, domain.RecoveryRequired)
	state := c.execution.turns[result.TurnID]
	if !c.execution.paused || state.Turn.Status != TurnRunning || state.safety == nil || state.safety.reroute == nil || state.safety.reroute.to != "private-to-sentinel" {
		t.Fatal("reroute lost evidence or settled selected model")
	}
	_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
	assertCode(t, err, domain.RecoveryRequired)
	if len(requestsOf(t, capture, "turn/start")) != 1 {
		t.Fatal("reroute triggered fresh input")
	}
	if _, err := observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": fixtureTurn(result.TurnID, TurnCompleted)}); err == nil || c.execution.turns[result.TurnID].Turn.Status != TurnRunning {
		t.Fatal("reroute became selected-model success")
	}
	replay := safetyFixture(c, result.TurnID, "model/rerouted")
	replay["toModel"] = "replacement-sentinel"
	if _, err := observeFixture(c, "model/rerouted", replay); err == nil || c.execution.turns[result.TurnID].safety.reroute.to != "private-to-sentinel" {
		t.Fatal("original reroute evidence was replaced")
	}
	if strings.Contains(logs.String(), "sentinel") || strings.Contains(logs.String(), "highRiskCyberActivity") || !strings.Contains(logs.String(), "model-safety-reroute") {
		t.Fatal("reroute diagnostic leaked private payload or lost safe classification")
	}
}
