// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestWorkspaceStorageWaitsForIndependentTerminalCleanup(t *testing.T) {
	f := newStorageFixture(t)
	request := f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", "")
	id := domain.NewID()
	var revision uint64
	states := []domain.Terminal{
		{State: domain.TerminalStarting},
		{State: domain.TerminalRunning},
		{State: domain.TerminalExited},
		{State: domain.TerminalClosed},
		{State: domain.TerminalUncertain},
		{State: domain.TerminalClosed, CleanupVerified: true},
	}
	for i, value := range states {
		_, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "storage.terminal.fixture", i, func(tx *store.Tx) (any, error) {
			r, err := tx.Put(domain.TerminalKind, id, revision, f.session, "", value)
			revision = r.Revision
			return r, err
		})
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, request))
		if value.CleanupVerified {
			if err != nil || accepted.Msg.Replayed || accepted.Msg.RequestId != request.Mutation.RequestId {
				t.Fatal("original storage request remained blocked after terminal cleanup", err)
			}
			continue
		}
		if connect.CodeOf(err) != connect.CodeAborted || f.sessionRecord().Revision != request.Mutation.ExpectedRevision {
			t.Fatal("unconfirmed terminal cleanup did not block storage atomically", value.State, err)
		}
		if err := f.service.Store.Read(f.ownerContext, func(tx *store.Tx) error {
			jobs, err := tx.List(store.Filter{Kind: domain.JobKind, SessionID: f.session, Limit: store.MaxPage})
			if err == nil && len(jobs) != 1 {
				t.Fatal("blocked storage created a native job", len(jobs))
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func newStorageTerminalFixture(t *testing.T) (*storageFixture, delidevv1connect.TerminalServiceClient, *pb.Resource) {
	t.Helper()
	f := newStorageFixture(t)
	ctx := context.Background()
	_, err := f.worker.AttachWorker(ctx, ownerRequest(f.workerIdentity, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: string(f.machine), InstanceId: string(f.instance), Version: rpc.Version, Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_SESSION_TERMINALS_V1}}))
	if err != nil {
		t.Fatal(err)
	}
	product := delidevv1connect.NewTerminalServiceClient(http.DefaultClient, f.server.URL)
	r := f.sessionRecord()
	created, err := product.CreateTerminal(ctx, ownerRequest(f.service.Identity, &pb.CreateTerminalRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Rows: 24, Columns: 80}))
	if err != nil {
		t.Fatal("terminal creation was not eligible before storage exclusion", err)
	}
	return f, product, created.Msg.Terminal
}

func setTerminalStorageState(t *testing.T, f *storageFixture, state domain.WorkspaceStorageState) {
	t.Helper()
	_, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "storage.terminal.state.fixture", state, func(tx *store.Tx) (any, error) {
		r, session, err := sessionRecord(tx, f.session)
		if err != nil {
			return nil, err
		}
		session.Storage = &domain.WorkspaceStorage{State: state, JobID: domain.NewID(), SnapshotID: domain.NewID()}
		return tx.Put(domain.SessionKind, r.ID, r.Revision, r.SessionID, r.ProjectID, session)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceStorageExcludesTerminalCreationAndOriginalClaims(t *testing.T) {
	f, product, original := newStorageTerminalFixture(t)
	ctx := context.Background()
	var value domain.Terminal
	if err := domain.Decode(original.DocumentJson, &value); err != nil {
		t.Fatal(err)
	}
	// These are authenticated ownership fixtures, not native shell acceptance.
	for _, state := range []domain.WorkspaceStorageState{domain.WorkspaceStoragePending, domain.WorkspaceStorageUncertain, domain.WorkspaceStored} {
		setTerminalStorageState(t, f, state)
		r := f.sessionRecord()
		_, err := product.CreateTerminal(ctx, ownerRequest(f.service.Identity, &pb.CreateTerminalRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Rows: 24, Columns: 80}))
		if connect.CodeOf(err) != connect.CodeAborted {
			t.Fatal("unavailable storage admitted a new terminal", state, err)
		}
		_, err = f.worker.ClaimTerminal(ctx, ownerRequest(f.workerIdentity, &pb.ClaimTerminalRequest{RequestId: string(domain.NewID()), MachineId: string(f.machine), InstanceId: string(f.instance), TerminalId: original.Id, OperationId: string(value.Pending.ID)}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatal("unavailable storage admitted an original native create claim", state, err)
		}
		if err := f.service.Store.Read(f.ownerContext, func(tx *store.Tx) error {
			records, err := tx.SessionTerminals(f.session)
			if err == nil && len(records) != 1 {
				t.Fatal("blocked creation published a new terminal", len(records))
			}
			_, current, err := terminalRecord(tx, domain.ID(original.Id))
			if err == nil && current.Pending.Claimed {
				t.Fatal("blocked claim consumed native authority")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	closed, err := product.ControlTerminal(ctx, ownerRequest(f.service.Identity, &pb.ControlTerminalRequest{Mutation: acctMutation(original, domain.NewID()), Action: pb.TerminalAction_TERMINAL_ACTION_CLOSE}))
	if err != nil || domain.Decode(closed.Msg.Terminal.DocumentJson, &value) != nil {
		t.Fatal("storage blocked original close acceptance", err)
	}
	_, err = f.worker.ClaimTerminal(ctx, ownerRequest(f.workerIdentity, &pb.ClaimTerminalRequest{RequestId: string(domain.NewID()), MachineId: string(f.machine), InstanceId: string(f.instance), TerminalId: original.Id, OperationId: string(value.CloseRequestID)}))
	if err != nil {
		t.Fatal("storage blocked original cleanup claim", err)
	}
}

func TestWorkspaceStorageExcludesTerminalInputAndResize(t *testing.T) {
	f, product, original := newStorageTerminalFixture(t)
	ctx := context.Background()
	var current store.Record
	setRunning := func() {
		t.Helper()
		_, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "storage.terminal.running.fixture", nil, func(tx *store.Tx) (any, error) {
			r, value, err := terminalRecord(tx, domain.ID(original.Id))
			if err != nil {
				return nil, err
			}
			value.State, value.Pending = domain.TerminalRunning, nil
			current, err = tx.Put(domain.TerminalKind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
			return current, err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range []pb.TerminalAction{pb.TerminalAction_TERMINAL_ACTION_INPUT, pb.TerminalAction_TERMINAL_ACTION_RESIZE} {
		setTerminalStorageState(t, f, domain.WorkspacePresent)
		setRunning()
		request := func() *pb.ControlTerminalRequest {
			r := &pb.ControlTerminalRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: original.Id, ExpectedRevision: current.Revision}, Action: action}
			if action == pb.TerminalAction_TERMINAL_ACTION_INPUT {
				r.Input = []byte("fixture input")
			} else {
				r.Rows, r.Columns = 25, 81
			}
			return r
		}
		if _, err := product.ControlTerminal(ctx, ownerRequest(f.service.Identity, request())); err != nil {
			t.Fatal("control was not eligible before storage exclusion", action, err)
		}
		for _, state := range []domain.WorkspaceStorageState{domain.WorkspaceStoragePending, domain.WorkspaceStorageUncertain, domain.WorkspaceStored} {
			setRunning()
			setTerminalStorageState(t, f, state)
			if _, err := product.ControlTerminal(ctx, ownerRequest(f.service.Identity, request())); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("unavailable storage admitted terminal control", state, action, err)
			}
			if err := f.service.Store.Read(f.ownerContext, func(tx *store.Tx) error {
				r, value, err := terminalRecord(tx, domain.ID(original.Id))
				if err == nil && (r.Revision != current.Revision || value.Pending != nil) {
					t.Fatal("blocked control published pending native work", state, action)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
}
