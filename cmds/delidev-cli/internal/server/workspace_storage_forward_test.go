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
		if err != nil || accepted.Msg.Replayed != (i != 0) || accepted.Msg.RequestId != request.Mutation.RequestId {
			t.Fatal("forward cleanup blocked or duplicated storage admission", i, err)
		}
		saved, err := f.service.Store.Get(f.ownerContext, domain.ForwardKind, forwardID)
		observed, decodeErr := store.Decode[domain.Forward](saved)
		if err != nil || decodeErr != nil || observed.ClientClean != value.ClientClean || observed.WorkerClean != value.WorkerClean {
			t.Fatal("storage invented forward cleanup", err, decodeErr)
		}

	}
}

func TestWorkspaceStorageExcludesNewForwardAndLiveAuthority(t *testing.T) {
	f := newForwardFixture(t)
	owner := domain.WithPrincipal(f.ctx, domain.Principal{Type: domain.OwnerDevice})
	_, accepted := f.start(t, 46332, 0, f.identity)
	value := forwardValue(t, accepted.Forward)
	peer := &pb.ForwardPeer{ForwardId: accepted.Forward.Id, SessionId: string(f.session.ID), RuntimeId: string(value.ClientRuntimeID)}
	actor := domain.Principal{Type: domain.OwnerDevice}
	// Prove all original device/instance/lane checks are eligible first. An
	// unrelated missing lease must not make the storage exclusion pass vacuously.
	if err := f.service.Store.Read(owner, func(tx *store.Tx) error {
		_, _, _, err := f.service.peerForward(tx, actor, peer, true)
		return err
	}); err != nil {
		t.Fatal("original forward authority was not eligible", err)
	}
	for _, state := range []domain.WorkspaceStorageState{domain.WorkspaceStoragePending, domain.WorkspaceStorageUncertain, domain.WorkspaceStored} {
		_, err := f.service.Store.Mutate(owner, domain.NewID(), "storage.forward.state.fixture", state, func(tx *store.Tx) (any, error) {
			row, session, err := sessionRecord(tx, f.session.ID)
			if err != nil {
				return nil, err
			}
			session.Storage = &domain.WorkspaceStorage{State: state, JobID: domain.NewID(), SnapshotID: domain.NewID()}
			f.session, err = tx.Put(domain.SessionKind, row.ID, row.Revision, row.SessionID, row.ProjectID, session)
			return f.session, err
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.client.StartForward(f.ctx, ownerRequest(f.identity, &pb.StartForwardRequest{RequestId: string(domain.NewID()), SessionId: string(f.session.ID), MachineId: string(f.machine), ExpectedSessionRevision: f.session.Revision, WorkerPort: 46332}))
		if state == domain.WorkspaceStored {
			if connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("absent stored workspace admitted forward", err)
			}
		} else if err != nil {
			t.Fatal("unconfirmed storage blocked forward", err)
		}
		err = f.service.Store.Read(owner, func(tx *store.Tx) error {
			if _, _, _, err := f.service.peerForward(tx, actor, peer, true); (err == nil) != (state != domain.WorkspaceStored) {
				t.Fatal("storage admitted retained live socket authority", state, err)
			}
			// Closing the original native lifetime remains authorized independently
			// of storage availability, so this guard cannot trap pending cleanup.
			if _, _, _, err := f.service.peerForward(tx, actor, peer, false); err != nil {
				t.Fatal("storage blocked original cleanup authority", state, err)
			}
			forwards, err := tx.List(store.Filter{Kind: domain.ForwardKind, SessionID: f.session.ID, Limit: store.MaxPage})
			if err == nil && len(forwards) != map[domain.WorkspaceStorageState]int{domain.WorkspaceStoragePending: 2, domain.WorkspaceStorageUncertain: 3, domain.WorkspaceStored: 3}[state] {
				t.Fatal("blocked start published a new forward", len(forwards))
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
