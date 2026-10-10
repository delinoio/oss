package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestSessionInboxReadRPCOriginalActorReceiptAndLaterAlerts(t *testing.T) {
	f, id := questionResponseFixture(t)
	client := inboxClient(f)
	ctx := context.Background()
	inbox, _ := readExecutionInbox(t, f, domain.InteractionInbox, id)
	request := &pb.MarkSessionInboxReadRequest{RequestId: string(domain.NewID()), SessionId: string(f.input.SessionID)}
	response, err := client.MarkSessionInboxRead(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || response.Msg.MarkedCount != 1 || response.Msg.Replayed || response.Msg.RequestId != request.RequestId || response.Msg.SessionId != request.SessionId || response.Msg.ObservedAt == "" {
		t.Fatal("invalid acknowledgment", response, err)
	}
	// Manual controls remain independent of the foreground receipt.
	_, err = client.SetInboxReadState(ctx, ownerRequest(f.service.Identity, &pb.SetInboxReadStateRequest{Mutation: &pb.Mutation{Id: string(inbox.ID), ExpectedRevision: 2, RequestId: string(domain.NewID())}, ReadState: pb.InboxReadState_INBOX_READ_STATE_UNREAD}))
	if err != nil {
		t.Fatal(err)
	}
	f.publish(t, f.interactionEvent(4, domain.NewID(), "later-alert"))
	replay, err := client.MarkSessionInboxRead(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.MarkedCount != 1 || replay.Msg.ObservedAt != response.Msg.ObservedAt {
		t.Fatal("receipt changed", replay, err)
	}
	count, err := client.GetUnreadInboxCount(ctx, ownerRequest(f.service.Identity, &pb.GetUnreadInboxCountRequest{}))
	if err != nil || count.Msg.UnreadCount != 2 {
		t.Fatal("later unread/new alert was marked again", count, err)
	}
	other, _ := pairedQuestionClient(t, f)
	if _, err := client.MarkSessionInboxRead(ctx, ownerRequest(other, request)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("foreign actor reused original receipt", err)
	}
	changed := &pb.MarkSessionInboxReadRequest{RequestId: request.RequestId, SessionId: string(domain.NewID())}
	if _, err := client.MarkSessionInboxRead(ctx, ownerRequest(f.service.Identity, changed)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("foreign session reused original receipt", err)
	}
}

func TestSessionInboxReadRPCRejectsInvalidMissingAndWorkerAuthority(t *testing.T) {
	f, _ := questionResponseFixture(t)
	client := inboxClient(f)
	request := &pb.MarkSessionInboxReadRequest{RequestId: string(domain.NewID()), SessionId: string(f.input.SessionID)}
	if _, err := client.MarkSessionInboxRead(context.Background(), connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("anonymous client admitted", err)
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: domain.NewID()})
	if _, err := f.service.MarkSessionInboxRead(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker admitted", err)
	}
	invalid := &pb.MarkSessionInboxReadRequest{RequestId: "not-a-uuid", SessionId: request.SessionId}
	if _, err := client.MarkSessionInboxRead(context.Background(), ownerRequest(f.service.Identity, invalid)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("invalid request admitted", err)
	}
	missing := &pb.MarkSessionInboxReadRequest{RequestId: string(domain.NewID()), SessionId: string(domain.NewID())}
	if _, err := client.MarkSessionInboxRead(context.Background(), ownerRequest(f.service.Identity, missing)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("missing session admitted", err)
	}
	// A valid receipt does not change Session revision or response authority.
	var before store.Record
	err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		var err error
		before, err = tx.Get(domain.SessionKind, f.input.SessionID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.MarkSessionInboxRead(context.Background(), ownerRequest(f.service.Identity, request)); err != nil {
		t.Fatal(err)
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		after, err := tx.Get(domain.SessionKind, f.input.SessionID)
		if err == nil && after.Revision != before.Revision {
			t.Fatal("Inbox changed Session revision")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSessionInboxReadRPCRevocationBlocksFreshAndOriginalReplay(t *testing.T) {
	f, _ := questionResponseFixture(t)
	paired, device := pairedQuestionClient(t, f)
	client := inboxClient(f)
	ctx := context.Background()
	request := &pb.MarkSessionInboxReadRequest{RequestId: string(domain.NewID()), SessionId: string(f.input.SessionID)}
	if _, err := client.MarkSessionInboxRead(ctx, ownerRequest(paired, request)); err != nil {
		t.Fatal(err)
	}
	devices := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
	if _, err := devices.RevokeDevice(ctx, ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{Id: device.Id, ExpectedRevision: device.Revision, RequestId: string(domain.NewID())}})); err != nil {
		t.Fatal(err)
	}
	if _, err := client.MarkSessionInboxRead(ctx, ownerRequest(paired, request)); err == nil {
		t.Fatal("revoked client replayed original receipt")
	}
	request.RequestId = string(domain.NewID())
	stale := domain.WithPrincipal(ctx, domain.Principal{Type: domain.ClientDevice, DeviceID: domain.ID(device.Id)})
	if _, err := f.service.MarkSessionInboxRead(stale, connect.NewRequest(request)); err == nil {
		t.Fatal("stale authentication bypassed transaction authorization")
	}
}
