package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type questionControllerFixture struct {
	loseAcceptanceAck  bool
	acceptanceRequests []string
	delidevv1connect.WorkerServiceClient
	t                           *testing.T
	mapper                      *CodexEventPublisher
	control                     *pb.QuestionResponseControl
	path                        string
	mode                        string
	claims, sends, publications int
	inspections                 int
	inspection                  func(codex.InteractionStatus) (codex.InteractionStatus, error)
	claim                       domain.ID
	block                       chan struct{}
}

func TestNativeApprovalPublicationRejectsMalformedPayloadBeforeOutbox(t *testing.T) {
	f := newQuestionControllerFixture(t, "ready")
	err := f.mapper.publishInteraction(context.Background(), codex.Event{Kind: codex.InteractionRequestedEvent, TurnID: f.mapper.turn, Interaction: &codex.Interaction{ID: domain.NewID(), Kind: codex.ApprovalInteraction, Approval: &codex.ApprovalRequest{Kind: codex.CommandApproval}}})
	if err == nil || f.publications != 0 || f.claims != 0 || f.sends != 0 || len(f.mapper.interactions) != 1 {
		t.Fatal("private approval bypassed dedicated durable publication/owner authority", err)
	}
}

func newQuestionControllerFixture(t *testing.T, mode string) *questionControllerFixture {
	t.Helper()
	job, execution, interaction, response, session := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	root := filepath.Join(t.TempDir(), "worker")
	for _, p := range []string{root, filepath.Join(root, "jobs"), filepath.Join(root, "jobs", string(job))} {
		if err := security.PrivateDir(p); err != nil {
			t.Fatal(err)
		}
	}
	f := &questionControllerFixture{t: t, mode: mode, control: &pb.QuestionResponseControl{JobId: string(job), InteractionId: string(interaction), ResponseId: string(response), Revision: 2}, path: filepath.Join(root, "jobs", string(job), "responses", string(interaction)+".json"), block: make(chan struct{})}
	publisher := &ExecutionPublisher{config: PublicationConfig{Root: root, Client: f, Instance: domain.NewID(), Credential: Credential{ServerID: domain.NewID(), DeviceID: domain.NewID(), MachineID: domain.NewID(), Type: domain.WorkerDevice}}, execution: execution, job: job, input: domain.ExecutionJobInput{SessionID: session}, path: filepath.Join(root, "jobs", string(job), "publication.json"), state: publicationJournal{Version: 1, JobID: job, LastSequence: 3}}
	f.mapper = NewCodexEventPublisher(publisher)
	f.mapper.thread, f.mapper.turn = domain.NewID(), domain.NewID()
	f.mapper.interactions[interaction] = domain.ExecutionInteractionUpdate{ID: interaction, NativeItemID: "native-question", NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: "original-native"}, Type: domain.UserQuestionInteraction}
	return f
}

func (f *questionControllerFixture) readJournal(want responseJournalState) questionResponseJournal {
	f.t.Helper()
	raw, err := security.ReadPrivate(f.path, 64<<10)
	var journal questionResponseJournal
	if err != nil || domain.Decode(raw, &journal) != nil || journal.State != want || journal.ClaimID.Validate() != nil || journal.Control.ResponseID != domain.ID(f.control.ResponseId) || strings.Contains(string(raw), "exact fixture answer") || strings.Contains(string(raw), "Original question") {
		f.t.Fatal("native/RPC side effect preceded its metadata-only journal", err)
	}
	return journal
}

func (f *questionControllerFixture) ClaimQuestionResponse(_ context.Context, req *connect.Request[pb.ClaimQuestionResponseRequest]) (*connect.Response[pb.ClaimQuestionResponseResponse], error) {
	f.claims++
	j := f.readJournal(responsePrepared)
	if string(j.ClaimID) != req.Msg.Mutation.RequestId {
		f.t.Fatal("claim RPC replaced its durable identity")
	}
	f.claim = j.ClaimID
	if f.mode == "claim-ack-lost" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("fixture lost claim acknowledgment"))
	}
	c := f.mapper.publisher.config
	value := domain.ExecutionInteraction{ExecutionID: f.mapper.publisher.execution, NativeThreadID: string(f.mapper.thread), NativeTurnID: string(f.mapper.turn), NativeItemID: "native-question", NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: "original-native"}, Type: domain.UserQuestionInteraction, Closure: domain.InteractionOpen, Questions: &domain.QuestionRequest{Blocking: true, Questions: []domain.Question{{ID: "choice", Header: "Choice", Text: "Original question", Other: true}}}, Response: &domain.QuestionResponse{ID: domain.ID(f.control.ResponseId), State: domain.QuestionResponseClaimed, Input: domain.QuestionResponseInput{Answers: map[string][]string{"choice": {"exact fixture answer"}}}, Claim: &domain.QuestionResponseClaim{ID: j.ClaimID, JobID: domain.ID(f.control.JobId), MachineID: c.Credential.MachineID, InstanceID: c.Instance, DeviceID: c.Credential.DeviceID}}}
	if f.mode == "foreign-native-request" {
		value.NativeRequestID.Text = "foreign"
	}
	if f.mode == "foreign-claim" {
		value.Response.Claim.InstanceID = domain.NewID()
	}
	raw, _ := json.Marshal(value)
	return connect.NewResponse(&pb.ClaimQuestionResponseResponse{Interaction: &pb.Resource{Id: f.control.InteractionId, SessionId: string(f.mapper.publisher.input.SessionID), Kind: pb.EntityKind_ENTITY_KIND_INTERACTION, SchemaVersion: 1, Revision: 3, DocumentJson: raw}}), nil
}

func (f *questionControllerFixture) AnswerQuestions(ctx context.Context, response, interaction, turn domain.ID, answers codex.QuestionAnswers) (codex.InteractionStatus, error) {
	f.sends++
	f.readJournal(responseSendIntent)
	if response != domain.ID(f.control.ResponseId) || interaction != domain.ID(f.control.InteractionId) || turn != f.mapper.turn || len(answers.Answers["choice"]) != 1 || answers.Answers["choice"][0] != "exact fixture answer" {
		f.t.Fatal("native reply changed its immutable original answer/ownership")
	}
	status := codex.InteractionStatus{ID: interaction, TurnID: turn, ItemID: "native-question", ResponseID: response, Closure: codex.InteractionOpen, Delivery: codex.QuestionTransmitted}
	switch f.mode {
	case "uncertain":
		status.Delivery = codex.QuestionDeliveryUncertain
		return status, domain.Fail(domain.RecoveryRequired, "Fixture uncertain write.", "")
	case "not-sent":
		return codex.InteractionStatus{}, domain.Fail(domain.Conflict, "Fixture closed before send.", "")
	case "stop":
		close(f.block)
		<-ctx.Done()
		return codex.InteractionStatus{}, domain.SafeError(ctx.Err())
	}
	return status, nil
}

func (f *questionControllerFixture) InspectInteraction(context.Context, domain.ID) (codex.InteractionStatus, error) {
	return codex.InteractionStatus{ID: domain.ID(f.control.InteractionId), TurnID: f.mapper.turn, ItemID: "native-question", Delivery: codex.QuestionNotSent, Closure: codex.InteractionNativeClosed}, nil
}

func (f *questionControllerFixture) InspectInteractionResponse(ctx context.Context, response, interaction domain.ID) (codex.InteractionStatus, error) {
	f.inspections++
	f.readJournal(responseObserved)
	if response != domain.ID(f.control.ResponseId) || interaction != domain.ID(f.control.InteractionId) {
		f.t.Fatal("inspection replaced the original response/arrival")
	}
	if _, ok := ctx.Deadline(); !ok {
		f.t.Fatal("unbounded response inspection")
	}
	status := codex.InteractionStatus{ID: interaction, ResponseID: response, TurnID: f.mapper.turn, ItemID: "native-question", Delivery: codex.QuestionDeliveryUncertain, Closure: codex.InteractionNativeClosed, Accepted: true}
	if f.inspection != nil {
		return f.inspection(status)
	}
	status.Accepted = false
	return status, publicationUncertain()
}

func (f *questionControllerFixture) PublishExecution(_ context.Context, req *connect.Request[pb.PublishExecutionRequest]) (*connect.Response[pb.PublishExecutionResponse], error) {
	f.publications++
	j := f.readJournal(responseObserved)
	var e domain.ExecutionEvent
	if domain.Decode(req.Msg.EventJson, &e) != nil || e.Validate() != nil {
		f.t.Fatal("invalid response publication")
	}
	if e.Kind == domain.ExecutionQuestionAccepted {
		if e.QuestionAcceptance.ClaimID != j.ClaimID || e.QuestionAcceptance.ResponseID != domain.ID(f.control.ResponseId) {
			f.t.Fatal("acceptance replaced original durable claim")
		}
		f.acceptanceRequests = append(f.acceptanceRequests, req.Msg.Mutation.RequestId)
		if f.loseAcceptanceAck {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("fixture lost acceptance acknowledgment"))
		}
		return connect.NewResponse(&pb.PublishExecutionResponse{AcknowledgedSequence: e.Sequence}), nil
	}
	if e.Kind != domain.ExecutionQuestionDeliveryObserved || e.QuestionResponse.ClaimID != j.ClaimID || e.QuestionResponse.Delivery != j.Delivery {
		f.t.Fatal("delivery event does not match retained response observation")
	}
	if f.mode == "publication-ack-lost" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("fixture lost delivery acknowledgment"))
	}
	return connect.NewResponse(&pb.PublishExecutionResponse{AcknowledgedSequence: e.Sequence}), nil
}

func TestQuestionControllerJournalsClaimAndSendWithoutAnswerContent(t *testing.T) {
	for _, mode := range []string{"transmitted", "claim-ack-lost", "foreign-claim", "foreign-native-request", "uncertain", "not-sent", "publication-ack-lost", "existing-journal"} {
		t.Run(mode, func(t *testing.T) {
			f := newQuestionControllerFixture(t, mode)
			if mode == "existing-journal" {
				if err := security.PrivateDir(filepath.Dir(f.path)); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f.path, []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := f.mapper.deliverQuestionResponse(context.Background(), context.Background(), f.control, f)
			if (err == nil) != (mode == "transmitted" || mode == "not-sent") {
				t.Fatalf("unexpected controller result: %v", err)
			}
			expectedInspections := 0
			if mode == "uncertain" {
				expectedInspections = 1
			}
			if f.inspections != expectedInspections {
				t.Fatal("automatic inspection did not match uncertain delivery")
			}
			expectedSends, expectedClaims := 1, 1
			if mode == "claim-ack-lost" || mode == "foreign-claim" || mode == "foreign-native-request" || mode == "existing-journal" {
				expectedSends = 0
			}
			if mode == "existing-journal" {
				expectedClaims = 0
			}
			if f.sends != expectedSends || f.claims != expectedClaims {
				t.Fatal("uncertain/foreign ownership reached another native attempt")
			}
			if mode == "publication-ack-lost" {
				f.mode = "transmitted"
				if err := f.mapper.publisher.ReplayPending(context.Background()); err != nil {
					t.Fatal(err)
				}
				if f.sends != 1 || f.publications != 2 {
					t.Fatal("delivery receipt replay repeated native send")
				}
			}
			if err := f.mapper.deliverQuestionResponse(context.Background(), context.Background(), f.control, f); err == nil {
				t.Fatal("an existing response attempt was automatically replayed")
			}
			if f.sends != expectedSends || f.claims != expectedClaims {
				t.Fatal("duplicate control repeated a claim or native send")
			}
		})
	}
}

func TestQuestionControllerStopJoinsWithoutCancelingNativeGrace(t *testing.T) {
	f := newQuestionControllerFixture(t, "stop")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nativeCtx, cancelNative := context.WithCancel(context.Background())
	defer cancelNative()
	controls := make(chan *pb.QuestionResponseControl, 1)
	controls <- f.control
	finish := startQuestionResponseController(ctx, nativeCtx, cancelNative, controls, f.mapper, f)
	select {
	case <-f.block:
	case <-time.After(3 * time.Second):
		t.Fatal("native response did not start")
	}
	cancel()
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	if nativeCtx.Err() != nil || f.sends != 1 || f.publications != 1 || f.readJournal(responseObserved).Delivery != domain.QuestionNotSent {
		t.Fatal("Stop lost proven unsent state or canceled its separate native grace period")
	}
}
