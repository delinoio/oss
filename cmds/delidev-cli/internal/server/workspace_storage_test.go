// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	if _, err := f.worker.AttachWorker(context.Background(), ownerRequest(identity, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: string(f.machine), InstanceId: string(f.instance), Version: rpc.Version})); err != nil {
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
		digest := sha256.Sum256([]byte(f.workerIdentity.Token))
		actor, err := tx.Authenticate(digest[:])
		if err != nil {
			return nil, err
		}
		j.AssignedDeviceID = actor.DeviceID
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
	reported, err := f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: claimed.Id, ExpectedRevision: claimed.Revision}, MachineId: string(f.machine), InstanceId: string(f.instance), OutputJson: raw}))
	if err != nil {
		f.t.Fatal(err)
	}
	var final domain.Job
	if domain.Decode(reported.Msg.Job.DocumentJson, &final) != nil || final.State != domain.JobSucceeded {
		f.t.Fatal("native storage result did not settle accepted job", final.State)
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

func TestWorkspaceStorageFailedRecoveryValidatesOriginalAction(t *testing.T) {
	for _, action := range []workspace.StorageAction{workspace.StoragePreview, workspace.StorageCreate, workspace.StorageCleanup, workspace.StorageInspect, workspace.StorageRestore, workspace.StorageDelete} {
		t.Run(string(action), func(t *testing.T) {
			session, machine, snapshot := domain.NewID(), domain.NewID(), domain.NewID()
			original := workspace.StorageRequest{OperationID: domain.NewID(), Action: action, PreviousState: domain.WorkspacePresent, SnapshotID: snapshot, Preparation: workspace.PrepareRequest{SessionID: session, MachineID: machine}}
			if action == workspace.StorageRestore {
				original.PreviousState = domain.WorkspaceStored
			}
			output := workspace.StorageResult{Version: 1, OperationID: domain.NewID(), Action: workspace.StorageRecover, SessionID: session, MachineID: machine, CleanupVerified: true, RecoveredJobID: original.OperationID, RecoveredJobState: domain.JobFailed, WorkspaceState: original.PreviousState, PreviewDigest: strings.Repeat("b", 64)}
			if action == workspace.StorageInspect || action == workspace.StorageRestore || action == workspace.StorageDelete {
				original.SnapshotDigest = strings.Repeat("a", 64)
				output.Snapshot = &workspace.SnapshotMetadata{ID: snapshot, SessionID: session, MachineID: machine, SHA256: original.SnapshotDigest, CreatedAt: time.Now().UTC()}
			}
			input := workspace.StorageRequest{OperationID: output.OperationID, Action: workspace.StorageRecover, SnapshotID: snapshot, Preparation: original.Preparation, Recovery: &workspace.StorageRecovery{Original: original}}
			check := func(output workspace.StorageResult) error {
				raw, _ := json.Marshal(output)
				return validateWorkspaceStorageResult(input, raw)
			}
			if err := check(output); err != nil {
				t.Fatal("valid preserved outcome rejected", err)
			}
			bad := output
			bad.WorkspaceState = domain.WorkspaceStored
			if output.WorkspaceState == domain.WorkspaceStored {
				bad.WorkspaceState = domain.WorkspacePresent
			}
			if check(bad) == nil {
				t.Fatal("failed action changed availability")
			}
			bad = output
			bad.SourceBytes, bad.RemovedSourceBytes = 1, 1
			if check(bad) == nil {
				t.Fatal("failed action claimed removed bytes")
			}
			if output.Snapshot != nil {
				bad = output
				bad.Snapshot = nil
				if check(bad) == nil {
					t.Fatal("required retained snapshot omitted")
				}
				changed := *output.Snapshot
				changed.SHA256 = strings.Repeat("c", 64)
				bad = output
				bad.Snapshot = &changed
				if check(bad) == nil {
					t.Fatal("retained snapshot digest changed")
				}
				changed = *output.Snapshot
				changed.Deleted = true
				bad.Snapshot = &changed
				if check(bad) == nil {
					t.Fatal("failed deletion claimed a deleted artifact")
				}
			}
		})
	}
}

func TestWorkspaceStorageFailedCleanupRecoveryCannotInventStoredCopy(t *testing.T) {
	f := newStorageFixture(t)
	preview, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", "")))
	if err != nil {
		t.Fatal(err)
	}
	f.execute(preview.Msg.Job)
	cleanup, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CLEANUP, "", preview.Msg.Job.Id, "")))
	if err != nil {
		t.Fatal(err)
	}
	claimed := f.claim(cleanup.Msg.Job)
	_, err = f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: claimed.Id, ExpectedRevision: claimed.Revision}, MachineId: string(f.machine), InstanceId: string(f.instance), Problem: &pb.ErrorDetail{Code: string(domain.RecoveryRequired)}}))
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RECOVER, "", "", claimed.Id)))
	if err != nil {
		t.Fatal(err)
	}
	assigned := f.claim(recovery.Msg.Job)
	var job domain.Job
	if domain.Decode(assigned.DocumentJson, &job) != nil {
		t.Fatal("invalid assignment")
	}
	var input workspace.StorageRequest
	if domain.Decode(job.Input, &input) != nil {
		t.Fatal("invalid recovery input")
	}
	output, err := f.manager.Storage(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	output.WorkspaceState = domain.WorkspaceStored
	raw, _ := json.Marshal(output)
	reported, err := f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: assigned.Id, ExpectedRevision: assigned.Revision}, MachineId: string(f.machine), InstanceId: string(f.instance), OutputJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	if domain.Decode(reported.Msg.Job.DocumentJson, &job) != nil || job.State != domain.JobUncertain {
		t.Fatal("malformed recovery settled ownership")
	}
	state, err := store.Decode[domain.Session](f.sessionRecord())
	if err != nil || state.Storage.State != domain.WorkspaceStorageUncertain || state.Storage.SnapshotID != "" {
		t.Fatal("malformed recovery invented stored copy", err)
	}
}

func TestWorkspaceStorageUnsuccessfulRecoveryRestoresPredecessor(t *testing.T) {
	for _, outcome := range []string{"queued-canceled", "claimed-canceled", "claimed-failed", "nested-queued-canceled", "nested-claimed-canceled", "nested-claimed-failed"} {
		t.Run(outcome, func(t *testing.T) {
			f := newStorageFixture(t)
			preview, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", "")))
			if err != nil {
				t.Fatal(err)
			}
			f.execute(preview.Msg.Job)
			cleanup, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CLEANUP, "", preview.Msg.Job.Id, "")))
			if err != nil {
				t.Fatal(err)
			}
			claimed := f.claim(cleanup.Msg.Job)
			_, err = f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: claimed.Id, ExpectedRevision: claimed.Revision}, MachineId: string(f.machine), InstanceId: string(f.instance), Problem: &pb.ErrorDetail{Code: string(domain.RecoveryRequired)}}))
			if err != nil {
				t.Fatal(err)
			}
			recovery, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RECOVER, "", "", claimed.Id)))
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(outcome, "nested-") {
				intermediate := f.claim(recovery.Msg.Job)
				if _, err := f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: intermediate.Id, ExpectedRevision: intermediate.Revision}, MachineId: string(f.machine), InstanceId: string(f.instance), Problem: &pb.ErrorDetail{Code: string(domain.RecoveryRequired)}})); err != nil {
					t.Fatal(err)
				}
				claimed = intermediate
				recovery, err = f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RECOVER, "", "", claimed.Id)))
				if err != nil {
					t.Fatal(err)
				}
				outcome = strings.TrimPrefix(outcome, "nested-")
			}
			if outcome == "queued-canceled" {
				cancel, err := f.client.CancelWorkspaceStorageOperation(context.Background(), ownerRequest(f.service.Identity, &pb.CancelWorkspaceStorageOperationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: recovery.Msg.Job.Id, ExpectedRevision: recovery.Msg.Job.Revision}}))
				if err != nil {
					t.Fatal(err)
				}
				var canceled domain.Job
				if domain.Decode(cancel.Msg.Job.DocumentJson, &canceled) != nil || canceled.State != domain.JobCanceled {
					t.Fatal("queued recovery was not canceled")
				}
			} else {
				assigned := f.claim(recovery.Msg.Job)
				code := domain.ResourceExhausted
				if outcome == "claimed-canceled" {
					code = domain.Canceled
				}
				if _, err := f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: assigned.Id, ExpectedRevision: assigned.Revision}, MachineId: string(f.machine), InstanceId: string(f.instance), Problem: &pb.ErrorDetail{Code: string(code)}})); err != nil {
					t.Fatal(err)
				}
			}
			state, err := store.Decode[domain.Session](f.sessionRecord())
			if err != nil || state.Storage.State != domain.WorkspaceStorageUncertain || state.Storage.JobID != domain.ID(claimed.Id) {
				t.Fatal("unsuccessful recovery did not restore uncertain predecessor", err)
			}
			if _, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RECOVER, "", "", claimed.Id))); err != nil {
				t.Fatal("predecessor could not be recovered after unsuccessful recovery", err)
			}

		})
	}
}

func TestWorkspaceStorageAdmissionReservesPermanentDeletionJobCapacity(t *testing.T) {
	f := newStorageFixture(t)
	state, err := store.Decode[domain.Session](f.sessionRecord())
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(f.ownerContext, domain.NewID(), "storage.capacity.fixture", nil, func(tx *store.Tx) (any, error) {
		original, err := tx.Get(domain.JobKind, state.Preparation.JobID)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](original)
		if err != nil {
			return nil, err
		}
		// The existing preparation plus these records leave one complete recovery lineage.
		for i := 0; i < 4086; i++ {
			if _, err := tx.PutJob(domain.NewID(), 0, f.session, "", job); err != nil {
				return nil, err
			}
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", "")))
	if err != nil {
		t.Fatal("final deletion-plan slot was not usable", err)
	}
	if _, err := f.client.CancelWorkspaceStorageOperation(context.Background(), ownerRequest(f.service.Identity, &pb.CancelWorkspaceStorageOperationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: accepted.Msg.Job.Id, ExpectedRevision: accepted.Msg.Job.Revision}})); err != nil {
		t.Fatal(err)
	}
	before := f.sessionRecord().Revision
	if _, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", ""))); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatal("admission exceeded permanent-deletion inventory", err)
	}
	if f.sessionRecord().Revision != before {
		t.Fatal("rejected storage changed the session")
	}
}

func TestWorkspaceCleanupPreservesRestoreAdmissionAtCapacity(t *testing.T) {
	for _, variant := range []struct {
		existing int
		failure  string
	}{{4078, ""}, {4078, "queued-canceled"}, {4078, "claimed-failed"}, {4079, ""}} {
		existing := variant.existing
		t.Run(fmt.Sprint(existing, "/", variant.failure), func(t *testing.T) {
			f := newStorageFixture(t)
			preview, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", "")))
			if err != nil {
				t.Fatal(err)
			}
			f.execute(preview.Msg.Job)
			state, err := store.Decode[domain.Session](f.sessionRecord())
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.service.Store.Mutate(f.ownerContext, domain.NewID(), "storage.restore.capacity.fixture", nil, func(tx *store.Tx) (any, error) {
				record, err := tx.Get(domain.JobKind, state.Preparation.JobID)
				if err != nil {
					return nil, err
				}
				job, err := store.Decode[domain.Job](record)
				if err != nil {
					return nil, err
				}
				for i := 2; i < existing; i++ {
					if _, err := tx.PutJob(domain.NewID(), 0, f.session, "", job); err != nil {
						return nil, err
					}
				}
				return true, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			before := f.sessionRecord().Revision
			cleanup, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CLEANUP, "", preview.Msg.Job.Id, "")))
			if existing == 4079 {
				if connect.CodeOf(err) != connect.CodeResourceExhausted || f.sessionRecord().Revision != before {
					t.Fatal("cleanup consumed the final restore slot", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// Lose the original and seven recovery reports. The eighth recovery
			// settles the same native operation at the exact inventory bound.
			settleAfterLostReports := func(resource *pb.Resource) workspace.StorageResult {
				attempts := workspace.MaxStorageRecoveryClaims
				if variant.failure != "" {
					attempts--
				}
				for attempt := 0; attempt < attempts; attempt++ {
					assigned := f.claim(resource)
					var original domain.Job
					var input workspace.StorageRequest
					if domain.Decode(assigned.DocumentJson, &original) != nil || workspace.DecodeStorageRequest(original.Input, &input) != nil {
						t.Fatal("invalid lost-report assignment")
					}
					if _, err := f.manager.Storage(context.Background(), input); err != nil {
						t.Fatal(err)
					}
					_, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "fixture.storage.expired-worker", nil, func(tx *store.Tx) (any, error) {
						return nil, tx.SetWorkerInstance(f.machine, f.instance, time.Now().UTC().Add(-2*workerLease))
					})
					if err != nil {
						t.Fatal(err)
					}
					f.instance = domain.NewID()
					if _, err := f.worker.AttachWorker(context.Background(), ownerRequest(f.workerIdentity, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: string(f.machine), InstanceId: string(f.instance), Version: rpc.Version})); err != nil {
						t.Fatal(err)
					}
					recovery, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RECOVER, "", "", assigned.Id)))
					if err != nil {
						t.Fatal("reserved recovery lineage exhausted early", attempt, err)
					}
					if attempt == 0 && variant.failure != "" {
						if variant.failure == "queued-canceled" {
							_, err = f.client.CancelWorkspaceStorageOperation(context.Background(), ownerRequest(f.service.Identity, &pb.CancelWorkspaceStorageOperationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: recovery.Msg.Job.Id, ExpectedRevision: recovery.Msg.Job.Revision}}))
						} else {
							failed := f.claim(recovery.Msg.Job)
							_, err = f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: failed.Id, ExpectedRevision: failed.Revision}, MachineId: string(f.machine), InstanceId: string(f.instance), Problem: &pb.ErrorDetail{Code: string(domain.ResourceExhausted)}}))
						}
						if err != nil {
							t.Fatal(err)
						}
						recovery, err = f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RECOVER, "", "", assigned.Id)))
						if err != nil {
							t.Fatal("terminal recovery consumed reserved successor", err)
						}
					}
					resource = recovery.Msg.Job
				}
				return f.execute(resource)
			}
			output := settleAfterLostReports(cleanup.Msg.Job)
			if output.WorkspaceState != domain.WorkspaceStored {
				t.Fatal("fixture cleanup did not store workspace")
			}
			inspect, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_INSPECT, string(output.Snapshot.ID), "", "")))
			if connect.CodeOf(err) != connect.CodeResourceExhausted || inspect != nil {
				t.Fatal("inspection consumed reserved restore lineage", err)
			}
			restore, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RESTORE, string(output.Snapshot.ID), "", "")))
			if err != nil {
				t.Fatal("stored workspace lost its full restore lineage", err)
			}
			restored := settleAfterLostReports(restore.Msg.Job)
			if restored.WorkspaceState != domain.WorkspacePresent || !restored.CleanupVerified {
				t.Fatal("last-slot restore did not complete")
			}
		})
	}
}

func TestWorkspaceStorageSurvivingBytesAndCleanupPreviewAreBound(t *testing.T) {
	for _, action := range []workspace.StorageAction{workspace.StorageCreate, workspace.StorageCleanup, workspace.StorageInspect, workspace.StorageRestore} {
		for _, recovering := range []bool{false, true} {
			t.Run(fmt.Sprint(action, recovering), func(t *testing.T) {
				snapshot := &workspace.SnapshotMetadata{ID: domain.NewID(), SessionID: domain.NewID(), MachineID: domain.NewID(), SHA256: strings.Repeat("a", 64), SizeBytes: 123, CreatedAt: time.Now().UTC()}
				input := workspace.StorageRequest{OperationID: domain.NewID(), Action: action, PreviousState: domain.WorkspacePresent, SnapshotID: snapshot.ID, PreviewDigest: strings.Repeat("b", 64), Preparation: workspace.PrepareRequest{SessionID: snapshot.SessionID, MachineID: snapshot.MachineID}}
				output := workspace.StorageResult{Version: 1, OperationID: input.OperationID, Action: action, SessionID: snapshot.SessionID, MachineID: snapshot.MachineID, Snapshot: snapshot, CleanupVerified: true, WorkspaceState: domain.WorkspacePresent, RetainedSnapshotBytes: 123, PreviewDigest: input.PreviewDigest}
				if action == workspace.StorageCleanup {
					output.WorkspaceState = domain.WorkspaceStored
					output.SourceBytes = 7
					output.RemovedSourceBytes = 7
				}
				if recovering {
					original := input
					input.OperationID = domain.NewID()
					input.Action = workspace.StorageRecover
					input.Recovery = &workspace.StorageRecovery{Original: original}
					output.OperationID = input.OperationID
					output.Action = workspace.StorageRecover
					output.RecoveredJobID = original.OperationID
					output.RecoveredJobState = domain.JobSucceeded
				}
				check := func(v workspace.StorageResult) error {
					raw, _ := json.Marshal(v)
					return validateWorkspaceStorageResult(input, raw)
				}
				if err := check(output); err != nil {
					t.Fatal("valid retained result", err)
				}
				bad := output
				bad.RetainedSnapshotBytes = 122
				if check(bad) == nil {
					t.Fatal("underreported retained snapshot accepted")
				}
				if action == workspace.StorageCleanup {
					for _, digest := range []string{"", strings.Repeat("c", 64), strings.ToUpper(input.PreviewDigest)} {
						bad = output
						bad.PreviewDigest = digest
						if check(bad) == nil {
							t.Fatal("unbound cleanup preview accepted", digest)
						}
					}
				}
			})
		}
	}
}
