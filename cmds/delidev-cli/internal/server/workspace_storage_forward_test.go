// SPDX-License-Identifier: Apache-2.0
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

func TestWorkspaceStorageWaitsForBothForwardCleanupReports(t *testing.T) {
	f := newStorageFixture(t)
	request := f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", "")
	forwardID := domain.NewID()
	var revision uint64
	states := []domain.Forward{
		{State: domain.ForwardActive, ClientClaimed: true, WorkerClaimed: true},
		{State: domain.ForwardStopping, ClientClaimed: true, WorkerClaimed: true, ClientClean: true},
		{State: domain.ForwardStopping, ClientClaimed: true, WorkerClaimed: true, WorkerClean: true},
		{State: domain.ForwardStopped, ClientClaimed: true, WorkerClaimed: true, ClientClean: true},
		{State: domain.ForwardStopped, ClientClaimed: true, WorkerClaimed: true, ClientClean: true, WorkerClean: true},
	}
	for i, value := range states {
		_, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "storage.forward.fixture", i, func(tx *store.Tx) (any, error) {
			row, err := tx.Put(domain.ForwardKind, forwardID, revision, f.session, "", value)
			revision = row.Revision
			return row, err
		})
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, request))
		if i == len(states)-1 {
			if err != nil || accepted.Msg.Replayed || accepted.Msg.RequestId != request.Mutation.RequestId {
				t.Fatal("original storage intent did not become eligible after both cleanup reports", err)
			}
			continue
		}
		if connect.CodeOf(err) != connect.CodeAborted || f.sessionRecord().Revision != request.Mutation.ExpectedRevision {
			t.Fatal("forward ownership did not block storage atomically", i, err)
		}
		err = f.service.Store.Read(f.ownerContext, func(tx *store.Tx) error {
			jobs, err := tx.List(store.Filter{Kind: domain.JobKind, SessionID: f.session, Limit: store.MaxPage})
			if err == nil && len(jobs) != 1 {
				t.Fatal("blocked storage created a native job", len(jobs))
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkspaceStorageExcludesNewForwardAndLiveAuthority(t *testing.T) {
	f := newStorageFixture(t)
	f.service.forwardInit()
	client := delidevv1connect.NewForwardServiceClient(f.server.Client(), f.server.URL)
	forwardID, runtimeID := domain.NewID(), domain.NewID()
	_, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "storage.forward.fixture", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.ForwardKind, forwardID, 0, f.session, "", domain.Forward{MachineID: f.machine, ClientType: domain.OwnerDevice, ClientRuntimeID: runtimeID, Epoch: f.service.forwardEpoch, State: domain.ForwardActive})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []domain.WorkspaceStorageState{domain.WorkspaceStoragePending, domain.WorkspaceStorageUncertain, domain.WorkspaceStored} {
		_, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "storage.forward.state.fixture", state, func(tx *store.Tx) (any, error) {
			row, session, err := sessionRecord(tx, f.session)
			if err != nil {
				return nil, err
			}
			session.Storage = &domain.WorkspaceStorage{State: state, JobID: domain.NewID(), SnapshotID: domain.NewID()}
			return tx.Put(domain.SessionKind, row.ID, row.Revision, row.SessionID, row.ProjectID, session)
		})
		if err != nil {
			t.Fatal(err)
		}
		row := f.sessionRecord()
		_, err = client.StartForward(context.Background(), ownerRequest(f.service.Identity, &pb.StartForwardRequest{RequestId: string(domain.NewID()), SessionId: string(f.session), MachineId: string(f.machine), ExpectedSessionRevision: row.Revision, WorkerPort: 46332}))
		if connect.CodeOf(err) != connect.CodeAborted {
			t.Fatal("storage admitted new forward ownership", state, err)
		}
		err = f.service.Store.Read(f.ownerContext, func(tx *store.Tx) error {
			peer := &pb.ForwardPeer{ForwardId: string(forwardID), SessionId: string(f.session), RuntimeId: string(runtimeID)}
			actor := domain.Principal{Type: domain.OwnerDevice}
			if _, _, _, err := f.service.peerForward(tx, actor, peer, true); domain.SafeError(err).Code != domain.Unavailable {
				t.Fatal("storage admitted retained live socket authority", state, err)
			}
			// Closing the original native lifetime remains authorized independently
			// of storage availability, so this guard cannot trap pending cleanup.
			if _, _, _, err := f.service.peerForward(tx, actor, peer, false); err != nil {
				t.Fatal("storage blocked original cleanup authority", state, err)
			}
			forwards, err := tx.List(store.Filter{Kind: domain.ForwardKind, SessionID: f.session, Limit: store.MaxPage})
			if err == nil && len(forwards) != 1 {
				t.Fatal("blocked start published a new forward", len(forwards))
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
