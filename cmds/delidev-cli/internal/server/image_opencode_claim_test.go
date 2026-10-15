package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func machineWith(capabilities ...domain.WorkerCapability) domain.Machine {
	return domain.Machine{WorkerCapabilities: capabilities}
}

func executionJobWith(t *testing.T, configuration domain.ExecutionConfiguration) domain.Job {
	t.Helper()
	raw, err := json.Marshal(domain.ExecutionJobInput{Version: 1, Configuration: configuration})
	if err != nil {
		t.Fatal(err)
	}
	return domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobQueued, Input: raw}
}

// TestQueuedOpenCodeImageDeclarationClaimTable pins every cell of the claim-time
// recheck: a queued job that carries a frozen OpenCode image declaration, in any
// of the assignment positions a job input can hold one, is claimable only by a
// Runner Device that currently reports the OpenCode image pair.
func TestQueuedOpenCodeImageDeclarationClaimTable(t *testing.T) {
	declared := domain.ExecutionConfiguration{Harness: domain.OpenCode, ImageInputDeclared: true}
	textOnly := domain.ExecutionConfiguration{Harness: domain.OpenCode}
	codex := domain.ExecutionConfiguration{Harness: domain.Codex, ImageInputDeclared: true}
	execution := func(c domain.ExecutionConfiguration) domain.Job { return executionJobWith(t, c) }
	compaction := func(restore, assignment domain.ExecutionConfiguration) domain.Job {
		raw, err := json.Marshal(domain.SessionCompactionInput{Restore: domain.ExecutionJobInput{Configuration: restore}, Assignment: domain.ExecutionJobInput{Configuration: assignment}})
		if err != nil {
			t.Fatal(err)
		}
		return domain.Job{Type: domain.CompactSessionJob, State: domain.JobQueued, Input: raw}
	}
	fork := func(source, snapshot domain.ExecutionConfiguration) domain.Job {
		raw, err := json.Marshal(domain.ForkJobInput{SourceAssignment: domain.ExecutionJobInput{Configuration: source}, Snapshot: domain.InitialExecution{Configuration: snapshot}})
		if err != nil {
			t.Fatal(err)
		}
		return domain.Job{Type: domain.ForkSessionJob, State: domain.JobQueued, Input: raw}
	}
	withPair := machineWith(domain.ImageInputsV1, domain.OpenCodeImageInputsV1)
	withoutPair := machineWith(domain.ImageInputsV1)
	bare := machineWith()
	for _, tc := range []struct {
		name    string
		job     domain.Job
		machine domain.Machine
		waits   bool
	}{
		{"execution declared, pair reported", execution(declared), withPair, false},
		{"execution declared, base image capability only", execution(declared), withoutPair, true},
		{"execution declared, no image capability", execution(declared), bare, true},
		{"execution text-only snapshot, older Worker", execution(textOnly), withoutPair, false},
		{"execution Codex declaration, older Worker", execution(codex), withoutPair, false},
		{"compaction declared in assignment, older Worker", compaction(textOnly, declared), withoutPair, true},
		{"compaction declared in restore, older Worker", compaction(declared, textOnly), withoutPair, true},
		{"compaction declared, pair reported", compaction(declared, declared), withPair, false},
		{"compaction text-only, older Worker", compaction(textOnly, textOnly), withoutPair, false},
		{"fork declared in source assignment, older Worker", fork(declared, textOnly), withoutPair, true},
		{"fork declared in child snapshot, older Worker", fork(textOnly, declared), withoutPair, true},
		{"fork declared, pair reported", fork(declared, declared), withPair, false},
		{"fork text-only, older Worker", fork(textOnly, textOnly), withoutPair, false},
		{"undecodable execution payload is left to its own checks", domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobQueued, Input: json.RawMessage(`{"configuration":`)}, withoutPair, false},
		{"workspace preparation never carries a declaration", domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobQueued, Input: json.RawMessage(`{}`)}, withoutPair, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := queuedOpenCodeImageWorkerMissing(tc.machine, tc.job); got != tc.waits {
				t.Fatalf("queuedOpenCodeImageWorkerMissing = %v, want %v", got, tc.waits)
			}
		})
	}
}

func openCodeClaimCapabilities(withPair bool) []pb.WorkerCapability {
	capabilities := []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_BRANCH_PREFIX_INSTRUCTIONS_V1, pb.WorkerCapability_WORKER_CAPABILITY_NAMED_WORKSPACE_DIRECTORIES_V1, pb.WorkerCapability_WORKER_CAPABILITY_REMOTE_WORKSPACE_CLONE_V1, pb.WorkerCapability_WORKER_CAPABILITY_INLINE_MODEL_EXECUTION_V1, pb.WorkerCapability_WORKER_CAPABILITY_EXECUTION_STARTUP_V1, pb.WorkerCapability_WORKER_CAPABILITY_IMAGE_INPUTS_V1}
	if withPair {
		capabilities = append(capabilities, pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_IMAGE_INPUTS_V1)
	}
	return capabilities
}

func queuedJobState(t *testing.T, f *firstDispatchFixture, id domain.ID) domain.JobState {
	t.Helper()
	var state domain.JobState
	if err := f.service.Store.Read(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), func(tx *store.Tx) error {
		row, err := tx.Get(domain.JobKind, id)
		if err != nil {
			return err
		}
		job, err := store.Decode[domain.Job](row)
		state = job.State
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return state
}

// TestQueuedOpenCodeImageJobWaitsForWorkerThatReportsThePair drives the real
// WatchWork claim: a declared OpenCode job queued while only a Worker without
// the pair is attached is not assigned to it, stays queued without failing, and
// is assigned once a Worker reporting the pair attaches on the same machine.
func TestQueuedOpenCodeImageJobWaitsForWorkerThatReportsThePair(t *testing.T) {
	f, _, _ := imageRPCFixture(t, domain.OpenCode)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	attach := func(withPair bool) {
		t.Helper()
		if _, err := f.workerClient.AttachWorker(context.Background(), ownerRequest(f.workerIdentity, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, Version: rpc.Version, Capabilities: openCodeClaimCapabilities(withPair)})); err != nil {
			t.Fatal(err)
		}
	}
	watch := func() (*connect.ServerStreamForClient[pb.WatchWorkResponse], func()) {
		t.Helper()
		watchCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		stream, err := f.workerClient.WatchWork(watchCtx, ownerRequest(f.workerIdentity, &pb.WatchWorkRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		return stream, func() { stream.Close(); cancel() }
	}

	// The fixture Worker reconnects as an older build before the job exists, so
	// every claim below sees the current, reduced capability list.
	attach(false)
	f.workerStream.Close()
	raw, err := json.Marshal(domain.ExecutionJobInput{Version: 1, MachineID: domain.ID(f.machine.Id), Configuration: domain.ExecutionConfiguration{Harness: domain.OpenCode, ImageInputDeclared: true}})
	if err != nil {
		t.Fatal(err)
	}
	var jobID domain.ID
	if _, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.queued-opencode-image-job", nil, func(tx *store.Tx) (any, error) {
		record, err := tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobQueued, MachineID: domain.ID(f.machine.Id), Input: raw, AcceptedAt: time.Now().UTC()})
		jobID = record.ID
		return record, err
	}); err != nil {
		t.Fatal(err)
	}
	assigned := func(stream *connect.ServerStreamForClient[pb.WatchWorkResponse], within time.Duration) bool {
		found := make(chan bool, 1)
		go func() {
			for stream.Receive() {
				if job := stream.Msg().Job; job != nil && job.Id == string(jobID) {
					found <- true
					return
				}
			}
			found <- false
		}()
		select {
		case ok := <-found:
			return ok
		case <-time.After(within):
			return false
		}
	}

	older, closeOlder := watch()
	if assigned(older, 1500*time.Millisecond) {
		t.Fatal("a Worker without the OpenCode image pair was assigned a declared OpenCode job")
	}
	if state := queuedJobState(t, f, jobID); state != domain.JobQueued {
		t.Fatalf("job left the queue for an older Worker: %s", state)
	}
	closeOlder()

	attach(true)
	current, closeCurrent := watch()
	defer closeCurrent()
	if !assigned(current, 10*time.Second) {
		t.Fatal("a Worker reporting the OpenCode image pair was not assigned the waiting job")
	}
	if state := queuedJobState(t, f, jobID); state != domain.JobClaimed {
		t.Fatalf("assigned job is %s, want claimed", state)
	}
}
