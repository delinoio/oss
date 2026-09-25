package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func acceptanceEvent(f *publicationFixture, claim *pb.ClaimQuestionResponseRequest, sequence uint64) domain.ExecutionEvent {
	e := f.event(domain.ExecutionQuestionAccepted, sequence)
	e.QuestionAcceptance = &domain.ExecutionQuestionAcceptanceUpdate{InteractionID: domain.ID(claim.Mutation.Id), ResponseID: domain.ID(claim.ResponseId), ClaimID: domain.ID(claim.Mutation.RequestId), NativeItemID: "question-tool", Evidence: domain.NativeQuestionOutput}
	return e
}

func TestQuestionAcceptanceReconcilesExactlyOnceWithoutClearingPriorRecovery(t *testing.T) {
	for _, delivery := range []domain.QuestionDelivery{domain.QuestionTransmitted, domain.QuestionDeliveryUncertain} {
		for _, closedFirst := range []bool{false, true} {
			t.Run(string(delivery)+map[bool]string{false: "/open", true: "/closed"}[closedFirst], func(t *testing.T) {
				f, claim := questionClaimFixture(t)
				if _, err := claimQuestion(f, claim); err != nil {
					t.Fatal(err)
				}
				f.publish(t, questionDeliveryEvent(f, claim, delivery))
				sequence := uint64(5)
				id := domain.ID(claim.Mutation.Id)
				if closedFirst {
					closed := f.interactionEvent(sequence, id, "native-question")
					closed.Kind, closed.Interaction.Questions, closed.Interaction.Closure = domain.ExecutionInteractionClosed, nil, domain.InteractionNativeClosed
					f.publish(t, closed)
					sequence++
				}
				receipt := f.publish(t, acceptanceEvent(f, claim, sequence))
				if replay, err := f.call(receipt); err != nil || !replay.Msg.Replayed {
					t.Fatal("acceptance receipt lost", err)
				}
				before, accepted := readPublishedInteraction(t, f, id)
				if accepted.Response.State != domain.QuestionResponseAccepted || accepted.Response.Acceptance == nil || accepted.Response.Acceptance.Sequence != sequence || accepted.Response.Acceptance.Evidence != domain.NativeQuestionOutput || accepted.Response.Delivery.State != delivery || !reflect.DeepEqual(accepted.Response.Input, responseInput()) {
					t.Fatal("native acceptance changed original transport/content evidence")
				}
				if _, err := f.call(f.requestEvent(t, acceptanceEvent(f, claim, sequence+1))); err == nil {
					t.Fatal("acceptance was counted twice")
				}
				after, value := readPublishedInteraction(t, f, id)
				if after.Revision != before.Revision || !reflect.DeepEqual(accepted, value) {
					t.Fatal("duplicate partially replaced acceptance")
				}
				terminal := f.event(domain.ExecutionTurnFinished, sequence+1)
				terminal.Outcome = domain.ExecutionSucceeded
				f.publish(t, terminal)
				sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				session, err := store.Decode[domain.Session](sr)
				if err != nil || session.Execution.UnconfirmedResponses != 0 || session.Execution.Outcome != domain.ExecutionSucceeded || session.Execution.CleanupVerified {
					t.Fatal("acceptance invented native cleanup or lost terminal facts")
				}
				if (delivery == domain.QuestionTransmitted && session.Recovery != domain.NoRecovery) || (delivery == domain.QuestionDeliveryUncertain && session.Recovery != domain.NeedsRecovery) {
					t.Fatal("acceptance failed to distinguish healthy execution from prior recovery")
				}
				_, value = readPublishedInteraction(t, f, id)
				if value.Response.State != domain.QuestionResponseAccepted || value.Closure == domain.InteractionOpen {
					t.Fatal("terminal erased native acceptance")
				}
			})
		}
	}
}

func TestQuestionAcceptanceRejectsUnownedOrContentBearingEvidenceAtomically(t *testing.T) {
	for _, change := range []string{"no-delivery", "not-sent", "claim", "response", "interaction", "item", "turn", "thread", "evidence", "delivery-field", "answers"} {
		t.Run(change, func(t *testing.T) {
			f, claim := questionClaimFixture(t)
			if _, err := claimQuestion(f, claim); err != nil {
				t.Fatal(err)
			}
			sequence := uint64(4)
			if change != "no-delivery" {
				delivery := domain.QuestionTransmitted
				if change == "not-sent" {
					delivery = domain.QuestionNotSent
				}
				f.publish(t, questionDeliveryEvent(f, claim, delivery))
				sequence++
			}
			before, original := readPublishedInteraction(t, f, domain.ID(claim.Mutation.Id))
			e := acceptanceEvent(f, claim, sequence)
			switch change {
			case "claim":
				e.QuestionAcceptance.ClaimID = domain.NewID()
			case "response":
				e.QuestionAcceptance.ResponseID = domain.NewID()
			case "interaction":
				e.QuestionAcceptance.InteractionID = domain.NewID()
			case "item":
				e.QuestionAcceptance.NativeItemID = "foreign"
			case "turn":
				e.NativeTurnID = string(domain.NewID())
			case "thread":
				e.NativeThreadID = string(domain.NewID())
			case "evidence":
				e.QuestionAcceptance.Evidence = "native-closed"
			case "delivery-field":
				e.QuestionResponse = questionDeliveryEvent(f, claim, domain.QuestionTransmitted).QuestionResponse
			}
			req := f.requestEvent(t, e)
			if change == "answers" {
				var raw map[string]any
				_ = json.Unmarshal(req.EventJson, &raw)
				raw["question_acceptance"].(map[string]any)["answers"] = responseInput().Answers
				req.EventJson, _ = json.Marshal(raw)
			}
			if _, err := f.call(req); err == nil {
				t.Fatal("unowned or content-bearing acceptance entered persistence")
			}
			after, value := readPublishedInteraction(t, f, before.ID)
			if after.Revision != before.Revision || !reflect.DeepEqual(value, original) {
				t.Fatal("invalid acceptance partially changed the response")
			}
		})
	}
}

func TestQuestionAcceptanceOutboxReplaysLostAcknowledgmentWithoutAnswerContent(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	cfg := publicationWorkerConfig(t, f)
	path := filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json")
	transport := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: path, dropAt: 5}
	cfg.Client = transport
	publisher, mapper := bindNativeMapper(t, f, cfg)
	id, responseID := domain.NewID(), domain.NewID()
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.InteractionRequestedEvent, ItemID: "question-tool", Interaction: &codex.Interaction{ID: id, Kind: codex.UserInputInteraction, NativeID: codex.NativeRequestID{Kind: codex.TextRequestID, Text: "native-question"}, Questions: &codex.QuestionRequest{Questions: []codex.Question{{ID: "choice", Text: "Original question", Other: true}}}}})
	answer := domain.QuestionResponseInput{Answers: map[string][]string{"choice": {"private-fixture-answer"}}}
	if _, err := acceptFixtureResponse(f, responseID, id, 1, answer); err != nil {
		t.Fatal(err)
	}
	claim := &pb.ClaimQuestionResponseRequest{Mutation: &pb.Mutation{Id: string(id), ExpectedRevision: 2, RequestId: string(domain.NewID())}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(responseID)}
	if _, err := claimQuestion(f, claim); err != nil {
		t.Fatal(err)
	}
	if err := mapper.PublishQuestionDelivery(context.Background(), *questionDeliveryEvent(f, claim, domain.QuestionTransmitted).QuestionResponse); err != nil {
		t.Fatal(err)
	}
	e := codex.Event{Kind: codex.QuestionAcceptedEvent, ThreadID: f.thread, TurnID: f.turn, ItemID: "question-tool", Correlated: true, InteractionState: &codex.InteractionStatus{ID: id, TurnID: f.turn, ItemID: "question-tool", ResponseID: responseID, Delivery: codex.QuestionTransmitted, Closure: codex.InteractionOpen, Accepted: true}}
	if handled, err := mapper.PublishCore(context.Background(), e); !handled || err == nil {
		t.Fatal("lost acceptance acknowledgment was not retained")
	}
	raw, err := security.ReadPrivate(path, 1<<20)
	if err != nil || strings.Contains(string(raw), "private-fixture-answer") || strings.Contains(string(raw), "Original question") || !strings.Contains(string(raw), "question-accepted") {
		t.Fatal("acceptance outbox lost evidence or retained content")
	}
	before, accepted := readPublishedInteraction(t, f, id)
	if accepted.Response.Acceptance == nil {
		t.Fatal("accepted publication was lost")
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := worker.OpenExecutionPublisher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, value := readPublishedInteraction(t, f, id)
	if after.Revision != before.Revision || !reflect.DeepEqual(value, accepted) || len(transport.calls) != 6 || transport.calls[4] != transport.calls[5] {
		t.Fatal("replay changed native acceptance or its exact receipt")
	}
}
