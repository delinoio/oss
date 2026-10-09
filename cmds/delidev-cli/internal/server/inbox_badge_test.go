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
