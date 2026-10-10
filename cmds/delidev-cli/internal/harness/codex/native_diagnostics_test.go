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

func diagnosticFixture(c *Client, turn domain.ID, method string) map[string]any {
	p := map[string]any{"threadId": c.thread, "turnId": turn}
	if method == "error" {
		p["willRetry"] = true
		p["error"] = map[string]any{"message": "private-error-sentinel", "codexErrorInfo": nil, "additionalDetails": "private-details-sentinel", "misalignment": nil}
	} else {
		p["provider"], p["message"] = c.execution.settings.Provider, "private-auth-sentinel"
	}
	return p
}

func TestNativeDiagnosticsRemainPrivateOriginalTurnTelemetry(t *testing.T) {
	c, turn := observationClient()
	settings := c.execution.settings
	for _, method := range []string{"error", "modelProvider/authRecoveryStarted", "modelProvider/authRecoveryCompleted"} {
		event, err := observeFixture(c, method, diagnosticFixture(c, turn, method))
		if err != nil || event.Kind != MetadataEvent || event.Metadata != NativeDiagnosticObserved || !event.Correlated || event.Late || event.TurnID != turn || event.Native != nil {
			t.Fatal("owned diagnostic rejected", method, err)
		}
		raw, _ := json.Marshal(event)
		if strings.Contains(string(raw), "sentinel") || strings.Contains(string(raw), "fixture-provider") {
			t.Fatal("private native diagnostic serialized")
		}
	}
	p := diagnosticFixture(c, turn, "error")
	p["willRetry"] = false
	detail := p["error"].(map[string]any)
	detail["codexErrorInfo"] = "usageLimitExceeded"
	detail["misalignment"] = map[string]any{"errorType": "future-native-category", "detailedExplanation": "private-explanation-sentinel", "reviewTarget": "private-target-sentinel", "steer": map[string]any{"message": "private-steer-sentinel"}}
	if _, err := observeFixture(c, "error", p); err != nil {
		t.Fatal(err)
	}
	state := c.execution.turns[turn].diagnostics
	if state == nil || state.retryingError == nil || !state.retryingError.willRetry || state.nonretryingError == nil || state.nonretryingError.willRetry || state.nonretryingError.code != "usageLimitExceeded" || state.nonretryingError.misalignment == nil || *state.nonretryingError.misalignment.steer != "private-steer-sentinel" || state.authStarted == nil || state.authCompleted == nil {
		t.Fatal("typed native observations not retained separately")
	}
	raw, _ := json.Marshal(state)
	if string(raw) != "{}" {
		t.Fatal("private retained diagnostics serialized")
	}
	if !reflect.DeepEqual(settings, c.execution.settings) || c.execution.active != turn || c.execution.paused || c.problem != nil || c.execution.turns[turn].Turn.QuotaBlock != "" {
		t.Fatal("native telemetry changed model/account/input/credit authority")
	}
	// Even a reported completion cannot clear preexisting uncertainty or paused state.
	c.problem, c.execution.paused = turnUncertain(), true
	original := c.problem
	if _, err := observeFixture(c, "modelProvider/authRecoveryCompleted", diagnosticFixture(c, turn, "modelProvider/authRecoveryCompleted")); err != nil || c.problem != original || !c.execution.paused {
		t.Fatal("auth notice cleared original uncertainty", err)
	}
}

func TestNativeDiagnosticClosedErrorVariants(t *testing.T) {
	unit := []nativeErrorCode{"contextWindowExceeded", "sessionBudgetExceeded", "usageLimitExceeded", "rateLimitExceeded", "flexUnavailable", "serverOverloaded", "cyberPolicy", "misalignmentPolicyViolation", "tooManyDenials", "internalServerError", "unauthorized", "badRequest", "threadRollbackFailed", "sandboxError", "other"}
	for _, tag := range unit {
		c, turn := observationClient()
		p := diagnosticFixture(c, turn, "error")
		p["error"].(map[string]any)["codexErrorInfo"] = tag
		if _, err := observeFixture(c, "error", p); err != nil || c.execution.turns[turn].diagnostics.retryingError.code != tag {
			t.Fatal("observed unit variant rejected", tag, err)
		}
	}
	for _, tag := range []string{"httpConnectionFailed", "responseStreamConnectionFailed", "responseStreamDisconnected", "responseTooManyFailedAttempts"} {
		for _, status := range []any{nil, 0, 503, 65535} {
			c, turn := observationClient()
			p := diagnosticFixture(c, turn, "error")
			p["error"].(map[string]any)["codexErrorInfo"] = map[string]any{tag: map[string]any{"httpStatusCode": status}}
			if _, err := observeFixture(c, "error", p); err != nil || c.execution.turns[turn].diagnostics.retryingError.code != nativeErrorCode(tag) {
				t.Fatal("observed HTTP variant rejected", tag, err)
			}
		}
	}
	for _, kind := range []nonSteerableTurnKind{nativeReviewTurn, nativeCompactTurn} {
		c, turn := observationClient()
		p := diagnosticFixture(c, turn, "error")
		p["error"].(map[string]any)["codexErrorInfo"] = map[string]any{"activeTurnNotSteerable": map[string]any{"turnKind": kind}}
		if _, err := observeFixture(c, "error", p); err != nil || c.execution.turns[turn].diagnostics.retryingError.turnKind != kind {
			t.Fatal("closed non-steerable turn rejected", err)
		}
	}
	invalid := []any{"futureError", 1, []any{}, map[string]any{}, map[string]any{"futureVariant": map[string]any{}}, map[string]any{"httpConnectionFailed": nil}, map[string]any{"httpConnectionFailed": map[string]any{"httpStatusCode": -1}}, map[string]any{"httpConnectionFailed": map[string]any{"httpStatusCode": 65536}}, map[string]any{"httpConnectionFailed": map[string]any{"httpStatusCode": 1.5}}, map[string]any{"httpConnectionFailed": map[string]any{"httpStatusCode": "503"}}, map[string]any{"httpConnectionFailed": map[string]any{"extra": true}}, map[string]any{"httpConnectionFailed": map[string]any{}, "responseStreamDisconnected": map[string]any{}}, map[string]any{"activeTurnNotSteerable": map[string]any{}}, map[string]any{"activeTurnNotSteerable": map[string]any{"turnKind": "future"}}}
	for _, code := range invalid {
		c, turn := observationClient()
		p := diagnosticFixture(c, turn, "error")
		p["error"].(map[string]any)["codexErrorInfo"] = code
		if _, err := observeFixture(c, "error", p); err == nil {
			t.Fatal("unknown or malformed error union accepted")
		}
	}
}

func TestNativeDiagnosticRequiredShapesAndOriginalFences(t *testing.T) {
	for _, method := range []string{"error", "modelProvider/authRecoveryStarted", "modelProvider/authRecoveryCompleted"} {
		keys := []string{"threadId", "turnId", "provider", "message"}
		if method == "error" {
			keys = []string{"threadId", "turnId", "willRetry", "error"}
		}
		for _, key := range keys {
			for _, null := range []bool{false, true} {
				c, turn := observationClient()
				p := diagnosticFixture(c, turn, method)
				if null {
					p[key] = nil
				} else {
					delete(p, key)
				}
				if _, err := observeFixture(c, method, p); err == nil {
					t.Fatal("missing/null required field accepted", method, key)
				}
			}
		}
		for _, scenario := range []string{"foreign-thread", "unknown-turn", "unknown-field", "malformed", "duplicate", "late", "request"} {
			t.Run(method+"/"+scenario, func(t *testing.T) {
				c, turn := observationClient()
				p := diagnosticFixture(c, turn, method)
				switch scenario {
				case "foreign-thread":
					p["threadId"] = domain.NewID()
				case "unknown-turn":
					p["turnId"] = domain.NewID()
				case "unknown-field":
					p["extra"] = true
				case "malformed":
					p["turnId"] = true
				case "late":
					prior := c.execution.turns[turn]
					prior.Turn.Status = TurnFailed
					c.execution.turns[turn] = prior
					c.execution.active = ""
					c.execution.paused = true
				}
				raw, _ := json.Marshal(p)
				native := nativewire.Event{Kind: nativewire.Notification, Method: method, Params: raw}
				if scenario == "duplicate" {
					native.Params = json.RawMessage(strings.Replace(string(raw), `"turnId":`, `"turnId":"duplicate","turnId":`, 1))
				}
				if scenario == "request" {
					native.Kind = nativewire.ServerRequest
				}
				event, err := c.observeEventLocked(native)
				switch scenario {
				case "foreign-thread":
					if err != nil || event.Kind != NativeExtensionEvent || c.execution.turns[turn].diagnostics != nil {
						t.Fatal("foreign thread attributed", err)
					}
				case "late":
					if err != nil || !event.Late || c.execution.turns[turn].diagnostics != nil || c.execution.active != "" || !c.execution.paused {
						t.Fatal("late diagnostic changed original outcome", err)
					}
				case "request":
					if err == nil && event.Kind == MetadataEvent {
						t.Fatal("request acquired notification authority")
					}
				default:
					if err == nil {
						t.Fatal("invalid diagnostic accepted", scenario)
					}
				}
			})
		}
	}
	c, turn := observationClient()
	for _, method := range []string{"modelProvider/authRecoveryStarted", "modelProvider/authRecoveryCompleted"} {
		p := diagnosticFixture(c, turn, method)
		p["provider"] = "foreign-provider"
		if _, err := observeFixture(c, method, p); err == nil {
			t.Fatal("foreign provider gained recovery attribution")
		}
		p = diagnosticFixture(c, turn, method)
		p["message"] = strings.Repeat("x", nativewire.MaxFrame+1)
		if _, err := observeFixture(c, method, p); err == nil {
			t.Fatal("unbounded native text accepted")
		}
	}
	for _, bad := range []map[string]any{{"message": nil}, {"message": 1}, {"message": "x", "additionalDetails": true}, {"message": "x", "extra": true}, {"message": "x", "misalignment": map[string]any{"steer": map[string]any{}}}, {"message": "x", "misalignment": map[string]any{"steer": map[string]any{"message": true}}}, {"message": "x", "misalignment": map[string]any{"reviewTarget": 1}}, {"message": strings.Repeat("x", nativewire.MaxFrame+1)}} {
		p := diagnosticFixture(c, turn, "error")
		p["error"] = bad
		if _, err := observeFixture(c, "error", p); err == nil {
			t.Fatal("malformed private TurnError accepted")
		}
	}
}

func TestNativeDiagnosticOriginalProcessOutcomeAndNoResend(t *testing.T) {
	for _, outcome := range []TurnStatus{TurnCompleted, TurnFailed} {
		t.Run(string(outcome), func(t *testing.T) {
			c, capture, _, _ := boundTurnFixture(t, "ready")
			defer c.Close()
			result, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
			if err != nil {
				t.Fatal(err)
			}
			nextKind(t, c, TurnStartedEvent)
			var logs bytes.Buffer
			c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			for _, method := range []string{"error", "modelProvider/authRecoveryStarted", "modelProvider/authRecoveryCompleted"} {
				p := diagnosticFixture(c, result.TurnID, method)
				if method == "error" {
					p["willRetry"] = outcome == TurnCompleted
				}
				raw, _ := json.Marshal(p)
				c.pendingEvent = &nativewire.Event{Kind: nativewire.Notification, Method: method, Params: raw}
				event, err := c.NextEvent(context.Background())
				if err != nil || event.Kind != MetadataEvent || event.Metadata != NativeDiagnosticObserved {
					t.Fatal("native telemetry interrupted owned process", err)
				}
			}
			if len(requestsOf(t, capture, "turn/start")) != 1 || c.execution.active != result.TurnID {
				t.Fatal("native telemetry retried or released input")
			}
			fixtureSignal(t, c, "finish", map[string]any{"status": outcome})
			event := nextKind(t, c, TurnCompletedEvent)
			if event.Turn.Status != outcome || !event.Correlated || event.Late || c.execution.active != "" || (outcome == TurnFailed && (event.Turn.Problem == nil || !c.execution.paused)) || len(requestsOf(t, capture, "turn/start")) != 1 {
				t.Fatal("native outcome was fabricated or input resent")
			}
			if strings.Contains(logs.String(), "sentinel") {
				t.Fatal("native private text logged")
			}
		})
	}
}

func TestNativeDiagnosticLostTerminalNeverPermitsAnotherInput(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "ready")
	defer c.Close()
	result, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, TurnStartedEvent)
	raw, _ := json.Marshal(diagnosticFixture(c, result.TurnID, "error"))
	c.pendingEvent = &nativewire.Event{Kind: nativewire.Notification, Method: "error", Params: raw}
	if _, err := c.NextEvent(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
	assertCode(t, err, domain.Conflict)
	// Simulate the existing transport-loss recovery boundary, not a native retry.
	c.problem, c.execution.paused = turnUncertain(), true
	auth := diagnosticFixture(c, result.TurnID, "modelProvider/authRecoveryCompleted")
	if _, err := observeFixture(c, "modelProvider/authRecoveryCompleted", auth); err != nil {
		t.Fatal(err)
	}
	_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
	assertCode(t, err, domain.RecoveryRequired)
	if len(requestsOf(t, capture, "turn/start")) != 1 || c.execution.turns[result.TurnID].Turn.Status != TurnRunning || c.execution.active != result.TurnID || !c.execution.paused {
		t.Fatal("lost native terminal triggered retry or inferred outcome")
	}
}
