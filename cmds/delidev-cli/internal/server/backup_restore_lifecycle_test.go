// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type restoreAdmissionLog struct {
	slog.Handler
	admitted func(context.Context)
}

func (h restoreAdmissionLog) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == "backup_restore_admitted" {
		h.admitted(ctx)
	}
	return h.Handler.Handle(ctx, record)
}
func lifecycleRestoreFixture(t *testing.T) (*Service, context.Context, *pb.RestoreBackupRequest) {
	t.Helper()
	s, _ := newDoctorFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := s.Store.BindIdentity(ctx, s.Identity.ServerID); err != nil {
		t.Fatal(err)
	}
	backup, err := createCurrentBackupFixture(s, ctx, connect.NewRequest(&pb.RequestBackupRequest{RequestId: string(domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := s.InspectBackup(ctx, connect.NewRequest(&pb.InspectBackupRequest{Id: backup.Msg.Job.BackupId}))
	if err != nil {
		t.Fatal(err)
	}
	revision := inspection.Msg.RestoreRevision
	s.stop = func() {}
	return s, ctx, &pb.RestoreBackupRequest{RequestId: string(domain.NewID()), Backup: inspection.Msg.Backup, Sha256: inspection.Msg.Sha256, ExpectedRestoreRevision: &revision, Confirm: true}
}

func TestRestorePreparationOutlivesRequestWaitAndThirtySeconds(t *testing.T) {
	for _, cancelWait := range []bool{false, true} {
		t.Run(map[bool]string{false: "elapsed", true: "canceled-client"}[cancelWait], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, owner, request := lifecycleRestoreFixture(t)
				wait, cancel := context.WithCancel(owner)
				defer cancel()
				started := time.Now()
				s.logger = slog.New(restoreAdmissionLog{Handler: slog.NewJSONHandler(io.Discard, nil), admitted: func(ctx context.Context) {
					if _, deadline := ctx.Deadline(); deadline {
						t.Fatal("request deadline reached admitted preparation")
					}
					if cancelWait {
						cancel()
					}
					// Advance the deterministic clock at the admitted preparation boundary.
					// No real wait, native execution or large-image throughput is measured.
					time.Sleep(31 * time.Second)
					if ctx.Err() != nil {
						t.Fatal("admitted preparation canceled by client wait or ordinary deadline", ctx.Err())
					}
				}})
				result, err := s.RestoreBackup(wait, connect.NewRequest(request))
				if err != nil || result.Msg.Replayed || result.Msg.Receipt.RequestId != request.RequestId || result.Msg.Receipt.State != pb.BackupRestoreState_BACKUP_RESTORE_STATE_PUBLISHED {
					t.Fatal(result, err)
				}
				if time.Since(started) < 31*time.Second {
					t.Fatal("controlled preparation did not cross old cap")
				}
				root := s.Store.Root()
				s.Store.Close()
				reopened, err := store.Open(owner, root)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				receipt, err := reopened.GetBackupRestore(owner, domain.ID(request.RequestId))
				if err != nil || receipt.State != store.RestoreCompleted || receipt.Input.Backup.ID != domain.ID(request.Backup.Id) {
					t.Fatal(receipt, err)
				}
				inspection, err := reopened.InspectBackup(owner, domain.ID(request.Backup.Id), s.Identity.ServerID)
				if err != nil || inspection.SHA256 != request.Sha256 {
					t.Fatal("original backup changed", err)
				}
			})
		})
	}
}

func TestRestoreShutdownCancelsAndJoinsOriginalAdmission(t *testing.T) {
	s, owner, request := lifecycleRestoreFixture(t)
	entered, release, canceled, returned, joined := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan error, 1), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	s.logger = slog.New(restoreAdmissionLog{Handler: slog.NewJSONHandler(io.Discard, nil), admitted: func(ctx context.Context) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
	}})
	go func() { _, err := s.RestoreBackup(owner, connect.NewRequest(request)); returned <- err }()
	<-entered
	go func() { s.closeBackupRestores(); close(joined) }()
	<-canceled
	select {
	case <-joined:
		t.Fatal("shutdown retired original restore before it returned")
	default:
	}
	if _, _, err := s.beginBackupRestore(domain.Principal{Type: domain.OwnerDevice}); domain.SafeError(err).Code != domain.Unavailable {
		t.Fatal("shutdown admitted new restore", err)
	}
	unblock()
	if err := <-returned; err == nil {
		t.Fatal("shutdown ignored original cancellation")
	}
	<-joined
	if s.Store.RestoreFrozen() {
		t.Fatal("preparation cancellation manufactured publication")
	}
	receipt, err := s.Store.GetBackupRestore(owner, domain.ID(request.RequestId))
	if domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("preparation cancellation manufactured receipt", receipt, err)
	}
	inspected, err := s.Store.InspectBackup(owner, domain.ID(request.Backup.Id), s.Identity.ServerID)
	if err != nil || inspected.SHA256 != request.Sha256 {
		t.Fatal("shutdown changed original backup", err)
	}
}

func TestRestorePreAdmissionWaitRemainsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, owner, request := lifecycleRestoreFixture(t)
		unlock, err := s.lockAccounts(owner)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		started := time.Now()
		if _, err := s.RestoreBackup(owner, connect.NewRequest(request)); err == nil {
			t.Fatal("blocked pre-admission wait did not expire")
		}
		if time.Since(started) != 30*time.Second || s.Store.RestoreFrozen() {
			t.Fatal("admission bound or live database changed", time.Since(started))
		}
	})
}

func TestRestoreOriginalServerEpochOwnsCancellation(t *testing.T) {
	parent, stop := context.WithCancel(context.Background())
	defer stop()
	s := &Service{}
	s.initializeBackupRestores(parent)
	actor := domain.Principal{Type: domain.ClientDevice, DeviceID: domain.NewID()}
	operation, finish, err := s.beginBackupRestore(actor)
	if err != nil {
		t.Fatal(err)
	}
	original, ok := domain.PrincipalFrom(operation)
	if !ok || original != actor {
		t.Fatal("original actor changed")
	}
	stop()
	<-operation.Done()
	if _, _, err := s.beginBackupRestore(actor); domain.SafeError(err).Code != domain.Unavailable {
		t.Fatal("canceled epoch admitted new restore", err)
	}
	joined := make(chan struct{})
	go func() { s.closeBackupRestores(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("original server cancellation substituted for a join")
	default:
	}
	finish()
	<-joined
}
