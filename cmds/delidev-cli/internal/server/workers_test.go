package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"runtime"
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

func randomCode() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func pairedWorker(t *testing.T, ctx context.Context, endpoint Endpoint, owner security.Identity) (security.Identity, *pb.PairDeviceResponse) {
	t.Helper()
	client := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, endpoint.URL)
	code, token := randomCode(), randomCode()
	digest, verifier := sha256.Sum256([]byte(code)), sha256.Sum256([]byte(token))
	grant, err := client.CreatePairing(ctx, ownerRequest(owner, &pb.CreatePairingRequest{RequestId: string(domain.NewID()), Name: "test worker", Type: pb.DeviceType_DEVICE_TYPE_WORKER, CodeDigest: digest[:]}))
	if err != nil {
		t.Fatal(err)
	}
	machine, _ := json.Marshal(domain.Machine{Name: "test", OS: runtime.GOOS, Architecture: runtime.GOARCH, Version: rpc.Version})
	input := &pb.PairDeviceRequest{RequestId: string(domain.NewID()), PairingId: grant.Msg.Pairing.Id, Code: code, DeviceId: string(domain.NewID()), MachineId: string(domain.NewID()), MachineJson: machine, CredentialDigest: verifier[:]}
	paired, err := client.PairDevice(ctx, connect.NewRequest(input))
	if err != nil {
		t.Fatal(err)
	}
	retry, err := client.PairDevice(ctx, connect.NewRequest(input))
	if err != nil || !retry.Msg.Replayed || retry.Msg.Device.Id != paired.Msg.Device.Id {
		t.Fatalf("pair retry duplicated: %v %v", retry, err)
	}
	input.RequestId = string(domain.NewID())
	input.DeviceId = string(domain.NewID())
	input.MachineId = string(domain.NewID())
	if _, err := client.PairDevice(ctx, connect.NewRequest(input)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("pairing reused: %v", err)
	}
	return security.Identity{Token: token}, paired.Msg
}
func TestWorkerPairingOwnershipDispatchAndRevocation(t *testing.T) {
	endpoint, owner, stop, done := runTestServer(t, filepath.Join(t.TempDir(), "state"))
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	one, device := pairedWorker(t, ctx, endpoint, owner)
	two, other := pairedWorker(t, ctx, endpoint, owner)
	client := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, endpoint.URL)
	instance := string(domain.NewID())
	if _, err := client.AttachWorker(ctx, ownerRequest(two, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: device.Machine.Id, InstanceId: instance, Version: rpc.Version})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("foreign machine attached: %v", err)
	}
	attach := &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: device.Machine.Id, InstanceId: instance, Version: rpc.Version, Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_AUTOMATIC_TITLES_CODEX_V1, pb.WorkerCapability_WORKER_CAPABILITY_SESSION_FORWARDING_V1, pb.WorkerCapability_WORKER_CAPABILITY_SESSION_TERMINALS_V1}}
	// Verify duplicate rejection across the entire negotiated set, including
	// non-adjacent entries and duplicates after both older capabilities.
	for _, capabilities := range [][]pb.WorkerCapability{
		{attach.Capabilities[0], attach.Capabilities[0]},
		{attach.Capabilities[0], attach.Capabilities[1], attach.Capabilities[0]},
		{attach.Capabilities[0], attach.Capabilities[1], attach.Capabilities[2], attach.Capabilities[2]},
	} {
		invalid := &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: device.Machine.Id, InstanceId: instance, Version: rpc.Version, Capabilities: capabilities}
		if _, err := client.AttachWorker(ctx, ownerRequest(one, invalid)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("duplicate Worker capability accepted: %v", err)
		}
	}
	if _, err := client.AttachWorker(ctx, ownerRequest(one, attach)); err != nil {
		t.Fatal(err)
	}
	second := &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: device.Machine.Id, InstanceId: string(domain.NewID()), Version: rpc.Version}
	if _, err := client.AttachWorker(ctx, ownerRequest(one, second)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("live instance replaced: %v", err)
	}
	stream, err := client.WatchWork(ctx, ownerRequest(one, &pb.WatchWorkRequest{MachineId: device.Machine.Id, InstanceId: instance}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() || !stream.Msg().Heartbeat {
		t.Fatal("missing readiness heartbeat", stream.Err())
	}
	auxiliaryCtx, stopAuxiliary := context.WithCancel(ctx)
	defer stopAuxiliary()
	auxiliary, err := client.WatchAuxiliaryWork(auxiliaryCtx, ownerRequest(one, &pb.WatchAuxiliaryWorkRequest{MachineId: device.Machine.Id, InstanceId: instance}))
	if err != nil {
		t.Fatal(err)
	}
	if !auxiliary.Receive() || !auxiliary.Msg().Heartbeat {
		t.Fatal("paired Worker credential could not open its auxiliary work stream", auxiliary.Err())
	}
	inspect := &pb.InspectRepositoryRequest{RequestId: string(domain.NewID()), MachineId: device.Machine.Id, Path: "/example/checkout", PreferredRemote: "origin"}
	if _, err := client.InspectRepository(ctx, ownerRequest(one, inspect)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("worker invoked owner mutation: %v", err)
	}
	job, err := client.InspectRepository(ctx, ownerRequest(owner, inspect))
	if err != nil {
		t.Fatal(err)
	}
	retry, err := client.InspectRepository(ctx, ownerRequest(owner, inspect))
	if err != nil || retry.Msg.Job.Id != job.Msg.Job.Id || !retry.Msg.Replayed {
		t.Fatalf("inspect retry: %v %v", retry, err)
	}
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	claimed := stream.Msg().Job
	if claimed == nil || claimed.Id != job.Msg.Job.Id {
		t.Fatal("wrong assigned job")
	}
	output, _ := json.Marshal(workspace.Inspection{Root: "/example/checkout", Name: "checkout", Remotes: []string{"origin"}, DefaultRefs: map[string]string{"origin": "main"}})
	report := &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: claimed.Id, ExpectedRevision: claimed.Revision}, MachineId: device.Machine.Id, InstanceId: instance, OutputJson: output}
	if _, err := client.ReportWork(ctx, ownerRequest(two, report)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("foreign completion accepted: %v", err)
	}
	result, err := client.ReportWork(ctx, ownerRequest(one, report))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := client.ReportWork(ctx, ownerRequest(one, report))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Revision != result.Msg.Job.Revision {
		t.Fatalf("report replay failed: %v", err)
	}
	resources := delidevv1connect.NewResourceServiceClient(http.DefaultClient, endpoint.URL)
	if _, err := resources.GetResource(ctx, ownerRequest(one, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_DEVICE, Id: other.Device.Id})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("worker read owner resource: %v", err)
	}
	devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, endpoint.URL)

	unconfirmed, err := client.InspectRepository(ctx, ownerRequest(owner, &pb.InspectRepositoryRequest{RequestId: string(domain.NewID()), MachineId: device.Machine.Id, Path: "/tmp/pending"}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() || stream.Msg().Job == nil || stream.Msg().Job.Id != unconfirmed.Msg.Job.Id {
		t.Fatal("missing unconfirmed assignment")
	}
	queued, err := client.InspectRepository(ctx, ownerRequest(owner, &pb.InspectRepositoryRequest{RequestId: string(domain.NewID()), MachineId: other.Machine.Id, Path: "/tmp/queued"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := devices.RevokeDevice(ctx, ownerRequest(owner, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: other.Device.Id, ExpectedRevision: other.Device.Revision}})); err != nil {
		t.Fatal(err)
	}
	queuedResult, err := resources.GetResource(ctx, ownerRequest(owner, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_JOB, Id: queued.Msg.Job.Id}))
	if err != nil {
		t.Fatal(err)
	}
	var queuedJob domain.Job
	if err := domain.Decode(queuedResult.Msg.Resource.DocumentJson, &queuedJob); err != nil {
		t.Fatal(err)
	}
	if queuedJob.State != domain.JobCanceled {
		t.Fatal("revoked queued work remained runnable")
	}
	revoke := &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: device.Device.Id, ExpectedRevision: device.Device.Revision}}
	if _, err := devices.RevokeDevice(ctx, ownerRequest(owner, revoke)); err != nil {
		t.Fatal(err)
	}
	if stream.Receive() {
		t.Fatal("revoked stream remained open")
	}
	uncertain, err := resources.GetResource(ctx, ownerRequest(owner, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_JOB, Id: unconfirmed.Msg.Job.Id}))
	if err != nil {
		t.Fatal(err)
	}
	var uncertainJob domain.Job
	if err := domain.Decode(uncertain.Msg.Resource.DocumentJson, &uncertainJob); err != nil {
		t.Fatal(err)
	}
	if uncertainJob.State != domain.JobUncertain || uncertainJob.InstanceID != domain.ID(instance) {
		t.Fatal("revocation lost accepted execution uncertainty")
	}

	if _, err := client.AttachWorker(ctx, ownerRequest(one, attach)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("revoked credential accepted receipt retry: %v", err)
	}
}

func TestReplacingExpiredWorkerPreservesUncertainty(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	machine, device, previous, current, jobID := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	_, err = db.Mutate(ctx, domain.NewID(), "fixture", nil, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "fixture", OS: "linux", Architecture: "amd64", Version: rpc.Version}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "fixture", Type: domain.WorkerDevice, MachineID: machine, PairedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		if err := tx.SetWorkerInstance(machine, previous, time.Now().Add(-time.Minute)); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(domain.RepositoryInspectionInput{Path: "/tmp/repo"})
		return tx.PutJob(jobID, 0, "", "", domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobClaimed, MachineID: machine, InstanceID: previous, Input: raw, AcceptedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: db, Identity: security.Identity{ServerID: domain.NewID()}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	actor := domain.WithPrincipal(ctx, domain.Principal{Type: domain.WorkerDevice, MachineID: machine, DeviceID: device})
	_, err = service.AttachWorker(actor, connect.NewRequest(&pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: string(machine), InstanceId: string(current), Version: rpc.Version}))
	if err != nil {
		t.Fatal(err)
	}
	record, err := db.Get(ctx, domain.JobKind, jobID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](record)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != domain.JobUncertain || job.InstanceID != previous || job.Problem == nil || job.Problem.Code != domain.RecoveryRequired {
		t.Fatalf("disconnected work was reassigned: %+v", job)
	}
	_, err = service.ReportWork(actor, connect.NewRequest(&pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(jobID), ExpectedRevision: 1}, MachineId: string(machine), InstanceId: string(previous), Problem: &pb.ErrorDetail{Code: string(domain.Canceled)}}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale process reported after replacement: %v", err)
	}
}

func TestWorkerRevocationSettlesQueuedAndClaimedTitleJobs(t *testing.T) {
	for _, state := range []string{"queued", "claimed"} {
		t.Run(state, func(t *testing.T) {
			f := recoveredAutomaticTitleFixture(t)
			_, recovery := acceptRecovery(t, f)
			completeRecovery(t, f, recovery.ExecutionRecoveryJob)
			initialSessionRecord, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			var session domain.Session
			if domain.Decode(initialSessionRecord.Data, &session) != nil || session.TitleJobID == "" {
				t.Fatalf("recovery did not create a title job: %+v", session)
			}
			if state == "claimed" {
				if _, err := claimTitleJob(context.Background(), f.service, f.input.MachineID, f.instance, f.device, session.TitleJobID); err != nil {
					t.Fatal(err)
				}
			}
			_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.archive-pending", session.TitleJobID, func(tx *store.Tx) (any, error) {
				r, value, err := sessionRecord(tx, f.input.SessionID)
				if err != nil {
					return nil, err
				}
				value.Archive = domain.ArchivePending
				return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, value)
			})
			if err != nil {
				t.Fatal(err)
			}
			deviceRecord, err := f.service.Store.Get(context.Background(), domain.DeviceKind, f.device)
			if err != nil {
				t.Fatal(err)
			}
			var device domain.Device
			if domain.Decode(deviceRecord.Data, &device) != nil {
				t.Fatal("could not read paired Worker before revocation")
			}
			devices := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
			_, err = devices.RevokeDevice(context.Background(), ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{
				RequestId: string(domain.NewID()), Id: string(f.device), ExpectedRevision: deviceRecord.Revision,
			}}))
			if err != nil {
				t.Fatal(err)
			}
			jobRecord, err := f.service.Store.Get(context.Background(), domain.JobKind, session.TitleJobID)
			if err != nil {
				t.Fatal(err)
			}
			var job domain.Job
			if domain.Decode(jobRecord.Data, &job) != nil {
				t.Fatal("could not read revoked title job")
			}
			updatedSession, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if domain.Decode(updatedSession.Data, &session) != nil {
				t.Fatal("could not read session after Worker revocation")
			}
			if state == "queued" {
				if job.State != domain.JobCanceled || session.TitleState != domain.TitleSkipped || session.TitleReason != domain.TitleReasonAuthorityLost || session.Archive != domain.Archived {
					t.Fatalf("queued title or pending archive was not settled: job=%+v session=%+v", job, session)
				}
			} else if job.State != domain.JobUncertain || session.TitleState != domain.TitleUncertain || session.TitleReason != domain.TitleReasonCleanupUncertain || session.Archive != domain.ArchivePending {
				t.Fatalf("claimed title cleanup uncertainty was lost: job=%+v session=%+v", job, session)
			}
		})
	}
}
