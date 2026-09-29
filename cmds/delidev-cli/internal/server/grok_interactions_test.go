package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type grokPublicServerFixture struct {
	f        *publicationFixture
	sequence uint64
	native   uint64
}

func grokPublicServer(t *testing.T, mode domain.SessionMode) *grokPublicServerFixture {
	f := newGrokPublicationFixture(t, mode)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	return &grokPublicServerFixture{f: f, sequence: 2}
}
func (g *grokPublicServerFixture) publish(t *testing.T, event domain.ExecutionEvent) *pb.PublishExecutionRequest {
	g.sequence++
	event.Version = 1
	event.ExecutionID = g.f.input.ExecutionID
	event.Sequence = g.sequence
	event.NativeThreadID = string(g.f.thread)
	event.NativeTurnID = string(g.f.turn)
	return g.f.publish(t, event)
}
func (g *grokPublicServerFixture) tool(t *testing.T, id domain.ID, native string, name domain.GrokToolName, phase domain.GrokToolPhase, content string, origin *domain.GrokPlanFileOrigin, policy domain.ID, questions []domain.GrokQuestion) domain.ExecutionEvent {
	g.native++
	snapshot := domain.ToolSnapshot{Kind: domain.GrokNativeTool, Status: domain.ToolPending, Grok: &domain.GrokToolObservation{Name: name, Phase: phase, Content: content, PlanFile: origin, InheritedPermission: policy, Questions: questions, Metadata: &domain.GrokToolMetadata{EventID: string(g.f.thread) + "-" + strconv.FormatUint(g.native, 10), ContextTokens: "9007199254740993", TimestampMS: "1", StreamStartMS: "1", TurnStartMS: "1"}}}
	kind := domain.ExecutionToolUpdated
	if name == domain.GrokWrite || name == domain.GrokRead {
		snapshot.Grok.Path = "/fixture/file"
	}
	if origin != nil {
		snapshot.Grok.Path = ""
	}
	if phase == domain.GrokArguments {
		snapshot.Grok.Metadata = nil
		snapshot.Grok.Content = ""
		snapshot.Grok.Path = ""
		snapshot.Grok.PlanFile = nil
		snapshot.Grok.InheritedPermission = ""
		snapshot.Grok.Questions = nil
		n := name
		snapshot.Grok.Arguments = &domain.GrokArgumentChunk{ID: &native, Name: &n, Index: 0, Text: "original arguments"}
		kind = domain.ExecutionToolStarted
	}
	if phase == domain.GrokCompleted || phase == domain.GrokFailed {
		kind = domain.ExecutionToolCompleted
		snapshot.Status = domain.ToolCompleted
		if phase == domain.GrokFailed {
			snapshot.Status = domain.ToolFailed
		}
		output := "Original native tool result"
		snapshot.Grok.Output = &output
	}
	return domain.ExecutionEvent{Kind: kind, Tool: &domain.ExecutionToolUpdate{ID: id, NativeID: native, Snapshot: &snapshot}}
}
func (g *grokPublicServerFixture) described(t *testing.T, native string, name domain.GrokToolName, content string, origin *domain.GrokPlanFileOrigin, policy domain.ID, questions []domain.GrokQuestion) domain.ID {
	id := domain.NewID()
	for _, phase := range []domain.GrokToolPhase{domain.GrokArguments, domain.GrokDeclared, domain.GrokDescribed} {
		g.publish(t, g.tool(t, id, native, name, phase, content, origin, policy, questions))
	}
	return id
}
func (g *grokPublicServerFixture) request(t *testing.T, native string, request *domain.GrokInteractionRequest) domain.ID {
	request.Version = domain.GrokProtocolVersion
	request.ArrivalID = domain.NewID()
	request.RequestDigest = strings.Repeat("ab", 32)
	request.ProposalDigest = strings.Repeat("cd", 32)
	kind := domain.NativeApprovalInteraction
	if request.Kind == domain.GrokQuestionInteraction {
		kind = domain.UserQuestionInteraction
	}
	g.publish(t, domain.ExecutionEvent{Kind: domain.ExecutionInteractionRequested, Interaction: &domain.ExecutionInteractionUpdate{ID: request.ArrivalID, NativeItemID: native, NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: string(request.ArrivalID)}, Type: kind, Grok: request}})
	return request.ArrivalID
}
func (g *grokPublicServerFixture) finishApproval(t *testing.T, id domain.ID, native string, decision domain.GrokDecision, completed func() domain.ExecutionEvent, before ...func()) {
	f := g.f
	response := domain.NewID()
	if _, err := acceptFixtureApproval(f, response, id, 1, domain.ApprovalResponseInput{Grok: &domain.GrokApprovalResponse{Decision: decision}}); err != nil {
		t.Fatal(err)
	}
	claim := &pb.ClaimApprovalResponseRequest{Mutation: &pb.Mutation{Id: string(id), ExpectedRevision: 2, RequestId: string(domain.NewID())}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)}
	if _, err := claimApproval(f, claim); err != nil {
		t.Fatal(err)
	}
	delivery := domain.ExecutionEvent{Kind: domain.ExecutionApprovalDeliveryObserved, ApprovalResponse: &domain.ExecutionApprovalResponseUpdate{InteractionID: id, ResponseID: response, ClaimID: domain.ID(claim.Mutation.RequestId), NativeItemID: native, Delivery: domain.ApprovalTransmitted}}
	receipt := g.publish(t, delivery)
	if r, err := f.call(receipt); err != nil || !r.Msg.Replayed {
		t.Fatal("lost delivery receipt", err)
	}
	// Delivery has no semantic-acceptance authority before original tool output.
	premature := f.event(domain.ExecutionApprovalAccepted, g.sequence+1)
	premature.ApprovalAcceptance = &domain.ExecutionApprovalAcceptanceUpdate{InteractionID: id, ResponseID: response, ClaimID: domain.ID(claim.Mutation.RequestId), NativeItemID: native, Evidence: domain.NativeGrokApprovalOutput}
	if _, err := f.call(f.requestEvent(t, premature)); err == nil {
		t.Fatal("pipe delivery fabricated native acceptance")
	}
	for _, callback := range before {
		callback()
	}
	g.publish(t, completed())
	g.publish(t, domain.ExecutionEvent{Kind: domain.ExecutionApprovalAccepted, ApprovalAcceptance: premature.ApprovalAcceptance})
	g.publish(t, domain.ExecutionEvent{Kind: domain.ExecutionInteractionClosed, Interaction: &domain.ExecutionInteractionUpdate{ID: id, NativeItemID: native, NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: string(id)}, Type: domain.NativeApprovalInteraction, Closure: domain.InteractionNativeClosed}})
	_, v := readPublishedInteraction(t, f, id)
	if v.ApprovalResponse.State != domain.ApprovalResponseAccepted || v.Closure != domain.InteractionNativeClosed {
		t.Fatal("native acceptance lost original response")
	}
}
func TestGrokPublicFileDecisionsAndRememberedPolicy(t *testing.T) {
	for _, decision := range []domain.GrokDecision{domain.GrokAllowOnce, domain.GrokAllowEditsSession, domain.GrokRejectOnce} {
		t.Run(string(decision), func(t *testing.T) {
			g := grokPublicServer(t, domain.ExecuteMode)
			// Read is a native automatic permission cycle, with no fabricated response.
			read := g.described(t, "original-read", domain.GrokRead, "", nil, "", nil)
			for _, stage := range []domain.GrokNativeInteractionStage{domain.GrokPermissionPending, domain.GrokPermissionResolved} {
				g.publish(t, domain.ExecutionEvent{Kind: domain.ExecutionProgressObserved, Progress: &domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.GrokInteractionProgress, GrokInteraction: &domain.GrokNativeInteractionObservation{ToolID: "original-read", Stage: stage}}}})
			}
			g.publish(t, g.tool(t, read, "original-read", domain.GrokRead, domain.GrokCompleted, "", nil, "", nil))
			write := g.described(t, "original-write", domain.GrokWrite, "Exact original contents", nil, "", nil)
			id := g.request(t, "original-write", &domain.GrokInteractionRequest{Kind: domain.GrokFilePermission, Mode: domain.GrokDefaultMode, ToolName: domain.GrokWrite, Path: "/fixture/file", Content: "Exact original contents"})
			phase := domain.GrokCompleted
			if decision == domain.GrokRejectOnce {
				phase = domain.GrokFailed
			}
			g.finishApproval(t, id, "original-write", decision, func() domain.ExecutionEvent {
				return g.tool(t, write, "original-write", domain.GrokWrite, phase, "Exact original contents", nil, "", nil)
			})
			if decision == domain.GrokAllowEditsSession {
				later := g.described(t, "later-write", domain.GrokWrite, "Later original contents", nil, id, nil)
				g.publish(t, g.tool(t, later, "later-write", domain.GrokWrite, domain.GrokCompleted, "Later original contents", nil, id, nil))
			}
		})
	}
}
func TestGrokPublicPlanRevisionsFollowNativeMode(t *testing.T) {
	g := grokPublicServer(t, domain.ExecuteMode)
	entry := g.described(t, "original-entry", domain.GrokEnterPlan, "", nil, "", nil)
	mode := func(mode domain.GrokMode, tool string) {
		g.native++
		g.publish(t, domain.ExecutionEvent{Kind: domain.ExecutionProgressObserved, Progress: &domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.GrokModeProgress, GrokMode: &domain.GrokModeObservation{ToolID: tool, Mode: mode, EventID: string(g.f.thread) + "-" + strconv.FormatUint(g.native, 10), TimestampMS: "1"}}}})
	}
	mode(domain.GrokPlanMode, "original-entry")
	completed := g.tool(t, entry, "original-entry", domain.GrokEnterPlan, domain.GrokCompleted, "", nil, "", nil)
	entryEvent := completed.Tool.Snapshot.Grok.Metadata.EventID
	g.publish(t, completed)
	for revision := uint64(1); revision <= 2; revision++ {
		content := "Original Plan revision " + strconv.FormatUint(revision, 10)
		writeNative := "plan-write-" + strconv.FormatUint(revision, 10)
		origin := &domain.GrokPlanFileOrigin{EntryToolID: "original-entry", EntryEventID: entryEvent, Revision: revision - 1}
		write := g.described(t, writeNative, domain.GrokWrite, content, origin, "", nil)
		g.publish(t, g.tool(t, write, writeNative, domain.GrokWrite, domain.GrokCompleted, content, origin, "", nil))
		plan := &domain.GrokPlanRevision{EntryToolID: origin.EntryToolID, EntryEventID: entryEvent, Revision: revision, WriteToolID: writeNative, Content: content, ContentDigest: domain.GrokValueDigest(content)}
		artifact := domain.NewID()
		snapshot := &domain.ArtifactSnapshot{Kind: domain.PlanArtifact, Text: content, Grok: plan}
		for _, kind := range []domain.ExecutionEventKind{domain.ExecutionArtifactStarted, domain.ExecutionArtifactCompleted} {
			g.publish(t, domain.ExecutionEvent{Kind: kind, Artifact: &domain.ExecutionArtifactUpdate{ID: artifact, NativeID: entryEvent + "/revision/" + strconv.FormatUint(revision, 10), Snapshot: snapshot}})
		}
		exitNative := "exit-" + strconv.FormatUint(revision, 10)
		exit := g.described(t, exitNative, domain.GrokExitPlan, "", nil, "", nil)
		id := g.request(t, exitNative, &domain.GrokInteractionRequest{Kind: domain.GrokPlanApproval, Mode: domain.GrokPlanMode, ToolName: domain.GrokExitPlan, Plan: plan})
		decision := domain.GrokPlanCancelled
		if revision == 2 {
			decision = domain.GrokPlanApproved

		}
		g.finishApproval(t, id, exitNative, decision, func() domain.ExecutionEvent {
			return g.tool(t, exit, exitNative, domain.GrokExitPlan, domain.GrokCompleted, "", nil, "", nil)
		}, func() {
			if decision == domain.GrokPlanApproved {
				mode(domain.GrokDefaultMode, exitNative)
			}
		})
	}
	sr, _ := g.f.service.Store.Get(context.Background(), domain.SessionKind, g.f.input.SessionID)
	session, _ := store.Decode[domain.Session](sr)
	if session.Execution.Observed.GrokMode != domain.GrokDefaultMode || session.Execution.GrokMode.Mode != domain.GrokDefaultMode || session.Execution.UnconfirmedResponses != 0 {
		t.Fatal("native transitions changed initial settings or common approval state")
	}
}
func TestGrokPublicQuestionsRejectCompetingStaleAndStoppedAnswers(t *testing.T) {
	for _, failure := range []string{"competing", "cancel", "revoke", "wrong-kind", "foreign-key", "stale"} {
		t.Run(failure, func(t *testing.T) {
			g := grokPublicServer(t, domain.ExecuteMode)
			f := g.f
			questions := []domain.GrokQuestion{{Question: "Original, exact question?", Options: []domain.QuestionOption{{Label: "One", Description: "Original"}}}}
			g.described(t, "question", domain.GrokAsk, "", nil, "", questions)
			id := g.request(t, "question", &domain.GrokInteractionRequest{Kind: domain.GrokQuestionInteraction, Mode: domain.GrokDefaultMode, ToolName: domain.GrokAsk, Questions: questions})
			input := domain.QuestionResponseInput{Grok: &domain.GrokQuestionResponse{Outcome: domain.GrokQuestionAccepted, Answers: map[string]string{questions[0].Question: "Exact, free answer\n🙂"}}}
			revision := uint64(1)
			if failure == "cancel" || failure == "revoke" {
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.revoke", failure, func(tx *store.Tx) (any, error) {
					if failure == "cancel" {
						return nil, tx.RequestJobCancellation(f.job)
					}
					return nil, tx.SetWorkerInstance(f.input.MachineID, domain.NewID(), time.Now().UTC())
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if failure == "foreign-key" {
				input.Grok.Answers = map[string]string{"foreign": "One"}
			}
			if failure == "wrong-kind" {
				if _, err := acceptFixtureApproval(f, domain.NewID(), id, revision, domain.ApprovalResponseInput{Grok: &domain.GrokApprovalResponse{Decision: domain.GrokAllowOnce}}); err == nil {
					t.Fatal("wrong response family accepted")
				}
				return
			}
			if failure == "stale" {
				revision = 2
			}
			if failure != "competing" {
				if _, err := acceptFixtureResponse(f, domain.NewID(), id, revision, input); err == nil {
					t.Fatal("stale/foreign/stopped answer acquired native send")
				}
				return
			}
			var group sync.WaitGroup
			results := make(chan domain.ID, 8)
			for range 8 {
				group.Go(func() {
					response := domain.NewID()
					if _, err := acceptFixtureResponse(f, response, id, revision, input); err == nil {
						results <- response
					}
				})
			}
			group.Wait()
			close(results)
			count := 0
			var accepted domain.ID
			for id := range results {
				count++
				accepted = id
			}
			if count != 1 {
				t.Fatal("competing answers gained multiple owners", count)
			}
			if result, err := acceptFixtureResponse(f, accepted, id, revision, input); err != nil || !result.Replayed {
				t.Fatal("lost exact response receipt", err)
			}
			_, value := readPublishedInteraction(t, f, id)
			if !reflect.DeepEqual(value.Response.Input, input) {
				t.Fatal("exact answer changed")
			}
		})
	}
}
func TestGrokPublicTerminalKeepsCleanupAndContinuationSeparate(t *testing.T) {
	f, event := grokServerTerminalFixture(t)
	inputDigest, _ := grok.TextInputClaimDigest(f.thread, f.input.Input.Prompt)
	event.GrokTerminal = nil
	event.GrokPublicTerminal = &domain.GrokPublicTerminal{InputRequestID: f.input.TurnRequestID, InputDigest: inputDigest, OutputDigest: domain.GrokDigest([]byte(grokServerText(f).GrokText.Text)), NativeFactsDigest: strings.Repeat("ab", 32), TextChunks: 1, NativeEventID: string(f.thread) + "-11", TimestampMS: "1", Model: f.input.Configuration.NativeModel, Mode: domain.GrokDefaultMode, Outcome: domain.ExecutionSucceeded, Responses: "1", Counts: grokServerResponse(f, 4, 1).GrokUsage.Counts, TotalTokens: "16", APIDurationMS: "2", Turns: "1", Idle: true, CleanupJoined: true}
	f.publish(t, event)
	r, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	s, _ := store.Decode[domain.Session](r)
	if s.Execution.CleanupVerified {
		t.Fatal("native terminal substituted workspace report")
	}
	f.reportCompletion(t, domain.ExecutionCompletion{Version: 1, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: domain.NativeIdentity(f.thread), NativeTurnID: domain.NativeIdentity(f.turn), LastSequence: 5, Outcome: domain.ExecutionSucceeded, CleanupVerified: true})
	r, _ = f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	s, _ = store.Decode[domain.Session](r)
	if !s.Execution.CleanupVerified || s.Dispatch != domain.DispatchPaused || s.NextExecutionIntent != "" {
		t.Fatal("rich completion granted continuation")
	}
	raw, _ := json.Marshal(s.Execution)
	if strings.Contains(string(raw), f.input.Input.Prompt) {
		t.Fatal("terminal metadata retained input text")
	}
}

func TestGrokPublicQuestionAcceptanceRequiresOriginalOutput(t *testing.T) {
	for _, outcome := range []domain.GrokQuestionOutcome{domain.GrokQuestionAccepted, domain.GrokQuestionCancelled, domain.GrokQuestionSkipped} {
		t.Run(string(outcome), func(t *testing.T) {
			g := grokPublicServer(t, domain.PlanMode)
			questions := []domain.GrokQuestion{{Question: "Original?", Options: []domain.QuestionOption{{Label: "One", Description: "Original"}}}}
			tool := g.described(t, "question", domain.GrokAsk, "", nil, "", questions)
			id := g.request(t, "question", &domain.GrokInteractionRequest{Kind: domain.GrokQuestionInteraction, Mode: domain.GrokPlanMode, ToolName: domain.GrokAsk, Questions: questions})
			answer := &domain.GrokQuestionResponse{Outcome: outcome}
			if outcome == domain.GrokQuestionAccepted {
				answer.Answers = map[string]string{"Original?": "Exact, 🙂\n"}
				answer.Annotations = map[string]domain.GrokQuestionAnnotation{"Original?": {Notes: "Original note"}}
			}
			if outcome == domain.GrokQuestionSkipped {
				answer.PartialAnswers = map[string]string{"Original?": "Partial"}
			}
			response := domain.NewID()
			if _, err := acceptFixtureResponse(g.f, response, id, 1, domain.QuestionResponseInput{Grok: answer}); err != nil {
				t.Fatal(err)
			}
			claim := &pb.ClaimQuestionResponseRequest{Mutation: &pb.Mutation{Id: string(id), ExpectedRevision: 2, RequestId: string(domain.NewID())}, MachineId: string(g.f.input.MachineID), InstanceId: string(g.f.instance), JobId: string(g.f.job), ResponseId: string(response)}
			if _, err := claimQuestion(g.f, claim); err != nil {
				t.Fatal(err)
			}
			g.publish(t, domain.ExecutionEvent{Kind: domain.ExecutionQuestionDeliveryObserved, QuestionResponse: &domain.ExecutionQuestionResponseUpdate{InteractionID: id, ResponseID: response, ClaimID: domain.ID(claim.Mutation.RequestId), NativeItemID: "question", Delivery: domain.QuestionTransmitted}})
			g.publish(t, g.tool(t, tool, "question", domain.GrokAsk, domain.GrokCompleted, "", nil, "", questions))
			g.publish(t, domain.ExecutionEvent{Kind: domain.ExecutionQuestionAccepted, QuestionAcceptance: &domain.ExecutionQuestionAcceptanceUpdate{InteractionID: id, ResponseID: response, ClaimID: domain.ID(claim.Mutation.RequestId), NativeItemID: "question", Evidence: domain.NativeGrokQuestionOutput}})
			g.publish(t, domain.ExecutionEvent{Kind: domain.ExecutionInteractionClosed, Interaction: &domain.ExecutionInteractionUpdate{ID: id, NativeItemID: "question", NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: string(id)}, Type: domain.UserQuestionInteraction, Closure: domain.InteractionNativeClosed}})
			_, v := readPublishedInteraction(t, g.f, id)
			if v.Response.State != domain.QuestionResponseAccepted || !reflect.DeepEqual(v.Response.Input.Grok, answer) || v.Closure != domain.InteractionNativeClosed {
				t.Fatal("question output changed outcome or invented Stop")
			}
		})
	}
}
