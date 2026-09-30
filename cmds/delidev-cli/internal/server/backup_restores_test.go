package server

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestStatusPreservesForwardingAndRestoreCapabilities(t *testing.T) {
	s, _ := newDoctorFixture(t)
	status, err := s.GetStatus(context.Background(), connect.NewRequest(&pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if pb.SystemCapability_SYSTEM_CAPABILITY_SESSION_FORWARDING_V1 != 2 || pb.SystemCapability_SYSTEM_CAPABILITY_MANAGED_BACKUP_RESTORE_V1 != 3 {
		t.Fatal("published forwarding capability or additive restore number changed")
	}
	for _, capability := range []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_SESSION_FORWARDING_V1, pb.SystemCapability_SYSTEM_CAPABILITY_MANAGED_BACKUP_RESTORE_V1} {
		if !slices.Contains(status.Msg.Capabilities, capability) {
			t.Fatal("supported capability was not advertised", capability)
		}
	}
}

func TestRestoreRPCRequiresOriginalInspectionAndCurrentAuthority(t *testing.T) {
	s, _ := newDoctorFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := s.Store.BindIdentity(ctx, s.Identity.ServerID); err != nil {
		t.Fatal(err)
	}
	backup, err := s.CreateBackup(ctx, connect.NewRequest(&pb.CreateBackupRequest{RequestId: string(domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := s.InspectBackup(ctx, connect.NewRequest(&pb.InspectBackupRequest{Id: backup.Msg.Id}))
	if err != nil {
		t.Fatal(err)
	}
	revision := inspection.Msg.RestoreRevision
	request := &pb.RestoreBackupRequest{RequestId: string(domain.NewID()), Backup: inspection.Msg.Backup, Sha256: inspection.Msg.Sha256, ExpectedRestoreRevision: &revision, Confirm: true}
	worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: domain.NewID()})
	if _, err := s.RestoreBackup(worker, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal(err)
	}
	if _, err := s.GetBackupRestore(worker, connect.NewRequest(&pb.GetBackupRestoreRequest{RequestId: request.RequestId})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal(err)
	}
	request.Confirm = false
	if _, err := s.RestoreBackup(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal(err)
	}
	request.Confirm = true
	request.ExpectedRestoreRevision = nil
	if _, err := s.RestoreBackup(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal(err)
	}
	request.ExpectedRestoreRevision = &revision
	done := make(chan struct{})
	s.stop = func() { close(done) }
	result, err := s.RestoreBackup(ctx, connect.NewRequest(request))
	if err != nil || result.Msg.Receipt.State != pb.BackupRestoreState_BACKUP_RESTORE_STATE_PUBLISHED || result.Msg.Replayed {
		t.Fatal(result, err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old process was not stopped")
	}
	intent, err := ReadLifecycle(s.Store.Root())
	if err != nil || intent.State != DesiredStopped {
		t.Fatal(intent, err)
	}
	root := s.Store.Root()
	s.Store.Close()
	reopened, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s.Store = reopened
	result, err = s.RestoreBackup(ctx, connect.NewRequest(request))
	if err != nil || !result.Msg.Replayed || result.Msg.Receipt.State != pb.BackupRestoreState_BACKUP_RESTORE_STATE_RESTORED {
		t.Fatal(result, err)
	}
	status, err := s.GetBackupRestore(ctx, connect.NewRequest(&pb.GetBackupRestoreRequest{RequestId: request.RequestId}))
	if err != nil || status.Msg.Receipt.State != pb.BackupRestoreState_BACKUP_RESTORE_STATE_RESTORED {
		t.Fatal(status, err)
	}
}
