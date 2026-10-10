// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
)

func recoveryObservationClient() (*Client, domain.ID) {
	c, turn := observationClient()
	c.execution.settings.Provider = managedOpenAIProviderKey
	c.execution.paused = false
	return c, turn
}
func retryErrorFixture(c *Client, turn domain.ID, retry bool) map[string]any {
	return map[string]any{"threadId": c.thread, "turnId": turn, "willRetry": retry, "error": map[string]any{"message": "private native error", "codexErrorInfo": nil, "additionalDetails": nil, "misalignment": nil}}
}
func TestNativeAuthRecoveryProviderDisplayNameUsesClosedProviderKeyMapping(t *testing.T) {
	if got, ok := nativeProviderDisplayName(managedOpenAIProviderKey); !ok || got != managedOpenAIProviderName {
		t.Fatal("managed provider display name mapping changed", got, ok)
	}
	for _, key := range []string{"OpenAI", "foreign", ""} {
		if _, ok := nativeProviderDisplayName(key); ok {
			t.Fatal("unsupported provider key gained a display-name mapping", key)
		}
	}
}

func TestNativeRetryErrorRetainsOriginalTurnUntilActualCompletion(t *testing.T) {
	for _, retry := range []bool{true, false} {
		c, turn := recoveryObservationClient()
		state := c.execution
		event, err := observeFixture(c, "error", retryErrorFixture(c, turn, retry))
		if err != nil || event.Kind != NoticeEvent || event.Notice != domain.NativeWarning || event.NativeError == nil || event.NativeError.WillRetry != retry || !event.Correlated || event.TurnID != turn || c.execution != state || state.active != turn || state.paused || c.problem != nil {
			t.Fatal("error telemetry altered original execution", event, err)
		}
		raw, _ := json.Marshal(event)
		if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "WillRetry") {
			t.Fatal("private diagnostic escaped")
		}
		observed := state.turns[turn].recoveryTelemetry
		if retry && !observed.RetryingError || !retry && !observed.NonretryingError {
			t.Fatal("retry semantics lost")
		}
		status := "completed"
		var failure any
		if !retry {
			status = "failed"
			failure = map[string]any{"message": "private native failure"}
		}
		done, err := observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": map[string]any{"id": turn, "items": []any{}, "status": status, "error": failure}})
		if err != nil || done.Kind != TurnCompletedEvent || done.Turn == nil || retry && done.Turn.Status != TurnCompleted || !retry && (done.Turn.Status != TurnFailed || done.Turn.Problem == nil) {
			t.Fatal("actual terminal lost", done, err)
		}
		if _, err := observeFixture(c, "error", retryErrorFixture(c, turn, retry)); err == nil {
			t.Fatal("terminal error replay admitted")
		}
	}
}
func TestNativeAuthRecoveryRetainsOriginalProviderAndNoExecutionAuthority(t *testing.T) {
	c, turn := recoveryObservationClient()
	state := c.execution
	settings := state.settings
	for _, method := range []string{"modelProvider/authRecoveryStarted", "modelProvider/authRecoveryCompleted"} {
		event, err := observeFixture(c, method, map[string]any{"threadId": c.thread, "turnId": turn, "provider": managedOpenAIProviderName, "message": "private recovery diagnostic"})
		if err != nil || event.Kind != MetadataEvent || !event.Correlated || event.TurnID != turn || c.execution != state || state.active != turn || state.paused || c.problem != nil || state.settings.Provider != settings.Provider {
			t.Fatal("recovery changed original authority", event, err)
		}
		raw, _ := json.Marshal(event)
		if strings.Contains(string(raw), "private") || strings.Contains(string(raw), settings.Provider) || strings.Contains(string(raw), managedOpenAIProviderName) {
			t.Fatal("private recovery text escaped")
		}
	}
	if state.turns[turn].Turn.Status != TurnRunning || !state.turns[turn].recoveryTelemetry.AuthStarted || !state.turns[turn].recoveryTelemetry.AuthCompleted {
		t.Fatal("recovery became terminal or telemetry lost")
	}
}
func TestNativeErrorClosedUnionsAndPrivateTextBounds(t *testing.T) {
	for _, code := range []any{nil, "unauthorized", "usageLimitExceeded", map[string]any{"httpConnectionFailed": map[string]any{"httpStatusCode": 503}}, map[string]any{"responseStreamDisconnected": map[string]any{"httpStatusCode": nil}}, map[string]any{"activeTurnNotSteerable": map[string]any{"turnKind": "compact"}}} {
		c, turn := recoveryObservationClient()
		params := retryErrorFixture(c, turn, true)
		params["error"].(map[string]any)["codexErrorInfo"] = code
		params["error"].(map[string]any)["misalignment"] = map[string]any{"detailedExplanation": "private", "errorType": "new native category", "reviewTarget": "opaque private target", "steer": map[string]any{"message": "do not send this native instruction"}}
		if _, err := observeFixture(c, "error", params); err != nil {
			t.Fatal("known closed union rejected", code, err)
		}
	}
	for _, mutate := range []func(map[string]any){
		func(p map[string]any) { delete(p, "willRetry") }, func(p map[string]any) { p["willRetry"] = nil }, func(p map[string]any) { p["extra"] = true }, func(p map[string]any) { p["ThreadId"] = p["threadId"]; delete(p, "threadId") },
		func(p map[string]any) { p["error"] = nil }, func(p map[string]any) { delete(p["error"].(map[string]any), "message") }, func(p map[string]any) { p["error"].(map[string]any)["message"] = strings.Repeat("x", (256<<10)+1) },
		func(p map[string]any) { p["error"].(map[string]any)["codexErrorInfo"] = "futureUnknown" }, func(p map[string]any) {
			p["error"].(map[string]any)["codexErrorInfo"] = map[string]any{"httpConnectionFailed": map[string]any{"httpStatusCode": 65536}}
		},
		func(p map[string]any) {
			p["error"].(map[string]any)["codexErrorInfo"] = map[string]any{"httpConnectionFailed": map[string]any{"secret": true}}
		}, func(p map[string]any) {
			p["error"].(map[string]any)["misalignment"] = map[string]any{"steer": map[string]any{"message": nil}}
		},
	} {
		c, turn := recoveryObservationClient()
		p := retryErrorFixture(c, turn, true)
		mutate(p)
		if _, err := observeFixture(c, "error", p); err == nil {
			t.Fatal("malformed private error accepted")
		}
	}
}
func TestNativeErrorAndAuthRecoveryFenceForeignStoppedAndUnknownTurns(t *testing.T) {
	for _, state := range []string{"foreign", "unknown", "paused", "interrupt", "problem", "terminal"} {
		c, turn := recoveryObservationClient()
		switch state {
		case "foreign":
			c.thread = domain.NewID()
		case "unknown":
			turn = domain.NewID()
		case "paused":
			c.execution.paused = true
		case "interrupt":
			c.execution.interrupt = domain.NewID()
		case "problem":
			c.problem = turnUncertain()
		case "terminal":
			p := c.execution.turns[turn]
			p.Turn.Status = TurnCompleted
			c.execution.turns[turn] = p
		}
		p := retryErrorFixture(c, turn, true)
		if state == "foreign" {
			p["threadId"] = domain.NewID()
		}
		e, err := observeFixture(c, "error", p)
		if state == "foreign" {
			if err != nil || e.Kind != NativeExtensionEvent {
				t.Fatal("foreign error gained ownership")
			}
		} else if err == nil {
			t.Fatal("stale error admitted", state)
		}
	}
	for _, change := range []map[string]any{{"provider": managedOpenAIProviderKey}, {"provider": "foreign"}, {"message": nil}, {"extra": true}, {"turnId": domain.NewID()}, {"message": strings.Repeat("x", (256<<10)+1)}} {
		c, turn := recoveryObservationClient()
		p := map[string]any{"threadId": c.thread, "turnId": turn, "provider": managedOpenAIProviderName, "message": "private"}
		for k, v := range change {
			p[k] = v
		}
		if _, err := observeFixture(c, "modelProvider/authRecoveryStarted", p); err == nil {
			t.Fatal("unowned authentication recovery accepted")
		}
	}
}

func TestNativeRetryErrorProcessFixtureSendsOneOriginalInput(t *testing.T) {
	for _, retry := range []bool{true, false} {
		c, capture := openThreadFixture(t, "thread-turn-ready")
		settings := threadSettings(t)
		settings.Provider = managedOpenAIProviderKey
		if _, err := c.StartThread(context.Background(), domain.NewID(), settings); err != nil {
			t.Fatal(err)
		}
		inputID := domain.NewID()
		result, err := c.StartTurn(context.Background(), domain.NewID(), inputID, input(domain.ExecuteMode))
		if err != nil {
			t.Fatal(err)
		}
		nextKind(t, c, TurnStartedEvent)
		for _, method := range []string{"modelProvider/authRecoveryStarted", "modelProvider/authRecoveryCompleted"} {
			fixtureSignal(t, c, "notify", map[string]any{"method": method, "params": map[string]any{"threadId": c.thread, "turnId": result.TurnID, "provider": managedOpenAIProviderName, "message": "private provider recovery"}})
			observed := nextKind(t, c, MetadataEvent)
			if observed.TurnID != result.TurnID || !observed.Correlated {
				t.Fatal("original auth recovery lost")
			}
		}
		fixtureSignal(t, c, "notify", map[string]any{"method": "error", "params": retryErrorFixture(c, result.TurnID, retry)})
		warning := nextKind(t, c, NoticeEvent)
		if warning.NativeError == nil || warning.NativeError.WillRetry != retry {
			t.Fatal("native retry telemetry lost")
		}
		status := TurnCompleted
		if !retry {
			status = TurnFailed
		}
		fixtureSignal(t, c, "finish", map[string]any{"status": status})
		completed := nextKind(t, c, TurnCompletedEvent)
		if completed.Turn.Status != status || len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 {
			t.Fatal("error initiated replacement input")
		}
	}
}
func TestNativeRetryErrorUncertainTerminalCannotResendAcceptedInput(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "ready")
	inputID := domain.NewID()
	result, err := c.StartTurn(context.Background(), domain.NewID(), inputID, input(domain.PlanMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, TurnStartedEvent)
	fixtureSignal(t, c, "notify", map[string]any{"method": "error", "params": retryErrorFixture(c, result.TurnID, true)})
	nextKind(t, c, NoticeEvent)
	fixtureSignal(t, c, "notify", map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": c.thread, "turn": map[string]any{"id": result.TurnID, "items": []any{}, "status": "unknown"}}})
	if _, err := c.NextEvent(context.Background()); err == nil || c.problem == nil || !c.execution.paused {
		t.Fatal("uncertain terminal did not retain recovery")
	}
	if _, err := c.StartTurn(context.Background(), domain.NewID(), inputID, input(domain.PlanMode)); err == nil {
		t.Fatal("uncertain input was resent")
	}
	if len(requestsOf(t, capture, "turn/start")) != 1 {
		t.Fatal("recovery replaced original input")
	}
}
