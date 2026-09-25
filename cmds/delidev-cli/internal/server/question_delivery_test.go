package server

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func questionDeliveryEvent(f *publicationFixture, claim *pb.ClaimQuestionResponseRequest, delivery domain.QuestionDelivery) domain.ExecutionEvent {
	e := f.event(domain.ExecutionQuestionDeliveryObserved, 4)
	e.QuestionResponse = &domain.ExecutionQuestionResponseUpdate{InteractionID: domain.ID(claim.Mutation.Id), ResponseID: domain.ID(claim.ResponseId), ClaimID: domain.ID(claim.Mutation.RequestId), NativeItemID: "question-tool", Delivery: delivery}
	return e
}

func TestQuestionDeliveryRetainsTransportSeparatelyFromAcceptance(t *testing.T) {
	for _, delivery := range []domain.QuestionDelivery{domain.QuestionNotSent, domain.QuestionTransmitted, domain.QuestionDeliveryUncertain} {
		t.Run(string(delivery), func(t *testing.T) {
			f, claim := questionClaimFixture(t)
			if _, err := claimQuestion(f, claim); err != nil {
				t.Fatal(err)
			}
			receipt := f.publish(t, questionDeliveryEvent(f, claim, delivery))
			if replay, err := f.call(receipt); err != nil || !replay.Msg.Replayed {
				t.Fatal("delivery lost its exact event receipt", err)
			}
			id := domain.ID(claim.Mutation.Id)
			r, value := readPublishedInteraction(t, f, id)
			state := domain.QuestionResponseTransmitted
			if delivery == domain.QuestionNotSent {
				state = domain.QuestionResponseCanceled
			} else if delivery == domain.QuestionDeliveryUncertain {
				state = domain.QuestionResponseUncertain
			}
			if r.Revision != 4 || value.Response.State != state || value.Closure != domain.InteractionOpen || value.Response.Delivery.State != delivery || value.Response.Delivery.Sequence != 4 || !reflect.DeepEqual(value.Response.Input, responseInput()) || value.LastSequence != 4 {
				t.Fatal("delivery changed original answers or invented native closure/acceptance")
			}
			duplicate := questionDeliveryEvent(f, claim, delivery)
			duplicate.Sequence = 5
			if _, err := f.call(f.requestEvent(t, duplicate)); err == nil {
				t.Fatal("another sequence replaced an immutable delivery observation")
			}
			closed := f.interactionEvent(5, id, "native-question")
			closed.Kind, closed.Interaction.Questions, closed.Interaction.Closure = domain.ExecutionInteractionClosed, nil, domain.InteractionNativeClosed
			f.publish(t, closed)
			terminal := f.event(domain.ExecutionTurnFinished, 6)
			terminal.Outcome = domain.ExecutionSucceeded
			f.publish(t, terminal)
			_, value = readPublishedInteraction(t, f, id)
			if value.Response.Delivery.State != delivery || value.Response.State != state || value.Closure != domain.InteractionNativeClosed {
				t.Fatal("native closure replaced the transport observation")
			}
			sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](sr)
			if err != nil || session.Execution.Outcome != domain.ExecutionSucceeded {
				t.Fatal("response gate changed the native outcome")
			}
			if delivery == domain.QuestionNotSent {
				if session.Execution.UnconfirmedResponses != 0 || session.Recovery != domain.NoRecovery {
					t.Fatal("proven unsent answer acquired acceptance uncertainty")
				}
			} else if session.Execution.UnconfirmedResponses != 1 || session.Recovery != domain.NeedsRecovery || session.Dispatch != domain.DispatchPaused || (delivery == domain.QuestionTransmitted && session.Outcome != domain.ExecutionSucceeded) {
				t.Fatal("pipe transmission or closure falsely cleared native acceptance recovery")
			}
		})
	}
}

func TestQuestionDeliveryRejectsUnclaimedAndMismatchedObservations(t *testing.T) {
	for _, changed := range []string{"unclaimed", "response", "claim", "interaction", "item", "turn", "thread", "delivery", "other-payload", "answers"} {
		t.Run(changed, func(t *testing.T) {
			f, claim := questionClaimFixture(t)
			if changed != "unclaimed" {
				if _, err := claimQuestion(f, claim); err != nil {
					t.Fatal(err)
				}
			}
			before, original := readPublishedInteraction(t, f, domain.ID(claim.Mutation.Id))
			e := questionDeliveryEvent(f, claim, domain.QuestionTransmitted)
			switch changed {
			case "response":
				e.QuestionResponse.ResponseID = domain.NewID()
			case "claim":
				e.QuestionResponse.ClaimID = domain.NewID()
			case "interaction":
				e.QuestionResponse.InteractionID = domain.NewID()
			case "item":
				e.QuestionResponse.NativeItemID = "foreign"
			case "turn":
				e.NativeTurnID = string(domain.NewID())
			case "thread":
				e.NativeThreadID = string(domain.NewID())
			case "delivery":
				e.QuestionResponse.Delivery = "accepted"
			case "other-payload":
				e.Outcome = domain.ExecutionSucceeded
			}
			req := f.requestEvent(t, e)
			if changed == "answers" {
				var document map[string]any
				_ = json.Unmarshal(req.EventJson, &document)
				document["question_response"].(map[string]any)["answers"] = responseInput().Answers
				req.EventJson, _ = json.Marshal(document)
			}
			if _, err := f.call(req); err == nil {
				t.Fatal("invalid native delivery acquired response ownership")
			}
			after, value := readPublishedInteraction(t, f, before.ID)
			if after.Revision != before.Revision || !reflect.DeepEqual(value, original) {
				t.Fatal("rejected observation partially changed response evidence")
			}
			sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](sr)
			if err != nil || session.Execution.LastSequence != 3 || session.Execution.UnconfirmedResponses != 0 {
				t.Fatal("rejected observation consumed sequence or altered response accounting")
			}
		})
	}
}
