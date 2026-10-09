// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
	"testing"
)

func progressFixture(t *testing.T) *authorityFixture {
	f := directStartupFixture(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.progress-capability", nil, func(tx *store.Tx) (any, error) {
		r, m, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.SessionStartupProgressV1)
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func progressRequest(f *authorityFixture) *connect.Request[pb.ReportSessionStartupProgressRequest] {
	r := connect.NewRequest(&pb.ReportSessionStartupProgressRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.job), ExpectedRevision: 1}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), SessionId: string(f.input.SessionID), ExecutionId: string(f.input.ExecutionID), Sequence: 1, NativePhase: pb.ExecutionStartupPhase_EXECUTION_STARTUP_PHASE_RESOLVE, State: pb.SessionStartupProgressState_SESSION_STARTUP_PROGRESS_STATE_RUNNING})
	r.Header().Set("Authorization", "Bearer "+f.workerToken)
	return r
}
func TestSessionStartupProgressExactReceiptsPreserveAuthority(t *testing.T) {
	f := progressFixture(t)
	f.registerGrant(t)
	r := progressRequest(f)
	first, err := f.client.ReportSessionStartupProgress(context.Background(), r)
	if err != nil || first.Msg.Replayed {
		t.Fatal("first report", err)
	}
	again, err := f.client.ReportSessionStartupProgress(context.Background(), r)
	if err != nil || !again.Msg.Replayed {
		t.Fatal("lost acknowledgment replay", err)
	}
	if lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token); err == nil {
		lease.Release()
		t.Fatal("telemetry granted inference")
	}
	jr, _ := f.service.Store.Get(context.Background(), domain.JobKind, f.job)
	if jr.Revision != 1 {
		t.Fatal("job revision advanced")
	}
	sr, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	s, err := store.Decode[domain.Session](sr)
	if err != nil || s.Startup != nil || s.Execution != nil || s.StartupProgress.Native.LastSequence != 1 || len(s.StartupProgress.Native.Steps) != 6 {
		t.Fatal("authoritative state changed", err)
	}
	changed := connect.NewRequest(proto.Clone(r.Msg).(*pb.ReportSessionStartupProgressRequest))
	changed.Header().Set("Authorization", "Bearer "+f.workerToken)
	changed.Msg.State = pb.SessionStartupProgressState_SESSION_STARTUP_PROGRESS_STATE_COMPLETED
	if _, err := f.client.ReportSessionStartupProgress(context.Background(), changed); err == nil {
		t.Fatal("changed receipt accepted")
	}
	changed.Msg.Mutation.RequestId = string(domain.NewID())
	if _, err := f.client.ReportSessionStartupProgress(context.Background(), changed); err == nil {
		t.Fatal("changed duplicate sequence accepted")
	}
	changed.Msg.Sequence = 2
	if _, err := f.client.ReportSessionStartupProgress(context.Background(), changed); err != nil {
		t.Fatal("confirmed completion", err)
	}
	r.Msg.Mutation.RequestId = string(domain.NewID())
	r.Msg.Sequence = 3
	if _, err := f.client.ReportSessionStartupProgress(context.Background(), r); err == nil {
		t.Fatal("reopened completed operation")
	}
}
func TestSessionStartupProgressRejectsForeignAndTerminalOwners(t *testing.T) {
	f := progressFixture(t)
	for _, change := range []string{"machine", "instance", "session", "execution", "job", "revision", "repository", "enum", "mixed", "sequence"} {
		t.Run(change, func(t *testing.T) {
			r := progressRequest(f)
			switch change {
			case "machine":
				r.Msg.MachineId = string(domain.NewID())
			case "instance":
				r.Msg.InstanceId = string(domain.NewID())
			case "session":
				r.Msg.SessionId = string(domain.NewID())
			case "execution":
				r.Msg.ExecutionId = string(domain.NewID())
			case "job":
				r.Msg.Mutation.Id = string(domain.NewID())
			case "revision":
				r.Msg.Mutation.ExpectedRevision++
			case "repository":
				r.Msg.RepositoryId = string(domain.NewID())
			case "enum":
				r.Msg.NativePhase = 99
			case "mixed":
				r.Msg.WorkspaceOperation = 1
			case "sequence":
				r.Msg.Sequence = 0
			}
			if _, err := f.client.ReportSessionStartupProgress(context.Background(), r); err == nil {
				t.Fatal("foreign observation accepted")
			}
		})
	}
	r := progressRequest(f)
	if _, err := f.client.ReportSessionStartupProgress(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.progress-stop", nil, func(tx *store.Tx) (any, error) { return nil, tx.RequestJobCancellation(f.job) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.ReportSessionStartupProgress(context.Background(), r); err == nil {
		t.Fatal("stopped receipt resurrected active presentation")
	}
}

func TestSessionStartupProgressOriginalPreparationAndTerminalFence(t *testing.T) {
	f := progressFixture(t)
	ctx := context.Background()
	repo := domain.NewID()
	originalJob := f.job
	f.job = domain.NewID()
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.progress-preparation", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, originalJob)
		if err != nil {
			return nil, err
		}
		j, err := store.Decode[domain.Job](r)
		if err != nil {
			return nil, err
		}
		j.Type = domain.PrepareWorkspaceJob
		j.Input = []byte(`{"session_id":"` + string(f.input.SessionID) + `","machine_id":"` + string(f.input.MachineID) + `","type":"worktree","primary_repository":"` + string(repo) + `","repositories":[{"id":"` + string(repo) + `","source_kind":"remote-clone","remote_url":"https://github.com/fixture/repo.git","checkout":"","base":{"type":"","name":""},"starting":{"type":"","name":""},"auto_fetch":false}]}`)
		if _, err := tx.PutJob(f.job, 0, r.SessionID, r.ProjectID, j); err != nil {
			return nil, err
		}
		sr, s, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		s.Preparation = &domain.SessionPreparation{JobID: f.job, State: domain.PreparationPending}
		s.ActiveExecutionID = ""
		s.Dispatch = domain.DispatchBlocked
		s.Outcome = domain.ExecutionNotStarted
		s.Problem = domain.InitialExecutionPending()
		return tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.SessionID, sr.ProjectID, s)
	})
	if err != nil {
		t.Fatal(err)
	}
	r := progressRequest(f)
	r.Msg.Mutation.ExpectedRevision = 1
	r.Msg.ExecutionId = ""
	r.Msg.NativePhase = 0
	r.Msg.WorkspaceOperation = pb.SessionStartupWorkspaceOperation_SESSION_STARTUP_WORKSPACE_OPERATION_CLONE
	r.Msg.RepositoryId = string(repo)
	r.Msg.RepositoryOrdinal = 1
	r.Msg.RepositoryCount = 1
	if _, err := f.client.ReportSessionStartupProgress(ctx, r); err != nil {
		t.Fatal("original pending preparation rejected", err)
	}
	bad := progressRequest(f)
	bad.Msg = proto.Clone(r.Msg).(*pb.ReportSessionStartupProgressRequest)
	bad.Msg.Mutation.RequestId = string(domain.NewID())
	bad.Msg.RepositoryId = string(domain.NewID())
	bad.Msg.Sequence = 2
	if _, err := f.client.ReportSessionStartupProgress(ctx, bad); err == nil {
		t.Fatal("foreign original repository accepted")
	}
	sr, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	s, err := store.Decode[domain.Session](sr)
	if err != nil || len(s.StartupProgress.Workspace.Steps) != 7 || s.Problem == nil || s.Dispatch != domain.DispatchBlocked {
		t.Fatal("preparation presentation changed admission", err)
	}
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.progress-terminal", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return nil, err
		}
		j, err := store.Decode[domain.Job](r)
		if err != nil {
			return nil, err
		}
		j.State = domain.JobSucceeded
		return tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, j)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.ReportSessionStartupProgress(ctx, r); err == nil {
		t.Fatal("terminal preparation replay accepted")
	}
}
