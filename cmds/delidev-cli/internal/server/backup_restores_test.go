package server

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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
	if pb.SystemCapability_SYSTEM_CAPABILITY_SESSION_FORWARDING_V1 != 2 || pb.SystemCapability_SYSTEM_CAPABILITY_USER_SERVICES_V1 != 3 || pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_OUTBOUND_PROXY_V1 != 6 || pb.SystemCapability_SYSTEM_CAPABILITY_NATIVE_ACCOUNTING_V1 != 4 || pb.SystemCapability_SYSTEM_CAPABILITY_STOPPED_CODEX_ACCOUNT_SWITCH_V1 != 5 || pb.SystemCapability_SYSTEM_CAPABILITY_MANAGED_BACKUP_RESTORE_V1 != 7 || pb.SystemCapability_SYSTEM_CAPABILITY_PERMANENT_SESSION_DELETION_V1 != 9 || pb.SystemCapability_SYSTEM_CAPABILITY_WORKSPACE_STORAGE_V1 != 11 || pb.SystemCapability_SYSTEM_CAPABILITY_CODEX_SESSION_FORK_V1 != 13 {
		t.Fatal("published forwarding/service capability or additive restore number changed")
	}
	for _, capability := range []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_SESSION_FORWARDING_V1, pb.SystemCapability_SYSTEM_CAPABILITY_USER_SERVICES_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_OUTBOUND_PROXY_V1, pb.SystemCapability_SYSTEM_CAPABILITY_NATIVE_ACCOUNTING_V1, pb.SystemCapability_SYSTEM_CAPABILITY_STOPPED_CODEX_ACCOUNT_SWITCH_V1, pb.SystemCapability_SYSTEM_CAPABILITY_MANAGED_BACKUP_RESTORE_V1, pb.SystemCapability_SYSTEM_CAPABILITY_PERMANENT_SESSION_DELETION_V1, pb.SystemCapability_SYSTEM_CAPABILITY_WORKSPACE_STORAGE_V1, pb.SystemCapability_SYSTEM_CAPABILITY_CODEX_SESSION_FORK_V1} {
		if !slices.Contains(status.Msg.Capabilities, capability) {
			t.Fatal("supported capability was not advertised", capability)
		}
	}
}

func TestBackupRestorePreservesOriginalGrokAccountingAndCurrentDeletion(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		name := "retained"
		if deleted {
			name = "deleted"
		}
		t.Run(name, func(t *testing.T) {
			f, terminal := grokServerTerminalFixture(t, true)
			f.publish(t, terminal)
			f.reportCompletion(t, grokCompletion(f, terminal.Sequence))
			profile := pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1
			before := grokAccountingSummary(t, f, profile)
			if len(before.Totals.Accounting) != 1 {
				t.Fatal("fixture has no original verified accounting")
			}
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			s := f.service.Store
			if err := s.BindIdentity(ctx, f.service.Identity.ServerID); err != nil {
				t.Fatal(err)
			}
			backup, err := s.Backup(ctx)
			if err != nil {
				t.Fatal(err)
			}
			inspection, err := s.InspectBackup(ctx, backup, f.service.Identity.ServerID)
			if err != nil {
				t.Fatal(err)
			}
			if deleted {
				// Exercise the shared tombstone/cascade boundary with a unit
				// produced by the authenticated original completion path.
				_, err = s.Mutate(ctx, domain.NewID(), "fixture.delete-accounting-session", nil, func(tx *store.Tx) (any, error) {
					r, err := tx.Get(domain.SessionKind, f.input.SessionID)
					if err != nil {
						return nil, err
					}
					return nil, tx.Delete(r.Kind, r.ID, r.Revision)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			f.service.executionAuthority.close()
			f.http.Close()
			revision, err := s.RestoreRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			input := store.BackupRestoreInput{Backup: inspection.Backup, SHA256: inspection.SHA256, ServerID: f.service.Identity.ServerID, Actor: domain.Principal{Type: domain.OwnerDevice}, ExpectedRevision: revision}
			if _, _, err := s.RestoreBackup(ctx, domain.NewID(), input); err != nil {
				t.Fatal(err)
			}
			root := s.Root()
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			selection := domain.UsageSelection{From: time.Now().UTC().Add(-time.Hour), Until: time.Now().UTC().Add(time.Hour), AccountingProfile: domain.NativeUnitsV1Accounting}
			if err := reopened.Read(ctx, func(tx *store.Tx) error {
				got, err := tx.UsageSummary(selection)
				if err != nil {
					return err
				}
				if deleted {
					if len(got.Totals.Accounting) != 0 || len(got.Groups) != 0 {
						t.Fatal("restore revived deleted native accounting")
					}
				} else if len(got.Totals.Accounting) != 1 || got.Totals.Accounting[0].Units != 1 || got.Totals.Accounting[0].KnownTotal != "16" {
					t.Fatal("restore lost or duplicated original accounting")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if !deleted {
				r, err := reopened.Get(ctx, domain.SessionKind, f.input.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				session, err := store.Decode[domain.Session](r)
				if err != nil || session.Dispatch != domain.DispatchPaused || session.Recovery != domain.NeedsRecovery || !reflect.DeepEqual(session.Execution.GrokTerminal, terminal.GrokTerminal) {
					t.Fatal("restore changed original native proof or unpaused execution", err)
				}
			}
		})
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
	lifecycle, err := LockLifecycle(root)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := WriteRunning(root, Config{})
	lifecycle.Close()
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.RestoreBackup(ctx, connect.NewRequest(request))
	if err != nil || !result.Msg.Replayed || result.Msg.Receipt.State != pb.BackupRestoreState_BACKUP_RESTORE_STATE_RESTORED {
		t.Fatal(result, err)
	}
	intent, err = ReadLifecycle(root)
	if err != nil || intent != replacement {
		t.Fatal("receipt replay stopped the explicitly restarted epoch", intent, err)
	}
	status, err := s.GetBackupRestore(ctx, connect.NewRequest(&pb.GetBackupRestoreRequest{RequestId: request.RequestId}))
	if err != nil || status.Msg.Receipt.State != pb.BackupRestoreState_BACKUP_RESTORE_STATE_RESTORED {
		t.Fatal(status, err)
	}
}

func TestRestoreStoppedIntentFailurePreservesOriginalDatabase(t *testing.T) {
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
	root := s.Store.Root()
	intent := filepath.Join(root, "server-lifecycle.json")
	if err := os.Mkdir(intent, 0700); err != nil {
		t.Fatal(err)
	}
	s.stop = func() {}
	requestID := domain.NewID()
	revision := inspection.Msg.RestoreRevision
	_, err = s.RestoreBackup(ctx, connect.NewRequest(&pb.RestoreBackupRequest{RequestId: string(requestID), Backup: inspection.Msg.Backup, Sha256: inspection.Msg.Sha256, ExpectedRestoreRevision: &revision, Confirm: true}))
	if err == nil {
		t.Fatal("restore accepted an uncommitted stopped intent")
	}
	if err := os.Remove(intent); err != nil {
		t.Fatal(err)
	}
	s.Store.Close()
	reopened, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s.Store = reopened
	receipt, err := reopened.GetBackupRestore(ctx, requestID)
	if err != nil || receipt.State != store.RestoreRolledBack {
		t.Fatal("stopped-intent failure published a replacement", receipt, err)
	}
	current, err := reopened.RestoreRevision(ctx)
	if err != nil || current != revision {
		t.Fatal("stopped-intent failure changed the live revision", current, err)
	}
	observed, err := reopened.InspectBackup(ctx, domain.ID(backup.Msg.Id), s.Identity.ServerID)
	if err != nil || observed.SHA256 != inspection.Msg.Sha256 {
		t.Fatal("stopped-intent failure changed the source backup", observed, err)
	}
}
