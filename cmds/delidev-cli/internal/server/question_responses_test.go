package server

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func questionResponseFixture(t *testing.T) (*publicationFixture, domain.ID) {
	t.Helper()
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	id := domain.NewID()
	e := f.interactionEvent(3, id, "native-question")
	e.Interaction.Questions.Questions = append(e.Interaction.Questions.Questions, domain.Question{ID: "optional", Header: "Optional", Text: "Optional question", Other: true})
	f.publish(t, e)
	return f, id
}

func responseInput() domain.QuestionResponseInput {
	return domain.QuestionResponseInput{Answers: map[string][]string{"choice": {"Second", "First", "Exact 답변\n"}, "optional": {}}}
}

func acceptFixtureResponse(f *publicationFixture, responseID, interactionID domain.ID, revision uint64, input domain.QuestionResponseInput) (store.Result, error) {
	identity := struct {
		InteractionID domain.ID
		Revision      uint64
		Input         domain.QuestionResponseInput
	}{interactionID, revision, input}
	return f.service.Store.Mutate(context.Background(), responseID, "fixture.question.respond", identity, func(tx *store.Tx) (any, error) {
		r, err := f.service.acceptQuestionResponse(tx, responseID, interactionID, revision, input)
		return struct{ ID domain.ID }{r.ID}, err
	})
}

func TestQuestionResponseConcurrentAcceptanceAndClosureReplay(t *testing.T) {
	for _, closure := range []domain.InteractionClosure{domain.InteractionNativeClosed, domain.InteractionTurnEnded} {
		t.Run(string(closure), func(t *testing.T) {
			f, id := questionResponseFixture(t)
			_, original := readPublishedInteraction(t, f, id)
			type outcome struct {
				id     domain.ID
				result store.Result
				err    error
			}
			results := make(chan outcome, 8)
			var group sync.WaitGroup
			for range 8 {
				group.Go(func() {
					responseID := domain.NewID()
					result, err := acceptFixtureResponse(f, responseID, id, 1, responseInput())
					results <- outcome{responseID, result, err}
				})
			}
			group.Wait()
			close(results)
			var accepted outcome
			count := 0
			for result := range results {
				if result.err == nil {
					accepted = result
					count++
				} else if domain.SafeError(result.err).Code != domain.Conflict {
					t.Fatal(result.err)
				}
			}
			if count != 1 {
				t.Fatalf("accepted %d conflicting responses", count)
			}
			r, value := readPublishedInteraction(t, f, id)
			if r.Revision != 2 || value.Response == nil || value.Response.ID != accepted.id || value.Response.State != domain.QuestionResponseQueued || value.Response.AcceptedAt.IsZero() || !reflect.DeepEqual(value.Response.Input, responseInput()) || !reflect.DeepEqual(value.Questions, original.Questions) {
				t.Fatal("response acceptance changed the original question or exact answer")
			}
			var receipt map[string]json.RawMessage
			if json.Unmarshal(accepted.result.Data, &receipt) != nil || len(receipt) != 1 || string(receipt["ID"]) != `"`+string(id)+`"` {
				t.Fatal("response receipt retained content instead of an interaction reference")
			}
			if _, err := acceptFixtureResponse(f, domain.NewID(), id, r.Revision, responseInput()); domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("current revision permitted a second response")
			}
			e := f.interactionEvent(4, id, "native-question")
			e.Kind, e.Interaction.Questions, e.Interaction.Closure = domain.ExecutionInteractionClosed, nil, domain.InteractionNativeClosed
			if closure == domain.InteractionTurnEnded {
				e = f.event(domain.ExecutionTurnFinished, 4)
				e.Outcome = domain.ExecutionStopped
			}
			f.publish(t, e)
			if replay, err := acceptFixtureResponse(f, accepted.id, id, 1, responseInput()); err != nil || !replay.Replayed {
				t.Fatalf("accepted response lost its exact retry receipt: %v", err)
			}
			r, value = readPublishedInteraction(t, f, id)
			if r.Revision != 3 || value.Closure != closure || value.Response.State != domain.QuestionResponseCanceled || value.Response.ID != accepted.id || !reflect.DeepEqual(value.Response.Input, responseInput()) || !reflect.DeepEqual(value.Questions, original.Questions) {
				t.Fatal("native closure/replay lost evidence or requeued an unsent answer")
			}
		})
	}
}

func TestQuestionResponseRejectsChangedExecutionAuthority(t *testing.T) {
	for _, change := range []string{"pause", "archive", "recovery", "thread", "turn", "execution", "cleanup", "cancel", "finished-job", "worker", "heartbeat", "device", "account", "connection", "epoch", "revision", "secret", "wrong-kind", "answers"} {
		t.Run(change, func(t *testing.T) {
			f, id := questionResponseFixture(t)
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.response-authority", change, func(tx *store.Tx) (any, error) {
				switch change {
				case "cancel":
					return nil, tx.RequestJobCancellation(f.job)
				case "worker":
					return nil, tx.SetWorkerInstance(f.input.MachineID, domain.NewID(), time.Now().UTC())
				case "heartbeat":
					return nil, tx.SetWorkerInstance(f.input.MachineID, f.instance, time.Now().Add(-time.Minute))
				case "epoch", "revision", "answers":
					return nil, nil
				}
				kind, recordID := domain.SessionKind, f.input.SessionID
				switch change {
				case "device":
					kind, recordID = domain.DeviceKind, f.device
				case "account", "connection":
					kind, recordID = domain.AccountKind, f.input.AccountID
				case "finished-job":
					kind, recordID = domain.JobKind, f.job
				case "secret", "wrong-kind":
					kind, recordID = domain.InteractionKind, id
				}
				r, err := tx.Get(kind, recordID)
				if err != nil {
					return nil, err
				}
				var document map[string]any
				if err := json.Unmarshal(r.Data, &document); err != nil {
					return nil, err
				}
				switch change {
				case "pause":
					document["dispatch"] = domain.DispatchPaused
				case "archive":
					document["archive"] = domain.Archived
				case "recovery":
					document["recovery"] = domain.NeedsRecovery
				case "thread", "turn":
					document["execution"].(map[string]any)["native_"+change+"_id"] = domain.NewID()
				case "execution":
					document["active_execution_id"] = domain.NewID()
				case "cleanup":
					document["execution"].(map[string]any)["cleanup_verified"] = true
				case "finished-job":
					document["state"] = domain.JobSucceeded
				case "device":
					document["revoked"] = true
				case "account":
					document["enabled"] = false
				case "connection":
					document["connection"].(map[string]any)["id"] = domain.NewID()
				case "secret":
					document["questions"].(map[string]any)["questions"].([]any)[0].(map[string]any)["secret"] = true
				case "wrong-kind":
					document["type"] = "approval"
				}
				return tx.Put(kind, recordID, r.Revision, r.SessionID, r.ProjectID, document)
			})
			if err != nil {
				t.Fatal(err)
			}
			if change == "epoch" {
				f.service.executionAuthority.epoch = domain.NewID()
			}
			r, before := readPublishedInteraction(t, f, id)
			revision, input := r.Revision, responseInput()
			if change == "revision" {
				revision++
			}
			if change == "answers" {
				delete(input.Answers, "choice")
			}
			if _, err := acceptFixtureResponse(f, domain.NewID(), id, revision, input); err == nil {
				t.Fatal("response bypassed current execution authority or original request")
			}
			after, value := readPublishedInteraction(t, f, id)
			if after.Revision != r.Revision || !reflect.DeepEqual(before, value) {
				t.Fatal("rejected response partially changed the interaction")
			}
		})
	}
}
