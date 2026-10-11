package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"testing"
	"time"
)

func TestUnreadInboxCountRPCReadOnlyAndAuthenticated(t *testing.T) {
	f, _ := questionResponseFixture(t)
	client := inboxClient(f)
	response, err := client.GetUnreadInboxCount(context.Background(), ownerRequest(f.service.Identity, &pb.GetUnreadInboxCountRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Msg.UnreadCount != 1 {
		t.Fatalf("count=%d", response.Msg.UnreadCount)
	}
	if _, err := time.Parse(time.RFC3339Nano, response.Msg.ObservedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetUnreadInboxCount(context.Background(), connect.NewRequest(&pb.GetUnreadInboxCountRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated=%v", err)
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: domain.NewID()})
	if _, err := f.service.GetUnreadInboxCount(ctx, connect.NewRequest(&pb.GetUnreadInboxCountRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("Worker accepted: %v", err)
	}
	ctx = domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: domain.NewID()})
	if _, err := f.service.GetUnreadInboxCount(ctx, connect.NewRequest(&pb.GetUnreadInboxCountRequest{})); err == nil {
		t.Fatal("unpaired/revoked client accepted")
	}
}

func TestMarkSessionInboxReadRPCReplaysOriginalResultAndRejectsMisuse(t *testing.T) {
	f, _ := questionResponseFixture(t)
	client := inboxClient(f)
	request := &pb.MarkSessionInboxReadRequest{RequestId: string(domain.NewID()), SessionId: string(f.input.SessionID)}
	first, err := client.MarkSessionInboxRead(context.Background(), ownerRequest(f.service.Identity, request))
	if err != nil {
		t.Fatal(err)
	}
	if first.Msg.MarkedCount != 1 || first.Msg.Replayed || first.Msg.SessionId != request.SessionId || first.Msg.RequestId != request.RequestId {
		t.Fatalf("unexpected acknowledgment %+v", first.Msg)
	}
	if _, err := time.Parse(time.RFC3339Nano, first.Msg.ObservedAt); err != nil {
		t.Fatal(err)
	}
	count, err := client.GetUnreadInboxCount(context.Background(), ownerRequest(f.service.Identity, &pb.GetUnreadInboxCountRequest{}))
	if err != nil || count.Msg.UnreadCount != 0 {
		t.Fatalf("count=%v err=%v", count, err)
	}
	replay, err := client.MarkSessionInboxRead(context.Background(), ownerRequest(f.service.Identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.MarkedCount != 1 || replay.Msg.ObservedAt != first.Msg.ObservedAt {
		t.Fatalf("replay changed the original result: %+v %v", replay, err)
	}
	other := &pb.MarkSessionInboxReadRequest{RequestId: request.RequestId, SessionId: string(domain.NewID())}
	if _, err := client.MarkSessionInboxRead(context.Background(), ownerRequest(f.service.Identity, other)); err == nil {
		t.Fatal("request ID reused for another session")
	}
	missing := &pb.MarkSessionInboxReadRequest{RequestId: string(domain.NewID()), SessionId: string(domain.NewID())}
	if _, err := client.MarkSessionInboxRead(context.Background(), ownerRequest(f.service.Identity, missing)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("missing session: %v", err)
	}
	if _, err := client.MarkSessionInboxRead(context.Background(), connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated=%v", err)
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: domain.NewID()})
	if _, err := f.service.MarkSessionInboxRead(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("Worker accepted: %v", err)
	}
}
