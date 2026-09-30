// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type forkReceipt struct {
	JobID domain.ID `json:"job_id"`
}

func forkConflict() error {
	return domain.Fail(domain.Conflict, "This session has no eligible completed Codex fork boundary.", "Finish native work and confirm its original completion, workspace ownership and cleanup first.")
}

func forkBoundary(tx *store.Tx, id domain.ID, expected domain.NativeIdentity) (store.Record, domain.Session, domain.ForkJobInput, error) {
	r, session, err := sessionRecord(tx, id)
	var input domain.ForkJobInput
	if err != nil {
		return r, session, input, err
	}
	if session.Fork != nil || session.InitialExecution == nil || session.Execution == nil || session.ActiveExecutionID != "" || session.PendingSteerID != "" || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.Outcome != domain.ExecutionSucceeded || !session.Execution.CleanupVerified || session.Execution.Waiting != (domain.NativeWaiting{}) || session.Execution.UnconfirmedResponses != 0 || session.Execution.NativeTurnID != string(expected) || session.Preparation == nil || session.Preparation.State != domain.PreparationReady {
		return r, session, input, forkConflict()
	}
	prior, err := tx.SessionExecutionJob(id, session.ExecutionSelection().ID)
	if err != nil {
		return r, session, input, err
	}
	job, err := store.Decode[domain.Job](prior)
	if err != nil {
		return r, session, input, err
	}
	if job.Type != domain.ExecuteSessionJob || job.State != domain.JobSucceeded || job.FinishedAt == nil || domain.Decode(job.Input, &input.SourceAssignment) != nil || !session.OwnsExecution(input.SourceAssignment) || domain.Decode(job.Output, &input.Completion) != nil || input.Completion.Version != 2 || input.Completion.Validate() != nil || input.SourceAssignment.Configuration.Harness != domain.Codex {
		return r, session, input, forkConflict()
	}
	if err := checkContinuationInputs(tx, id, input.SourceAssignment, *session.Execution); err != nil {
		return r, session, input, err
	}
	_, machine, err := activeMachine(tx, session.MachineID)
	if err != nil {
		return r, session, input, err
	}
	installation, err := checkedExecutionSelection(tx, session, machine, input.SourceAssignment)
	if err != nil {
		return r, session, input, err
	}
	if installation.Version != input.SourceAssignment.Installation.Version || installation.ResolvedPath != input.SourceAssignment.Installation.ResolvedPath {
		return r, session, input, forkConflict()
	}
	instance, seen, err := tx.WorkerInstance(session.MachineID)
	if err != nil || instance.Validate() != nil || time.Since(seen) > domain.WorkerConnectionTimeout || seen.After(time.Now().UTC().Add(time.Second)) {
		return r, session, input, domain.Fail(domain.Unavailable, "The original Worker is not connected.", "Reconnect the same machine before forking.")
	}
	input.Version, input.SourceSessionID, input.SourceRevision = 1, id, r.Revision
	input.SourceJobID, input.Progress, input.Snapshot = prior.ID, *session.Execution, *session.InitialExecution
	return r, session, input, nil
}

func (s *Service) ForkSession(ctx context.Context, req *connect.Request[pb.ForkSessionRequest]) (*connect.Response[pb.ForkSessionResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	meta := req.Msg.Mutation
	if meta == nil || domain.ID(meta.RequestId).Validate() != nil || domain.ID(meta.Id).Validate() != nil || meta.ExpectedRevision == 0 || domain.NativeIdentity(req.Msg.ExpectedTurnId).Validate(domain.Codex, domain.NativeTurnIdentity) != nil || domain.Text(req.Msg.Name, "fork name", 256, true) != nil {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Fork requires exact source identities, revision, boundary and name.", "Read the completed source session before submitting a fork."), correlation)
	}
	kind := domain.WorkspaceType("")
	switch req.Msg.Workspace {
	case pb.ForkWorkspace_FORK_WORKSPACE_UNSPECIFIED:
	case pb.ForkWorkspace_FORK_WORKSPACE_WORKTREE:
		kind = domain.Worktree
	case pb.ForkWorkspace_FORK_WORKSPACE_GENERAL_CHAT:
		kind = domain.GeneralChat
	case pb.ForkWorkspace_FORK_WORKSPACE_LOCAL:
		kind = domain.Local
	default:
		return nil, rpc.Error(forkConflict(), correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	var origin *domain.LocalOrigin
	var originDigest [32]byte
	if kind == domain.Local {
		var source domain.Session
		if err := s.Store.Read(ctx, func(tx *store.Tx) error { _, v, e := sessionRecord(tx, domain.ID(meta.Id)); source = v; return e }); err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if source.Workspace != domain.Local {
			return nil, rpc.Error(workspace.ValidateLocalForkSource(workspace.Manifest{Type: source.Workspace}), correlation)
		}
		var err error
		origin, originDigest, err = s.authenticateLocalOrigin(ctx, domain.CreateSession{Workspace: domain.Local, MachineID: source.MachineID}, req.Msg.LocalWorkerToken)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
	} else if req.Msg.LocalWorkerToken != "" {
		return nil, rpc.Error(localOriginRequired(), correlation)
	}
	identity := struct {
		Source    domain.ID
		Revision  uint64
		Turn      string
		Name      string
		Workspace domain.WorkspaceType
		Actor     domain.Principal
		Origin    *domain.LocalOrigin
	}{domain.ID(meta.Id), meta.ExpectedRevision, req.Msg.ExpectedTurnId, req.Msg.Name, kind, actor, origin}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "session.fork", identity, func(tx *store.Tx) (any, error) {
		if err := tx.RequireForkActor(actor); err != nil {
			return nil, err
		}
		if err := tx.RequireNoSessionFork(identity.Source); err != nil {
			return nil, err
		}
		r, session, input, err := forkBoundary(tx, identity.Source, domain.NativeIdentity(identity.Turn))
		if err != nil {
			return nil, err
		}
		if r.Revision != identity.Revision {
			return nil, forkConflict()
		}
		input.Workspace = kind
		if kind == "" {
			if session.Workspace == domain.GeneralChat {
				input.Workspace = domain.GeneralChat
			} else {
				input.Workspace = domain.Worktree
			}
		}
		if (session.Workspace == domain.GeneralChat) != (input.Workspace == domain.GeneralChat) {
			return nil, forkConflict()
		}
		if err := validateForkSharing(input); err != nil {
			return nil, err
		}
		if origin != nil {
			current, err := tx.Authenticate(originDigest[:])
			if err != nil {
				return nil, err
			}
			if current.DeviceID != origin.DeviceID || current.MachineID != session.MachineID || current.Type != domain.WorkerDevice {
				return nil, localOriginRequired()
			}
		}
		input.LocalOrigin, input.Actor, input.CreatedBy = origin, actor, actor.DeviceID
		input.ChildSessionID, input.RuntimeID, input.NativeRequestID = domain.NewID(), domain.NewID(), domain.NewID()
		input.Name = identity.Name
		if err := input.Validate(); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		jobID := domain.NewID()
		_, err = tx.PutJob(jobID, 0, r.ID, r.ProjectID, domain.Job{Type: domain.ForkSessionJob, State: domain.JobQueued, MachineID: session.MachineID, Input: raw, AcceptedAt: time.Now().UTC()})
		return forkReceipt{jobID}, err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt forkReceipt
	if domain.Decode(result.Data, &receipt) != nil {
		return nil, rpc.Error(forkConflict(), correlation)
	}
	response, err := s.readSessionFork(ctx, receipt.JobID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response.RequestId, response.Replayed = string(result.RequestID), result.Replayed
	s.logger.InfoContext(ctx, "session_fork_accepted", "source_session_id", meta.Id, "job_id", receipt.JobID, "replayed", result.Replayed)
	connected := connect.NewResponse(response)
	rpc.CopyCorrelation(connected, req.Header())
	return connected, nil
}

func (s *Service) readSessionFork(ctx context.Context, id domain.ID) (*pb.ForkSessionResponse, error) {
	response := &pb.ForkSessionResponse{}
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		r, err := tx.Get(domain.JobKind, id)
		if err != nil {
			return err
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil {
			return err
		}
		if job.Type != domain.ForkSessionJob {
			return forkConflict()
		}
		response.Job = rpc.Resource(r)
		if job.State == domain.JobSucceeded {
			var output domain.ForkJobResult
			if domain.Decode(job.Output, &output) != nil {
				return forkConflict()
			}
			child, err := tx.Get(domain.SessionKind, output.ChildSessionID)
			if err != nil {
				return err
			}
			response.Session = rpc.Resource(child)
		}
		return nil
	})
	return response, err
}

func (s *Service) GetSessionFork(ctx context.Context, req *connect.Request[pb.GetSessionForkRequest]) (*connect.Response[pb.GetSessionForkResponse], error) {
	value, err := s.readSessionFork(ctx, domain.ID(req.Msg.JobId))
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.GetSessionForkResponse{Job: value.Job, Session: value.Session})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func validateForkAuthority(tx *store.Tx, input domain.ForkJobInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	if err := tx.RequireForkActor(input.Actor); err != nil {
		return err
	}
	_, source, now, err := forkBoundary(tx, input.SourceSessionID, input.Completion.NativeTurnID)
	if err != nil {
		return err
	}
	if now.SourceRevision != input.SourceRevision || now.SourceJobID != input.SourceJobID || now.Completion != input.Completion || now.SourceAssignment.ConfigurationDigest != input.SourceAssignment.ConfigurationDigest || source.ExecutionSelection().AccountID != input.SourceAssignment.AccountID || source.ExecutionSelection().ConnectionID != input.SourceAssignment.ConnectionID {
		return forkConflict()
	}
	if err := validateForkSharing(input); err != nil {
		return err
	}
	if input.LocalOrigin != nil {
		return validateLocalOrigin(tx, domain.Session{Workspace: domain.Local, MachineID: source.MachineID, LocalOrigin: input.LocalOrigin})
	}
	return nil
}

func finishSessionFork(tx *store.Tx, r store.Record, job domain.Job, revision uint64, raw json.RawMessage, problem *domain.Error) (any, error) {
	var input domain.ForkJobInput
	if domain.Decode(job.Input, &input) != nil || input.Validate() != nil {
		return nil, forkConflict()
	}
	var output domain.ForkJobResult
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if problem == nil {
		if err := validateForkAuthority(tx, input); err != nil {
			problem = domain.Fail(domain.RecoveryRequired, "Fork publication lost its original authority.", "Preserve the accepted native child and reconcile its original job before another fork.")
		}
		_, machine, err := activeMachine(tx, job.MachineID)
		if err != nil || domain.Decode(raw, &output) != nil || output.ValidateIdentity(input) != nil || domain.Decode(output.Preparation, &preparation) != nil || domain.Decode(output.Manifest, &manifest) != nil || preparation.SessionID != input.ChildSessionID || preparation.MachineID != job.MachineID || preparation.Type != input.Workspace || preparation.ForkSourceID != input.SourceSessionID || workspace.ValidateResult(preparation, manifest, machine.OS) != nil {
			problem = domain.Fail(domain.RecoveryRequired, "Fork completion does not prove the independent native child and workspace.", "Retain the original job/runtime and do not retry creation.")
		}
		if validateForkWorkspace(input, preparation, manifest) != nil {
			problem = domain.Fail(domain.RecoveryRequired, "Fork workspace does not match its original source inventory.", "Preserve the original job and all private workspace evidence.")
		}
	}
	now := time.Now().UTC()
	job.FinishedAt = &now
	if problem != nil {
		job.Output = nil
		job.Problem = problem
		job.State = domain.JobFailed
		if problem.Code == domain.RecoveryRequired {
			job.State = domain.JobUncertain
		}
		if problem.Code == domain.Canceled {
			job.State = domain.JobCanceled
		}
		return tx.PutJob(r.ID, revision, r.SessionID, r.ProjectID, job)
	}
	// Publish the preparation, child and successful original job together. No
	// source queue, transcript, interaction, routing or session record is written.
	preparationID := domain.NewID()
	if _, err := tx.PutJob(preparationID, 0, input.ChildSessionID, r.ProjectID, domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobSucceeded, MachineID: job.MachineID, Input: output.Preparation, Output: output.Manifest, AcceptedAt: job.AcceptedAt, FinishedAt: &now}); err != nil {
		return nil, err
	}
	child := domain.Session{Name: input.Name, NameOwner: domain.ManualNameOwner, AgentID: input.SourceAssignment.Configuration.AgentID, MachineID: job.MachineID, ProjectID: r.ProjectID, Workspace: input.Workspace, LocalOrigin: input.LocalOrigin, Source: domain.ManualSession, CreatedBy: input.CreatedBy, Outcome: domain.ExecutionNotStarted, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Dispatch: domain.DispatchPaused, Preparation: &domain.SessionPreparation{JobID: preparationID, State: domain.PreparationReady}, Fork: &domain.ForkOrigin{SourceSessionID: input.SourceSessionID, SourceRevision: input.SourceRevision, SourceExecutionID: input.Completion.ExecutionID, SourceTurnID: input.Completion.NativeTurnID, JobID: r.ID, RuntimeID: input.RuntimeID, NativeThreadID: output.NativeThreadID, CheckpointDigest: output.CheckpointDigest, Snapshot: input.Snapshot, WorkerDeviceID: job.AssignedDeviceID, JobInputDigest: forkInputDigest(job.Input)}}
	if _, err := tx.Put(domain.SessionKind, input.ChildSessionID, 0, input.ChildSessionID, r.ProjectID, child); err != nil {
		return nil, err
	}
	job.State, job.Output = domain.JobSucceeded, raw
	return tx.PutJob(r.ID, revision, r.SessionID, r.ProjectID, job)
}

func validateForkWorkspace(input domain.ForkJobInput, preparation workspace.PrepareRequest, manifest workspace.Manifest) error {
	if err := validateForkSharing(input); err != nil {
		return err
	}
	var source workspace.Manifest
	if domain.Decode(input.SourceAssignment.Manifest, &source) != nil || len(preparation.Repositories) != len(source.Repositories) || len(manifest.Repositories) != len(source.Repositories) {
		return forkConflict()
	}
	if input.Workspace == domain.GeneralChat {
		if preparation.ForkSourcePath != source.PrimaryPath {
			return forkConflict()
		}
		return nil
	}
	if preparation.ForkSourcePath != "" {
		return forkConflict()
	}
	for i, repo := range source.Repositories {
		spec := preparation.Repositories[i]
		if spec.ID != repo.ID || spec.Checkout != repo.Path || spec.AutoFetch || spec.PRTarget != nil || spec.PreferredRemote != "" || spec.Base.Type != domain.CommitReference || spec.Base.Name != manifest.Repositories[i].BaseCommit {
			return forkConflict()
		}
		if input.Workspace == domain.Worktree && (spec.ForkRegistrationSource != repo.Source || spec.Starting != spec.Base || manifest.Repositories[i].StartingCommit != spec.Base.Name) {
			return forkConflict()
		}
		if input.Workspace == domain.Local && (spec.ForkRegistrationSource != "" || spec.Starting != (domain.Reference{}) || preparation.OriginMachineID != input.SourceAssignment.MachineID) {
			return forkConflict()
		}
		if repo.Path == source.PrimaryPath && preparation.PrimaryRepository != repo.ID {
			return forkConflict()
		}
	}
	return nil
}

func validateForkSharing(input domain.ForkJobInput) error {
	if input.Workspace != domain.Local {
		return nil
	}
	var source workspace.Manifest
	if domain.Decode(input.SourceAssignment.Manifest, &source) != nil {
		return forkConflict()
	}
	return workspace.ValidateLocalForkSource(source)
}

func forkInputDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
