package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func publicationQuestion() *domain.QuestionRequest {
	return &domain.QuestionRequest{Blocking: true, Questions: []domain.Question{{ID: "choice", Header: "Choose", Text: "Original question", Other: true, Options: []domain.QuestionOption{{Label: "First", Description: "First option"}, {Label: "Second", Description: "Second option"}}}}}
}

func (f *publicationFixture) interactionEvent(sequence uint64, id domain.ID, native string) domain.ExecutionEvent {
	e := f.event(domain.ExecutionInteractionRequested, sequence)
	e.Interaction = &domain.ExecutionInteractionUpdate{ID: id, NativeItemID: "question-tool", NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: native}, Type: domain.UserQuestionInteraction, Questions: publicationQuestion()}
	return e
}

func readPublishedInteraction(t *testing.T, f *publicationFixture, id domain.ID) (store.Record, domain.ExecutionInteraction) {
	t.Helper()
	r, err := f.service.Store.Get(context.Background(), domain.InteractionKind, id)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Decode[domain.ExecutionInteraction](r)
	if err != nil {
		t.Fatal(err)
	}
	return r, value
}

func TestExecutionInteractionsRetainOriginalQuestionsAndSeparateWaitingClosure(t *testing.T) {
	f := newPublicationFixture(t)
	_, mapper := bindNativeMapper(t, f, publicationWorkerConfig(t, f))
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ThreadStatusEvent, Status: &codex.ThreadStatus{Type: codex.ThreadActive, ActiveFlags: []codex.ActiveFlag{codex.WaitingInput, codex.WaitingApproval}}})
	number, duration := int64(7), uint64(100)
	first, second := domain.NewID(), domain.NewID()
	request := &codex.Interaction{ID: first, Kind: codex.UserInputInteraction, NativeID: codex.NativeRequestID{Kind: codex.NumberRequestID, Number: &number}, Questions: &codex.QuestionRequest{Blocking: true, AutoResolutionMS: &duration, Questions: []codex.Question{{ID: "choice", Header: "Choose", Text: "Original question", Other: true, Secret: true, Options: []codex.QuestionOption{{Label: "First", Description: "First option"}, {Label: "Second", Description: "Second option"}}}}}}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.InteractionRequestedEvent, ItemID: "question-tool", Interaction: request})
	request.ID = second
	request.NativeID = codex.NativeRequestID{Kind: codex.TextRequestID, Text: "7"}
	request.Questions.Blocking, request.Questions.Questions[0].Options = false, nil
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.InteractionRequestedEvent, ItemID: "question-tool-2", Interaction: request})
	// An idle/waiting-clear observation grants no response/closure authority.
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ThreadStatusEvent, Status: &codex.ThreadStatus{Type: codex.ThreadIdle}})
	r, original := readPublishedInteraction(t, f, first)
	if r.Revision != 1 || original.FirstSequence != 4 || original.LastSequence != 4 || original.ExecutionID != f.input.ExecutionID || original.NativeThreadID != string(f.thread) || original.NativeTurnID != string(f.turn) || original.NativeRequestID.Kind != domain.InteractionNumberID || *original.NativeRequestID.Number != 7 || original.Closure != domain.InteractionOpen || !original.Questions.Blocking || *original.Questions.AutoResolutionMS != 100 || !original.Questions.Questions[0].Secret || original.Questions.Questions[0].Options[1].Label != "Second" {
		t.Fatal("question identity, flags, payload or open state changed")
	}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.InteractionClosedEvent, ItemID: "question-tool", InteractionState: &codex.InteractionStatus{ID: first, TurnID: f.turn, ItemID: "question-tool", Closure: codex.InteractionNativeClosed, Delivery: codex.QuestionNotSent}})
	r, original = readPublishedInteraction(t, f, first)
	if r.Revision != 2 || original.Closure != domain.InteractionNativeClosed || original.LastSequence != 7 || original.Questions.Questions[0].Text != "Original question" || len(original.Questions.Questions[0].Options) != 2 {
		t.Fatal("closure discarded original request or invented an answer")
	}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.TurnCompletedEvent, Turn: &codex.Turn{ID: f.turn, Status: codex.TurnInterrupted}})
	r, retained := readPublishedInteraction(t, f, second)
	if retained.Closure != domain.InteractionTurnEnded || retained.LastSequence != 8 || retained.FirstSequence != 5 || retained.NativeRequestID.Kind != domain.InteractionTextID || retained.NativeRequestID.Text != "7" || retained.Questions.Questions[0].Options != nil || retained.Questions.Blocking || r.Revision != 2 {
		t.Fatal("turn completion lost unanswered request provenance")
	}
	sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Decode[domain.Session](sr)
	if err != nil || s.Outcome != domain.ExecutionStopped || s.Execution.Waiting != (domain.NativeWaiting{}) || s.Execution.LastSequence != 8 {
		t.Fatal("waiting state changed outcome or survived terminal state")
	}
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		ids, err := tx.OpenExecutionInteractions(f.input.ExecutionID)
		if len(ids) != 0 {
			t.Fatal("terminal index retained open requests")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionInteractionsRejectReplacementScopeAndResponsePayloads(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	id := domain.NewID()
	f.publish(t, f.interactionEvent(3, id, "native-original"))
	for _, bad := range []string{"duplicate-native", "duplicate-id", "wrong-turn", "wrong-native", "wrong-item", "wrong-kind", "approval", "questions-on-close", "premature-terminal-closure", "mixed-event", "response", "null-flag", "duplicate-option", "oversize"} {
		t.Run(bad, func(t *testing.T) {
			e := f.interactionEvent(4, id, "native-original")
			e.Kind, e.Interaction.Questions, e.Interaction.Closure = domain.ExecutionInteractionClosed, nil, domain.InteractionNativeClosed
			switch bad {
			case "duplicate-native":
				e = f.interactionEvent(4, domain.NewID(), "native-original")
			case "duplicate-id":
				e = f.interactionEvent(4, id, "different-native")
			case "wrong-turn":
				e.NativeTurnID = string(domain.NewID())
			case "wrong-native":
				e.Interaction.NativeRequestID.Text = "foreign"
			case "wrong-item":
				e.Interaction.NativeItemID = "foreign"
			case "wrong-kind":
				e.Interaction.Type = "approval"
			case "premature-terminal-closure":
				e.Interaction.Closure = domain.InteractionTurnEnded
			case "questions-on-close":
				e.Interaction.Questions = publicationQuestion()
			case "mixed-event":
				e.Outcome = domain.ExecutionSucceeded
			case "null-flag", "approval", "response", "duplicate-option", "oversize":
				e = f.interactionEvent(4, domain.NewID(), "new-native")
				if bad == "duplicate-option" {
					e.Interaction.Questions.Questions[0].Options[1] = e.Interaction.Questions.Questions[0].Options[0]
				}
				if bad == "oversize" {
					e.Interaction.Questions.Questions[0].Options[0].Description = strings.Repeat("x", domain.MaxMessageText)
					e.Interaction.Questions.Questions[0].Options[1].Description = strings.Repeat("x", domain.MaxMessageText)
				}
			}
			req := f.requestEvent(t, e)
			if bad == "null-flag" {
				req.EventJson = []byte(strings.Replace(string(req.EventJson), `"blocking":true`, `"blocking":null`, 1))
			}
			if bad == "approval" || bad == "response" {
				var wire map[string]any
				_ = json.Unmarshal(req.EventJson, &wire)
				field := "answers"
				if bad == "approval" {
					field = "decision"
				}
				wire["interaction"].(map[string]any)[field] = "accept"
				req.EventJson, _ = json.Marshal(wire)
			}
			if _, err := f.call(req); err == nil {
				t.Fatal("invalid interaction publication accepted")
			}
		})
	}
	e := f.interactionEvent(4, id, "native-original")
	e.Kind, e.Interaction.Questions, e.Interaction.Closure = domain.ExecutionInteractionClosed, nil, domain.InteractionNativeClosed
	receipt := f.publish(t, e)
	if ack, err := f.call(receipt); err != nil || !ack.Msg.Replayed {
		t.Fatalf("closure replay failed: %v", err)
	}
	if _, err := f.call(f.requestEvent(t, f.interactionEvent(5, domain.NewID(), "native-original"))); err == nil {
		t.Fatal("closed native identity was reused")
	}
	r, value := readPublishedInteraction(t, f, id)
	if r.Revision != 2 || value.FirstSequence != 3 || value.LastSequence != 4 || value.Questions.Questions[0].Text != "Original question" {
		t.Fatal("rejected event or replay changed original request")
	}
}

func TestExecutionQuestionLostAcknowledgmentReplaysExactArrival(t *testing.T) {
	f := newPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	client := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"), dropAt: 3}
	cfg.Client = client
	publisher, mapper := bindNativeMapper(t, f, cfg)
	id := domain.NewID()
	e := codex.Event{Kind: codex.InteractionRequestedEvent, ThreadID: f.thread, TurnID: f.turn, Correlated: true, ItemID: "question-tool", Interaction: &codex.Interaction{ID: id, Kind: codex.UserInputInteraction, NativeID: codex.NativeRequestID{Kind: codex.TextRequestID, Text: "native"}, Questions: &codex.QuestionRequest{Questions: []codex.Question{{ID: "question", Text: "Retained once"}}}}}
	if handled, err := mapper.PublishCore(context.Background(), e); !handled || err == nil {
		t.Fatal("lost question acknowledgment was not retained")
	}
	e.Interaction.Questions.Questions[0].Text = "Changed after attempted publication"
	if _, err := mapper.PublishCore(context.Background(), e); err == nil {
		t.Fatal("pending publication allowed replacement")
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
	r, value := readPublishedInteraction(t, f, id)
	if r.Revision != 1 || value.Questions.Questions[0].Text != "Retained once" || len(client.calls) != 4 || client.calls[2] != client.calls[3] {
		t.Fatal("question replay changed arrival or original payload")
	}
}

func TestExecutionQuestionLimitAndTerminalClosureAreAtomic(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	for i := 0; i < domain.MaxOpenInteractions; i++ {
		f.publish(t, f.interactionEvent(uint64(3+i), domain.NewID(), strconv.Itoa(i)))
	}
	sequence := uint64(3 + domain.MaxOpenInteractions)
	extra := domain.NewID()
	if _, err := f.call(f.requestEvent(t, f.interactionEvent(sequence, extra, "overflow"))); err == nil {
		t.Fatal("open question bound was not enforced")
	}
	if _, err := f.service.Store.Get(context.Background(), domain.InteractionKind, extra); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("rejected request persisted a partial interaction")
	}
	e := f.event(domain.ExecutionTurnFinished, sequence)
	e.Outcome = domain.ExecutionStopped
	f.publish(t, e)
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: store.MaxPage})
	if err != nil || len(rows) != domain.MaxOpenInteractions {
		t.Fatal("terminal closure lost requests")
	}
	for _, row := range rows {
		value, err := store.Decode[domain.ExecutionInteraction](row)
		if err != nil || value.Closure != domain.InteractionTurnEnded || value.LastSequence != sequence || row.Revision != 2 {
			t.Fatal("terminal did not atomically close unanswered requests")
		}
	}
}

func TestExecutionQuestionClosureRollsBackWithIncompleteTerminal(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	id, message := domain.NewID(), domain.NewID()
	f.publish(t, f.interactionEvent(3, id, "request"))
	e := f.event(domain.ExecutionMessageStarted, 4)
	e.Message = &domain.ExecutionMessageUpdate{ID: message, NativeID: "streaming-message", Role: domain.AssistantMessage}
	f.publish(t, e)
	e = f.event(domain.ExecutionTurnFinished, 5)
	e.Outcome = domain.ExecutionSucceeded
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("incomplete transcript permitted successful terminal")
	}
	r, value := readPublishedInteraction(t, f, id)
	if r.Revision != 1 || value.Closure != domain.InteractionOpen || value.LastSequence != 3 {
		t.Fatal("failed terminal retained partial interaction closure")
	}
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		ids, err := tx.OpenExecutionInteractions(f.input.ExecutionID)
		if len(ids) != 1 || ids[0] != id {
			t.Fatal("failed terminal changed open request index")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.Outcome = domain.ExecutionStopped
	f.publish(t, e)
	_, value = readPublishedInteraction(t, f, id)
	if value.Closure != domain.InteractionTurnEnded {
		t.Fatal("confirmed interruption did not close unanswered question")
	}
}

func TestExecutionQuestionCannotPublishAnUnclaimedNativeResponse(t *testing.T) {
	f := newPublicationFixture(t)
	_, mapper := bindNativeMapper(t, f, publicationWorkerConfig(t, f))
	id := domain.NewID()
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.InteractionRequestedEvent, ItemID: "question", Interaction: &codex.Interaction{ID: id, Kind: codex.UserInputInteraction, NativeID: codex.NativeRequestID{Kind: codex.TextRequestID, Text: "request"}, Questions: &codex.QuestionRequest{Questions: []codex.Question{{ID: "choice", Text: "Original"}}}}})
	e := codex.Event{Kind: codex.InteractionClosedEvent, ThreadID: f.thread, TurnID: f.turn, ItemID: "question", Correlated: true, InteractionState: &codex.InteractionStatus{ID: id, TurnID: f.turn, ItemID: "question", Closure: codex.InteractionNativeClosed, Delivery: codex.QuestionTransmitted, ResponseID: domain.NewID()}}
	if handled, err := mapper.PublishCore(context.Background(), e); !handled || domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("unclaimed response became authorized closure")
	}
	r, value := readPublishedInteraction(t, f, id)
	if r.Revision != 1 || value.Closure != domain.InteractionOpen || value.LastSequence != 3 {
		t.Fatal("unclaimed response changed retained lifecycle")
	}
}
