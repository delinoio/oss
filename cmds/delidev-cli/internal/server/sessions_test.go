package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func sessionClient(f *accountFixture) delidevv1connect.SessionServiceClient {
	return delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.endpoint.URL)
}
func sessionBody(t *testing.T, r *pb.Resource) domain.Session {
	t.Helper()
	var v domain.Session
	if err := domain.Decode(r.DocumentJson, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func inputBody(t *testing.T, r *pb.Resource) domain.QueuedInput {
	t.Helper()
	var v domain.QueuedInput
	if err := domain.Decode(r.DocumentJson, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func sessionSelection(t *testing.T, f *accountFixture) (domain.CreateSession, security.Identity) {
	t.Helper()
	worker, paired := pairedWorker(t, context.Background(), f.endpoint, f.identity)
	p := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Fixture", Endpoint: "http://127.0.0.1:1/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth})
	m := f.save(pb.EntityKind_ENTITY_KIND_MODEL, domain.Model{Name: "Fixture model", NativeID: "fixture", ProviderID: domain.ID(p.Id), Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared})
	a := f.save(pb.EntityKind_ENTITY_KIND_AGENT, domain.Agent{Name: "Fixture Agent", Harness: domain.Codex, ModelID: domain.ID(m.Id), Options: domain.AgentOptions{Permission: domain.PermissionDefault}})
	return domain.CreateSession{Name: "Fixture session", AgentID: domain.ID(a.Id), MachineID: domain.ID(paired.Machine.Id), Workspace: domain.GeneralChat, Prompt: "initial private prompt", Mode: domain.ExecuteMode, Source: domain.ExternalCLISession}, worker
}
func createSessionFixture(t *testing.T, f *accountFixture, input domain.CreateSession) (*pb.CreateSessionRequest, *pb.SessionChange) {
	t.Helper()
	raw, _ := json.Marshal(input)
	req := &pb.CreateSessionRequest{RequestId: string(domain.NewID()), DocumentJson: raw}
	response, err := sessionClient(f).CreateSession(context.Background(), ownerRequest(f.identity, req))
	if err != nil {
		t.Fatal(err)
	}
	return req, response.Msg.Change
}

func TestSessionAcceptanceRestartAndCurrentReceipt(t *testing.T) {
	f := newAccountFixture(t)
	selection, worker := sessionSelection(t, f)
	request, change := createSessionFixture(t, f, selection)
	v := sessionBody(t, change.Session)
	if v.Outcome != domain.ExecutionNotStarted || v.Dispatch != domain.DispatchBlocked || v.Recovery != domain.NoRecovery || v.Archive != domain.NotArchived || v.Problem == nil || v.Problem.Code != domain.Unavailable || v.ActiveExecutionID != "" || v.Source != domain.ExternalCLISession || v.MachineID != selection.MachineID {
		t.Fatalf("creation invented execution: %+v", v)
	}
	q := inputBody(t, change.Input)
	if q.Sequence != 1 || q.ContentRevision != 1 || q.Delivery != domain.InputQueued || q.Mode != domain.ExecuteMode || v.PendingInputs != 1 {
		t.Fatal("initial input not accepted atomically")
	}
	if _, err := sessionClient(f).CreateSession(context.Background(), ownerRequest(worker, request)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("Worker created a session: %v", err)
	}
	modified, err := sessionClient(f).EditQueuedInput(context.Background(), ownerRequest(f.identity, &pb.EditQueuedInputRequest{Mutation: acctMutation(change.Input, domain.NewID()), SessionId: change.Session.Id, Prompt: "replacement private prompt"}))
	if err != nil {
		t.Fatal(err)
	}
	if inputBody(t, modified.Msg.Change.Input).ContentRevision != 2 {
		t.Fatal("content revision not advanced")
	}
	f.shutdown()
	f.start()
	retry, err := sessionClient(f).CreateSession(context.Background(), ownerRequest(f.identity, request))
	if err != nil || !retry.Msg.Change.Replayed || retry.Msg.Change.Session.Id != change.Session.Id || inputBody(t, retry.Msg.Change.Input).Prompt != "replacement private prompt" {
		t.Fatalf("restart replay rewrote acceptance: %v", err)
	}
	_, err = sessionClient(f).RemoveQueuedInput(context.Background(), ownerRequest(f.identity, &pb.RemoveQueuedInputRequest{Mutation: acctMutation(retry.Msg.Change.Input, domain.NewID()), SessionId: change.Session.Id}))
	if err != nil {
		t.Fatal(err)
	}
	retry, err = sessionClient(f).CreateSession(context.Background(), ownerRequest(f.identity, request))
	if err != nil || !retry.Msg.Change.Replayed || inputBody(t, retry.Msg.Change.Input).Prompt != "" || inputBody(t, retry.Msg.Change.Input).Delivery != domain.InputRemoved {
		t.Fatal("old creation receipt resurrected removed content")
	}
	request.DocumentJson = []byte(`{"name":"changed"}`)
	if _, err := sessionClient(f).CreateSession(context.Background(), ownerRequest(f.identity, request)); err == nil {
		t.Fatal("changed creation accepted")
	}
	selection.Name = "different valid input"
	request.DocumentJson, _ = json.Marshal(selection)
	if _, err := sessionClient(f).CreateSession(context.Background(), ownerRequest(f.identity, request)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("request identity reused: %v", err)
	}
	listed, err := sessionClient(f).ListSessions(context.Background(), ownerRequest(f.identity, &pb.ListSessionsRequest{}))
	if err != nil || len(listed.Msg.Sessions) != 1 {
		t.Fatal("retry duplicated session")
	}
	for _, kind := range []pb.EntityKind{pb.EntityKind_ENTITY_KIND_SESSION, pb.EntityKind_ENTITY_KIND_QUEUE} {
		_, err := f.config.SaveConfiguration(context.Background(), ownerRequest(f.identity, &pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, Kind: kind, SchemaVersion: 1, DocumentJson: []byte(`{}`)}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("generic configuration bypassed lifecycle")
		}
	}
}

func TestSessionQueueConcurrentOrderRevisionRemovalAndScope(t *testing.T) {
	f := newAccountFixture(t)
	selection, _ := sessionSelection(t, f)
	_, initial := createSessionFixture(t, f, selection)
	client := sessionClient(f)
	ctx := context.Background()
	const count = 24
	var wg sync.WaitGroup
	failures := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			raw, _ := json.Marshal(domain.SessionInput{Prompt: fmt.Sprintf("queued-%d", i), Mode: domain.PlanMode})
			_, err := client.EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: initial.Session.Id, DocumentJson: raw}))
			if err != nil {
				failures <- err
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	var records []*pb.Resource
	page := ""
	firstCursor := ""
	for {
		result, err := client.ListQueue(ctx, ownerRequest(f.identity, &pb.ListQueueRequest{SessionId: initial.Session.Id, PageSize: 3, PageToken: page}))
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, result.Msg.Inputs...)
		page = result.Msg.NextPageToken
		if firstCursor == "" {
			firstCursor = page
		}
		if page == "" {
			break
		}
	}
	if len(records) != count+1 {
		t.Fatalf("lost inputs: %d", len(records))
	}
	for i, r := range records {
		if inputBody(t, r).Sequence != uint64(i+1) {
			t.Fatal("queue order came from client timing or UUID")
		}
	}
	_, other := createSessionFixture(t, f, selection)
	if _, err := client.ListQueue(ctx, ownerRequest(f.identity, &pb.ListQueueRequest{SessionId: other.Session.Id, PageToken: firstCursor})); err == nil {
		t.Fatal("foreign queue cursor accepted")
	}
	item := records[1]
	editReq := &pb.EditQueuedInputRequest{Mutation: acctMutation(item, domain.NewID()), SessionId: initial.Session.Id, Prompt: "edited content"}
	edited, err := client.EditQueuedInput(ctx, ownerRequest(f.identity, editReq))
	if err != nil {
		t.Fatal(err)
	}
	v := inputBody(t, edited.Msg.Change.Input)
	if v.Mode != domain.PlanMode || v.Sequence != 2 || v.ContentRevision != 2 {
		t.Fatal("edit reinterpreted accepted mode/order")
	}
	editReq.Mutation.RequestId = string(domain.NewID())
	if _, err := client.EditQueuedInput(ctx, ownerRequest(f.identity, editReq)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale content revision accepted: %v", err)
	}
	removeReq := &pb.RemoveQueuedInputRequest{Mutation: acctMutation(edited.Msg.Change.Input, domain.NewID()), SessionId: initial.Session.Id}
	removed, err := client.RemoveQueuedInput(ctx, ownerRequest(f.identity, removeReq))
	if err != nil {
		t.Fatal(err)
	}
	if value := inputBody(t, removed.Msg.Change.Input); value.Prompt != "" || value.Delivery != domain.InputRemoved {
		t.Fatal("removal retained executable input")
	}
	replay, err := client.RemoveQueuedInput(ctx, ownerRequest(f.identity, removeReq))
	if err != nil || !replay.Msg.Change.Replayed || sessionBody(t, replay.Msg.Change.Session).PendingInputs != count {
		t.Fatal("remove retry changed queue accounting")
	}
	removeReq.Mutation.RequestId = string(domain.NewID())
	removeReq.Mutation.ExpectedRevision = removed.Msg.Change.Input.Revision
	if _, err := client.RemoveQueuedInput(ctx, ownerRequest(f.identity, removeReq)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("already removed input changed again")
	}
	foreign := &pb.EditQueuedInputRequest{Mutation: acctMutation(records[2], domain.NewID()), SessionId: other.Session.Id, Prompt: "foreign"}
	if _, err := client.EditQueuedInput(ctx, ownerRequest(f.identity, foreign)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("cross-session input edit accepted")
	}
}

func TestSessionArchiveRestoreKeepsOutcomeQueueAndDispatchIndependent(t *testing.T) {
	f := newAccountFixture(t)
	selection, _ := sessionSelection(t, f)
	_, initial := createSessionFixture(t, f, selection)
	client := sessionClient(f)
	ctx := context.Background()
	control := func(record *pb.Resource, action pb.SessionAction) *pb.SessionChange {
		t.Helper()
		r, err := client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(record, domain.NewID()), Action: action}))
		if err != nil {
			t.Fatal(err)
		}
		return r.Msg.Change
	}
	stopped := control(initial.Session, pb.SessionAction_SESSION_ACTION_STOP)
	if v := sessionBody(t, stopped.Session); v.Dispatch != domain.DispatchPaused || v.Outcome != domain.ExecutionNotStarted {
		t.Fatal("Stop invented execution outcome")
	}
	archived := control(stopped.Session, pb.SessionAction_SESSION_ACTION_ARCHIVE)
	if v := sessionBody(t, archived.Session); v.Archive != domain.Archived || v.PendingInputs != 1 {
		t.Fatal("Archive removed queue")
	}
	for include, want := range map[bool]int{false: 0, true: 1} {
		r, err := client.ListSessions(ctx, ownerRequest(f.identity, &pb.ListSessionsRequest{IncludeArchived: include}))
		if err != nil || len(r.Msg.Sessions) != want {
			t.Fatal("archive list visibility wrong")
		}
	}
	_, err := client.EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: initial.Session.Id, DocumentJson: []byte(`{"prompt":"must not execute","mode":"execute"}`)}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("archived input accepted")
	}
	restored := control(archived.Session, pb.SessionAction_SESSION_ACTION_RESTORE)
	if v := sessionBody(t, restored.Session); v.Archive != domain.NotArchived || v.Dispatch != domain.DispatchPaused || v.Outcome != domain.ExecutionNotStarted {
		t.Fatal("restore resumed or changed outcome")
	}
	_, err = client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(restored.Session, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_RESUME}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("unprepared workspace resumed execution")
	}
	r, err := client.RenameSession(ctx, ownerRequest(f.identity, &pb.RenameSessionRequest{Mutation: acctMutation(restored.Session, domain.NewID()), Name: "Renamed"}))
	if err != nil {
		t.Fatal(err)
	}
	if v := sessionBody(t, r.Msg.Change.Session); v.Name != "Renamed" || v.Dispatch != domain.DispatchPaused || v.PendingInputs != 1 {
		t.Fatal("rename altered lifecycle")
	}
	_, err = client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(restored.Session, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_ARCHIVE}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale control accepted")
	}
}

func TestSessionNativeUncertaintyCannotBeEditedOrDeclaredStopped(t *testing.T) {
	f := newAccountFixture(t)
	selection, _ := sessionSelection(t, f)
	_, initial := createSessionFixture(t, f, selection)
	f.shutdown()
	db, err := store.Open(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Mutate(context.Background(), domain.NewID(), "fixture.accepted-native-state", nil, func(tx *store.Tx) (any, error) {
		r, v, err := sessionRecord(tx, domain.ID(initial.Session.Id))
		if err != nil {
			return nil, err
		}
		v.ActiveExecutionID = domain.NewID()
		v.Outcome = domain.ExecutionRunning
		v.Recovery = domain.NeedsRecovery
		v.Dispatch = domain.DispatchPaused
		if _, err := tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, v); err != nil {
			return nil, err
		}
		q, err := tx.Get(domain.QueueKind, domain.ID(initial.Input.Id))
		if err != nil {
			return nil, err
		}
		input, err := store.Decode[domain.QueuedInput](q)
		if err != nil {
			return nil, err
		}
		input.Delivery = domain.InputUncertain
		return tx.Put(domain.QueueKind, q.ID, q.Revision, q.SessionID, q.ProjectID, input)
	})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	f.start()
	current := currentCatalogResource(t, f, initial.Session)
	item := currentCatalogResource(t, f, initial.Input)
	_, err = sessionClient(f).ControlSession(context.Background(), ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(current, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_ARCHIVE}))
	wantAccountCode(t, err, domain.RecoveryRequired)
	_, err = sessionClient(f).EditQueuedInput(context.Background(), ownerRequest(f.identity, &pb.EditQueuedInputRequest{Mutation: acctMutation(item, domain.NewID()), SessionId: current.Id, Prompt: "unsafe replay"}))
	wantAccountCode(t, err, domain.Conflict)
	if after := currentCatalogResource(t, f, current); after.Revision != current.Revision {
		t.Fatal("uncertain native outcome was rewritten")
	}
}

func TestSessionSelectionRejectsAmbiguityBeforePersistence(t *testing.T) {
	f := newAccountFixture(t)
	selection, _ := sessionSelection(t, f)
	for _, modify := range []func(*domain.CreateSession){
		func(v *domain.CreateSession) { v.ProjectID = domain.NewID() },
		func(v *domain.CreateSession) { v.AgentID = domain.NewID() },
		func(v *domain.CreateSession) { v.Workspace = domain.Local; v.ProjectID = domain.NewID() },
		func(v *domain.CreateSession) { v.Mode = "unsupported" },
		func(v *domain.CreateSession) { v.Prompt = "" },
	} {
		v := selection
		modify(&v)
		raw, _ := json.Marshal(v)
		if _, err := sessionClient(f).CreateSession(context.Background(), ownerRequest(f.identity, &pb.CreateSessionRequest{RequestId: string(domain.NewID()), DocumentJson: raw})); err == nil {
			t.Fatal("invalid selection accepted")
		}
	}
	r, err := sessionClient(f).ListSessions(context.Background(), ownerRequest(f.identity, &pb.ListSessionsRequest{}))
	if err != nil || len(r.Msg.Sessions) != 0 {
		t.Fatal("failed validation partially persisted a session")
	}
}
