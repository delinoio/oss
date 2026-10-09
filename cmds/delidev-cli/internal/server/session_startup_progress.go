// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"slices"
)

func (s *Service) ReportSessionStartupProgress(ctx context.Context, req *connect.Request[pb.ReportSessionStartupProgressRequest]) (*connect.Response[pb.ReportSessionStartupProgressResponse], error) {
	q := req.Msg
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, q.MachineId, q.InstanceId); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	meta := q.Mutation
	step := domain.StartupProgressStep{WorkspaceOperation: domain.StartupWorkspaceOperation(q.WorkspaceOperation), NativePhase: domain.ExecutionStartupPhase(q.NativePhase), State: domain.StartupProgressState(q.State), Sequence: q.Sequence, RepositoryID: domain.ID(q.RepositoryId), RepositoryOrdinal: q.RepositoryOrdinal, RepositoryCount: q.RepositoryCount}
	if meta == nil || meta.ExpectedRevision == 0 || domain.ID(meta.Id).Validate() != nil || domain.ID(q.SessionId).Validate() != nil || step.Validate() != nil {
		return nil, rpc.Error(domain.StartupProgressRejected(), correlation)
	}
	identity := struct {
		Job, Machine, Instance, Device, Epoch, Session, Execution domain.ID
		Revision                                                  uint64
		Step                                                      domain.StartupProgressStep
	}{domain.ID(meta.Id), domain.ID(q.MachineId), domain.ID(q.InstanceId), actor.DeviceID, s.executionAuthority.epoch, domain.ID(q.SessionId), domain.ID(q.ExecutionId), meta.ExpectedRevision, step}
	scope := func(tx *store.Tx) (store.Record, domain.Session, []domain.StartupProgressStep, error) {
		if err := currentInstance(tx, identity.Machine, identity.Instance); err != nil {
			return store.Record{}, domain.Session{}, nil, err
		}
		r, err := tx.Get(domain.JobKind, identity.Job)
		if err != nil {
			return r, domain.Session{}, nil, err
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil || r.Revision != identity.Revision || r.SessionID != identity.Session || job.State != domain.JobClaimed || job.MachineID != identity.Machine || job.InstanceID != identity.Instance || job.AssignedDeviceID != identity.Device {
			return r, domain.Session{}, nil, domain.StartupProgressRejected()
		}
		_, machine, err := activeMachine(tx, identity.Machine)
		if err != nil || !slices.Contains(machine.WorkerCapabilities, domain.SessionStartupProgressV1) {
			return r, domain.Session{}, nil, domain.StartupProgressRejected()
		}
		sr, session, err := sessionRecord(tx, identity.Session)
		if err != nil || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery {
			return sr, session, nil, domain.StartupProgressRejected()
		}
		canceled, err := tx.JobCancellationRequested(identity.Job)
		if err != nil || canceled {
			return sr, session, nil, domain.StartupProgressRejected()
		}
		if job.Type == domain.PrepareWorkspaceJob {
			var input workspace.PrepareRequest
			if domain.Decode(job.Input, &input) != nil || input.SessionID != identity.Session || input.MachineID != identity.Machine || len(input.Repositories) > 100 || session.Preparation == nil || session.Preparation.JobID != identity.Job || session.Preparation.State != domain.PreparationPending || session.Problem != nil && (session.Problem.Code != domain.Unavailable || session.Problem.Message != domain.InitialExecutionPending().Message) || identity.Execution != "" || step.NativePhase != 0 {
				return sr, session, nil, domain.StartupProgressRejected()
			}
			return sr, session, workspace.StartupProgressPlan(input), nil
		}
		if job.Type != domain.ExecuteSessionJob || step.WorkspaceOperation != 0 {
			return sr, session, nil, domain.StartupProgressRejected()
		}
		input, sr, session, err := nativeExecutionScope(tx, r, job)
		if err != nil || session.Problem != nil || (session.Outcome != domain.ExecutionNotStarted && session.Outcome != domain.ExecutionRunning) || input.Version != 4 || input.ExecutionID != identity.Execution || session.Dispatch != domain.DispatchClaimed || session.Startup != nil && session.Startup.Failure != nil || session.Execution != nil && (session.Execution.CleanupVerified || session.Execution.Waiting.UserInput || session.Execution.Waiting.Approval) {
			return sr, session, nil, domain.StartupProgressRejected()
		}
		plan := []domain.StartupProgressStep{}
		for phase := domain.StartupResolve; phase <= domain.StartupExecution; phase++ {
			plan = append(plan, domain.StartupProgressStep{NativePhase: phase})
		}
		return sr, session, plan, nil
	}
	// Receipt replay is read-only, but cannot authenticate a departed/settled owner.
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { _, _, _, err := scope(tx); return err }); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "session.startup-progress", identity, func(tx *store.Tx) (any, error) {
		sr, session, plan, err := scope(tx)
		if err != nil {
			return nil, err
		}
		if session.StartupProgress == nil {
			session.StartupProgress = &domain.SessionStartupProgress{}
		}
		target := &session.StartupProgress.Workspace
		if identity.Execution != "" {
			target = &session.StartupProgress.Native
		}
		if *target == nil || (*target).JobID != identity.Job {
			*target = &domain.StartupProgressAttempt{JobID: identity.Job, ExecutionID: identity.Execution, AssignmentRevision: identity.Revision, MachineID: identity.Machine, InstanceID: identity.Instance, DeviceID: identity.Device, ServerEpoch: identity.Epoch, Steps: plan}
		}
		a := *target
		if a.ExecutionID != identity.Execution || a.AssignmentRevision != identity.Revision || a.InstanceID != identity.Instance || a.DeviceID != identity.Device || a.ServerEpoch != identity.Epoch || a.MachineID != identity.Machine {
			return nil, domain.StartupProgressRejected()
		}
		previousSequence := a.LastSequence
		if err := a.Apply(step); err != nil {
			return nil, err
		}
		if step.Sequence <= previousSequence {
			return step, nil
		}
		_, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
		return step, err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "session_startup_progress_reported", "session_id", identity.Session, "job_id", identity.Job, "sequence", step.Sequence, "workspace_operation", step.WorkspaceOperation, "native_phase", step.NativePhase, "state", step.State, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ReportSessionStartupProgressResponse{Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
