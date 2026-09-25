package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func questionOutputFixture(t *testing.T, c *Client, turn domain.ID, item string, answers []string) nativewire.Event {
	t.Helper()
	output := string(mustJSON(t, nativeQuestionResponse{Answers: map[string]nativeQuestionAnswer{"choice": {Answers: answers}}}))
	return nativewire.Event{Kind: nativewire.Notification, Method: "rawResponseItem/completed", Params: mustJSON(t, map[string]any{"threadId": c.thread, "turnId": turn, "item": map[string]any{"type": "function_call_output", "call_id": item, "output": output}})}
}

func TestQuestionAcceptanceRequiresExactNativeOutputAndPreservesOtherRecovery(t *testing.T) {
	for _, priorRecovery := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy", true: "prior-recovery"}[priorRecovery], func(t *testing.T) {
			c, turn, request := ownedQuestionFixture(t, questionFixture())
			responseID := domain.NewID()
			answers := QuestionAnswers{Answers: map[string][]string{"choice": {"First"}}}
			if _, err := c.AnswerQuestions(context.Background(), responseID, request.Interaction.ID, turn, answers); err != nil {
				t.Fatal(err)
			}
			answers.Answers["choice"][0] = "Second"
			nextKind(t, c, InteractionClosedEvent)
			if !c.execution.interactions.blocksInput() {
				t.Fatal("closure falsely confirmed acceptance")
			}
			if priorRecovery {
				c.problem, c.execution.paused = interactionUncertain(), true
			}
			evidence := questionOutputFixture(t, c, turn, request.ItemID, []string{"First"})
			e, err := c.observeEventLocked(evidence)
			if err != nil || e.Kind != QuestionAcceptedEvent || e.Native != nil || !e.Correlated || e.InteractionState == nil || !e.InteractionState.Accepted || e.InteractionState.ResponseID != responseID || e.InteractionState.ID != request.Interaction.ID || e.InteractionState.Closure != InteractionNativeClosed || e.InteractionState.Delivery != QuestionTransmitted {
				t.Fatal("exact native output lost independent acceptance facts", err)
			}
			raw := string(mustJSON(t, e))
			if strings.Contains(raw, "First") || strings.Contains(raw, "answers") || strings.Contains(raw, "function_call_output") || c.execution.interactions.blocksInput() {
				t.Fatal("raw answer escaped or confirmed question remained unconfirmed")
			}
			duplicate, err := c.observeEventLocked(evidence)
			if err != nil || duplicate.Kind != MetadataEvent || duplicate.Metadata != RawSupplementDiscarded {
				t.Fatal("duplicate acceptance can publish twice")
			}
			if priorRecovery && (c.problem == nil || !c.execution.paused) {
				t.Fatal("acceptance cleared unrelated native recovery")
			}
			if !priorRecovery {
				fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
				nextKind(t, c, TurnCompletedEvent)
				if _, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode)); err != nil {
					t.Fatal("confirmed completed question prevented the next native turn", err)
				}
			}
		})
	}
}

func TestQuestionAcceptanceRejectsMismatchedOrAmbiguousEvidence(t *testing.T) {
	for _, change := range []string{"answer", "empty", "null", "missing", "extra", "duplicate", "name", "namespace", "unknown-turn", "no-send", "foreign-thread", "foreign-call", "other-type"} {
		t.Run(change, func(t *testing.T) {
			c, turn, request := ownedQuestionFixture(t, questionFixture())
			if change != "no-send" {
				if _, err := c.AnswerQuestions(context.Background(), domain.NewID(), request.Interaction.ID, turn, QuestionAnswers{Answers: map[string][]string{"choice": {"First"}}}); err != nil {
					t.Fatal(err)
				}
				nextKind(t, c, InteractionClosedEvent)
			}
			e := questionOutputFixture(t, c, turn, request.ItemID, []string{"First"})
			var fields map[string]any
			_ = json.Unmarshal(e.Params, &fields)
			item := fields["item"].(map[string]any)
			switch change {
			case "answer":
				item["output"] = `{"answers":{"choice":{"answers":["Second"]}}}`
			case "empty":
				item["output"] = `{"answers":{"choice":{"answers":[]}}}`
			case "null":
				item["output"] = `{"answers":{"choice":{"answers":null}}}`
			case "missing":
				item["output"] = `{"answers":{}}`
			case "extra":
				item["output"] = `{"answers":{"choice":{"answers":["First"]}},"approved":true}`
			case "duplicate":
				item["output"] = `{"answers":{"choice":{"answers":["First"]},"choice":{"answers":["First"]}}}`
			case "name":
				item["name"] = "foreign_tool"
			case "namespace":
				item["namespace"] = "foreign"
			case "unknown-turn":
				fields["turnId"] = domain.NewID()
			case "foreign-thread":
				fields["threadId"] = domain.NewID()
			case "foreign-call":
				item["call_id"] = "foreign"
			case "other-type":
				item["type"] = "message"
			}
			e.Params = mustJSON(t, fields)
			observed, err := c.observeEventLocked(e)
			if change == "foreign-thread" || change == "foreign-call" || change == "other-type" || change == "no-send" {
				if err != nil || observed.Kind == QuestionAcceptedEvent {
					t.Fatal("unowned supplement acquired acceptance", err)
				}
			} else if err == nil {
				t.Fatal("mismatched evidence accepted")
			}
			status, err := c.InspectInteraction(context.Background(), request.Interaction.ID)
			if err != nil || status.Accepted || !c.execution.interactions.blocksInput() {
				t.Fatal("invalid evidence changed response ownership")
			}
		})
	}
}

func TestQuestionAcceptanceRetainsExplicitEmptyAnswersAndRejectsCallReuse(t *testing.T) {
	c, turn, request := ownedQuestionFixture(t, questionFixture())
	if _, err := c.AnswerQuestions(context.Background(), domain.NewID(), request.Interaction.ID, turn, QuestionAnswers{Answers: map[string][]string{"choice": {}}}); err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, InteractionClosedEvent)
	e, err := c.observeEventLocked(questionOutputFixture(t, c, turn, request.ItemID, []string{}))
	if err != nil || e.Kind != QuestionAcceptedEvent {
		t.Fatal("explicit empty answer was lost", err)
	}
	fixtureSignal(t, c, "question", map[string]any{"requestId": 8, "questions": []any{questionFixture()}})
	_, err = c.NextEvent(context.Background())
	assertCode(t, err, domain.RecoveryRequired)
}

func TestRawSupplementsNeverPublishPrivateContentsOrInventUsage(t *testing.T) {
	c, turn := observationClient()
	for _, method := range []string{"rawResponseItem/completed", "rawResponse/completed"} {
		params := map[string]any{"threadId": c.thread, "turnId": turn}
		if method == "rawResponseItem/completed" {
			params["item"] = map[string]any{"type": "message", "content": "private-instructions"}
		} else {
			params["responseId"], params["usage"], params["usageMetadata"] = "native-response", map[string]any{"inputTokens": 10}, map[string]any{"amount": "private-amount"}
		}
		e, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: method, Params: mustJSON(t, params)})
		if err != nil || e.Kind != MetadataEvent || e.Metadata != RawSupplementDiscarded || e.Usage != nil || e.Native != nil || strings.Contains(string(mustJSON(t, e)), "private-") {
			t.Fatal("private supplement escaped or invented product usage", err)
		}
	}
}
