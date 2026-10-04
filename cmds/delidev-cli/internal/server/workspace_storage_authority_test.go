// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestWorkspaceStorageDirectHandlersRejectNonClients(t *testing.T) {
	f := newStorageFixture(t)
	accepted, err := f.service.RequestWorkspaceStorage(f.ownerContext, connect.NewRequest(f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", "")))
	if err != nil {
		t.Fatal(err)
	}
	before := f.sessionRecord()
	var worker domain.Principal
	if err := f.service.Store.Read(f.ownerContext, func(tx *store.Tx) error {
		devices, err := tx.List(store.Filter{Kind: domain.DeviceKind, Limit: store.MaxPage})
		for _, r := range devices {
			d, decodeErr := store.Decode[domain.Device](r)
			if decodeErr != nil {
				return decodeErr
			}
			if d.Type == domain.WorkerDevice && d.MachineID == f.machine {
				worker = domain.Principal{Type: d.Type, DeviceID: r.ID, MachineID: d.MachineID}
			}
		}
		return err
	}); err != nil || worker.DeviceID == "" {
		t.Fatal("paired Worker fixture is missing", err)
	}
	for name, ctx := range map[string]context.Context{
		"missing": context.Background(),
		"worker":  domain.WithPrincipal(context.Background(), worker),
		"unknown": domain.WithPrincipal(context.Background(), domain.Principal{}),
	} {
		t.Run(name, func(t *testing.T) {
			for _, action := range []pb.WorkspaceStorageAction{
				pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW,
				pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CREATE,
				pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CLEANUP,
				pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_INSPECT,
				pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RESTORE,
				pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_DELETE,
				pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RECOVER,
			} {
				_, err := f.service.RequestWorkspaceStorage(ctx, connect.NewRequest(f.request(action, "", "", "")))
				if connect.CodeOf(err) != connect.CodePermissionDenied {
					t.Fatalf("action %v accepted non-client authority: %v", action, err)
				}
			}
			if _, err := f.service.GetWorkspaceStorageOperation(ctx, connect.NewRequest(&pb.GetWorkspaceStorageOperationRequest{Id: accepted.Msg.Job.Id})); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal("operation read accepted non-client authority", err)
			}
			if _, err := f.service.CancelWorkspaceStorageOperation(ctx, connect.NewRequest(&pb.CancelWorkspaceStorageOperationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: accepted.Msg.Job.Id, ExpectedRevision: accepted.Msg.Job.Revision}})); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal("operation cancellation accepted non-client authority", err)
			}
		})
	}
	observed, err := f.service.GetWorkspaceStorageOperation(f.ownerContext, connect.NewRequest(&pb.GetWorkspaceStorageOperationRequest{Id: accepted.Msg.Job.Id}))
	if err != nil || observed.Msg.Job.Revision != accepted.Msg.Job.Revision || f.sessionRecord().Revision != before.Revision {
		t.Fatal("denied calls changed original operation or session", err)
	}
}

func TestWorkspaceStorageDirectHandlersRequireCurrentClient(t *testing.T) {
	for _, role := range []domain.DeviceType{domain.OwnerDevice, domain.ClientDevice} {
		t.Run(string(role), func(t *testing.T) {
			f := newStorageFixture(t)
			ctx := f.ownerContext
			clientID := domain.NewID()
			if role == domain.ClientDevice {
				doctorPut(t, f.service, domain.DeviceKind, clientID, 0, domain.Device{Type: role})
				ctx = domain.WithPrincipal(context.Background(), domain.Principal{Type: role, DeviceID: clientID})
			}
			accepted, err := f.service.RequestWorkspaceStorage(ctx, connect.NewRequest(f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", "")))
			if err != nil {
				t.Fatal(err)
			}
			read := connect.NewRequest(&pb.GetWorkspaceStorageOperationRequest{Id: accepted.Msg.Job.Id})
			if _, err := f.service.GetWorkspaceStorageOperation(ctx, read); err != nil {
				t.Fatal(err)
			}
			cancel := connect.NewRequest(&pb.CancelWorkspaceStorageOperationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: accepted.Msg.Job.Id, ExpectedRevision: accepted.Msg.Job.Revision}})
			if _, err := f.service.CancelWorkspaceStorageOperation(ctx, cancel); err != nil {
				t.Fatal(err)
			}
			if role == domain.ClientDevice {
				doctorPut(t, f.service, domain.DeviceKind, clientID, 1, domain.Device{Type: role, Revoked: true})
				if _, err := f.service.GetWorkspaceStorageOperation(ctx, read); connect.CodeOf(err) != connect.CodeUnauthenticated {
					t.Fatal("revoked client retained read authority", err)
				}
				if _, err := f.service.CancelWorkspaceStorageOperation(ctx, cancel); connect.CodeOf(err) != connect.CodeUnauthenticated {
					t.Fatal("revoked client replayed cancellation", err)
				}
				if _, err := f.service.RequestWorkspaceStorage(ctx, connect.NewRequest(f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", ""))); connect.CodeOf(err) != connect.CodeUnauthenticated {
					t.Fatal("revoked client retained mutation authority", err)
				}
			}
			var job domain.Job
			if err := f.service.Store.Read(f.ownerContext, func(tx *store.Tx) error {
				r, err := tx.Get(domain.JobKind, domain.ID(accepted.Msg.Job.Id))
				if err == nil {
					job, err = store.Decode[domain.Job](r)
				}
				return err
			}); err != nil || job.State != domain.JobCanceled {
				t.Fatal("authorized cancellation did not settle the original operation", err)
			}
		})
	}
}
