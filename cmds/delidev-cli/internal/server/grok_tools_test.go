package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type grokPublicRow struct {
	Kind   string                `json:"kind"`
	Method domain.GrokToolMethod `json:"method"`
	Params json.RawMessage       `json:"params"`
}

func publicGrokWriteFixture(t *testing.T, requestIDs ...domain.InteractionRequestID) (*publicationFixture, []grokPublicRow, domain.ID, uint64) {
	t.Helper()
	f := newGrokPublicationFixture(t, domain.ExecuteMode)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	raw, err := os.ReadFile("../harness/grok/testdata/file-tool-write.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), "019f6de0-a760-7000-8000-000000000071", string(f.thread)))
	var rows []grokPublicRow
	if json.Unmarshal(raw, &rows) != nil {
		t.Fatal("fixture shape")
	}
	sequence := uint64(2)
	id := domain.ID("")
	for i, row := range rows {
		var payload domain.GrokToolPayload
		if domain.Decode(row.Params, &payload) != nil {
			t.Fatal("original shape", i)
		}
		observation := domain.GrokToolEvent{Method: row.Method, Payload: payload}
		if row.Kind == "server-request" {
			native := domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: "original-public-file"}
			if len(requestIDs) != 0 {
				native = requestIDs[0]
			}
			observation.ArrivalID, observation.RequestID = domain.NewID(), &native
			observation.ProposalJSON = string(row.Params)
		}
		sequence++
		event := f.event(domain.ExecutionGrokToolObserved, sequence)
		message := domain.NewID()
		event.GrokTool = &domain.ExecutionGrokToolUpdate{ID: message, Observation: observation}
		receipt := f.publish(t, event)
		if response, err := f.call(receipt); err != nil || !response.Msg.Replayed {
			t.Fatal("lost original observation receipt", err)
		}
		if observation.RequestID != nil {
			digest, _ := grok.PublicRequestDigest(*observation.RequestID)
			sum := sha256.Sum256(row.Params)
			request := &domain.GrokInteractionRequest{Version: domain.GrokProtocolVersion, ObservationID: message, Event: observation, RequestDigest: digest, ProposalDigest: hex.EncodeToString(sum[:])}
			id = domain.NewID()
			sequence++
			event = f.event(domain.ExecutionInteractionRequested, sequence)
			event.Interaction = &domain.ExecutionInteractionUpdate{ID: id, Type: domain.NativeApprovalInteraction, NativeRequestID: *observation.RequestID, NativeItemID: *payload.Tool.ID, Grok: request}
			rejectChangedGrokProposal(t, f, event)
			rejectChangedGrokRequestRepresentation(t, f, event)
			f.publish(t, event)
			return f, rows[i+1:], id, sequence
		}
	}
	t.Fatal("no original Write request")
	return nil, nil, "", 0
}

func rejectChangedGrokProposal(t *testing.T, f *publicationFixture, event domain.ExecutionEvent) {
	t.Helper()
	raw, err := json.Marshal(event)
	var changed domain.ExecutionEvent
	if err != nil || domain.Decode(raw, &changed) != nil {
		t.Fatal("original Grok request fixture", err)
	}
	changed.Interaction.Grok.ProposalDigest = strings.Repeat("0", 64)
	before, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.call(f.requestEvent(t, changed)); err == nil {
		t.Fatal("changed proposal digest admitted before native response")
	}
	if _, err := f.service.Store.Get(context.Background(), domain.InteractionKind, changed.Interaction.ID); err == nil {
		t.Fatal("rejected proposal created an interaction")
	}
	after, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rejected proposal changed retained session progress", err)
	}
}

func TestGrokPublicLegacyProposalCannotAcquireReply(t *testing.T) {
	f, _, id, _ := publicGrokWriteFixture(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.legacy-grok-proposal", "original", func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.InteractionKind, id)
		if err != nil {
			return nil, err
		}
		value, err := store.Decode[domain.ExecutionInteraction](r)
		if err != nil {
			return nil, err
		}
		value.Grok.Event.ProposalJSON = ""
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
	})
	if err != nil {
		t.Fatal(err)
	}
	r, retained := readPublishedInteraction(t, f, id)
	input := domain.ApprovalResponseInput{Grok: &domain.GrokApprovalResponse{Decision: domain.GrokAllowOnce}}
	if _, err := acceptFixtureApproval(f, domain.NewID(), id, r.Revision, input); err == nil {
		t.Fatal("typed-only historical proposal acquired a new reply")
	}
	_, after := readPublishedInteraction(t, f, id)
	if !reflect.DeepEqual(retained, after) {
		t.Fatal("rejected historical reply changed retained evidence")
	}
}

func rejectChangedGrokRequestRepresentation(t *testing.T, f *publicationFixture, event domain.ExecutionEvent) {
	t.Helper()
	id := event.Interaction.NativeRequestID
	if id.Kind != domain.InteractionDecimalID {
		return
	}
	number, err := strconv.ParseInt(id.Decimal, 10, 64)
	if err != nil {
		return
	}
	legacy := domain.InteractionRequestID{Kind: domain.InteractionNumberID, Number: &number}
	originalKey, _ := id.Key()
	legacyKey, _ := legacy.Key()
	if originalKey != legacyKey {
		// The original -0 spelling already has a distinct namespace key.
		return
	}
	raw, err := json.Marshal(event)
	var changed domain.ExecutionEvent
	if err != nil || domain.Decode(raw, &changed) != nil {
		t.Fatal("original numeric Grok request", err)
	}
	changed.Interaction.NativeRequestID = legacy
	before, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.call(f.requestEvent(t, changed)); err == nil {
		t.Fatal("normalized request key substituted a different representation")
	}
	if _, err := f.service.Store.Get(context.Background(), domain.InteractionKind, changed.Interaction.ID); err == nil {
		t.Fatal("changed numeric representation created an interaction")
	}
	after, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("changed numeric representation advanced session progress", err)
	}
}
func TestGrokPublicWriteAcceptanceResultAndLostReceipts(t *testing.T) {
	for _, decision := range []domain.GrokFileDecision{domain.GrokAllowOnce, domain.GrokAllowSession, domain.GrokRejectOnce} {
		t.Run(string(decision), func(t *testing.T) {
			f, rows, id, sequence := publicGrokWriteFixture(t)
			response := domain.NewID()
			input := domain.ApprovalResponseInput{Grok: &domain.GrokApprovalResponse{Decision: decision}}
			if _, err := acceptFixtureApproval(f, response, id, 1, input); err != nil {
				t.Fatal("public decision", err)
			}
			meta := &pb.Mutation{Id: string(id), ExpectedRevision: 2, RequestId: string(domain.NewID())}
			if _, err := claimApproval(f, &pb.ClaimApprovalResponseRequest{Mutation: meta, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)}); err != nil {
				t.Fatal("original claim", err)
			}
			sequence++
			event := f.event(domain.ExecutionApprovalDeliveryObserved, sequence)
			_, value := readPublishedInteraction(t, f, id)
			event.ApprovalResponse = &domain.ExecutionApprovalResponseUpdate{InteractionID: id, ResponseID: response, ClaimID: domain.ID(meta.RequestId), NativeItemID: value.NativeItemID, Delivery: domain.ApprovalTransmitted}
			f.publish(t, event)
			for i, row := range rows {
				raw := row.Params
				if decision == domain.GrokRejectOnce && i == len(rows)-1 {
					raw, _ = os.ReadFile("../harness/grok/testdata/file-reject-failed.json")
					raw = []byte(strings.ReplaceAll(string(raw), "019f6de0-a760-7000-8000-000000000071", string(f.thread)))
				}
				var payload domain.GrokToolPayload
				if domain.Decode(raw, &payload) != nil {
					t.Fatal("result fixture")
				}
				sequence++
				event = f.event(domain.ExecutionGrokToolObserved, sequence)
				event.GrokTool = &domain.ExecutionGrokToolUpdate{ID: domain.NewID(), Observation: domain.GrokToolEvent{Method: row.Method, Payload: payload}}
				request := f.publish(t, event)
				if _, err := f.call(request); err != nil {
					t.Fatal("lost result receipt", err)
				}
			}
			_, value = readPublishedInteraction(t, f, id)
			if value.Closure != domain.InteractionNativeClosed || value.ApprovalResponse.State != domain.ApprovalResponseAccepted || value.ApprovalResponse.Acceptance.Evidence != domain.NativeGrokApprovalResult {
				t.Fatal("result did not independently accept original response")
			}
			if replay, err := acceptFixtureApproval(f, response, id, 1, input); err != nil || !replay.Replayed {
				t.Fatal("response receipt lost", err)
			}

			sequence++
			usage := grokServerResponse(f, sequence, 1)
			f.publish(t, usage)
			sequence++
			event = f.event(domain.ExecutionTurnFinished, sequence)
			terminal := &domain.GrokToolsTerminal{Kind: domain.GrokClosedFirstTools, NativeEventID: string(f.thread) + "-100", TimestampMS: "1790537697000", ElapsedMS: "3", Model: f.input.Configuration.NativeModel, Reason: domain.GrokToolsEndTurn, Counts: usage.GrokUsage.Counts, TotalTokens: "16", ModelCalls: "1", APIDurationMS: "2", Turns: "1"}
			if decision == domain.GrokRejectOnce {
				terminal.Reason = domain.GrokToolsPermissionRejected
				terminal.Rejection = &domain.GrokFileRejection{Tool: "write", Reason: "Original fixture rejection"}
			}
			event.GrokToolsTerminal, event.Outcome = terminal, terminal.Outcome()
			bad := event
			altered := *terminal
			altered.Counts.Input = "12"
			bad.GrokToolsTerminal = &altered
			if _, err := f.call(f.requestEvent(t, bad)); err == nil {
				t.Fatal("foreign aggregate terminal accepted")
			}
			receipt := f.publish(t, event)
			if replay, err := f.call(receipt); err != nil || !replay.Msg.Replayed {
				t.Fatal("terminal receipt lost", err)
			}
			sessionRecord, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			session, err := store.Decode[domain.Session](sessionRecord)
			if err != nil || session.Execution.GrokToolsTerminal == nil || session.Execution.CleanupVerified {
				t.Fatal("native terminal fabricated workspace cleanup", err)
			}
			f.reportCompletion(t, domain.ExecutionCompletion{Version: 1, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: domain.NativeIdentity(f.thread), NativeTurnID: domain.NativeIdentity(f.turn), LastSequence: sequence, Outcome: terminal.Outcome(), CleanupVerified: true})
			sessionRecord, _ = f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			session, err = store.Decode[domain.Session](sessionRecord)
			if err != nil || !session.Execution.CleanupVerified || session.Execution.Outcome != terminal.Outcome() || session.Dispatch != domain.DispatchPaused {
				t.Fatal("tools cleanup/report borrowed continuation or changed native outcome", err)
			}
		})
	}
}
func TestGrokPublicWriteCompetingWrongKindStopAndRevocation(t *testing.T) {
	for _, scenario := range []string{"competing", "wrong-kind", "stale", "stop", "revocation"} {
		t.Run(scenario, func(t *testing.T) {
			f, _, id, _ := publicGrokWriteFixture(t)
			input := domain.ApprovalResponseInput{Grok: &domain.GrokApprovalResponse{Decision: domain.GrokAllowOnce}}
			if scenario == "competing" {
				var wg sync.WaitGroup
				results := make(chan error, 4)
				for range 4 {
					wg.Go(func() { _, err := acceptFixtureApproval(f, domain.NewID(), id, 1, input); results <- err })
				}
				wg.Wait()
				close(results)
				accepted := 0
				for err := range results {
					if err == nil {
						accepted++
					} else if domain.SafeError(err).Code != domain.Conflict {
						t.Fatal(err)
					}
				}
				if accepted != 1 {
					t.Fatal("competing answers", accepted)
				}
				return
			}
			revision := uint64(1)
			if scenario == "wrong-kind" {
				input.Grok = &domain.GrokApprovalResponse{Outcome: domain.GrokPlanApproved}
			}
			if scenario == "stale" {
				revision++
			}
			if scenario == "stop" || scenario == "revocation" {
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok-response-race", scenario, func(tx *store.Tx) (any, error) {
					if scenario == "stop" {
						return nil, tx.RequestJobCancellation(f.job)
					}
					return nil, tx.SetWorkerInstance(f.input.MachineID, domain.NewID(), time.Now().UTC())
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := acceptFixtureApproval(f, domain.NewID(), id, revision, input)
			if scenario == "revocation" {
				if err != nil {
					t.Fatal("instance metadata blocked the authenticated control", err)
				}
				return
			}
			if err == nil {
				t.Fatal(fmt.Sprintf("%s granted a response", scenario))
			}
			_, value := readPublishedInteraction(t, f, id)
			if value.ApprovalResponse != nil {
				t.Fatal("rejected response persisted before side effects")
			}
		})
	}
}

// Replay the original native Plan fixture through real SQLite transactions and
// authenticated claims. No synthetic Plan artifact or common approval is used.
func TestGrokPublicOriginalPlanQuestionsRevisionsAndTransitions(t *testing.T) {
	for _, scenario := range []string{"approved", "cancelled", "abandoned", "revised"} {
		t.Run(scenario, func(t *testing.T) {
			f := newGrokPublicationFixture(t, domain.ExecuteMode)
			f.registerGrant(t)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			raw, err := os.ReadFile("../harness/grok/testdata/plan-" + scenario + ".json")
			if err != nil {
				t.Fatal(err)
			}
			raw = []byte(strings.ReplaceAll(string(raw), "019f6de0-a760-7000-8000-000000000071", string(f.thread)))
			var rows []grokPublicRow
			if json.Unmarshal(raw, &rows) != nil {
				t.Fatal("Plan fixture")
			}
			sequence := uint64(2)
			var origin domain.GrokPlanOrigin
			var writeID string
			origins := map[string]domain.GrokPlanOrigin{}
			interactions := []domain.ID{}
			for i, row := range rows {
				var payload domain.GrokToolPayload
				if domain.Decode(row.Params, &payload) != nil {
					t.Fatal(i, "original shape")
				}
				v := domain.GrokToolEvent{Method: row.Method, Payload: payload}
				u := payload.Update
				if u != nil && u.Kind != nil && *u.Kind == "tool_call" && u.Name == nil && u.Title != nil && *u.Title == "write" && u.ID != nil {
					origins[*u.ID] = origin
				}
				if u != nil && u.ID != nil {
					if p, ok := origins[*u.ID]; ok {
						copy := p
						v.PlanOrigin = &copy
					}
				}
				if row.Kind == "server-request" {
					id := domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: fmt.Sprintf("original-plan-request-%d", i)}
					v.RequestID = &id
					v.ArrivalID = domain.NewID()
					v.ProposalJSON = string(row.Params)
				}
				sequence++
				event := f.event(domain.ExecutionGrokToolObserved, sequence)
				message := domain.NewID()
				event.GrokTool = &domain.ExecutionGrokToolUpdate{ID: message, Observation: v}
				f.publish(t, event)
				if u != nil && u.Output != nil {
					if u.Output.Entered != nil {
						origin = domain.GrokPlanOrigin{EntryToolID: *u.ID, EntryEventID: payload.Meta.Event}
					}
					if u.Output.Write != nil {
						origin.Revision++
						writeID = *u.ID
					}
				}
				if v.RequestID == nil {
					continue
				}
				digest, _ := grok.PublicRequestDigest(*v.RequestID)
				sum := sha256.Sum256(row.Params)
				request := &domain.GrokInteractionRequest{Version: domain.GrokProtocolVersion, ObservationID: message, Event: v, RequestDigest: digest, ProposalDigest: hex.EncodeToString(sum[:])}
				kind := domain.UserQuestionInteraction
				if row.Method == domain.GrokPlanMethod {
					kind = domain.NativeApprovalInteraction
					encoded, _ := json.Marshal(*payload.PlanContent)
					content := sha256.Sum256(encoded)
					request.Plan = &domain.GrokPlanProposal{Origin: origin, WriteToolID: writeID, ContentDigest: hex.EncodeToString(content[:])}
				}
				id, response := domain.NewID(), domain.NewID()
				interactions = append(interactions, id)
				sequence++
				event = f.event(domain.ExecutionInteractionRequested, sequence)
				event.Interaction = &domain.ExecutionInteractionUpdate{ID: id, Type: kind, NativeRequestID: *v.RequestID, NativeItemID: *payload.ToolID, Grok: request}
				rejectChangedGrokProposal(t, f, event)
				f.publish(t, event)
				meta := &pb.Mutation{Id: string(id), ExpectedRevision: 2, RequestId: string(domain.NewID())}
				if kind == domain.UserQuestionInteraction {
					input := domain.QuestionResponseInput{Grok: &domain.GrokQuestionResponse{Outcome: domain.GrokQuestionAccepted, Answers: map[string]string{"Which original mixed option?": "Blue"}}}
					if _, err := acceptFixtureResponse(f, response, id, 1, input); err != nil {
						t.Fatal("public original answer", err)
					}
					if _, err := claimQuestion(f, &pb.ClaimQuestionResponseRequest{Mutation: meta, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)}); err != nil {
						t.Fatal("original answer claim", err)
					}
					sequence++
					event = f.event(domain.ExecutionQuestionDeliveryObserved, sequence)
					event.QuestionResponse = &domain.ExecutionQuestionResponseUpdate{InteractionID: id, ResponseID: response, ClaimID: domain.ID(meta.RequestId), NativeItemID: *payload.ToolID, Delivery: domain.QuestionTransmitted}
				} else {
					outcome := domain.GrokPlanDecision(scenario)
					if scenario == "revised" {
						outcome = domain.GrokPlanCancelled
						if origin.Revision == 2 {
							outcome = domain.GrokPlanApproved
						}
					}
					input := domain.ApprovalResponseInput{Grok: &domain.GrokApprovalResponse{Outcome: outcome}}
					// An earlier artifact revision cannot substitute for the retained proposal.
					if origin.Revision == 2 {
						altered := *request
						altered.Plan = &domain.GrokPlanProposal{Origin: origin, WriteToolID: writeID, ContentDigest: request.Plan.ContentDigest}
						altered.Plan.Origin.Revision = 1
						bad := f.event(domain.ExecutionInteractionRequested, sequence+1)
						bad.Interaction = &domain.ExecutionInteractionUpdate{ID: domain.NewID(), Type: kind, NativeRequestID: *v.RequestID, NativeItemID: *payload.ToolID, Grok: &altered}
						if _, err := f.call(f.requestEvent(t, bad)); err == nil {
							t.Fatal("stale artifact accepted")
						}
					}
					if _, err := acceptFixtureApproval(f, response, id, 1, input); err != nil {
						t.Fatal("public native Plan reply", err)
					}
					if _, err := claimApproval(f, &pb.ClaimApprovalResponseRequest{Mutation: meta, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)}); err != nil {
						t.Fatal("original Plan claim", err)
					}
					sequence++
					event = f.event(domain.ExecutionApprovalDeliveryObserved, sequence)
					event.ApprovalResponse = &domain.ExecutionApprovalResponseUpdate{InteractionID: id, ResponseID: response, ClaimID: domain.ID(meta.RequestId), NativeItemID: *payload.ToolID, Delivery: domain.ApprovalTransmitted}
				}
				f.publish(t, event)
				_, waiting := readPublishedInteraction(t, f, id)
				if waiting.Closure != domain.InteractionOpen {
					t.Fatal("delivery fabricated native acceptance")
				}
			}
			for _, id := range interactions {
				_, v := readPublishedInteraction(t, f, id)
				if v.Closure != domain.InteractionNativeClosed {
					t.Fatal("original tool result did not close interaction")
				}
			}
			if scenario == "revised" && origin.Revision != 2 {
				t.Fatal("artifact revision lost")
			}
		})
	}
}

func TestGrokInitialPlanTerminalDoesNotCreateCommonPlanGate(t *testing.T) {
	f := newGrokPublicationFixture(t, domain.PlanMode)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	usage := grokServerResponse(f, 3, 1)
	f.publish(t, usage)
	event := f.event(domain.ExecutionTurnFinished, 4)
	event.Outcome = domain.ExecutionSucceeded
	event.GrokToolsTerminal = &domain.GrokToolsTerminal{Kind: domain.GrokClosedFirstTools, NativeEventID: string(f.thread) + "-100", TimestampMS: "1", ElapsedMS: "3", Model: f.input.Configuration.NativeModel, Reason: domain.GrokToolsEndTurn, Counts: usage.GrokUsage.Counts, TotalTokens: "16", ModelCalls: "1", APIDurationMS: "2", Turns: "1"}
	f.publish(t, event)
	f.reportCompletion(t, domain.ExecutionCompletion{Version: 1, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: domain.NativeIdentity(f.thread), NativeTurnID: domain.NativeIdentity(f.turn), LastSequence: 4, Outcome: domain.ExecutionSucceeded, CleanupVerified: true})
	record, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	session, decodeErr := store.Decode[domain.Session](record)
	if err != nil || decodeErr != nil || session.Execution.GrokToolsTerminal == nil || session.Dispatch != domain.DispatchPaused {
		t.Fatal("original Plan terminal/report was replaced with a common gate", err, decodeErr)
	}
}

func TestGrokPublicRememberedWriteRetainsOriginalCompletedApproval(t *testing.T) {
	f, remainder, id, sequence := publicGrokWriteFixture(t)
	response := domain.NewID()
	input := domain.ApprovalResponseInput{Grok: &domain.GrokApprovalResponse{Decision: domain.GrokAllowSession}}
	if _, err := acceptFixtureApproval(f, response, id, 1, input); err != nil {
		t.Fatal(err)
	}
	meta := &pb.Mutation{Id: string(id), ExpectedRevision: 2, RequestId: string(domain.NewID())}
	if _, err := claimApproval(f, &pb.ClaimApprovalResponseRequest{Mutation: meta, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)}); err != nil {
		t.Fatal(err)
	}
	_, original := readPublishedInteraction(t, f, id)
	sequence++
	delivery := f.event(domain.ExecutionApprovalDeliveryObserved, sequence)
	delivery.ApprovalResponse = &domain.ExecutionApprovalResponseUpdate{InteractionID: id, ResponseID: response, ClaimID: domain.ID(meta.RequestId), NativeItemID: original.NativeItemID, Delivery: domain.ApprovalTransmitted}
	f.publish(t, delivery)
	for _, row := range remainder {
		var payload domain.GrokToolPayload
		if domain.Decode(row.Params, &payload) != nil {
			t.Fatal("original completed Write fixture")
		}
		sequence++
		event := f.event(domain.ExecutionGrokToolObserved, sequence)
		event.GrokTool = &domain.ExecutionGrokToolUpdate{ID: domain.NewID(), Observation: domain.GrokToolEvent{Method: row.Method, Payload: payload}}
		f.publish(t, event)
	}
	_, completed := readPublishedInteraction(t, f, id)
	if completed.ApprovalResponse.State != domain.ApprovalResponseAccepted {
		t.Fatal("remembered edits preceded original result acceptance")
	}
	raw, err := os.ReadFile("../harness/grok/testdata/file-tool-write.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(strings.ReplaceAll(string(raw), "019f6de0-a760-7000-8000-000000000071", string(f.thread)), original.NativeItemID, "later-remembered-write"))
	var rows []grokPublicRow
	if json.Unmarshal(raw, &rows) != nil {
		t.Fatal("second native Write fixture")
	}
	inherited := domain.ID("")
	for _, row := range rows {
		if row.Kind == "server-request" {
			continue // Native session permission suppresses another explicit request.
		}
		var payload domain.GrokToolPayload
		if domain.Decode(row.Params, &payload) != nil {
			t.Fatal("second original Write shape")
		}
		if payload.Meta != nil {
			index, err := domain.GrokEventIndex(payload.Meta.Event, string(f.thread))
			if err != nil {
				t.Fatal(err)
			}
			payload.Meta.Event = fmt.Sprintf("%s-%d", f.thread, index+10)
		}
		if payload.Update != nil && payload.Update.Kind != nil && *payload.Update.Kind == "tool_call" {
			inherited = original.Grok.Event.ArrivalID
		}
		sequence++
		event := f.event(domain.ExecutionGrokToolObserved, sequence)
		event.GrokTool = &domain.ExecutionGrokToolUpdate{ID: domain.NewID(), Observation: domain.GrokToolEvent{Method: row.Method, Payload: payload, InheritedPermission: inherited}}
		if inherited != "" {
			wrong := event
			copy := *event.GrokTool
			copy.Observation.InheritedPermission = domain.NewID()
			wrong.GrokTool = &copy
			if _, err := f.call(f.requestEvent(t, wrong)); err == nil {
				t.Fatal("foreign remembered approval accepted")
			}
		}
		f.publish(t, event)
	}
	record, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	session, decodeErr := store.Decode[domain.Session](record)
	if err != nil || decodeErr != nil || session.Execution.UnconfirmedResponses != 0 {
		t.Fatal("later remembered Write created another response", err, decodeErr)
	}
}

func TestGrokPublicNumericApprovalPreservesRequestAndClaim(t *testing.T) {
	for _, spelling := range []string{"0", "42", "-42", "-0", "9223372036854775808", "-9223372036854775809"} {
		t.Run(spelling, func(t *testing.T) {
			identity := domain.InteractionRequestID{Kind: domain.InteractionDecimalID, Decimal: spelling}
			f, _, id, _ := publicGrokWriteFixture(t, identity)
			_, original := readPublishedInteraction(t, f, id)
			if original.NativeRequestID != identity || *original.Grok.Event.RequestID != identity {
				t.Fatal("public request lost original decimal spelling")
			}
			response := domain.NewID()
			input := domain.ApprovalResponseInput{Grok: &domain.GrokApprovalResponse{Decision: domain.GrokAllowOnce}}
			if _, err := acceptFixtureApproval(f, response, id, 1, input); err != nil {
				t.Fatal("numeric approval", err)
			}
			meta := &pb.Mutation{Id: string(id), ExpectedRevision: 2, RequestId: string(domain.NewID())}
			claim := &pb.ClaimApprovalResponseRequest{Mutation: meta, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)}
			if _, err := claimApproval(f, claim); err != nil {
				t.Fatal("numeric native response claim", err)
			}
			if replay, err := acceptFixtureApproval(f, response, id, 1, input); err != nil || !replay.Replayed {
				t.Fatal("numeric response receipt replay", err)
			}
		})
	}
}
