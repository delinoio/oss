package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestBackupPagesAndInspectionRecheckAuthorityAndInventory(t *testing.T) {
	s, secrets := newDoctorFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := s.Store.BindIdentity(ctx, s.Identity.ServerID); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := s.CreateBackup(ctx, connect.NewRequest(&pb.CreateBackupRequest{RequestId: string(domain.NewID())})); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ListBackups(ctx, connect.NewRequest(&pb.ListBackupsRequest{PageSize: 1}))
	if err != nil || len(page.Msg.Backups) != 1 || page.Msg.NextPageToken == "" {
		t.Fatal(page, err)
	}
	next, err := s.ListBackups(ctx, connect.NewRequest(&pb.ListBackupsRequest{PageSize: 1, PageToken: page.Msg.NextPageToken}))
	if err != nil || len(next.Msg.Backups) != 1 || next.Msg.Backups[0].Id == page.Msg.Backups[0].Id {
		t.Fatal(next, err)
	}
	checked, err := s.InspectBackup(ctx, connect.NewRequest(&pb.InspectBackupRequest{Id: page.Msg.Backups[0].Id}))
	if err != nil || checked.Msg.ServerId != string(s.Identity.ServerID) || len(checked.Msg.Sha256) != 64 || len(secrets.refs) != 0 {
		t.Fatal(checked, err)
	}
	if _, err := s.CreateBackup(ctx, connect.NewRequest(&pb.CreateBackupRequest{RequestId: string(domain.NewID())})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListBackups(ctx, connect.NewRequest(&pb.ListBackupsRequest{PageSize: 1, PageToken: page.Msg.NextPageToken})); err == nil {
		t.Fatal("changed inventory retained cursor")
	}
	worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: domain.NewID()})
	if _, err := s.CreateBackup(worker, connect.NewRequest(&pb.CreateBackupRequest{RequestId: string(domain.NewID())})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker created backup", err)
	}
	if _, err := s.ListBackups(worker, connect.NewRequest(&pb.ListBackupsRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker listed backups", err)
	}
	client := domain.NewID()
	doctorPut(t, s, domain.DeviceKind, client, 0, domain.Device{Type: domain.ClientDevice, Revoked: true})
	revoked := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: client})
	if _, err := s.InspectBackup(revoked, connect.NewRequest(&pb.InspectBackupRequest{Id: page.Msg.Backups[0].Id})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("revoked client inspected backup", err)
	}
}
