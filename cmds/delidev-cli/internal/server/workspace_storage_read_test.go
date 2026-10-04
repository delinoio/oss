// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http"
	"testing"
)

func TestWorkspaceReadsRejectUnavailableStorageBeforePublication(t *testing.T) {
	for _, state := range []domain.WorkspaceStorageState{domain.WorkspaceStoragePending, domain.WorkspaceStorageUncertain, domain.WorkspaceStored} {
		t.Run(string(state), func(t *testing.T) {
			f := newStorageFixture(t)
			_, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "fixture.storage-read-state", nil, func(tx *store.Tx) (any, error) {
				r, session, err := sessionRecord(tx, f.session)
				if err != nil {
					return nil, err
				}
				session.Storage = &domain.WorkspaceStorage{State: state, JobID: domain.NewID()}
				return tx.Put(domain.SessionKind, r.ID, r.Revision, r.SessionID, r.ProjectID, session)
			})
			if err != nil {
				t.Fatal(err)
			}
			client := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.server.URL)
			for _, operation := range []domain.WorkspaceReadOperation{domain.WorkspaceRoots, domain.WorkspaceFile, domain.WorkspaceDirectory} {
				query, _ := json.Marshal(domain.WorkspaceReadQuery{Operation: operation, Path: "note"})
				if operation == domain.WorkspaceRoots {
					query, _ = json.Marshal(domain.WorkspaceReadQuery{Operation: operation})
				}
				_, err := client.ReadSessionWorkspace(context.Background(), ownerRequest(f.service.Identity, &pb.ReadSessionWorkspaceRequest{SessionId: string(f.session), QueryJson: query}))
				if connect.CodeOf(err) != connect.CodeUnavailable {
					t.Fatal(operation, "read published without present storage", err)
				}
			}
		})
	}
}

func TestWorkspaceReadPublicationRechecksStorageAfterInitialScope(t *testing.T) {
	f := newFirstDispatchFixture(t)
	ctx := context.Background()
	var input workspace.PrepareRequest
	var manifest workspace.Manifest
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		var err error
		input, manifest, err = workspaceReadScope(tx, domain.ID(f.change.Session.Id))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stream, err := f.workerClient.WatchWorkspaceReads(ctx, ownerRequest(f.workerIdentity, &pb.WatchWorkspaceReadsRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() || !stream.Msg().Heartbeat {
		t.Fatal(stream.Err())
	}
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.storage-read-race", nil, func(tx *store.Tx) (any, error) {
		r, session, err := sessionRecord(tx, domain.ID(f.change.Session.Id))
		if err != nil {
			return nil, err
		}
		session.Storage = &domain.WorkspaceStorage{State: domain.WorkspaceStoragePending, JobID: domain.NewID()}
		return tx.Put(domain.SessionKind, r.ID, r.Revision, r.SessionID, r.ProjectID, session)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.observeWorkerWorkspace(ctx, domain.ID(f.change.Session.Id), input, manifest, domain.WorkspaceReadQuery{Operation: domain.WorkspaceFile, Path: "note"}, nil); domain.SafeError(err).Code != domain.Unavailable {
		t.Fatal("stale scope published", err)
	}
	f.service.workspaceReadsMu.Lock()
	reader := f.service.workspaceReaders[input.MachineID]
	pending, queued := reader.pending, len(reader.requests)
	f.service.workspaceReadsMu.Unlock()
	if pending != nil || queued != 0 {
		t.Fatal("unavailable read reached Worker lane")
	}
}
