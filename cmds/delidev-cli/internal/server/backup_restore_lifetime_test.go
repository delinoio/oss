// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestAdmittedBackupRestoreOutlivesOrdinaryDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service := &Service{}
		actor := domain.Principal{Type: domain.OwnerDevice}
		caller, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), actor), 30*time.Second)
		defer cancel()
		operation, finish, err := service.admitBackupRestore(caller)
		if err != nil {
			t.Fatal(err)
		}
		// Controlled copy/validation stage without 31 seconds of physical CI waiting.
		time.Sleep(31 * time.Second)
		if !errors.Is(caller.Err(), context.DeadlineExceeded) {
			t.Fatal("fixture deadline did not expire")
		}
		if operation.Err() != nil {
			t.Fatal("valid admitted restore inherited ordinary deadline")
		}
		if _, bounded := operation.Deadline(); bounded {
			t.Fatal("full restore has an ordinary deadline")
		}
		if principal, ok := domain.PrincipalFrom(operation); !ok || principal != actor {
			t.Fatal("operation lost original actor")
		}
		finish()
		service.closeBackupRestores()
	})
}

func TestBackupRestoreShutdownCancelsAndJoinsOriginalOperation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, cancelServer := context.WithCancel(context.Background())
		service := &Service{}
		service.initializeBackupRestores(parent)
		caller, cancelCaller := context.WithCancel(context.Background())
		operation, finish, err := service.admitBackupRestore(caller)
		if err != nil {
			t.Fatal(err)
		}
		cancelCaller()
		if operation.Err() != nil {
			t.Fatal("client wait cancellation canceled admitted work")
		}
		joined := make(chan struct{})
		go func() { service.closeBackupRestores(); close(joined) }()
		synctest.Wait()
		if !errors.Is(operation.Err(), context.Canceled) {
			t.Fatal("explicit shutdown did not cancel original operation")
		}
		select {
		case <-joined:
			t.Fatal("shutdown released owners before original work finished")
		default:
		}
		finish()
		<-joined
		if _, _, err := service.admitBackupRestore(context.Background()); err == nil {
			t.Fatal("closed lifecycle admitted replacement work")
		}
		cancelServer()
	})
}

func TestCanceledBackupRestoreIsRejectedBeforeAdmission(t *testing.T) {
	service := &Service{}
	caller, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := service.admitBackupRestore(caller); err == nil {
		t.Fatal("canceled request acquired restore ownership")
	}
	service.closeBackupRestores()
}

func TestAdmittedBackupRestorePreservesReceiptAfterCallerDisconnect(t *testing.T) {
	testAdmittedBackupRestoreReceipt(t, false)
}

func TestAdmittedBackupRestorePublishesAfterSlowPreparedStage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) { testAdmittedBackupRestoreReceipt(t, true) })
}

func testAdmittedBackupRestoreReceipt(t *testing.T, slowStage bool) {
	t.Helper()
	service, _ := newDoctorFixture(t)
	caller, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
	defer cancel()
	if err := service.Store.BindIdentity(caller, service.Identity.ServerID); err != nil {
		t.Fatal(err)
	}
	created, err := createCurrentBackupFixture(service, caller, connect.NewRequest(&pb.RequestBackupRequest{RequestId: string(domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	inspected, err := service.InspectBackup(caller, connect.NewRequest(&pb.InspectBackupRequest{Id: created.Msg.Job.BackupId}))
	if err != nil {
		t.Fatal(err)
	}
	operation, finish, err := service.admitBackupRestore(caller)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	if !slowStage {
		cancel()
	}
	modified, err := time.Parse(time.RFC3339Nano, inspected.Msg.Backup.ModifiedAt)
	if err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	input := store.BackupRestoreInput{Backup: store.Backup{ID: domain.ID(inspected.Msg.Backup.Id), Bytes: inspected.Msg.Backup.SizeBytes, ModifiedAt: modified}, SHA256: inspected.Msg.Sha256, ExpectedRevision: inspected.Msg.RestoreRevision, ServerID: service.Identity.ServerID, Actor: domain.Principal{Type: domain.OwnerDevice}}
	receipt, replayed, err := service.Store.RestoreBackupWithBarrier(operation, id, input, func() error {
		if slowStage {
			// Hold the actual store restore at its original prepared barrier past
			// ordinary admission time, then publish the same request and image.
			time.Sleep(31 * time.Second)
			if !errors.Is(caller.Err(), context.DeadlineExceeded) {
				t.Fatal("controlled stage did not exceed ordinary deadline")
			}
		}
		if operation.Err() != nil {
			t.Fatal("caller cancellation reached prepared barrier")
		}
		return nil
	})
	if err != nil || replayed || receipt.RequestID != id || receipt.State != store.RestorePublished {
		t.Fatal("admitted original request did not publish", receipt, err)
	}
}
