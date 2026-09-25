package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

type publicationFixture struct {
	*authorityFixture
	thread, turn domain.ID
}

func newPublicationFixture(t *testing.T) *publicationFixture {
	t.Helper()
	return publicationFixtureFromAuthority(t, newAuthorityFixture(t, "http://127.0.0.1:1"))
}

func publicationFixtureFromAuthority(t *testing.T, authority *authorityFixture) *publicationFixture {
	t.Helper()
	f := &publicationFixture{authorityFixture: authority, thread: domain.NewID(), turn: domain.NewID()}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claimed-input", nil, func(tx *store.Tx) (any, error) {
		sr, s, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		s.Outcome = domain.ExecutionNotStarted
		s.PendingInputs = 1
		s.PendingInputBytes = uint64(len(f.input.Input.Prompt))
		if _, err := tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, "", s); err != nil {
			return nil, err
		}
		ir, err := tx.Get(domain.QueueKind, f.input.InputID)
		if err != nil {
			return nil, err
		}
		q, err := store.Decode[domain.QueuedInput](ir)
		if err != nil {
			return nil, err
		}
		q.Delivery = domain.InputClaimed
		return tx.Put(ir.Kind, ir.ID, ir.Revision, sr.ID, "", q)
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *publicationFixture) event(kind domain.ExecutionEventKind, sequence uint64) domain.ExecutionEvent {
	e := domain.ExecutionEvent{Version: 1, ExecutionID: f.input.ExecutionID, Sequence: sequence, Kind: kind, NativeThreadID: string(f.thread), NativeTurnID: string(f.turn)}
	if kind == domain.ExecutionThreadBound {
		e.NativeTurnID = ""
		e.Observed = &domain.ObservedExecutionSettings{Model: f.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}
	}
	return e
}

func (f *publicationFixture) requestEvent(t *testing.T, e domain.ExecutionEvent) *pb.PublishExecutionRequest {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return &pb.PublishExecutionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.job), ExpectedRevision: 1}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), EventJson: raw}
}

func (f *publicationFixture) call(req *pb.PublishExecutionRequest) (*connect.Response[pb.PublishExecutionResponse], error) {
	r := connect.NewRequest(req)
	r.Header().Set("Authorization", "Bearer "+f.workerToken)
	return f.client.PublishExecution(context.Background(), r)
}

func (f *publicationFixture) publish(t *testing.T, e domain.ExecutionEvent) *pb.PublishExecutionRequest {
	t.Helper()
	req := f.requestEvent(t, e)
	r, err := f.call(req)
	if err != nil || r.Msg.AcknowledgedSequence != e.Sequence || r.Msg.Replayed {
		t.Fatalf("publication failed at %s/%d: %v", e.Kind, e.Sequence, err)
	}
	return req
}

func TestExecutionPublicationRetainsOrderedTranscriptAndExactReplay(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	accepted := f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	if response, err := f.call(accepted); err != nil || !response.Msg.Replayed {
		t.Fatalf("input acceptance retry failed: %v", err)
	}
	userID, assistantID := domain.NewID(), domain.NewID()
	for index, kind := range []domain.ExecutionEventKind{domain.ExecutionMessageStarted, domain.ExecutionMessageCompleted} {
		e := f.event(kind, uint64(3+index))
		e.Message = &domain.ExecutionMessageUpdate{ID: userID, NativeID: "user-fixture", Role: domain.UserMessage, InputID: f.input.InputID, Text: f.input.Input.Prompt}
		f.publish(t, e)
	}
	phase := domain.FinalMessage
	message := domain.ExecutionMessageUpdate{ID: assistantID, NativeID: "assistant-fixture", Role: domain.AssistantMessage, Phase: &phase}
	e := f.event(domain.ExecutionMessageStarted, 5)
	e.Message = &message
	f.publish(t, e)
	e = f.event(domain.ExecutionTextAppended, 6)
	message.Text = "Fixture 답변"
	e.Message = &message
	delta := f.requestEvent(t, e)
	var wg sync.WaitGroup
	results := make(chan bool, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			response, err := f.call(delta)
			errs <- err
			if err == nil {
				results <- response.Msg.Replayed
			}
		})
	}
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	fresh := 0
	for replayed := range results {
		if !replayed {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatal("concurrent event retries repeated an append")
	}
	e = f.event(domain.ExecutionMessageCompleted, 7)
	e.Message = &message
	f.publish(t, e)
	e = f.event(domain.ExecutionTurnFinished, 8)
	e.Outcome = domain.ExecutionSucceeded
	f.publish(t, e)
	if lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token); err == nil {
		lease.Release()
		t.Fatal("terminal execution retained inference authority")
	}
	r, err := f.service.Store.Get(context.Background(), domain.MessageKind, assistantID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil || m.Text != "Fixture 답변" || m.State != domain.MessageComplete || m.LastSequence != 7 {
		t.Fatal("native transcript was duplicated or changed")
	}
	sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Decode[domain.Session](sr)
	if err != nil || s.PendingInputs != 0 || s.PendingInputBytes != 0 || s.Outcome != domain.ExecutionSucceeded || s.ActiveExecutionID != f.input.ExecutionID || s.Execution == nil || s.Execution.LastSequence != 8 {
		t.Fatal("terminal publication lost queue/native state or claimed cleanup")
	}
	job, err := f.service.Store.Get(context.Background(), domain.JobKind, f.job)
	if err != nil {
		t.Fatal(err)
	}
	j, err := store.Decode[domain.Job](job)
	if err != nil || j.State != domain.JobClaimed {
		t.Fatal("native completion claimed owned process cleanup")
	}
	// A restarted server acknowledges only the already committed publication.
	// It does not regenerate a token, resend native input or append text again.
	f.service.executionAuthority.close()
	f.http.Close()
	root := f.service.Store.Root()
	if err := f.service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	f.service = &Service{Store: reopened, Identity: f.service.Identity, logger: f.service.logger, accountSecrets: f.service.accountSecrets}
	f.http = httptest.NewServer(f.service.Handler(nil, true))
	f.client = delidevv1connect.NewWorkerServiceClient(http.DefaultClient, f.http.URL)
	if response, err := f.call(delta); err != nil || !response.Msg.Replayed || response.Msg.AcknowledgedSequence != 6 {
		t.Fatalf("restart lost accepted event identity: %v", err)
	}
	different := proto.Clone(delta).(*pb.PublishExecutionRequest)
	different.Mutation.RequestId = string(domain.NewID())
	if _, err := f.call(different); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("old sequence was republished under a new identity: %v", err)
	}
	r, err = f.service.Store.Get(context.Background(), domain.MessageKind, assistantID)
	if err != nil {
		t.Fatal(err)
	}
	m, err = store.Decode[domain.ExecutionMessage](r)
	if err != nil || m.Text != "Fixture 답변" {
		t.Fatal("restart replay appended native text twice")
	}
}

func TestExecutionPublicationRejectsScopeSequenceAndDuplicateNativeItems(t *testing.T) {
	f := newPublicationFixture(t)
	for _, change := range []string{"sequence", "execution", "model", "job-revision", "instance", "owner"} {
		e := f.event(domain.ExecutionThreadBound, 1)
		switch change {
		case "sequence":
			e.Sequence = 2
		case "execution":
			e.ExecutionID = domain.NewID()
		case "model":
			e.Observed.Model = "foreign"
		}
		req := f.requestEvent(t, e)
		switch change {
		case "job-revision":
			req.Mutation.ExpectedRevision = 2
		case "instance":
			req.InstanceId = string(domain.NewID())
		}
		var err error
		if change == "owner" {
			_, err = f.client.PublishExecution(context.Background(), ownerRequest(f.service.Identity, req))
		} else {
			_, err = f.call(req)
		}
		if err == nil {
			t.Fatalf("invalid %s publication succeeded", change)
		}
	}
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	m := domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: "retained-item", Role: domain.AssistantMessage}
	e := f.event(domain.ExecutionMessageStarted, 3)
	e.Message = &m
	f.publish(t, e)
	m.ID = domain.NewID()
	e = f.event(domain.ExecutionMessageStarted, 4)
	e.Message = &m
	if _, err := f.call(f.requestEvent(t, e)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("native item received another product identity: %v", err)
	}
	if _, err := f.service.Store.Get(context.Background(), domain.MessageKind, m.ID); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("rejected native duplicate left a transcript row")
	}
	e = f.event(domain.ExecutionTurnFinished, 4)
	e.Outcome = domain.ExecutionSucceeded
	if _, err := f.call(f.requestEvent(t, e)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("success discarded an unfinished message: %v", err)
	}
	// Failure retains partial text without inventing completion or clearing it.
	e.Outcome = domain.ExecutionFailed
	e.ProblemCode = domain.ResourceExhausted
	f.publish(t, e)
	e = f.event(domain.ExecutionTurnFinished, 5)
	e.Outcome = domain.ExecutionSucceeded
	if _, err := f.call(f.requestEvent(t, e)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("late success replaced a native failure: %v", err)
	}
}

func TestExecutionPublicationAfterStopRetainsFactsWithoutRevivingDispatch(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.stop-before-ack", nil, func(tx *store.Tx) (any, error) {
		r, s, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		s.Dispatch = domain.DispatchPaused
		s.Archive = domain.ArchivePending
		if err := tx.RequestJobCancellation(f.job); err != nil {
			return nil, err
		}
		return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, s)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	e := f.event(domain.ExecutionTurnFinished, 3)
	e.Outcome = domain.ExecutionSucceeded
	f.publish(t, e)
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Decode[domain.Session](r)
	if err != nil || s.Dispatch != domain.DispatchPaused || s.Archive != domain.ArchivePending || s.Outcome != domain.ExecutionStopped || s.Execution.Outcome != domain.ExecutionSucceeded || s.PendingInputs != 0 {
		t.Fatal("late native completion cleared Stop or claimed Archive cleanup")
	}
}

func TestExecutionPublicationRetainsBoundedMessageWithoutTruncation(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	m := domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: "bounded-item", Role: domain.AssistantMessage, Text: strings.Repeat("x", domain.MaxMessageText)}
	e := f.event(domain.ExecutionMessageStarted, 3)
	e.Message = &m
	f.publish(t, e)
	m.Text = "overflow"
	e = f.event(domain.ExecutionTextAppended, 4)
	e.Message = &m
	if _, err := f.call(f.requestEvent(t, e)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("oversized accumulated message was accepted: %v", err)
	}
	r, err := f.service.Store.Get(context.Background(), domain.MessageKind, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil || len(retained.Text) != domain.MaxMessageText || retained.LastSequence != 3 {
		t.Fatal("failed append changed or truncated retained text")
	}
}
