package codex

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func responseHistoryFixture(t *testing.T, accepted bool) (*Client, domain.ID, domain.ID, map[string]any) {
	t.Helper()
	c, turn, request := ownedQuestionFixture(t, questionFixture())
	response := domain.NewID()
	if _, err := c.AnswerQuestions(context.Background(), response, request.Interaction.ID, turn, QuestionAnswers{Answers: map[string][]string{"choice": {"First"}}}); err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, InteractionClosedEvent)
	// Deterministic transport uncertainty retains the same immutable attempt.
	// Pipe blocking/lost-write behavior is independently exercised by the native
	// interaction transport fixtures; this test controls subsequent history.
	c.execution.interactions.arrivals[request.Interaction.ID].status.Delivery = QuestionDeliveryUncertain
	c.problem, c.execution.paused = interactionUncertain(), true
	if accepted {
		event, err := c.observeEventLocked(questionOutputFixture(t, c, turn, request.ItemID, []string{"First"}))
		if err != nil || event.Kind != QuestionAcceptedEvent {
			t.Fatal("fixture lost exact live answer evidence", err)
		}
	}
	history := responseHistoryPage(c, turn)
	fixtureSignal(t, c, "history", map[string]any{"page": history})
	return c, response, request.Interaction.ID, history
}

func responseHistoryPage(c *Client, turnID domain.ID) map[string]any {
	turn := fixtureTurn(turnID, c.execution.turns[turnID].Turn.Status)
	var items []any
	for _, id := range c.execution.turns[turnID].Inputs {
		items = append(items, map[string]any{"type": "userMessage", "id": string(domain.NewID()), "clientId": id, "content": []any{map[string]any{"type": "text", "text": input(domain.PlanMode).Prompt, "text_elements": []any{}}}})
	}
	turn["items"], turn["itemsView"] = items, "full"
	return map[string]any{"data": []any{turn}, "nextCursor": nil, "backwardsCursor": nil}
}

func TestInteractionResponseInspectionPreservesExactLiveEvidenceAndPause(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-output", true: "live-output"}[accepted], func(t *testing.T) {
			c, response, arrival, _ := responseHistoryFixture(t, accepted)
			prior := c.problem
			bounded, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			status, err := c.InspectInteractionResponse(bounded, response, arrival)
			if accepted && err != nil {
				t.Fatal(err)
			}
			if !accepted {
				assertCode(t, err, domain.RecoveryRequired)
			}
			if status.ID != arrival || status.ResponseID != response || status.Accepted != accepted || status.Delivery != QuestionDeliveryUncertain || c.problem != prior || !c.execution.paused {
				t.Fatal("inspection changed response, transport or independent recovery")
			}
			raw := string(mustJSON(t, status))
			if strings.Contains(raw, "First") || strings.Contains(raw, "Fixture prompt") {
				t.Fatal("inspection exposed response/input content")
			}
			// Inspection cannot answer again, acknowledge another input or consume
			// queued events. The ordinary terminal publication remains available.
			fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
			nextKind(t, c, TurnCompletedEvent)
			_, err = c.AnswerQuestions(context.Background(), domain.NewID(), arrival, status.TurnID, QuestionAnswers{Answers: map[string][]string{"choice": {"First"}}})
			assertCode(t, err, domain.RecoveryRequired)
		})
	}
}

func TestInteractionResponseInspectionWaitsWithoutBlockingOriginalEvents(t *testing.T) {
	for _, closeNative := range []bool{false, true} {
		t.Run(map[bool]string{false: "live", true: "immediate-cleanup"}[closeNative], func(t *testing.T) {
			c, response, arrival, page := responseHistoryFixture(t, false)
			original, err := c.InspectInteraction(context.Background(), arrival)
			if err != nil {
				t.Fatal(err)
			}
			proof := questionOutputFixture(t, c, original.TurnID, original.ItemID, []string{"First"})
			fixtureSignal(t, c, "history", map[string]any{"page": page, "notify": fixtureHistoryNotification{Method: proof.Method, Params: proof.Params}})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			observed := make(chan Event, 1)
			failed := make(chan error, 1)
			go func() {
				for {
					event, err := c.NextEvent(ctx)
					if err != nil {
						failed <- err
						return
					}
					if event.Kind == QuestionAcceptedEvent {
						if closeNative {
							if err := c.Close(); err != nil {
								failed <- err
								return
							}
						}
						observed <- event
						return
					}
				}
			}()
			prior := c.problem
			status, err := c.InspectInteractionResponse(ctx, response, arrival)
			if err != nil || !status.Accepted || status.ResponseID != response || status.Delivery != QuestionDeliveryUncertain {
				t.Fatal("inspector blocked original live evidence or lost cleanup-racing proof", err)
			}
			select {
			case event := <-observed:
				if event.InteractionState == nil || *event.InteractionState != status {
					t.Fatal("inspection replaced the original publication event")
				}
			case err := <-failed:
				t.Fatal(err)
			case <-ctx.Done():
				t.Fatal("original event reader did not complete")
			}
			if c.problem != prior || !c.execution.paused {
				t.Fatal("concurrent acceptance erased recovery")
			}
		})
	}
}

func TestInteractionResponseInspectionRejectsChangedNativeScope(t *testing.T) {
	for _, change := range []string{"missing", "foreign-turn", "summary", "input", "text", "duplicate", "provider", "cwd", "session", "second-root"} {
		t.Run(change, func(t *testing.T) {
			c, response, arrival, page := responseHistoryFixture(t, true)
			turn := page["data"].([]any)[0].(map[string]any)
			item := turn["items"].([]any)[0].(map[string]any)
			switch change {
			case "missing":
				page["data"] = []any{}
			case "foreign-turn":
				turn["id"] = domain.NewID()
			case "summary":
				turn["itemsView"] = "summary"
			case "input":
				item["clientId"] = domain.NewID()
			case "text":
				item["content"].([]any)[0].(map[string]any)["text"] = "changed private prompt"
			case "duplicate":
				turn["items"] = append(turn["items"].([]any), item)
			case "provider":
				fixtureSignal(t, c, "metadata", map[string]any{"modelProvider": "foreign"})
			case "cwd":
				fixtureSignal(t, c, "metadata", map[string]any{"cwd": "/foreign"})
			case "session":
				fixtureSignal(t, c, "metadata", map[string]any{"sessionId": domain.NewID()})
			case "second-root":
				fixtureSignal(t, c, "history", map[string]any{"page": page, "changeAfterRead": true})
			}
			if change != "second-root" {
				fixtureSignal(t, c, "history", map[string]any{"page": page})
			}
			prior := c.problem
			status, err := c.InspectInteractionResponse(context.Background(), response, arrival)
			assertCode(t, err, domain.RecoveryRequired)
			if !status.Accepted || c.problem != prior || !c.execution.paused {
				t.Fatal("failed inspection erased earlier facts/recovery")
			}
		})
	}
}

func TestInteractionResponseInspectionSerializesAndRejectsUnownedAttempts(t *testing.T) {
	c, response, arrival, _ := responseHistoryFixture(t, true)
	for _, ids := range [][2]domain.ID{{domain.NewID(), arrival}, {response, domain.NewID()}} {
		_, err := c.InspectInteractionResponse(context.Background(), ids[0], ids[1])
		assertCode(t, err, domain.Conflict)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.InspectInteractionResponse(ctx, response, arrival)
	assertCode(t, err, domain.Canceled)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			status, err := c.InspectInteractionResponse(context.Background(), response, arrival)
			if err != nil || !status.Accepted {
				t.Error("serialized inspector lost original evidence", err)
			}
		})
	}
	wg.Wait()
}
