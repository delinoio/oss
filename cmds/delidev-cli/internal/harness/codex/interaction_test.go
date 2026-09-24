package codex

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (f *threadFixture) questionReply(id, raw json.RawMessage) bool {
	var response nativeQuestionResponse
	if domain.Decode(raw, &response) != nil || response.Answers == nil {
		return false
	}
	f.notify("serverRequest/resolved", map[string]any{"threadId": f.thread["id"], "requestId": id})
	return true
}

func ownedQuestionFixture(t *testing.T, question map[string]any) (*Client, domain.ID, Event) {
	t.Helper()
	c, _, _, _ := boundTurnFixture(t, "questions")
	turn, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
	if err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, c, "question", map[string]any{"requestId": 7, "questions": []any{question}})
	event := nextKind(t, c, InteractionRequestedEvent)
	return c, turn.TurnID, event
}

func TestNativeQuestionAnswerClaimsOneExactOriginalArrival(t *testing.T) {
	c, turn, event := ownedQuestionFixture(t, questionFixture())
	id := event.Interaction.ID
	answers := QuestionAnswers{Answers: map[string][]string{"choice": {"First"}}}
	// Normalized observations belong to the caller. Editing one cannot change
	// the immutable request used by the native responder's authorization checks.
	event.Interaction.Questions.Questions[0].Other = true
	event.Interaction.Questions.Questions[0].Options[0].Label = "Changed"
	_, err := c.AnswerQuestions(context.Background(), domain.NewID(), id, turn, QuestionAnswers{Answers: map[string][]string{"choice": {"Changed"}}})
	assertCode(t, err, domain.InvalidArgument)
	_, err = c.AnswerQuestions(context.Background(), domain.NewID(), id, domain.NewID(), answers)
	assertCode(t, err, domain.Conflict)
	var wg sync.WaitGroup
	type result struct {
		status InteractionStatus
		err    error
	}
	results := make(chan result, 2)
	for range 2 {
		wg.Go(func() {
			status, err := c.AnswerQuestions(context.Background(), domain.NewID(), id, turn, answers)
			results <- result{status, err}
		})
	}
	wg.Wait()
	accepted, conflicts := 0, 0
	for range 2 {
		r := <-results
		status, err := r.status, r.err
		if err == nil {
			accepted++
			if status.Delivery != QuestionTransmitted || status.ResponseID == "" || status.Closure != InteractionOpen {
				t.Fatal("pipe delivery lost its unresolved semantic state")
			}
		} else if domain.SafeError(err).Code == domain.Conflict {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || conflicts != 1 {
		t.Fatal("concurrent responses did not claim exactly one arrival")
	}
	closed := nextKind(t, c, InteractionClosedEvent)
	if closed.InteractionState.ID != id || closed.InteractionState.Closure != InteractionNativeClosed || closed.InteractionState.Delivery != QuestionTransmitted {
		t.Fatal("native closure replaced original response identity/delivery")
	}
	status, err := c.InspectInteraction(context.Background(), id)
	if err != nil || status != *closed.InteractionState || !c.execution.interactions.blocksInput() {
		t.Fatal("closure was presented as semantic acceptance")
	}
	if c.execution.interactions.bytes != 0 || c.execution.interactions.open != 0 || c.execution.interactions.arrivals[id].questions != nil {
		t.Fatal("closed request retained its question payload")
	}
	_, err = c.AnswerQuestions(context.Background(), domain.NewID(), id, turn, answers)
	assertCode(t, err, domain.Conflict)
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	nextKind(t, c, TurnCompletedEvent)
	_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
	assertCode(t, err, domain.Conflict)
	raw, _ := json.Marshal(answers)
	if strings.Contains(string(raw), "First") || strings.Contains(string(raw), "choice") {
		t.Fatal("memory-only answer escaped ordinary serialization")
	}
}

func TestNativeQuestionClosureAndStopPreventStaleAnswers(t *testing.T) {
	for _, action := range []string{"resolved", "turn-ended", "interrupt", "foreign", "unknown", "text-id"} {
		t.Run(action, func(t *testing.T) {
			c, turn, event := ownedQuestionFixture(t, questionFixture())
			id := event.Interaction.ID
			wantClosed := InteractionNativeClosed
			switch action {
			case "resolved", "foreign", "unknown", "text-id":
				thread := c.thread
				var nativeID any = 7
				if action == "foreign" {
					thread = domain.NewID()
				}
				if action == "unknown" {
					nativeID = 8
				}
				if action == "text-id" {
					nativeID = "7"
				}
				fixtureSignal(t, c, "notify", map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": thread, "requestId": nativeID}})
				if action == "resolved" {
					nextKind(t, c, InteractionClosedEvent)
				} else {
					nextKind(t, c, NativeExtensionEvent)
				}
			case "turn-ended":
				fixtureSignal(t, c, "finish", map[string]any{"status": TurnInterrupted})
				nextKind(t, c, TurnCompletedEvent)
				wantClosed = InteractionTurnEnded
			case "interrupt":
				if _, err := c.Interrupt(context.Background(), domain.NewID(), turn); err != nil {
					t.Fatal(err)
				}
			}
			status, err := c.InspectInteraction(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if action == "resolved" || action == "turn-ended" {
				if status.Closure != wantClosed || status.Delivery != QuestionNotSent {
					t.Fatal("unanswered native closure invented a response")
				}
			} else if status.Closure != InteractionOpen {
				t.Fatal("unowned closure changed pending request")
			}
			_, err = c.AnswerQuestions(context.Background(), domain.NewID(), id, turn, QuestionAnswers{Answers: map[string][]string{"choice": {"First"}}})
			if action == "foreign" || action == "unknown" || action == "text-id" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				assertCode(t, err, domain.Conflict)
			}
		})
	}
}

func TestNativeQuestionsRefuseIdentityReuseAndRetainCanceledInput(t *testing.T) {
	c, turn, event := ownedQuestionFixture(t, questionFixture())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	responseID := domain.NewID()
	_, err := c.AnswerQuestions(ctx, responseID, event.Interaction.ID, turn, QuestionAnswers{Answers: map[string][]string{"choice": {"First"}}})
	assertCode(t, err, domain.Canceled)
	status, err := c.InspectInteraction(context.Background(), event.Interaction.ID)
	if err != nil || status.ResponseID != "" || status.Delivery != QuestionNotSent {
		t.Fatal("pre-send cancellation consumed response")
	}
	fixtureSignal(t, c, "notify", map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": c.thread, "requestId": 7}})
	nextKind(t, c, InteractionClosedEvent)
	fixtureSignal(t, c, "question", map[string]any{"requestId": 7, "questions": []any{questionFixture()}})
	_, err = c.NextEvent(context.Background())
	assertCode(t, err, domain.RecoveryRequired)
	if c.execution.interactions.arrivals[event.Interaction.ID].status.Closure != InteractionNativeClosed {
		t.Fatal("reused native identity replaced original ownership")
	}
}

func TestNativeQuestionAnswersValidateOriginalShapeAndSecretBoundary(t *testing.T) {
	for _, change := range []string{"missing", "unknown", "nil", "duplicate", "invalid-option", "oversize", "secret", "other", "freeform", "skip"} {
		t.Run(change, func(t *testing.T) {
			q, err := decodeQuestion(mustJSON(t, questionFixture()))
			if err != nil {
				t.Fatal(err)
			}
			answers := QuestionAnswers{Answers: map[string][]string{"choice": {"First"}}}
			switch change {
			case "missing":
				delete(answers.Answers, "choice")
			case "unknown":
				answers.Answers = map[string][]string{"decision": {"accept"}}
			case "nil":
				answers.Answers["choice"] = nil
			case "duplicate":
				answers.Answers["choice"] = []string{"First", "First"}
			case "invalid-option":
				answers.Answers["choice"] = []string{"acceptForSession"}
			case "oversize":
				q.Other = true
				answers.Answers["choice"] = []string{strings.Repeat("x", maxAnswerBytes)}
			case "secret":
				q.Secret = true
			case "other":
				q.Other = true
				answers.Answers["choice"] = []string{"Explicit text"}
			case "freeform":
				q.Options = nil
				answers.Answers["choice"] = []string{"Explicit text"}
			case "skip":
				answers.Answers["choice"] = []string{}
			}
			_, err = validateQuestionAnswers(&QuestionRequest{Questions: []Question{q}}, answers)
			if change == "other" || change == "freeform" || change == "skip" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid answer was accepted")
			}
		})
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestNativeQuestionSecretAndLateRequestNeverSend(t *testing.T) {
	q := questionFixture()
	q["isSecret"] = true
	c, turn, event := ownedQuestionFixture(t, q)
	event.Interaction.Questions.Questions[0].Secret = false
	_, err := c.AnswerQuestions(context.Background(), domain.NewID(), event.Interaction.ID, turn, QuestionAnswers{Answers: map[string][]string{"choice": {"First"}}})
	assertCode(t, err, domain.Unsupported)
	status, err := c.InspectInteraction(context.Background(), event.Interaction.ID)
	if err != nil || status.ResponseID != "" || status.Delivery != QuestionNotSent {
		t.Fatal("unsupported secret consumed a response claim")
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnInterrupted})
	nextKind(t, c, TurnCompletedEvent)
	fixtureSignal(t, c, "question", map[string]any{"requestId": 8, "itemId": "late-question", "questions": []any{questionFixture()}})
	late := nextKind(t, c, InteractionRequestedEvent)
	status, err = c.InspectInteraction(context.Background(), late.Interaction.ID)
	if err != nil || !late.Late || !late.Correlated || status.Closure != InteractionTurnEnded || status.Delivery != QuestionNotSent {
		t.Fatal("late question gained response authority")
	}
	_, err = c.AnswerQuestions(context.Background(), domain.NewID(), late.Interaction.ID, turn, QuestionAnswers{Answers: map[string][]string{"choice": {"First"}}})
	assertCode(t, err, domain.Conflict)
}

func TestNativeQuestionOwnershipBoundsDoNotReplaceRetainedState(t *testing.T) {
	for _, limit := range []string{"open", "bytes", "history"} {
		t.Run(limit, func(t *testing.T) {
			c, turn := observationClient()
			for i := 0; ; i++ {
				q := questionFixture()
				if limit == "bytes" {
					q["question"] = strings.Repeat("x", 512<<10)
				}
				event := questionEvent(t, c, turn, []any{q})
				event.ID = json.RawMessage(strconv.Itoa(i))
				var fields map[string]any
				_ = json.Unmarshal(event.Params, &fields)
				fields["itemId"] = "question-" + strconv.Itoa(i)
				event.Params = mustJSON(t, fields)
				if limit == "history" && i == 1 {
					for len(c.execution.interactions.arrivals) < maxTrackedInteractions {
						c.execution.interactions.arrivals[domain.NewID()] = &trackedInteraction{}
					}
				}
				before := len(c.execution.interactions.arrivals)
				_, err := c.observeEventLocked(event)
				if err != nil {
					assertCode(t, err, domain.ResourceExhausted)
					if len(c.execution.interactions.arrivals) != before || c.execution.interactions.arrivals[event.Token] != nil {
						t.Fatal("failed retention consumed an arrival")
					}
					break
				}
				if i > maxOpenInteractions {
					t.Fatal("pending ownership was not bounded")
				}
			}
		})
	}
}

func TestNativeQuestionUncertainPipeDeliveryRetainsAttemptAndBlocksReplay(t *testing.T) {
	c, _, _, _ := boundTurnFixture(t, "questions-blocked")
	turn, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
	if err != nil {
		t.Fatal(err)
	}
	q := questionFixture()
	q["isOther"] = true
	fixtureSignal(t, c, "question", map[string]any{"requestId": 7, "questions": []any{q}})
	event := nextKind(t, c, InteractionRequestedEvent)
	responseID := domain.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status, err := c.AnswerQuestions(ctx, responseID, event.Interaction.ID, turn.TurnID, QuestionAnswers{Answers: map[string][]string{"choice": {strings.Repeat("x", 200<<10)}}})
	assertCode(t, err, domain.RecoveryRequired)
	if status.ResponseID != responseID || status.Delivery != QuestionDeliveryUncertain || status.Closure != InteractionOpen || !c.execution.paused || !c.execution.interactions.blocksInput() {
		t.Fatal("uncertain delivery lost ownership or unpaused input")
	}
	_, err = c.AnswerQuestions(context.Background(), domain.NewID(), event.Interaction.ID, turn.TurnID, QuestionAnswers{Answers: map[string][]string{"choice": {"First"}}})
	assertCode(t, err, domain.RecoveryRequired)
	retained, err := c.InspectInteraction(context.Background(), event.Interaction.ID)
	if err != nil || retained != status {
		t.Fatal("attempted replay replaced uncertain response")
	}
}
