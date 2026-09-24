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
	attach := &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: device.Machine.Id, InstanceId: instance, Version: rpc.Version}
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
	revoke := &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: device.Device.Id, ExpectedRevision: device.Device.Revision}}
	if _, err := devices.RevokeDevice(ctx, ownerRequest(owner, revoke)); err != nil {
		t.Fatal(err)
	}
	if stream.Receive() {
		t.Fatal("revoked stream remained open")
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
