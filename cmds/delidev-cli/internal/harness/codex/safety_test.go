// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	case "turn/moderationMetadata":
		p["metadata"] = map[string]any{"private-safety-sentinel": []any{true, nil, "private-safety-sentinel"}}
	case "model/safetyBuffering/updated":
		p["model"], p["useCases"], p["reasons"] = c.execution.settings.Model, []string{"private-safety-sentinel"}, []string{"private-safety-sentinel"}
		p["showBufferingUi"], p["fasterModel"] = true, "private-safety-sentinel"
	case "model/rerouted":
		p["fromModel"], p["toModel"], p["reason"] = c.execution.settings.Model, "private-safety-sentinel", "highRiskCyberActivity"
	}
	return p
}

func TestSafetyPassiveObservationsRetainOriginalExecution(t *testing.T) {
	c, turn := observationClient()
	before := c.execution.settings
	paused := c.execution.paused
	for _, method := range []string{"model/verification", "turn/moderationMetadata", "model/safetyBuffering/updated"} {
		params := safetyFixture(c, turn, method)
		event, err := observeFixture(c, method, params)
		if err != nil || event.Kind != ModelSafetyEvent || !event.Safety.PassiveFor(c.thread, turn) || event.Late || !event.Correlated || c.execution.active != turn || c.execution.paused != paused || c.problem != nil || c.modelObservation != "" || !reflect.DeepEqual(before, c.execution.settings) {
			t.Fatal("safety observation changed original authority", method, err)
		}
		for _, value := range []any{event, event.Safety, c.safety} {
			raw, err := json.Marshal(value)
			if err != nil || strings.Contains(string(raw), "private-safety-sentinel") || strings.Contains(string(raw), "trustedAccessForCyber") {
				t.Fatal("private safety content serialized", err)
			}
		}
	}
	if len(c.safety.observations) != 3 {
		t.Fatal("typed private evidence not retained")
	}
	// Returned evidence and caller buffers cannot mutate retained evidence.
	event, _ := observeFixture(c, "model/safetyBuffering/updated", safetyFixture(c, turn, "model/safetyBuffering/updated"))
	event.Safety.buffering.reasons[0] = "changed"
	*event.Safety.buffering.fasterModel = "changed"
	event.Safety.canonical[0] = 'x'
	retained := c.safety.observations[3]
	if retained.buffering.reasons[0] != "private-safety-sentinel" || *retained.buffering.fasterModel != "private-safety-sentinel" || !json.Valid(retained.canonical) {
		t.Fatal("retained evidence shares mutable buffers")
	}
}

func TestSafetyPrivateProofRequiresMatchingEnvelope(t *testing.T) {
	c, turn := observationClient()
	for _, empty := range []bool{false, true} {
		params := safetyFixture(c, turn, "model/verification")
		if empty {
			params["verifications"] = []string{}
		}
		event, err := observeFixture(c, "model/verification", params)
		if err != nil || !event.Safety.MatchesPassiveEvent(event, c.thread, turn) {
			t.Fatal("valid passive envelope lost", err)
		}
		changed := event
		changed.Kind = NoticeEvent
		if event.Safety.MatchesPassiveEvent(changed, c.thread, turn) {
			t.Fatal("proof adopted another event family")
		}
		changed = event
		changed.Metadata = AuthRecoveryStartedObserved
		if event.Safety.MatchesPassiveEvent(changed, c.thread, turn) {
			t.Fatal("proof adopted another metadata family")
		}
		changed = event
		changed.TurnID = domain.NewID()
		if event.Safety.MatchesPassiveEvent(changed, c.thread, turn) {
			t.Fatal("proof adopted another turn")
		}
	}
}

func TestSafetySameModelRerouteRetainsDecisionWithoutChangingSelection(t *testing.T) {
	c, turn := observationClient()
	paused := c.execution.paused
	params := safetyFixture(c, turn, "model/rerouted")
	params["toModel"] = c.execution.settings.Model
	event, err := observeFixture(c, "model/rerouted", params)
	if err != nil || !event.Safety.MatchesPassiveEvent(event, c.thread, turn) || c.problem != nil || c.execution.paused != paused || len(c.safety.observations) != 1 {
		t.Fatal("same-model descriptor acquired execution authority", err)
	}
}

func TestSafetyModerationPreservesEveryJSONValue(t *testing.T) {
	for _, metadata := range []any{nil, false, "private-safety-sentinel", 1.25, []any{nil, true, "private-safety-sentinel"}, map[string]any{"nested": []any{nil}}} {
		c, turn := observationClient()
		params := safetyFixture(c, turn, "turn/moderationMetadata")
		params["metadata"] = metadata
		event, err := observeFixture(c, "turn/moderationMetadata", params)
		expected, _ := json.Marshal(metadata)
		if err != nil || !bytes.Equal(event.Safety.moderation, expected) || len(c.safety.observations) != 1 {
			t.Fatal("JSONValue narrowed or lost", err)
		}
	}
}

func TestSafetyBufferingTransitionsUsageAndTerminalRemainOriginal(t *testing.T) {
	c, turn := observationClient()
	params := safetyFixture(c, turn, "model/safetyBuffering/updated")
	for _, show := range []bool{true, false, true, false} {
		params["showBufferingUi"] = show
		event, err := observeFixture(c, "model/safetyBuffering/updated", params)
		if err != nil || event.Safety.buffering.showUI != show || c.execution.active != turn || c.problem != nil {
			t.Fatal("buffering took execution authority", err)
		}
	}
	// Existing raw response attribution remains the original source-bound path.
	paramsUsage := map[string]any{"threadId": c.thread, "turnId": turn, "tokenUsage": map[string]any{"total": usageCounts(18), "last": usageCounts(18), "modelContextWindow": nil}}
	event, err := observeFixture(c, "thread/tokenUsage/updated", paramsUsage)
	if err != nil || event.Kind != UsageEvent || event.TurnID != turn {
		t.Fatal("original usage lost", err)
	}
	event, err = observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnCompleted)})
	if err != nil || event.Kind != TurnCompletedEvent || c.execution.turns[turn].Turn.Status != TurnCompleted || c.problem != nil {
		t.Fatal("original completion lost", err)
	}
}

func TestSafetyBufferingModelMustMatchSelectedModel(t *testing.T) {
	const method = "model/safetyBuffering/updated"
	for _, model := range []string{"other-model", "Fixture-Model", "fixture-model ", "fixture-model-faster"} {
		c, turn := observationClient()
		params := safetyFixture(c, turn, method)
		params["model"] = model
		paused := c.execution.paused
		event, err := observeFixture(c, method, params)
		if err == nil || event.Kind == ModelSafetyEvent || event.Safety != nil || len(c.safety.observations) != 0 || c.safety.bytes != 0 || c.execution.paused != paused || c.execution.settings.Model != "fixture-model" || c.modelObservation != "" {
			t.Fatal("mismatched buffering model retained as passive evidence", model, err)
		}
		assertCode(t, err, domain.Unsupported)
	}
}

func TestSafetyBufferingIgnoresFasterModelForSelection(t *testing.T) {
	const method = "model/safetyBuffering/updated"
	for _, faster := range []any{"other-model", nil} {
		c, turn := observationClient()
		params := safetyFixture(c, turn, method)
		params["fasterModel"] = faster
		event, err := observeFixture(c, method, params)
		if err != nil || event.Kind != ModelSafetyEvent || !event.Safety.PassiveFor(c.thread, turn) || len(c.safety.observations) != 1 || c.execution.settings.Model != "fixture-model" || c.modelObservation != "" || c.problem != nil {
			t.Fatal("fasterModel affected passive buffering", faster, err)
		}
	}
}

func TestSafetyForeignBufferingModelStaysPrivate(t *testing.T) {
	c, turn := observationClient()
	params := safetyFixture(c, turn, "model/safetyBuffering/updated")
	params["threadId"], params["model"] = domain.NewID(), "other-model"
	event, err := observeFixture(c, "model/safetyBuffering/updated", params)
	if err != nil || event.Kind != NativeExtensionEvent || len(c.safety.observations) != 0 || c.problem != nil {
		t.Fatal("foreign buffering model changed root evidence", err)
	}
}

func TestSafetyTerminalReplayIsExactPrivateEvidence(t *testing.T) {
	for _, method := range []string{"model/verification", "turn/moderationMetadata", "model/safetyBuffering/updated", "model/rerouted"} {
		c, turn := observationClient()
		params := safetyFixture(c, turn, method)
		_, first := observeFixture(c, method, params)
		if method != "model/rerouted" && first != nil {
			t.Fatal(first)
		}
		_, _ = observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnCompleted)})
		count := len(c.safety.observations)
		event, err := observeFixture(c, method, params)
		if method == "model/rerouted" {
			if err != c.problem || !c.execution.paused {
				t.Fatal("reroute replay cleared recovery")
			}
		} else if err != nil || !event.Late || !event.Safety.PassiveFor(c.thread, turn) {
			t.Fatal("exact late replay lost", err)
		}
		if len(c.safety.observations) != count {
			t.Fatal("replay duplicated retained evidence")
		}
		switch method {
		case "model/verification":
			params["verifications"] = []string{}
		case "turn/moderationMetadata":
			params["metadata"] = "changed"
		case "model/safetyBuffering/updated":
			params["showBufferingUi"] = false
		case "model/rerouted":
			params["toModel"] = "changed"
		}
		if _, err := observeFixture(c, method, params); err == nil {
			t.Fatal("changed late evidence accepted")
		}
	}
}

func TestSafetyMalformedAndForeignBoundaries(t *testing.T) {
	for _, method := range []string{"model/verification", "turn/moderationMetadata", "model/safetyBuffering/updated", "model/rerouted"} {
		for _, change := range []string{"missing", "alias", "null-id", "unknown", "unowned", "wrong-type"} {
			c, turn := observationClient()
			params := safetyFixture(c, turn, method)
			switch change {
			case "missing":
				delete(params, "turnId")
			case "alias":
				params["TurnId"] = params["turnId"]
				delete(params, "turnId")
			case "null-id":
				params["turnId"] = nil
			case "unknown":
				params["unknown"] = true
			case "unowned":
				params["turnId"] = domain.NewID()
			case "wrong-type":
				params["threadId"] = []string{"private-safety-sentinel"}
			}
			if _, err := observeFixture(c, method, params); err == nil || len(c.safety.observations) != 0 || c.problem != nil {
				t.Fatal("malformed/unowned safety acquired authority", method, change)
			}
		}
		c, turn := observationClient()
		params := safetyFixture(c, turn, method)
		params["threadId"] = domain.NewID()
		event, err := observeFixture(c, method, params)
		if err != nil || event.Kind != NativeExtensionEvent || len(c.safety.observations) != 0 || c.problem != nil {
			t.Fatal("foreign safety attributed to root", err)
		}
		raw, _ := json.Marshal(safetyFixture(c, turn, method))
		event, err = c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: method, Params: raw})
		if err != nil || event.Kind != NativeExtensionEvent || len(c.safety.observations) != 0 {
			t.Fatal("server request treated as safety notification", err)
		}
	}
	for _, value := range []any{nil, []any{nil}, []string{"unknown"}, "trustedAccessForCyber"} {
		c, turn := observationClient()
		params := safetyFixture(c, turn, "model/verification")
		params["verifications"] = value
		if _, err := observeFixture(c, "model/verification", params); err == nil {
			t.Fatal("unknown verification accepted")
		}
	}
	for _, change := range []string{"unknown-reason", "foreign-model", "null-reason"} {
		c, turn := observationClient()
		params := safetyFixture(c, turn, "model/rerouted")
		switch change {
		case "unknown-reason":
			params["reason"] = "unknown"
		case "foreign-model":
			params["fromModel"] = "foreign"
		case "null-reason":
			params["reason"] = nil
		}
		if _, err := observeFixture(c, "model/rerouted", params); err == nil || c.problem != nil {
			t.Fatal("unverified reroute accepted")
		}
	}
	for _, value := range []any{nil, []any{nil}, []string{strings.Repeat("x", 4097)}, []string{}} {
		c, turn := observationClient()
		params := safetyFixture(c, turn, "model/safetyBuffering/updated")
		params["reasons"] = value
		_, err := observeFixture(c, "model/safetyBuffering/updated", params)
		if (value != nil && reflect.DeepEqual(value, []string{})) != (err == nil) {
			t.Fatal("buffering string array validation lost", err)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"metadata":null,"metadata":null}`, `{"metadata": [}`} {
		if _, err := decodeSafety("turn/moderationMetadata", json.RawMessage(raw)); err == nil {
			t.Fatal("malformed JSON accepted")
		}
	}
}

func TestSafetyEvidenceHasAggregateBounds(t *testing.T) {
	for _, bound := range []string{"count", "bytes"} {
		c, turn := observationClient()
		if bound == "count" {
			c.safety.observations = make([]*ModelSafetyObservation, maxSafetyObservations)
		} else {
			c.safety.bytes = maxSafetyBytes
		}
		if _, err := observeFixture(c, "turn/moderationMetadata", safetyFixture(c, turn, "turn/moderationMetadata")); err == nil {
			t.Fatal("aggregate bound not enforced")
		}
	}
	c, turn := observationClient()
	params := safetyFixture(c, turn, "turn/moderationMetadata")
	params["metadata"] = strings.Repeat("x", maxSafetyEnvelope)
	if _, err := observeFixture(c, "turn/moderationMetadata", params); err == nil {
		t.Fatal("envelope bound not enforced")
	}
}

func TestSafetyReroutePreservesFirstProblemAndNativeTerminalEvidence(t *testing.T) {
	for _, prior := range []bool{false, true} {
		c, turn := observationClient()
		if prior {
			c.problem = turnUncertain()
			c.execution.paused = true
		}
		first := c.problem
		_, err := observeFixture(c, "model/rerouted", safetyFixture(c, turn, "model/rerouted"))
		assertCode(t, err, domain.RecoveryRequired)
		if first != nil && c.problem != first || !c.execution.paused || !c.safety.rerouted[turn] || len(c.safety.observations) != 1 || c.execution.settings.Model != "fixture-model" || c.modelObservation != "" {
			t.Fatal("reroute replaced original execution")
		}
		problem := c.problem
		event, err := observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnCompleted)})
		if err != problem || event.Kind == TurnCompletedEvent || c.execution.turns[turn].Turn.Status != TurnCompleted || c.execution.active != "" {
			t.Fatal("rerouted completion asserted selected model success", err)
		}
	}
}

func TestSafetyRerouteLatchesWhenEvidenceLedgerIsFull(t *testing.T) {
	for _, bound := range []string{"count", "bytes"} {
		for _, prior := range []bool{false, true} {
			c, turn := observationClient()
			if bound == "count" {
				c.safety.observations = make([]*ModelSafetyObservation, maxSafetyObservations)
			} else {
				c.safety.bytes = maxSafetyBytes
			}
			if prior {
				c.problem = turnUncertain()
				c.execution.paused = true
			}
			first := c.problem
			count, size := len(c.safety.observations), c.safety.bytes
			_, err := observeFixture(c, "model/rerouted", safetyFixture(c, turn, "model/rerouted"))
			assertCode(t, err, domain.RecoveryRequired)
			if err != c.problem || first != nil && c.problem != first || !c.execution.paused || !c.safety.rerouted[turn] || len(c.safety.observations) != count || c.safety.bytes != size || c.execution.settings.Model != "fixture-model" || c.modelObservation != "" {
				t.Fatal("overflowed reroute did not latch recovery", bound, prior)
			}
			problem := c.problem
			event, err := observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnCompleted)})
			if err != problem || event.Kind == TurnCompletedEvent || c.execution.turns[turn].Turn.Status != TurnCompleted || c.execution.active != "" {
				t.Fatal("overflowed reroute completion asserted selected model success", bound, err)
			}
		}
	}
}

func TestSafetyOverflowWithoutModelChangeKeepsGenericRejection(t *testing.T) {
	sameModel := func(c *Client, turn domain.ID) (string, map[string]any) {
		params := safetyFixture(c, turn, "model/rerouted")
		params["toModel"] = c.execution.settings.Model
		return "model/rerouted", params
	}
	moderation := func(c *Client, turn domain.ID) (string, map[string]any) {
		return "turn/moderationMetadata", safetyFixture(c, turn, "turn/moderationMetadata")
	}
	for _, build := range []func(*Client, domain.ID) (string, map[string]any){moderation, sameModel} {
		for _, bound := range []string{"count", "bytes"} {
			c, turn := observationClient()
			if bound == "count" {
				c.safety.observations = make([]*ModelSafetyObservation, maxSafetyObservations)
			} else {
				c.safety.bytes = maxSafetyBytes
			}
			paused := c.execution.paused
			method, params := build(c, turn)
			_, err := observeFixture(c, method, params)
			assertCode(t, err, domain.Unsupported)
			if c.problem != nil || c.execution.paused != paused || c.safety.rerouted[turn] {
				t.Fatal("non-reroute overflow acquired recovery", method, bound)
			}
		}
	}
}

func TestSafetyBelowCapRerouteRecordsEvidenceAndLatch(t *testing.T) {
	c, turn := observationClient()
	_, err := observeFixture(c, "model/rerouted", safetyFixture(c, turn, "model/rerouted"))
	assertCode(t, err, domain.RecoveryRequired)
	if len(c.safety.observations) != 1 || c.safety.bytes != safetyEvidenceBytes(c.safety.observations[0]) || !c.safety.rerouted[turn] || !c.execution.paused {
		t.Fatal("below-cap reroute behavior changed")
	}
}

func TestSafetyReaderNeverResendsOrLeaksPrivateDescriptors(t *testing.T) {
	for _, prior := range []bool{false, true} {
		c, capture, _, _ := boundTurnFixture(t, "ready")
		result, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
		if err != nil {
			t.Fatal(err)
		}
		nextKind(t, c, MessageCompletedEvent)
		var logs bytes.Buffer
		c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
		if prior {
			fixtureSignal(t, c, "notify", map[string]any{"method": "remoteControl/status/changed", "params": remoteControlFixture(remoteControlConnected)})
			_, err = c.NextEvent(context.Background())
			assertCode(t, err, domain.RecoveryRequired)
		}
		problem := c.problem
		for _, method := range []string{"model/verification", "turn/moderationMetadata", "model/safetyBuffering/updated"} {
			fixtureSignal(t, c, "notify", map[string]any{"method": method, "params": safetyFixture(c, result.TurnID, method)})
			nextKind(t, c, ModelSafetyEvent)
			if c.problem != problem {
				t.Fatal("passive event replaced prior cause")
			}
		}
		fixtureSignal(t, c, "notify", map[string]any{"method": "model/rerouted", "params": safetyFixture(c, result.TurnID, "model/rerouted")})
		_, err = c.NextEvent(context.Background())
		assertCode(t, err, domain.RecoveryRequired)
		if problem != nil && c.problem != problem {
			t.Fatal("reader replaced first problem")
		}
		problem = c.problem
		_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
		assertCode(t, err, domain.RecoveryRequired)
		_, err = c.Steer(context.Background(), domain.NewID(), domain.NewID(), result.TurnID, input(domain.PlanMode))
		assertCode(t, err, domain.RecoveryRequired)
		fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
		_, err = c.NextEvent(context.Background())
		assertCode(t, err, domain.RecoveryRequired)
		if c.problem != problem || c.execution.turns[result.TurnID].Turn.Status != TurnCompleted || !c.execution.paused {
			t.Fatal("reader allowed rerouted success", err)
		}
		fixtureSignal(t, c, "notify", map[string]any{"method": "model/verification", "params": map[string]any{"threadId": c.thread, "turnId": result.TurnID, "verifications": []string{"unknown"}}})
		// Drain the original status supplement before the malformed observation.
		nextKind(t, c, ThreadStatusEvent)
		_, err = c.NextEvent(context.Background())
		assertCode(t, err, domain.RecoveryRequired)
		if c.problem != problem {
			t.Fatal("malformed later event replaced first problem")
		}
		if len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "thread/resume")) != 0 {
			t.Fatal("safety observation sent another input")
		}
		if strings.Contains(logs.String(), "private-safety-sentinel") || strings.Contains(logs.String(), "trustedAccessForCyber") || strings.Contains(logs.String(), "highRiskCyberActivity") || !strings.Contains(logs.String(), "model-reroute") {
			t.Fatal("safety logs leaked content or lost closed stage")
		}
		if err := c.Close(); err != nil {
			t.Fatal("original process cleanup failed", err)
		}
	}
}

func TestSafetyOpaqueEvidenceCannotEnterDiagnosticFormatting(t *testing.T) {
	c, turn := observationClient()
	for _, method := range []string{"model/verification", "model/safetyBuffering/updated", "turn/moderationMetadata", "model/rerouted"} {
		o, err := decodeSafety(method, mustSafetyJSON(t, safetyFixture(c, turn, method)))
		if err != nil {
			t.Fatal(err)
		}
		var logs bytes.Buffer
		for _, handler := range []slog.Handler{slog.NewJSONHandler(&logs, nil), slog.NewTextHandler(&logs, nil)} {
			slog.New(handler).Info("private observation", "evidence", o)
		}
		formatted := fmt.Sprintf("%v %+v %#v %+v %#v", o, o, o, *o, Event{Safety: o}) + logs.String()
		if strings.Contains(formatted, "private-safety-sentinel") || strings.Contains(formatted, "trustedAccessForCyber") || strings.Contains(formatted, "highRiskCyberActivity") {
			t.Fatal("opaque evidence entered diagnostic formatting")
		}
	}
}

func mustSafetyJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
