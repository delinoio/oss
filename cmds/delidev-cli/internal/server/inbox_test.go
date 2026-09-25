package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

func inboxClient(f *publicationFixture) delidevv1connect.InboxServiceClient {
	return delidevv1connect.NewInboxServiceClient(f.http.Client(), f.http.URL)
}

func TestInboxRPCReadStateReplayReturnsCurrentSourceWithoutReapplying(t *testing.T) {
	f, id := questionResponseFixture(t)
	client, ctx := inboxClient(f), context.Background()
	inbox, _ := readExecutionInbox(t, f, domain.InteractionInbox, id)
	request := &pb.SetInboxReadStateRequest{Mutation: &pb.Mutation{Id: string(inbox.ID), RequestId: string(domain.NewID()), ExpectedRevision: 1}, ReadState: pb.InboxReadState_INBOX_READ_STATE_READ}
	var group sync.WaitGroup
	results := make(chan *connect.Response[pb.SetInboxReadStateResponse], 6)
	errors := make(chan error, 6)
	for range 6 {
		group.Go(func() {
			response, err := client.SetInboxReadState(ctx, ownerRequest(f.service.Identity, request))
			results <- response
			errors <- err
		})
	}
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	fresh := 0
	for response := range results {
		if !response.Msg.Replayed {
			fresh++
		}
		if response.Msg.View.Entry.Revision != 2 || response.Msg.View.Interaction.Id != string(id) || response.Msg.View.Interaction.Revision != 1 || response.Msg.View.Session.Id != string(f.input.SessionID) || response.Header().Get(rpc.CorrelationHeader) == "" {
			t.Fatal("joined inbox mutation lost independent revision, scope or correlation")
		}
	}
	if fresh != 1 {
		t.Fatal("identical concurrent receipt retries repeated mark-read")
	}
	identity := inboxReadIdentity{inbox.ID, 1, domain.InboxRead, domain.Principal{Type: domain.OwnerDevice}}
	receipt, err := f.service.Store.Mutate(ctx, domain.ID(request.Mutation.RequestId), "inbox.read-state", identity, func(*store.Tx) (any, error) {
		t.Fatal("read-state receipt was reapplied")
		return nil, nil
	})
	if err != nil || !receipt.Replayed || string(receipt.Data) != `{"inbox_id":"`+string(inbox.ID)+`"}` {
		t.Fatal("read-state receipt retained original content")
	}
	other, _ := pairedQuestionClient(t, f)
	if _, err := client.SetInboxReadState(ctx, ownerRequest(other, request)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("another client adopted a read-state receipt")
	}
	changed := proto.Clone(request).(*pb.SetInboxReadStateRequest)
	changed.ReadState = pb.InboxReadState_INBOX_READ_STATE_UNREAD
	if _, err := client.SetInboxReadState(ctx, ownerRequest(f.service.Identity, changed)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("changed state adopted a read-state receipt")
	}
	changed.Mutation.RequestId, changed.Mutation.ExpectedRevision = string(domain.NewID()), 2
	if _, err := client.SetInboxReadState(ctx, ownerRequest(other, changed)); err != nil {
		t.Fatal(err)
	}
	respond := questionRPCRequest(t, id, responseInput())
	if _, err := respondQuestionRPC(f, f.service.Identity, respond); err != nil {
		t.Fatal(err)
	}
	replay, err := client.SetInboxReadState(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.View.Entry.Revision != 3 || replay.Msg.View.Interaction.Revision != 2 {
		t.Fatalf("receipt did not return current inbox and source state: %v", err)
	}
	var entry domain.InboxEntry
	var question domain.ExecutionInteraction
	if domain.Decode(replay.Msg.View.Entry.DocumentJson, &entry) != nil || entry.ReadState != domain.InboxUnread || domain.Decode(replay.Msg.View.Interaction.DocumentJson, &question) != nil || question.Response == nil || question.Response.State != domain.QuestionResponseQueued {
		t.Fatal("old read receipt overwrote later unread or answer state")
	}
	get, err := client.GetInboxEntry(ctx, ownerRequest(other, &pb.GetInboxEntryRequest{Id: string(inbox.ID)}))
	if err != nil || !proto.Equal(get.Msg.View, replay.Msg.View) {
		t.Fatal("paired clients did not observe the same current inbox/source state")
	}
}

func TestInboxRPCFiltersCursorScopeAndReadMembershipEpoch(t *testing.T) {
	f, id := questionResponseFixture(t)
	f.publish(t, f.interactionEvent(4, domain.NewID(), "second"))
	e := f.event(domain.ExecutionTurnFinished, 5)
	e.Outcome = domain.ExecutionFailed
	f.publish(t, e)
	client, ctx := inboxClient(f), context.Background()
	request := &pb.ListInboxRequest{SessionId: string(f.input.SessionID), PageSize: 1, ReadState: pb.InboxReadState_INBOX_READ_STATE_UNREAD}
	first, err := client.ListInbox(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || len(first.Msg.Entries) != 1 || first.Msg.NextPageToken == "" {
		t.Fatalf("inbox pagination failed: %v", err)
	}
	seen := map[string]bool{first.Msg.Entries[0].Entry.Id: true}
	cursor := first.Msg.NextPageToken
	for cursor != "" {
		next := proto.Clone(request).(*pb.ListInboxRequest)
		next.PageToken = cursor
		page, err := client.ListInbox(ctx, ownerRequest(f.service.Identity, next))
		if err != nil {
			t.Fatal(err)
		}
		for _, view := range page.Msg.Entries {
			if seen[view.Entry.Id] || view.Session.Id != request.SessionId {
				t.Fatal("inbox pagination repeated an entry or crossed session scope")
			}
			seen[view.Entry.Id] = true
		}
		cursor = page.Msg.NextPageToken
	}
	if len(seen) != 3 {
		t.Fatal("question and terminal entries were omitted")
	}
	for _, change := range []string{"session", "project", "source", "state"} {
		next := proto.Clone(request).(*pb.ListInboxRequest)
		next.PageToken = first.Msg.NextPageToken
		switch change {
		case "session":
			next.SessionId = string(domain.NewID())
		case "project":
			next.ProjectId = string(domain.NewID())
		case "source":
			next.Source = pb.InboxSource_INBOX_SOURCE_INTERACTION
		case "state":
			next.ReadState = pb.InboxReadState_INBOX_READ_STATE_READ
		}
		_, err := client.ListInbox(ctx, ownerRequest(f.service.Identity, next))
		wantAccountCode(t, err, domain.CursorExpired)
	}
	inbox, _ := readExecutionInbox(t, f, domain.InteractionInbox, id)
	_, err = client.SetInboxReadState(ctx, ownerRequest(f.service.Identity, &pb.SetInboxReadStateRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(inbox.ID), ExpectedRevision: 1}, ReadState: pb.InboxReadState_INBOX_READ_STATE_READ}))
	if err != nil {
		t.Fatal(err)
	}
	request.PageToken = first.Msg.NextPageToken
	_, err = client.ListInbox(ctx, ownerRequest(f.service.Identity, request))
	wantAccountCode(t, err, domain.CursorExpired)
	request.PageToken, request.PageSize, request.ReadState, request.Source = "", 200, pb.InboxReadState_INBOX_READ_STATE_READ, pb.InboxSource_INBOX_SOURCE_INTERACTION
	read, err := client.ListInbox(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || len(read.Msg.Entries) != 1 || read.Msg.Entries[0].Entry.Id != string(inbox.ID) || read.Msg.Entries[0].Interaction == nil || read.Msg.NextPageToken != "" {
		t.Fatal("inbox read/source filters lost current joined state")
	}
	request.ReadState, request.Source = pb.InboxReadState_INBOX_READ_STATE_UNSPECIFIED, pb.InboxSource_INBOX_SOURCE_EXECUTION_TERMINAL
	terminal, err := client.ListInbox(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || len(terminal.Msg.Entries) != 1 || terminal.Msg.Entries[0].Interaction != nil {
		t.Fatal("terminal filter fabricated a question source")
	}
}

func TestInboxRPCRejectsWorkerRevocationAndInvalidReadState(t *testing.T) {
	f, id := questionResponseFixture(t)
	inbox, _ := readExecutionInbox(t, f, domain.InteractionInbox, id)
	client, ctx := inboxClient(f), context.Background()
	mutation := &pb.SetInboxReadStateRequest{Mutation: &pb.Mutation{Id: string(inbox.ID), ExpectedRevision: 1, RequestId: string(domain.NewID())}, ReadState: pb.InboxReadState_INBOX_READ_STATE_READ}
	for _, actor := range []security.Identity{{}, {Token: f.workerToken}} {
		if _, err := client.GetInboxEntry(ctx, ownerRequest(actor, &pb.GetInboxEntryRequest{Id: string(inbox.ID)})); err == nil {
			t.Fatal("unauthorized principal inspected the inbox")
		}
		if _, err := client.ListInbox(ctx, ownerRequest(actor, &pb.ListInboxRequest{})); err == nil {
			t.Fatal("unauthorized principal listed the inbox")
		}
		if _, err := client.SetInboxReadState(ctx, ownerRequest(actor, mutation)); err == nil {
			t.Fatal("unauthorized principal changed inbox read state")
		}
	}
	for _, state := range []pb.InboxReadState{pb.InboxReadState_INBOX_READ_STATE_UNSPECIFIED, pb.InboxReadState(99)} {
		bad := proto.Clone(mutation).(*pb.SetInboxReadStateRequest)
		bad.ReadState = state
		_, err := client.SetInboxReadState(ctx, ownerRequest(f.service.Identity, bad))
		wantAccountCode(t, err, domain.InvalidArgument)
	}
	for _, req := range []*pb.ListInboxRequest{{PageSize: 201}, {ReadState: pb.InboxReadState(99)}, {Source: pb.InboxSource(99)}, {SessionId: "invalid"}} {
		_, err := client.ListInbox(ctx, ownerRequest(f.service.Identity, req))
		wantAccountCode(t, err, domain.InvalidArgument)
	}
	paired, device := pairedQuestionClient(t, f)
	if _, err := client.SetInboxReadState(ctx, ownerRequest(paired, mutation)); err != nil {
		t.Fatal(err)
	}
	devices := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
	if _, err := devices.RevokeDevice(ctx, ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{Id: device.Id, ExpectedRevision: device.Revision, RequestId: string(domain.NewID())}})); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetInboxReadState(ctx, ownerRequest(paired, mutation)); err == nil {
		t.Fatal("revoked client replayed a read-state receipt")
	}
	stale := domain.WithPrincipal(ctx, domain.Principal{Type: domain.ClientDevice, DeviceID: domain.ID(device.Id)})
	if _, err := f.service.SetInboxReadState(stale, connect.NewRequest(mutation)); err == nil {
		t.Fatal("stale authentication bypassed commit-time revocation")
	}
	if _, err := f.service.GetInboxEntry(stale, connect.NewRequest(&pb.GetInboxEntryRequest{Id: string(inbox.ID)})); err == nil {
		t.Fatal("stale authentication read original content after revocation")
	}
}

func TestInboxRPCJoinedPageSizePreservesCompleteSources(t *testing.T) {
	f, id := questionResponseFixture(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.large-inbox", nil, func(tx *store.Tx) (any, error) {
		for range 8 {
			r, err := tx.Get(domain.InteractionKind, id)
			if err != nil {
				return nil, err
			}
			q, err := store.Decode[domain.ExecutionInteraction](r)
			if err != nil {
				return nil, err
			}
			q.Questions.Questions[0].Text = strings.Repeat("q", 240<<10)
			q.Questions.Questions[1].Text = strings.Repeat("f", 240<<10)
			source := domain.NewID()
			if _, err := tx.Put(domain.InteractionKind, source, 0, r.SessionID, r.ProjectID, q); err != nil {
				return nil, err
			}
			if _, err := tx.CreateInboxEntry(r.SessionID, r.ProjectID, domain.InboxEntry{Source: domain.InteractionInbox, SourceID: source, ReadState: domain.InboxUnread}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	client := inboxClient(f)
	req := &pb.ListInboxRequest{PageSize: 200}
	seen := map[string]bool{}
	pages := 0
	for {
		response, err := client.ListInbox(context.Background(), ownerRequest(f.service.Identity, req))
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if proto.Size(response.Msg) > maxInboxViewBytes+2048 {
			t.Fatal("joined inbox page exceeded its transport bound")
		}
		for _, view := range response.Msg.Entries {
			if seen[view.Entry.Id] {
				t.Fatal("byte-bounded inbox pagination repeated an entry")
			}
			seen[view.Entry.Id] = true
			var q domain.ExecutionInteraction
			if json.Unmarshal(view.Interaction.DocumentJson, &q) != nil || (view.Interaction.Id != string(id) && (len(q.Questions.Questions[0].Text) != 240<<10 || len(q.Questions.Questions[1].Text) != 240<<10)) {
				t.Fatal("joined inbox pagination truncated original question content")
			}
		}
		if response.Msg.NextPageToken == "" {
			break
		}
		req.PageToken = response.Msg.NextPageToken
	}
	if pages < 2 || len(seen) != 9 {
		t.Fatal("joined source size did not bound inbox pages")
	}
}

func TestInboxRPCMissingSourceCannotPartiallyCommitReadState(t *testing.T) {
	f, id := questionResponseFixture(t)
	inbox, entry := readExecutionInbox(t, f, domain.InteractionInbox, id)
	entry.SourceID = domain.NewID()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.missing-inbox-source", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(inbox.Kind, inbox.ID, inbox.Revision, inbox.SessionID, inbox.ProjectID, entry)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = inboxClient(f).SetInboxReadState(context.Background(), ownerRequest(f.service.Identity, &pb.SetInboxReadStateRequest{Mutation: &pb.Mutation{Id: string(inbox.ID), ExpectedRevision: 2, RequestId: string(domain.NewID())}, ReadState: pb.InboxReadState_INBOX_READ_STATE_READ}))
	wantAccountCode(t, err, domain.NotFound)
	current, err := f.service.Store.Get(context.Background(), domain.InboxKind, inbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Decode[domain.InboxEntry](current)
	if err != nil || current.Revision != 2 || value.ReadState != domain.InboxUnread {
		t.Fatal("failed joined-source validation partially committed mark-read")
	}
}
