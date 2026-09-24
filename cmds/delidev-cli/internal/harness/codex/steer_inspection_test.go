package codex

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func steerHistoryFixture(t *testing.T, mode string) (*Client, string, TurnResult, domain.ID, domain.ID, map[string]any) {
	t.Helper()
	c, capture, _, _ := boundTurnFixture(t, mode)
	first, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	requestID, inputID := domain.NewID(), domain.NewID()
	items := []any{}
	for _, id := range []domain.ID{first.InputID, inputID} {
		items = append(items, map[string]any{"type": "userMessage", "id": string(domain.NewID()), "clientId": id, "content": []any{map[string]any{"type": "text", "text": input(domain.ExecuteMode).Prompt, "text_elements": []any{}}}})
	}
	turn := fixtureTurn(first.TurnID, TurnRunning)
	turn["items"], turn["itemsView"] = items, "full"
	page := map[string]any{"data": []any{turn}, "nextCursor": nil, "backwardsCursor": nil}
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	return c, capture, first, requestID, inputID, page
}

func TestSteerInspectionConfirmsExactHistoryWithoutReplay(t *testing.T) {
	for _, mode := range []string{"wrong-steer", "late-steer", "ready"} {
		t.Run(mode, func(t *testing.T) {
			c, capture, first, request, inputID, _ := steerHistoryFixture(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
			_, err := c.Steer(ctx, request, inputID, first.TurnID, input(domain.ExecuteMode))
			cancel()
			if mode == "ready" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				assertCode(t, err, domain.RecoveryRequired)
			}
			observation, err := c.InspectSteerAcceptance(context.Background(), request)
			if err != nil || observation.Delivery != SteerAccepted || observation.Evidence != SteerHistory || observation.InputID != inputID || observation.RequestID != request || observation.TurnID != first.TurnID || observation.ThreadID != c.thread {
				t.Fatalf("exact native Steer history was not confirmed: %+v, %v", observation, err)
			}
			if mode == "late-steer" {
				late := nextKind(t, c, LateTurnResponseEvent)
				if late.Steer == nil || *late.Steer != observation || !late.Correlated || !late.Late {
					t.Fatal("late acknowledgment lost previously reconciled original ownership")
				}
			}
			observation.InputID = domain.NewID()
			replay, err := c.InspectSteerAcceptance(context.Background(), request)
			if err != nil || replay.InputID != inputID || replay.Delivery != SteerAccepted {
				t.Fatal("caller changed retained acceptance")
			}
			_, err = c.Steer(context.Background(), domain.NewID(), inputID, first.TurnID, input(domain.ExecuteMode))
			assertCode(t, err, domain.Conflict)
			_, err = c.Steer(context.Background(), request, domain.NewID(), first.TurnID, input(domain.ExecuteMode))
			assertCode(t, err, domain.Conflict)
			if len(requestsOf(t, capture, "turn/steer")) != 1 || len(requestsOf(t, capture, "thread/turns/list")) != 1 {
				t.Fatal("inspection replayed the native input or repeated confirmed history")
			}
			if c.problem != nil {
				t.Fatal("the confirmed Steer's own uncertainty remained set")
			}
			_, err = c.Steer(context.Background(), domain.NewID(), domain.NewID(), first.TurnID, input(domain.ExecuteMode))
			// A fresh input reaches native Steer only after the original was
			// confirmed. The wrong-ack fixture deliberately loses this new ACK.
			if mode == "wrong-steer" {
				assertCode(t, err, domain.RecoveryRequired)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSteerInspectionMissingOrChangedHistoryKeepsUncertainty(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any, map[string]any)
	}{
		{"absent", func(page, _ map[string]any) { page["data"] = []any{} }},
		{"unmaterialized", func(_, turn map[string]any) { turn["items"] = turn["items"].([]any)[:1] }},
		{"foreign-turn", func(_, turn map[string]any) { turn["id"] = domain.NewID() }},
		{"summary", func(_, turn map[string]any) { turn["itemsView"] = "summary" }},
		{"reordered", func(_, turn map[string]any) { items := turn["items"].([]any); items[0], items[1] = items[1], items[0] }},
		{"foreign-input", func(_, turn map[string]any) { turn["items"].([]any)[1].(map[string]any)["clientId"] = domain.NewID() }},
		{"duplicate-input", func(_, turn map[string]any) {
			items := turn["items"].([]any)
			items[1].(map[string]any)["clientId"] = items[0].(map[string]any)["clientId"]
		}},
		{"changed-prompt", func(_, turn map[string]any) {
			turn["items"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] = "fixture-protected-altered-input"
		}},
		{"rich-input", func(_, turn map[string]any) {
			turn["items"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)["text_elements"] = []any{map[string]any{"extra": true}}
		}},
		{"unknown-field", func(page, _ map[string]any) { page["extra"] = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, capture, first, request, inputID, page := steerHistoryFixture(t, "wrong-steer")
			_, err := c.Steer(context.Background(), request, inputID, first.TurnID, input(domain.ExecuteMode))
			assertCode(t, err, domain.RecoveryRequired)
			test.change(page, page["data"].([]any)[0].(map[string]any))
			fixtureSignal(t, c, "history", map[string]any{"page": page})
			observation, err := c.InspectSteerAcceptance(context.Background(), request)
			assertCode(t, err, domain.RecoveryRequired)
			if observation.Delivery != SteerUncertain || observation.Evidence != "" || c.problem == nil {
				t.Fatal("incomplete history fabricated acceptance or rejection")
			}
			raw, _ := json.Marshal(observation)
			if strings.Contains(string(raw), "fixture-protected") || strings.Contains(string(raw), "Fixture prompt") {
				t.Fatal("inspection exposed native content")
			}
			_, err = c.Steer(context.Background(), domain.NewID(), inputID, first.TurnID, input(domain.ExecuteMode))
			assertCode(t, err, domain.RecoveryRequired)
			if len(requestsOf(t, capture, "turn/steer")) != 1 {
				t.Fatal("uncertain input was retried")
			}
		})
	}
}

func TestSteerInspectionDefiniteRejectionNeverNeedsHistoryOrResends(t *testing.T) {
	for _, mode := range []string{"steer-reject", "late-steer-reject", "steer-unsupported"} {
		t.Run(mode, func(t *testing.T) {
			c, capture, first, request, inputID, _ := steerHistoryFixture(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
			_, err := c.Steer(ctx, request, inputID, first.TurnID, input(domain.ExecuteMode))
			cancel()
			expected := domain.Conflict
			if mode == "steer-unsupported" {
				expected = domain.Unsupported
			} else if mode == "late-steer-reject" {
				expected = domain.RecoveryRequired
			}
			assertCode(t, err, expected)
			if mode == "late-steer-reject" {
				late := nextKind(t, c, LateTurnResponseEvent)
				if late.Steer == nil || late.Steer.Delivery != SteerNotSent || c.problem == nil {
					t.Fatal("late rejection lost proof or implicitly authorized input")
				}
			}
			result, err := c.InspectSteerAcceptance(context.Background(), request)
			if err != nil || result.Delivery != SteerNotSent || result.Evidence != SteerRejection || c.problem != nil {
				t.Fatalf("definite rejection not retained: %+v, %v", result, err)
			}
			if len(requestsOf(t, capture, "turn/steer")) != 1 || len(requestsOf(t, capture, "thread/turns/list")) != 0 {
				t.Fatal("definite rejection invoked new native operations")
			}
			if _, exists := c.execution.inputs[inputID]; exists || len(c.execution.turns[first.TurnID].Inputs) != 1 {
				t.Fatal("definitely rejected input remained in accepted history")
			}
		})
	}
}

func TestSteerInspectionPreservesOtherRecoveryAndStop(t *testing.T) {
	for _, otherFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "event-failure"}[otherFailure], func(t *testing.T) {
			c, _, first, request, inputID, page := steerHistoryFixture(t, "wrong-steer")
			_, err := c.Steer(context.Background(), request, inputID, first.TurnID, input(domain.ExecuteMode))
			assertCode(t, err, domain.RecoveryRequired)
			if otherFailure {
				fixtureSignal(t, c, "notify", map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": c.thread, "turn": nil}})
				_, err := c.NextEvent(context.Background())
				assertCode(t, err, domain.RecoveryRequired)
			} else {
				if _, err := c.Interrupt(context.Background(), domain.NewID(), first.TurnID); err != nil {
					t.Fatal(err)
				}
				fixtureSignal(t, c, "finish", map[string]any{"status": TurnInterrupted})
				nextKind(t, c, TurnCompletedEvent)
				page["data"].([]any)[0].(map[string]any)["status"] = TurnInterrupted
				fixtureSignal(t, c, "history", map[string]any{"page": page})
			}
			prior := c.problem
			observation, err := c.InspectSteerAcceptance(context.Background(), request)
			if err != nil || observation.Delivery != SteerAccepted || !c.execution.paused || (otherFailure && c.problem != prior) {
				t.Fatal("acceptance cleared unrelated recovery or pause")
			}
			_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			if err == nil {
				t.Fatal("Steer history authorized a new turn after Stop/failure")
			}
		})
	}
}

func TestSteerInspectionSerializesAndRejectsForeignOrCanceledReaders(t *testing.T) {
	c, capture, first, request, inputID, _ := steerHistoryFixture(t, "wrong-steer")
	_, err := c.Steer(context.Background(), request, inputID, first.TurnID, input(domain.ExecuteMode))
	assertCode(t, err, domain.RecoveryRequired)
	_, err = c.InspectSteerAcceptance(context.Background(), domain.NewID())
	assertCode(t, err, domain.NotFound)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.InspectSteerAcceptance(ctx, request)
	if err == nil || c.problem == nil || len(requestsOf(t, capture, "thread/turns/list")) != 0 {
		t.Fatal("canceled inspection changed acceptance")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			result, err := c.InspectSteerAcceptance(context.Background(), request)
			if err != nil || result.Delivery != SteerAccepted {
				t.Errorf("concurrent inspection: %v", err)
			}
		})
	}
	wg.Wait()
	if len(requestsOf(t, capture, "thread/turns/list")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 1 {
		t.Fatal("concurrent inspections repeated native operations")
	}
}

func TestSteerInspectionCanObserveDelayedHistoryWithoutResending(t *testing.T) {
	c, capture, first, request, inputID, page := steerHistoryFixture(t, "wrong-steer")
	_, err := c.Steer(context.Background(), request, inputID, first.TurnID, input(domain.ExecuteMode))
	assertCode(t, err, domain.RecoveryRequired)
	fixtureSignal(t, c, "history", map[string]any{"page": map[string]any{"data": []any{}, "nextCursor": nil, "backwardsCursor": nil}})
	observation, err := c.InspectSteerAcceptance(context.Background(), request)
	assertCode(t, err, domain.RecoveryRequired)
	if observation.Delivery != SteerUncertain {
		t.Fatal("not-yet-materialized input was classified as rejected")
	}
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	observation, err = c.InspectSteerAcceptance(context.Background(), request)
	if err != nil || observation.Delivery != SteerAccepted || observation.Evidence != SteerHistory || c.problem != nil {
		t.Fatal("later exact native history did not reconcile original attempt", err)
	}
	if len(requestsOf(t, capture, "turn/steer")) != 1 || len(requestsOf(t, capture, "thread/turns/list")) != 2 {
		t.Fatal("history inspection replayed input")
	}
}

func TestSteerInspectionChecksOriginalScopeAndPreservesAcknowledgment(t *testing.T) {
	for _, field := range []string{"cwd", "sessionId", "modelProvider", "canAcceptDirectInput"} {
		t.Run(field, func(t *testing.T) {
			c, capture, first, request, inputID, _ := steerHistoryFixture(t, "ready")
			if _, err := c.Steer(context.Background(), request, inputID, first.TurnID, input(domain.ExecuteMode)); err != nil {
				t.Fatal(err)
			}
			var value any = "fixture-foreign-scope"
			if field == "sessionId" {
				value = domain.NewID()
			}
			if field == "canAcceptDirectInput" {
				value = false
			}
			fixtureSignal(t, c, "metadata", map[string]any{field: value})
			observed, err := c.InspectSteerAcceptance(context.Background(), request)
			assertCode(t, err, domain.RecoveryRequired)
			if observed.Delivery != SteerAccepted || observed.Evidence != SteerAcknowledgment || c.problem == nil {
				t.Fatal("scope drift erased known acceptance or failed to block new sends")
			}
			if len(requestsOf(t, capture, "thread/turns/list")) != 0 {
				t.Fatal("scope mismatch authorized history inspection")
			}
			_, err = c.Steer(context.Background(), domain.NewID(), domain.NewID(), first.TurnID, input(domain.ExecuteMode))
			assertCode(t, err, domain.RecoveryRequired)
		})
	}
}

func TestSteerBoundReservesCompleteContinuationEvidence(t *testing.T) {
	c, capture, first, request, inputID, _ := steerHistoryFixture(t, "ready")
	retained := c.execution.turns[first.TurnID]
	retained.Inputs = make([]domain.ID, domain.MaxAcceptedExecutionInputs)
	c.execution.turns[first.TurnID] = retained
	_, err := c.Steer(context.Background(), request, inputID, first.TurnID, input(domain.ExecuteMode))
	assertCode(t, err, domain.ResourceExhausted)
	if len(requestsOf(t, capture, "turn/steer")) != 0 {
		t.Fatal("oversized same-turn input set reached native delivery")
	}
}
