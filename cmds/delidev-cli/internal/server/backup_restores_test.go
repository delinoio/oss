package server

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestStatusPreservesForwardingAndRestoreCapabilities(t *testing.T) {
	s, _ := newDoctorFixture(t)
	status, err := s.GetStatus(context.Background(), connect.NewRequest(&pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if pb.SystemCapability_SYSTEM_CAPABILITY_SESSION_FORWARDING_V1 != 2 || pb.SystemCapability_SYSTEM_CAPABILITY_USER_SERVICES_V1 != 3 || pb.SystemCapability_SYSTEM_CAPABILITY_MANAGED_BACKUP_RESTORE_V1 != 4 {
		t.Fatal("published forwarding/service capability or additive restore number changed")
	}
	for _, capability := range []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_SESSION_FORWARDING_V1, pb.SystemCapability_SYSTEM_CAPABILITY_USER_SERVICES_V1, pb.SystemCapability_SYSTEM_CAPABILITY_MANAGED_BACKUP_RESTORE_V1} {
		if !slices.Contains(status.Msg.Capabilities, capability) {
			t.Fatal("supported capability was not advertised", capability)
		}
	}
}

type blockedRestoreServiceControl struct {
	serviceFixture
	started chan struct{}
	release <-chan struct{}
}

func (b *blockedRestoreServiceControl) Install(ctx context.Context, spec userservice.Spec) error {
	close(b.started)
	select {
	case <-b.release:
		return b.serviceFixture.Install(ctx, spec)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestRestoreRefusesConcurrentUserServiceControl(t *testing.T) {
	s, _ := newDoctorFixture(t)
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 10*time.Second)
	defer cancel()
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
	release := make(chan struct{})
	defer close(release)
	backend := &blockedRestoreServiceControl{started: make(chan struct{}), release: release}
	s.userServiceBackend = backend
	done := make(chan error, 1)
	go func() {
		_, err := s.ControlUserService(ctx, connect.NewRequest(&pb.ControlUserServiceRequest{Kind: pb.UserServiceKind_USER_SERVICE_KIND_SERVER, Action: pb.UserServiceAction_USER_SERVICE_ACTION_INSTALL, RequestId: string(domain.NewID())}))
		done <- err
	}()
	select {
	case <-backend.started:
	case err := <-done:
		t.Fatal("service control never reached its original native write", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	revision := inspection.Msg.RestoreRevision
	_, err = s.RestoreBackup(ctx, connect.NewRequest(&pb.RestoreBackupRequest{RequestId: string(domain.NewID()), Backup: inspection.Msg.Backup, Sha256: inspection.Msg.Sha256, ExpectedRestoreRevision: &revision, Confirm: true}))
	if connect.CodeOf(err) != connect.CodeAborted || s.Store.RestoreFrozen() {
		t.Fatal("restore passed an unfinished native service control", err)
	}
	// Finish the admitted native operation before fixture teardown; no real OS
	// registration is created, and the original write must be observed only once.
	release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if backend.writes != 1 {
		t.Fatal("original service install was lost or replayed", backend.writes)
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
