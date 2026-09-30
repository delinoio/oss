// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type storageFixture struct {
	t                          *testing.T
	service                    *Service
	server                     *httptest.Server
	client                     delidevv1connect.WorkspaceStorageServiceClient
	worker                     delidevv1connect.WorkerServiceClient
	workerIdentity             security.Identity
	machine, instance, session domain.ID
	manager                    *workspace.Manager
	ownerContext               context.Context
}

func newStorageFixture(t *testing.T) *storageFixture {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: db, Identity: security.Identity{ServerID: domain.NewID(), Token: "isolated-storage-owner"}, logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	server := httptest.NewServer(service.Handler(nil, true))
	t.Cleanup(func() {
		service.executionAuthority.cancel()
		server.Close()
		service.executionAuthority.close()
		db.Close()
	})
	identity, paired := pairedWorker(t, context.Background(), Endpoint{URL: server.URL, ServerID: service.Identity.ServerID}, service.Identity)
	f := &storageFixture{t: t, service: service, server: server, workerIdentity: identity, machine: domain.ID(paired.Machine.Id), instance: domain.NewID(), session: domain.NewID(), ownerContext: domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})}
	f.client = delidevv1connect.NewWorkspaceStorageServiceClient(http.DefaultClient, server.URL)
	f.worker = delidevv1connect.NewWorkerServiceClient(http.DefaultClient, server.URL)
	if _, err := f.worker.AttachWorker(context.Background(), ownerRequest(identity, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: string(f.machine), InstanceId: string(f.instance), Version: rpc.Version})); err != nil {
		t.Fatal(err)
	}
	f.manager = &workspace.Manager{Root: filepath.Join(t.TempDir(), "worker"), Logger: service.logger}
	preparation := workspace.PrepareRequest{SessionID: f.session, MachineID: f.machine, Type: domain.GeneralChat}
	manifest, err := f.manager.Prepare(context.Background(), preparation)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(manifest.PrimaryPath, "note"), []byte("private workspace"), 0600)
	_, err = db.Mutate(f.ownerContext, domain.NewID(), "storage.fixture", nil, func(tx *store.Tx) (any, error) {
		input, _ := json.Marshal(preparation)
		output, _ := json.Marshal(manifest)
		id := domain.NewID()
		if _, err := tx.PutJob(id, 0, f.session, "", domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobSucceeded, MachineID: f.machine, Input: input, Output: output, AcceptedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		return tx.Put(domain.SessionKind, f.session, 0, f.session, "", domain.Session{Name: "Fixture", AgentID: domain.NewID(), MachineID: f.machine, Workspace: domain.GeneralChat, Source: domain.ExternalCLISession, Outcome: domain.ExecutionNotStarted, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Dispatch: domain.DispatchPaused, Preparation: &domain.SessionPreparation{JobID: id, State: domain.PreparationReady}})
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *storageFixture) sessionRecord() store.Record {
	f.t.Helper()
	var r store.Record
	err := f.service.Store.Read(f.ownerContext, func(tx *store.Tx) error { var err error; r, err = tx.Get(domain.SessionKind, f.session); return err })
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}
func (f *storageFixture) request(action pb.WorkspaceStorageAction, snapshot, preview, recovery string) *pb.RequestWorkspaceStorageRequest {
	r := f.sessionRecord()
	return &pb.RequestWorkspaceStorageRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: action, SnapshotId: snapshot, PreviewJobId: preview, RecoveryJobId: recovery}
}
func (f *storageFixture) claim(job *pb.Resource) *pb.Resource {
	f.t.Helper()
	var claimed store.Record
	_, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "storage.fixture.claim", job.Id, func(tx *store.Tx) (any, error) {
		record, err := tx.Get(domain.JobKind, domain.ID(job.Id))
		if err != nil {
			return nil, err
		}
		j, err := store.Decode[domain.Job](record)
		if err != nil {
			return nil, err
		}
		j.State = domain.JobClaimed
		j.InstanceID = f.instance
		// Paired worker report authorization still independently checks its machine
		// and current instance through the real authenticated Connect handler.
		claimed, err = tx.PutJob(record.ID, record.Revision, record.SessionID, record.ProjectID, j)
		return nil, err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return rpc.Resource(claimed)
}
func (f *storageFixture) execute(job *pb.Resource) workspace.StorageResult {
	f.t.Helper()
	claimed := f.claim(job)
	var j domain.Job
	if domain.Decode(claimed.DocumentJson, &j) != nil {
		f.t.Fatal("invalid claimed job")
	}
	var input workspace.StorageRequest
	if domain.Decode(j.Input, &input) != nil {
		f.t.Fatal("invalid storage input")
	}
	output, err := f.manager.Storage(context.Background(), input)
	if err != nil {
		f.t.Fatal(err)
	}
	raw, _ := json.Marshal(output)
	_, err = f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: claimed.Id, ExpectedRevision: claimed.Revision}, MachineId: string(f.machine), InstanceId: string(f.instance), OutputJson: raw}))
	if err != nil {
		f.t.Fatal(err)
	}
	return output
}
func TestWorkspaceStorageRPCReceiptsResumeRaceAndOnlyCopyProtection(t *testing.T) {
	f := newStorageFixture(t)
	previewRequest := f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", "")
	if _, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.workerIdentity, previewRequest)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker received owner storage authority", err)
	}
	accepted, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, previewRequest))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, previewRequest))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Msg.Job.Id {
		t.Fatal("acceptance replay duplicated work", err)
	}
	current := f.sessionRecord()
	sessions := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.server.URL)
	_, err = sessions.ControlSession(context.Background(), ownerRequest(f.service.Identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.session), ExpectedRevision: current.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("Resume bypassed storage reservation", err)
	}
	f.execute(accepted.Msg.Job)
	cleanupRequest := f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CLEANUP, "", accepted.Msg.Job.Id, "")
	cleanup, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, cleanupRequest))
	if err != nil {
		t.Fatal(err)
	}
	output := f.execute(cleanup.Msg.Job)
	if output.Snapshot == nil {
		t.Fatal("snapshot metadata absent")
	}
	state, err := store.Decode[domain.Session](f.sessionRecord())
	if err != nil || state.Storage.State != domain.WorkspaceStored || state.Dispatch != domain.DispatchPaused {
		t.Fatal("cleanup did not atomically retain paused snapshot state", err)
	}
	_, err = f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_DELETE, string(output.Snapshot.ID), "", "")))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("only recoverable copy was deletable", err)
	}
	restore, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RESTORE, string(output.Snapshot.ID), "", "")))
	if err != nil {
		t.Fatal(err)
	}
	f.execute(restore.Msg.Job)
	state, err = store.Decode[domain.Session](f.sessionRecord())
	if err != nil || !state.WorkspaceAvailable() || state.Dispatch != domain.DispatchPaused {
		t.Fatal("restore resumed or left partial storage state", err)
	}
	original, err := f.client.GetWorkspaceStorageOperation(context.Background(), ownerRequest(f.service.Identity, &pb.GetWorkspaceStorageOperationRequest{Id: cleanup.Msg.Job.Id}))
	if err != nil {
		t.Fatal(err)
	}
	var job domain.Job
	domain.Decode(original.Msg.Job.DocumentJson, &job)
	if job.State != domain.JobSucceeded {
		t.Fatal("accepted job lost terminal state")
	}
}
func TestWorkspaceStorageQueuedCancellationAndMalformedReport(t *testing.T) {
	f := newStorageFixture(t)
	accepted, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CREATE, "", "", "")))
	if err != nil {
		t.Fatal(err)
	}
	cancel := &pb.CancelWorkspaceStorageOperationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: accepted.Msg.Job.Id, ExpectedRevision: accepted.Msg.Job.Revision}}
	response, err := f.client.CancelWorkspaceStorageOperation(context.Background(), ownerRequest(f.service.Identity, cancel))
	if err != nil {
		t.Fatal(err)
	}
	var job domain.Job
	domain.Decode(response.Msg.Job.DocumentJson, &job)
	if job.State != domain.JobCanceled {
		t.Fatal("queued cancellation executed")
	}
	replay, err := f.client.CancelWorkspaceStorageOperation(context.Background(), ownerRequest(f.service.Identity, cancel))
	if err != nil || !replay.Msg.Replayed {
		t.Fatal("cancellation replay failed", err)
	}
	accepted, err = f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", "")))
	if err != nil {
		t.Fatal(err)
	}
	claimed := f.claim(accepted.Msg.Job)
	report, err := f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: claimed.Id, ExpectedRevision: claimed.Revision}, MachineId: string(f.machine), InstanceId: string(f.instance), OutputJson: []byte(`{"cleanup_verified":true}`)}))
	if err != nil {
		t.Fatal(err)
	}
	domain.Decode(report.Msg.Job.DocumentJson, &job)
	if job.State != domain.JobUncertain {
		t.Fatal("malformed proof settled ownership")
	}
	if _, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CREATE, "", "", ""))); err == nil {
		t.Fatal("uncertain work lost reservation")
	}
	recovery, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RECOVER, "", "", claimed.Id)))
	if err != nil {
		t.Fatal(err)
	}
	f.execute(recovery.Msg.Job)
	state, _ := store.Decode[domain.Session](f.sessionRecord())
	if !state.WorkspaceAvailable() || state.Dispatch != domain.DispatchPaused {
		t.Fatal("reconciliation lost paused live workspace")
	}
}
