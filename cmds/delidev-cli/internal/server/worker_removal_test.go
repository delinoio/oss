package server

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func removeWorkerRequest(machine *pb.Resource) *pb.RemoveWorkerRequest {
	return &pb.RemoveWorkerRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: machine.Id, ExpectedRevision: machine.Revision}, SchemaVersion: 1}
}

func machineIDs(t *testing.T, ctx context.Context, resources delidevv1connect.ResourceServiceClient, request func(*pb.ListResourcesRequest) *connect.Request[pb.ListResourcesRequest]) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	token := ""
	for {
		list, err := resources.ListResources(ctx, request(&pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, PageSize: 1, PageToken: token}}))
		if err != nil {
			t.Fatal(err)
		}
		for _, resource := range list.Msg.Resources {
			found[resource.Id] = true
		}
		if list.Msg.NextPageToken == "" {
			return found
		}
		token = list.Msg.NextPageToken
	}
}

func TestRemoveWorkerRetiresRevokedWorkersWithReplayAndEffects(t *testing.T) {
	endpoint, owner, stop, done := runTestServer(t, filepath.Join(t.TempDir(), "state"))
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	online, onlineDevice := pairedWorker(t, ctx, endpoint, owner)
	idle, idleDevice := pairedWorker(t, ctx, endpoint, owner)
	revoked, revokedDevice := pairedWorker(t, ctx, endpoint, owner)
	_ = idle
	_ = revoked
	workers := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, endpoint.URL)
	devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, endpoint.URL)
	resources := delidevv1connect.NewResourceServiceClient(http.DefaultClient, endpoint.URL)
	system := delidevv1connect.NewSystemServiceClient(http.DefaultClient, endpoint.URL)

	status, err := system.GetStatus(ctx, ownerRequest(owner, &pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	advertised := false
	for _, capability := range status.Msg.Capabilities {
		advertised = advertised || capability == pb.SystemCapability_SYSTEM_CAPABILITY_WORKER_REMOVAL_V1
	}
	if !advertised {
		t.Fatal("server did not negotiate Worker removal")
	}

	instance := string(domain.NewID())
	if _, err := workers.AttachWorker(ctx, ownerRequest(online, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: onlineDevice.Machine.Id, InstanceId: instance, Version: rpc.Version})); err != nil {
		t.Fatal(err)
	}
	stream, err := workers.WatchWork(ctx, ownerRequest(online, &pb.WatchWorkRequest{MachineId: onlineDevice.Machine.Id, InstanceId: instance}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() || !stream.Msg().Heartbeat {
		t.Fatal("missing readiness heartbeat", stream.Err())
	}
	claimedJob, err := workers.InspectRepository(ctx, ownerRequest(owner, &pb.InspectRepositoryRequest{RequestId: string(domain.NewID()), MachineId: onlineDevice.Machine.Id, Path: "/tmp/claimed"}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() || stream.Msg().Job == nil || stream.Msg().Job.Id != claimedJob.Msg.Job.Id {
		t.Fatal("missing claimed assignment")
	}
	queuedJob, err := workers.InspectRepository(ctx, ownerRequest(owner, &pb.InspectRepositoryRequest{RequestId: string(domain.NewID()), MachineId: idleDevice.Machine.Id, Path: "/tmp/queued"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := devices.RevokeDevice(ctx, ownerRequest(owner, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: revokedDevice.Device.Id, ExpectedRevision: revokedDevice.Device.Revision}})); err != nil {
		t.Fatal(err)
	}

	// Rejections write nothing.
	currentOnline, err := resources.GetResource(ctx, ownerRequest(owner, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, Id: onlineDevice.Machine.Id}))
	if err != nil {
		t.Fatal(err)
	}
	machine := currentOnline.Msg.Resource
	for name, bad := range map[string]*pb.RemoveWorkerRequest{
		"missing mutation": {SchemaVersion: 1},
		"missing revision": {Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: machine.Id}, SchemaVersion: 1},
		"schema":           {Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: machine.Id, ExpectedRevision: machine.Revision}, SchemaVersion: 2},
		"malformed id":     {Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: "not-an-id", ExpectedRevision: machine.Revision}, SchemaVersion: 1},
		"device as target": {Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: onlineDevice.Device.Id, ExpectedRevision: onlineDevice.Device.Revision}, SchemaVersion: 1},
		"foreign id":       {Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(domain.NewID()), ExpectedRevision: 1}, SchemaVersion: 1},
		"stale revision":   {Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: machine.Id, ExpectedRevision: machine.Revision + 5}, SchemaVersion: 1},
	} {
		if _, err := devices.RemoveWorker(ctx, ownerRequest(owner, bad)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if _, err := devices.RemoveWorker(ctx, ownerRequest(online, removeWorkerRequest(machine))); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("Worker credential removed a Worker: %v", err)
	}
	if _, err := devices.RemoveWorker(ctx, connect.NewRequest(removeWorkerRequest(machine))); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated caller removed a Worker: %v", err)
	}
	unchanged, err := resources.GetResource(ctx, ownerRequest(owner, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, Id: machine.Id}))
	if err != nil || unchanged.Msg.Resource.Revision != machine.Revision {
		t.Fatalf("rejected removal wrote state: %v", err)
	}

	// Online Worker: credentials stop, claimed work stays uncertain.
	request := removeWorkerRequest(machine)
	removed, err := devices.RemoveWorker(ctx, ownerRequest(owner, request))
	if err != nil {
		t.Fatal(err)
	}
	var retired domain.Machine
	if err := domain.Decode(removed.Msg.Machine.DocumentJson, &retired); err != nil || !retired.Retired || !retired.Disabled || removed.Msg.Replayed {
		t.Fatalf("machine was not retired: %+v %v", retired, err)
	}
	replay, err := devices.RemoveWorker(ctx, ownerRequest(owner, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Machine.Revision != removed.Msg.Machine.Revision {
		t.Fatalf("original request did not recover its receipt: %v", err)
	}
	// The retained machine keeps the additive schema-1 contract in the response
	// and in the replayed receipt.
	if removed.Msg.Machine.SchemaVersion != 1 || replay.Msg.Machine.SchemaVersion != 1 {
		t.Fatalf("retired machine left schema 1: %d %d", removed.Msg.Machine.SchemaVersion, replay.Msg.Machine.SchemaVersion)
	}
	for stream.Receive() {
	}
	if _, err := workers.AttachWorker(ctx, ownerRequest(online, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: onlineDevice.Machine.Id, InstanceId: string(domain.NewID()), Version: rpc.Version})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("removed Worker credential reconnected: %v", err)
	}
	var job domain.Job
	read, err := resources.GetResource(ctx, ownerRequest(owner, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_JOB, Id: claimedJob.Msg.Job.Id}))
	if err != nil || domain.Decode(read.Msg.Resource.DocumentJson, &job) != nil || job.State != domain.JobUncertain {
		t.Fatalf("claimed work lost uncertainty: %v %v", job.State, err)
	}
	device, err := resources.GetResource(ctx, ownerRequest(owner, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_DEVICE, Id: onlineDevice.Device.Id}))
	if err != nil {
		t.Fatal(err)
	}
	var storedDevice domain.Device
	if err := domain.Decode(device.Msg.Resource.DocumentJson, &storedDevice); err != nil || !storedDevice.Revoked {
		t.Fatalf("Worker device stayed authorized: %v", err)
	}

	// Idle Worker with queued work: queued work cancels.
	idleMachine, err := resources.GetResource(ctx, ownerRequest(owner, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, Id: idleDevice.Machine.Id}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := devices.RemoveWorker(ctx, ownerRequest(owner, removeWorkerRequest(idleMachine.Msg.Resource))); err != nil {
		t.Fatal(err)
	}
	read, err = resources.GetResource(ctx, ownerRequest(owner, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_JOB, Id: queuedJob.Msg.Job.Id}))
	if err != nil || domain.Decode(read.Msg.Resource.DocumentJson, &job) != nil || job.State != domain.JobCanceled {
		t.Fatalf("queued work stayed runnable: %v %v", job.State, err)
	}

	// An already revoked Worker can be removed explicitly.
	revokedMachine, err := resources.GetResource(ctx, ownerRequest(owner, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, Id: revokedDevice.Machine.Id}))
	if err != nil {
		t.Fatal(err)
	}
	var beforeRemoval domain.Machine
	if err := domain.Decode(revokedMachine.Msg.Resource.DocumentJson, &beforeRemoval); err != nil || !beforeRemoval.Disabled || beforeRemoval.Retired {
		t.Fatalf("revocation must not retire automatically: %+v %v", beforeRemoval, err)
	}
	if got := machineIDs(t, ctx, resources, func(r *pb.ListResourcesRequest) *connect.Request[pb.ListResourcesRequest] {
		return ownerRequest(owner, r)
	}); !got[revokedDevice.Machine.Id] {
		t.Fatal("revoked Worker was retired automatically")
	}
	if _, err := devices.RemoveWorker(ctx, ownerRequest(owner, removeWorkerRequest(revokedMachine.Msg.Resource))); err != nil {
		t.Fatal(err)
	}

	// Active inventories exclude retired Workers; exact reads keep identity.
	got := machineIDs(t, ctx, resources, func(r *pb.ListResourcesRequest) *connect.Request[pb.ListResourcesRequest] {
		return ownerRequest(owner, r)
	})
	for _, id := range []string{onlineDevice.Machine.Id, idleDevice.Machine.Id, revokedDevice.Machine.Id} {
		if got[id] {
			t.Fatalf("retired Worker %s remained in the active inventory", id)
		}
		exact, err := resources.GetResource(ctx, ownerRequest(owner, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, Id: id}))
		if err != nil || exact.Msg.Resource.Id != id {
			t.Fatalf("exact read of retired Worker failed: %v", err)
		}
		if exact.Msg.Resource.SchemaVersion != 1 {
			t.Fatalf("exact read of retired Worker left schema 1: %d", exact.Msg.Resource.SchemaVersion)
		}
	}
	snapshot, err := resources.GetSnapshot(ctx, ownerRequest(owner, &pb.GetSnapshotRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, PageSize: 50}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, resource := range snapshot.Msg.Resources {
		if resource.Id == onlineDevice.Machine.Id {
			t.Fatal("snapshot resurrected a retired Worker")
		}
	}
	// Active Workers that remain listed keep schema 1 on the list path.
	remaining, err := resources.ListResources(ctx, ownerRequest(owner, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, PageSize: 50}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, resource := range remaining.Msg.Resources {
		if resource.SchemaVersion != 1 {
			t.Fatalf("listed machine %s left schema 1: %d", resource.Id, resource.SchemaVersion)
		}
	}
}
