// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// parallelFixture is a real temporary SQLite and Connect server with one
// authenticated Worker instance that did or did not negotiate parallel sessions.
func parallelFixture(t *testing.T, negotiate bool) *storageFixture {
	t.Helper()
	f := newStorageFixture(t)
	if negotiate {
		_, err := f.worker.AttachWorker(context.Background(), ownerRequest(f.workerIdentity, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: string(f.machine), InstanceId: string(f.instance), Version: rpc.Version, Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_PARALLEL_SESSIONS_V1}}))
		if err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// queueSessionPreparation accepts one queued preparation job for the session.
func queueSessionPreparation(t *testing.T, f *storageFixture, session domain.ID) domain.ID {
	t.Helper()
	input, err := json.Marshal(workspace.PrepareRequest{SessionID: session, MachineID: f.machine, Type: domain.GeneralChat})
	if err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	if _, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "fixture.parallel-prepare", nil, func(tx *store.Tx) (any, error) {
		return tx.PutJob(id, 0, session, "", domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobQueued, MachineID: f.machine, Input: input, AcceptedAt: time.Now().UTC()})
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// queueSerialJob accepts one non-session primary job, which keeps the serial lane.
func queueSerialJob(t *testing.T, f *storageFixture) domain.ID {
	t.Helper()
	id := domain.NewID()
	if _, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "fixture.parallel-serial", nil, func(tx *store.Tx) (any, error) {
		return tx.PutJob(id, 0, "", "", domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobQueued, MachineID: f.machine, Input: []byte(`{"path":"/isolated/fixture","preferred_remote":"origin"}`), AcceptedAt: time.Now().UTC()})
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func jobState(t *testing.T, f *storageFixture, id domain.ID) domain.JobState {
	t.Helper()
	row, err := f.service.Store.Get(context.Background(), domain.JobKind, id)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](row)
	if err != nil {
		t.Fatal(err)
	}
	return job.State
}

func openParallelStream(t *testing.T, ctx context.Context, f *storageFixture) *connect.ServerStreamForClient[pb.WatchWorkResponse] {
	t.Helper()
	stream, err := f.worker.WatchWork(ctx, ownerRequest(f.workerIdentity, &pb.WatchWorkRequest{MachineId: string(f.machine), InstanceId: string(f.instance)}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stream.Close() })
	if !stream.Receive() || !stream.Msg().Heartbeat {
		t.Fatal("primary readiness missing", stream.Err())
	}
	return stream
}

// receiveJobs reads exactly count assignments, ignoring heartbeats.
func receiveJobs(t *testing.T, stream *connect.ServerStreamForClient[pb.WatchWorkResponse], count int) []*pb.Resource {
	t.Helper()
	jobs := []*pb.Resource{}
	for len(jobs) < count {
		if !stream.Receive() {
			t.Fatalf("stream ended after %d of %d assignments: %v", len(jobs), count, stream.Err())
		}
		if stream.Msg().Job != nil {
			jobs = append(jobs, stream.Msg().Job)
		}
	}
	return jobs
}

// reportJob terminalizes one assigned job with a definite typed failure. The
// fixture sessions are synthetic, so the report is committed directly to the
// durable job index instead of through the session-finalizing Worker RPC.
func reportJob(t *testing.T, f *storageFixture, job *pb.Resource) {
	t.Helper()
	if _, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "fixture.parallel-report", job.Id, func(tx *store.Tx) (any, error) {
		row, err := tx.Get(domain.JobKind, domain.ID(job.Id))
		if err != nil {
			return nil, err
		}
		value, err := store.Decode[domain.Job](row)
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		value.State, value.Problem, value.FinishedAt = domain.JobFailed, domain.Fail(domain.Canceled, "Fixture completion.", "None."), &now
		return tx.PutJob(row.ID, row.Revision, row.SessionID, row.ProjectID, value)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestParallelStreamDispatchesIndependentSessionsWithoutCeiling(t *testing.T) {
	f := parallelFixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream := openParallelStream(t, ctx, f)
	// Eight distinct sessions exceed any small fixed ceiling. None of them is
	// reported, so every one stays unresolved while the next must still claim.
	want := map[string]bool{}
	for range 8 {
		want[string(queueSessionPreparation(t, f, domain.NewID()))] = true
	}
	for _, job := range receiveJobs(t, stream, 8) {
		if !want[job.Id] {
			t.Fatal("unexpected or duplicate assignment", job.Id)
		}
		delete(want, job.Id)
		if jobState(t, f, domain.ID(job.Id)) != domain.JobClaimed {
			t.Fatal("assigned job was not claimed")
		}
	}
}

func TestParallelStreamNewSessionPreparesWhileOtherExecutionIsUnresolved(t *testing.T) {
	f := parallelFixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream := openParallelStream(t, ctx, f)
	first := queueSessionPreparation(t, f, domain.NewID())
	held := receiveJobs(t, stream, 1)[0]
	if held.Id != string(first) {
		t.Fatal("wrong first assignment")
	}
	second := queueSessionPreparation(t, f, domain.NewID())
	later := receiveJobs(t, stream, 1)[0]
	if later.Id != string(second) || jobState(t, f, first) != domain.JobClaimed {
		t.Fatal("independent session did not dispatch before the held job resolved")
	}
}

func TestParallelStreamKeepsOneJobPerSessionInOrder(t *testing.T) {
	f := parallelFixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session := domain.NewID()
	firstA := queueSessionPreparation(t, f, session)
	secondA := queueSessionPreparation(t, f, session)
	other := queueSessionPreparation(t, f, domain.NewID())
	stream := openParallelStream(t, ctx, f)
	jobs := receiveJobs(t, stream, 2)
	if jobs[0].Id != string(firstA) || jobs[1].Id != string(other) {
		t.Fatal("same-session job overlapped or reordered", jobs[0].Id, jobs[1].Id)
	}
	if jobState(t, f, secondA) != domain.JobQueued {
		t.Fatal("same-session job was preclaimed")
	}
	reportJob(t, f, jobs[0])
	next := receiveJobs(t, stream, 1)[0]
	if next.Id != string(secondA) {
		t.Fatal("same-session job did not follow its predecessor")
	}
}

func TestParallelStreamSerialLaneStaysSingleAndDoesNotStallSessions(t *testing.T) {
	f := parallelFixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	firstSerial := queueSerialJob(t, f)
	secondSerial := queueSerialJob(t, f)
	independent := queueSessionPreparation(t, f, domain.NewID())
	stream := openParallelStream(t, ctx, f)
	jobs := receiveJobs(t, stream, 2)
	if jobs[0].Id != string(firstSerial) || jobs[1].Id != string(independent) {
		t.Fatal("serial lane stalled an unrelated session or claimed twice", jobs[0].Id, jobs[1].Id)
	}
	if jobState(t, f, secondSerial) != domain.JobQueued {
		t.Fatal("a second non-session primary job was preclaimed")
	}
	reportJob(t, f, jobs[0])
	if next := receiveJobs(t, stream, 1)[0]; next.Id != string(secondSerial) {
		t.Fatal("serial lane did not advance after its report")
	}
}

func TestLegacyStreamKeepsSingletonDispatch(t *testing.T) {
	f := parallelFixture(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	first := queueSessionPreparation(t, f, domain.NewID())
	second := queueSessionPreparation(t, f, domain.NewID())
	stream := openParallelStream(t, ctx, f)
	held := receiveJobs(t, stream, 1)[0]
	if held.Id != string(first) {
		t.Fatal("wrong first assignment")
	}
	time.Sleep(300 * time.Millisecond)
	if jobState(t, f, second) != domain.JobQueued {
		t.Fatal("legacy peer received more than one unresolved assignment")
	}
	reportJob(t, f, held)
	if next := receiveJobs(t, stream, 1)[0]; next.Id != string(second) {
		t.Fatal("legacy stream did not continue after the report")
	}
}

func TestParallelControlsStayScopedToTheirJobAndSurviveOutOfOrderCompletion(t *testing.T) {
	f := parallelFixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream := openParallelStream(t, ctx, f)
	a := queueSessionPreparation(t, f, domain.NewID())
	b := queueSessionPreparation(t, f, domain.NewID())
	jobs := receiveJobs(t, stream, 2)
	// Stop only B: exactly B's context receives a cancellation control.
	if _, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "fixture.parallel-cancel", nil, func(tx *store.Tx) (any, error) { return nil, tx.RequestJobCancellation(b) }); err != nil {
		t.Fatal(err)
	}
	for stream.Receive() {
		if stream.Msg().CancelJobId != "" {
			if stream.Msg().CancelJobId != string(b) {
				t.Fatal("cancellation crossed job identities", stream.Msg().CancelJobId)
			}
			break
		}
	}
	if jobState(t, f, a) != domain.JobClaimed {
		t.Fatal("sibling session was affected by a targeted cancellation")
	}
	// Complete B before A. A's later report must still resolve its own job.
	reportJob(t, f, jobs[1])
	if jobState(t, f, a) != domain.JobClaimed || jobState(t, f, b) == domain.JobClaimed {
		t.Fatal("out-of-order completion crossed identities")
	}
	reportJob(t, f, jobs[0])
	if jobState(t, f, a) == domain.JobClaimed {
		t.Fatal("original job did not resolve")
	}
}

func TestParallelStreamLossResendsEachOwnedAssignmentOnce(t *testing.T) {
	f := parallelFixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first, second := queueSessionPreparation(t, f, domain.NewID()), queueSessionPreparation(t, f, domain.NewID())
	streamCtx, stopStream := context.WithCancel(ctx)
	stream := openParallelStream(t, streamCtx, f)
	receiveJobs(t, stream, 2)
	stopStream()
	// The same process reconnects: each still-claimed original is presented
	// exactly once, never claimed again and never given a second revision.
	reconnect := openParallelStream(t, ctx, f)
	seen := map[string]uint64{}
	for _, job := range receiveJobs(t, reconnect, 2) {
		seen[job.Id] = job.Revision
	}
	if len(seen) != 2 || seen[string(first)] == 0 || seen[string(second)] == 0 {
		t.Fatal("reconnect did not present each owned assignment exactly once", seen)
	}
	for id, revision := range seen {
		row, err := f.service.Store.Get(context.Background(), domain.JobKind, domain.ID(id))
		if err != nil || row.Revision != revision {
			t.Fatal("reconnect replayed or re-claimed an owned assignment", err)
		}
	}
}

func TestAttachWorkerEchoesParallelSessionsAndRecordsOnlyAdvertisedSupport(t *testing.T) {
	f := newStorageFixture(t)
	attach := func(capabilities ...pb.WorkerCapability) *pb.AttachWorkerResponse {
		t.Helper()
		response, err := f.worker.AttachWorker(context.Background(), ownerRequest(f.workerIdentity, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: string(f.machine), InstanceId: string(f.instance), Version: rpc.Version, Capabilities: capabilities}))
		if err != nil {
			t.Fatal(err)
		}
		return response.Msg
	}
	advertised := func(response *pb.AttachWorkerResponse) bool {
		var machine domain.Machine
		if err := domain.Decode(response.Machine.DocumentJson, &machine); err != nil {
			t.Fatal(err)
		}
		return slices.Contains(machine.WorkerCapabilities, domain.ParallelSessionsV1)
	}
	legacy := attach()
	if !slices.Contains(legacy.SupportedWorkerCapabilities, pb.WorkerCapability_WORKER_CAPABILITY_PARALLEL_SESSIONS_V1) || advertised(legacy) {
		t.Fatal("server must echo support but record nothing until the Worker advertises it")
	}
	negotiated := attach(pb.WorkerCapability_WORKER_CAPABILITY_PARALLEL_SESSIONS_V1)
	if !advertised(negotiated) {
		t.Fatal("advertised parallel sessions were not recorded on the machine")
	}
}
