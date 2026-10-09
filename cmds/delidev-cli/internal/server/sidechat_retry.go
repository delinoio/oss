// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type sidechatRetryView struct {
	Candidate          bool                   `json:"candidate"`
	Eligible           bool                   `json:"eligible"`
	ChildRevision      uint64                 `json:"child_revision,string"`
	QuestionID         domain.ID              `json:"question_id,omitempty"`
	QuestionRevision   uint64                 `json:"question_revision,string"`
	ParentID           domain.ID              `json:"parent_id,omitempty"`
	ParentRevision     uint64                 `json:"parent_revision,string"`
	ParentTurnID       domain.NativeIdentity  `json:"parent_turn_id,omitempty"`
	CurrentAnswer      domain.ID              `json:"current_answer,omitempty"`
	Generations        []domain.SidechatRetry `json:"generations"`
	ObservedGeneration domain.ID              `json:"observed_generation,omitempty"`
	Phase              domain.JobState        `json:"phase,omitempty"`
	Problem            *domain.Error          `json:"problem,omitempty"`
}

func retryConflict() *domain.Error {
	return domain.Fail(domain.Conflict, "Sidechat retry requires its settled original question and latest completed parent execution.", "Wait for original work and cleanup, then reload the current Sidechat and parent. No retry is scheduled.")
}

func sidechatRetryCandidate(tx *store.Tx, id domain.ID) (store.Record, domain.Session, store.Record, domain.QueuedInput, error) {
	cr, child, err := sessionRecord(tx, id)
	if err != nil {
		return cr, child, store.Record{}, domain.QueuedInput{}, err
	}
	if !child.IsSidechat() || child.Fork.Validate() != nil {
		return cr, child, store.Record{}, domain.QueuedInput{}, domain.SidechatUnavailable()
	}
	qr, q, err := tx.DirectSidechatQuestion(id)
	return cr, child, qr, q, err
}

func sidechatRetryBoundary(tx *store.Tx, id domain.ID) (store.Record, domain.Session, store.Record, domain.QueuedInput, store.Record, domain.ForkJobInput, error) {
	cr, child, qr, q, err := sidechatRetryCandidate(tx, id)
	empty := domain.ForkJobInput{}
	if err != nil {
		return cr, child, qr, q, store.Record{}, empty, err
	}
	if child.SidechatActiveRetry != "" || !child.WorkspaceAvailable() || child.ActiveExecutionID != "" || child.PendingInputs != 0 || child.PendingSteerID != "" || child.CompactionJobID != "" || child.ExecutionRecoveryJobID != "" || child.Archive != domain.NotArchived || child.Recovery != domain.NoRecovery || child.Execution == nil || !child.Execution.CleanupVerified || child.Execution.Waiting != (domain.NativeWaiting{}) || child.Execution.UnconfirmedResponses != 0 || child.Preparation == nil || child.Preparation.State != domain.PreparationReady {
		return cr, child, qr, q, store.Record{}, empty, retryConflict()
	}
	if err := requireSidechatParent(tx, child); err != nil {
		return cr, child, qr, q, store.Record{}, empty, err
	}
	pr, parent, err := sessionRecord(tx, child.Fork.SourceSessionID)
	if err != nil {
		return cr, child, qr, q, pr, empty, err
	}
	if parent.PendingInputs != 0 || parent.CompactionJobID != "" || parent.ExecutionRecoveryJobID != "" {
		return cr, child, qr, q, pr, empty, retryConflict()
	}
	if parent.Execution == nil {
		return cr, child, qr, q, pr, empty, retryConflict()
	}
	_, _, input, err := forkBoundary(tx, pr.ID, domain.NativeIdentity(parent.Execution.NativeTurnID))
	if err != nil {
		return cr, child, qr, q, pr, empty, err
	}
	if input.SourceAssignment.Configuration.Harness != domain.Codex || input.SourceAssignment.AccountID != child.Fork.Snapshot.InitialAccountID || input.SourceAssignment.ConnectionID != child.Fork.Snapshot.ConnectionID || input.SourceAssignment.ConfigurationDigest != child.Fork.SidechatParentSnapshot.ConfigurationDigest || input.SourceAssignment.MachineID != child.MachineID {
		return cr, child, qr, q, pr, empty, domain.SidechatUnavailable()
	}
	baseline := child.SidechatCurrentAnswer
	if baseline == "" {
		baseline = q.ExecutionID
	}
	answer, err := tx.SessionExecutionJob(id, baseline)
	if err != nil {
		return cr, child, qr, q, pr, empty, err
	}
	answerJob, err := store.Decode[domain.Job](answer)
	var completion domain.ExecutionCompletion
	if err != nil || answerJob.State != domain.JobSucceeded || domain.Decode(answerJob.Output, &completion) != nil || completion.Validate() != nil || completion.Outcome != domain.ExecutionSucceeded || completion.ExecutionID != baseline {
		return cr, child, qr, q, pr, empty, retryConflict()
	}
	_, machine, err := activeMachine(tx, child.MachineID)
	if err != nil || !slices.Contains(machine.WorkerCapabilities, domain.SidechatQuestionRetryV1) || !slices.Contains(machine.WorkerCapabilities, domain.CodexReadOnlySidechatWorkerV1) {
		return cr, child, qr, q, pr, empty, domain.SidechatUnavailable()
	}
	input.Version, input.Purpose, input.Workspace = 3, domain.SidechatFork, child.Workspace
	input.LocalOrigin = child.LocalOrigin
	if input.SourceAssignment.Configuration.Subscription {
		if !domain.ManagedSidechatSupported(machine.WorkerCapabilities) {
			return cr, child, qr, q, pr, empty, domain.SidechatUnavailable()
		}
		_, account, err := subscriptionAccount(tx, input.SourceAssignment.AccountID, 0)
		if err != nil || account.Subscription == nil || account.Subscription.Lease != nil || account.Subscription.RecoveryRequired || account.Subscription.Pending != nil || account.Subscription.ServerObservationActive() || account.Subscription.Observation != nil && account.Subscription.Observation.Active() {
			return cr, child, qr, q, pr, empty, subscriptionDenied()
		}
		input.SubscriptionGeneration = account.Subscription.Generation
	}
	return cr, child, qr, q, pr, input, nil
}

func (s *Service) RetrySidechatQuestion(ctx context.Context, req *connect.Request[pb.RetrySidechatQuestionRequest]) (*connect.Response[pb.RetrySidechatQuestionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if validateSessionMutation(m) != nil || domain.ID(req.Msg.QuestionId).Validate() != nil || req.Msg.ExpectedQuestionRevision == 0 || req.Msg.ExpectedParentRevision == 0 || domain.NativeIdentity(req.Msg.ExpectedParentTurnId).Validate(domain.Codex, domain.NativeTurnIdentity) != nil {
		return nil, rpc.Error(retryConflict(), c)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Child                            domain.ID
		Revision                         uint64
		Question                         domain.ID
		QuestionRevision, ParentRevision uint64
		Turn                             domain.NativeIdentity
		Actor                            domain.Principal
	}{domain.ID(m.Id), m.ExpectedRevision, domain.ID(req.Msg.QuestionId), req.Msg.ExpectedQuestionRevision, req.Msg.ExpectedParentRevision, domain.NativeIdentity(req.Msg.ExpectedParentTurnId), actor}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "session.sidechat-question-retry", identity, func(tx *store.Tx) (any, error) {
		if err := tx.RequireForkActor(actor); err != nil {
			return nil, err
		}
		cr, child, qr, q, pr, input, err := sidechatRetryBoundary(tx, identity.Child)
		if err != nil {
			return nil, err
		}
		if cr.Revision != identity.Revision || qr.ID != identity.Question || qr.Revision != identity.QuestionRevision || pr.Revision != identity.ParentRevision || input.Completion.NativeTurnID != identity.Turn {
			return nil, retryConflict()
		}
		if err := tx.RequireNoSessionFork(cr.ID); err != nil {
			return nil, err
		}
		if err := tx.RequireNoSessionFork(pr.ID); err != nil {
			return nil, err
		}
		if err := tx.CheckSidechatRetryCapacity(cr, pr, actor); err != nil {
			return nil, err
		}
		prepared, err := tx.Get(domain.JobKind, child.Preparation.JobID)
		if err != nil {
			return nil, err
		}
		preparation, err := store.Decode[domain.Job](prepared)
		if err != nil {
			return nil, err
		}
		input.ChildSessionID, input.RuntimeID, input.NativeRequestID = cr.ID, domain.NewID(), domain.NewID()
		input.Actor, input.CreatedBy, input.Name = actor, actor.DeviceID, child.Name
		instance, seen, err := tx.WorkerInstance(child.MachineID)
		if err != nil || instance.Validate() != nil || time.Since(seen) > domain.WorkerConnectionTimeout {
			return nil, retryConflict()
		}
		input.Retry = &domain.SidechatRetryFork{WorkerInstanceID: instance, WorkerDeviceID: child.Fork.WorkerDeviceID, GenerationID: domain.ID(m.RequestId), ChildRevision: cr.Revision, QuestionID: qr.ID, QuestionRevision: qr.Revision, PreviousJobID: child.Execution.JobID, PreviousExecutionID: child.Execution.ExecutionID, ChildPreparation: preparation.Input, ChildManifest: preparation.Output}
		if input.Validate() != nil {
			return nil, domain.SidechatUnavailable()
		}
		jobID := domain.NewID()
		child.SidechatRetries = append(child.SidechatRetries, domain.SidechatRetry{WorkerInstanceID: instance, WorkerDeviceID: child.Fork.WorkerDeviceID, ID: domain.ID(m.RequestId), ForkJobID: jobID, RuntimeID: input.RuntimeID, QuestionID: qr.ID, QuestionRevision: qr.Revision, ParentRevision: pr.Revision, ParentExecutionID: input.Completion.ExecutionID, ParentTurnID: input.Completion.NativeTurnID})
		if child.SidechatCurrentAnswer == "" {
			child.SidechatCurrentAnswer = q.ExecutionID
		}
		child.SidechatActiveRetry, child.AutomaticRemediationStopped = domain.ID(m.RequestId), false
		if _, err := tx.Put(domain.SessionKind, cr.ID, cr.Revision, cr.ID, cr.ProjectID, child); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		if _, err := tx.PutJob(jobID, 0, cr.ID, cr.ProjectID, domain.Job{Type: domain.ForkSessionJob, State: domain.JobQueued, MachineID: child.MachineID, Input: raw, AcceptedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		return struct {
			Child      domain.ID `json:"child"`
			Generation domain.ID `json:"generation"`
		}{cr.ID, domain.ID(m.RequestId)}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	raw, err := s.readSidechatRetry(ctx, identity.Child, domain.ID(m.RequestId))
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "sidechat_question_retry", "generation_id", m.RequestId, "phase", "observed", "replayed", result.Replayed)
	return connect.NewResponse(&pb.RetrySidechatQuestionResponse{DocumentJson: raw, RequestId: m.RequestId, Replayed: result.Replayed}), nil
}

func (s *Service) GetSidechatQuestionRetry(ctx context.Context, req *connect.Request[pb.GetSidechatQuestionRetryRequest]) (*connect.Response[pb.GetSidechatQuestionRetryResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	if domain.ID(req.Msg.SessionId).Validate() != nil || req.Msg.RequestId != "" && domain.ID(req.Msg.RequestId).Validate() != nil {
		return nil, rpc.Error(retryConflict(), c)
	}
	raw, err := s.readSidechatRetry(ctx, domain.ID(req.Msg.SessionId), domain.ID(req.Msg.RequestId))
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(&pb.GetSidechatQuestionRetryResponse{DocumentJson: raw}), nil
}

func (s *Service) readSidechatRetry(ctx context.Context, childID, generationID domain.ID) ([]byte, error) {
	var view sidechatRetryView
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		actor, _ := domain.PrincipalFrom(ctx)
		if err := tx.RequireForkActor(actor); err != nil {
			return err
		}
		cr, child, qr, q, err := sidechatRetryCandidate(tx, childID)
		view.ChildRevision, view.Generations = cr.Revision, child.SidechatRetries
		if view.Generations == nil {
			view.Generations = []domain.SidechatRetry{}
		}
		view.CurrentAnswer = child.SidechatCurrentAnswer
		if view.CurrentAnswer == "" {
			view.CurrentAnswer = q.ExecutionID
		}
		if cr.ID == "" {
			return err
		}
		if err != nil {
			view.Problem = domain.SafeError(err)
		} else {
			view.Candidate, view.QuestionID, view.QuestionRevision, view.ParentID = true, qr.ID, qr.Revision, child.Fork.SourceSessionID
			_, _, _, _, pr, input, boundaryErr := sidechatRetryBoundary(tx, childID)
			view.ParentRevision, view.ParentTurnID = pr.Revision, input.Completion.NativeTurnID
			view.Eligible = boundaryErr == nil
			if boundaryErr != nil {
				view.Problem = domain.SafeError(boundaryErr)
			}
		}
		observed := generationID
		if observed == "" {
			observed = child.SidechatActiveRetry
		}
		if observed == "" && len(child.SidechatRetries) != 0 {
			// Reopened presentations retain the last attempt's localized outcome;
			// this is observation only, never authority for another native send.
			observed = child.SidechatRetries[len(child.SidechatRetries)-1].ID
		}
		if observed != "" {
			for _, g := range child.SidechatRetries {
				if g.ID == observed {
					view.ObservedGeneration = g.ID
					jobID := g.ForkJobID
					if g.ExecutionJobID != "" {
						jobID = g.ExecutionJobID
					}
					r, err := tx.Get(domain.JobKind, jobID)
					if err != nil {
						return err
					}
					job, err := store.Decode[domain.Job](r)
					if err != nil {
						return err
					}
					view.Phase = job.State
					if job.Problem != nil {
						view.Problem = job.Problem
					}
					return nil
				}
			}
			return retryConflict()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// Replay refers to the accepted parent job, not the parent's later current turn.
// Current policy, native cleanup and the unchanged child workspace are still
// checked at every native claim and publication boundary.
func validateSidechatRetryForkAuthority(tx *store.Tx, input domain.ForkJobInput) error {
	if input.Validate() != nil || input.Retry == nil {
		return domain.SidechatUnavailable()
	}
	if err := tx.RequireForkActor(input.Actor); err != nil {
		return err
	}
	cr, child, qr, _, err := sidechatRetryCandidate(tx, input.ChildSessionID)
	if err != nil || qr.ID != input.Retry.QuestionID || qr.Revision != input.Retry.QuestionRevision || child.SidechatActiveRetry != input.Retry.GenerationID || child.AutomaticRemediationStopped || child.Archive != domain.NotArchived || child.Recovery != domain.NoRecovery || child.ActiveExecutionID != "" || child.PendingInputs != 0 || child.PendingSteerID != "" || child.CompactionJobID != "" || child.Execution == nil || !child.Execution.CleanupVerified || child.Execution.JobID != input.Retry.PreviousJobID || child.Execution.ExecutionID != input.Retry.PreviousExecutionID {
		return retryConflict()
	}
	if err := requireSidechatParent(tx, child); err != nil {
		return err
	}
	pr, parent, err := sessionRecord(tx, input.SourceSessionID)
	if err != nil {
		return err
	}
	if parent.Archive != domain.NotArchived || parent.Recovery != domain.NoRecovery || !parent.WorkspaceAvailable() {
		return retryConflict()
	}
	_, machine, err := activeMachine(tx, child.MachineID)
	if err != nil || !slices.Contains(machine.WorkerCapabilities, domain.SidechatQuestionRetryV1) || !slices.Contains(machine.WorkerCapabilities, domain.CodexReadOnlySidechatWorkerV1) {
		return domain.SidechatUnavailable()
	}
	instance, seen, instanceErr := tx.WorkerInstance(child.MachineID)
	if instanceErr != nil || instance != input.Retry.WorkerInstanceID || time.Since(seen) > domain.WorkerConnectionTimeout || input.Retry.WorkerDeviceID != child.Fork.WorkerDeviceID {
		return retryConflict()
	}
	if err := checkedExecutionSource(tx, pr, parent, machine, input.SourceAssignment); err != nil {
		return err
	}
	if input.SourceAssignment.AccountID != child.Fork.Snapshot.InitialAccountID || input.SourceAssignment.ConnectionID != child.Fork.Snapshot.ConnectionID || input.SourceAssignment.ConfigurationDigest != child.Fork.SidechatParentSnapshot.ConfigurationDigest {
		return domain.SidechatUnavailable()
	}
	original, err := tx.Get(domain.JobKind, input.SourceJobID)
	if err != nil {
		return err
	}
	originalJob, err := store.Decode[domain.Job](original)
	if err != nil || original.SessionID != input.SourceSessionID || originalJob.State != domain.JobSucceeded || originalJob.AssignedDeviceID != input.Retry.WorkerDeviceID || !bytes.Equal(originalJob.Input, mustForkValueJSON(input.SourceAssignment)) || !bytes.Equal(originalJob.Output, mustForkValueJSON(input.Completion)) {
		return retryConflict()
	}
	prepared, err := tx.Get(domain.JobKind, child.Preparation.JobID)
	if err != nil {
		return err
	}
	preparedJob, err := store.Decode[domain.Job](prepared)
	if err != nil || prepared.SessionID != cr.ID || preparedJob.State != domain.JobSucceeded || !bytes.Equal(preparedJob.Input, input.Retry.ChildPreparation) || !bytes.Equal(preparedJob.Output, input.Retry.ChildManifest) {
		return retryConflict()
	}
	if input.SubscriptionGeneration != "" {
		_, a, err := subscriptionAccount(tx, input.SourceAssignment.AccountID, 0)
		if err != nil || !domain.ManagedSidechatSupported(machine.WorkerCapabilities) || a.Subscription == nil || a.Subscription.RecoveryRequired || a.Connection == nil || a.Connection.ID != input.SourceAssignment.ConnectionID || a.Subscription.Generation != input.SubscriptionGeneration {
			return subscriptionDenied()
		}
	}
	return nil
}

func finishSidechatRetryFork(tx *store.Tx, r store.Record, job domain.Job, revision uint64, raw json.RawMessage, problem *domain.Error, input domain.ForkJobInput, output domain.ForkJobResult) (any, error) {
	cr, child, err := sessionRecord(tx, input.ChildSessionID)
	if err != nil {
		return nil, err
	}
	index := -1
	for i, g := range child.SidechatRetries {
		if g.ID == input.Retry.GenerationID && g.ForkJobID == r.ID && g.RuntimeID == input.RuntimeID {
			index = i
		}
	}
	if index < 0 {
		return nil, retryConflict()
	}
	now := time.Now().UTC()
	job.FinishedAt = &now
	// A verified completed private runtime remains a cleanup owner even when
	// current authority prevents public answer publication.
	if output.ValidateIdentity(input) == nil && bytes.Equal(output.Preparation, input.Retry.ChildPreparation) && bytes.Equal(output.Manifest, input.Retry.ChildManifest) {
		child.SidechatRetries[index].Fork = &domain.SessionDeletionFork{JobID: r.ID, RuntimeID: input.RuntimeID, CheckpointDigest: output.CheckpointDigest, JobInputDigest: forkInputDigest(job.Input)}
	}
	if problem != nil {
		job.State, job.Problem, job.Output = domain.JobFailed, problem, nil
		if problem.Code == domain.RecoveryRequired {
			job.State = domain.JobUncertain
		}
		if problem.Code == domain.Canceled {
			job.State = domain.JobCanceled
		}
		// A failed native creation lacks an execution cleanup proof. Keep its
		// original owner reachable and fence fresh attempts until explicit recovery.
		child.Recovery, child.Dispatch, child.Problem = domain.NeedsRecovery, domain.DispatchPaused, problem
		if _, err := tx.Put(domain.SessionKind, cr.ID, cr.Revision, cr.ID, cr.ProjectID, child); err != nil {
			return nil, err
		}
		return tx.PutJob(r.ID, revision, r.SessionID, r.ProjectID, job)
	}
	if !bytes.Equal(output.Preparation, input.Retry.ChildPreparation) || !bytes.Equal(output.Manifest, input.Retry.ChildManifest) {
		return nil, retryConflict()
	}
	_, q, err := tx.DirectSidechatQuestion(cr.ID)
	if err != nil {
		return nil, err
	}
	question := queuedSessionInput(q)
	inputID, err := appendSessionInput(tx, cr.ID, &child, question)
	if err != nil {
		return nil, err
	}
	queuedRecord, err := tx.Get(domain.QueueKind, inputID)
	if err != nil {
		return nil, err
	}
	queued, err := store.Decode[domain.QueuedInput](queuedRecord)
	if err != nil {
		return nil, err
	}
	snapshot := child.Fork.Snapshot
	assignment := domain.ExecutionJobInput{Version: 3, SessionID: cr.ID, MachineID: child.MachineID, ExecutionID: domain.NewID(), InputID: inputID, ThreadRequestID: domain.NewID(), TurnRequestID: domain.NewID(), Configuration: snapshot.Configuration, ConfigurationDigest: snapshot.ConfigurationDigest, AccountID: snapshot.InitialAccountID, ConnectionID: snapshot.ConnectionID, Input: question, Startup: input.Startup, Installation: input.SourceAssignment.Installation, Fork: &domain.ForkExecution{JobID: r.ID, RuntimeID: input.RuntimeID, NativeThreadID: output.NativeThreadID, NativeTurnID: output.NativeTurnID, CheckpointDigest: output.CheckpointDigest, HistoryRequestID: domain.NewID()}, SidechatRetry: &domain.SidechatRetryExecution{WorkerInstanceID: input.Retry.WorkerInstanceID, WorkerDeviceID: input.Retry.WorkerDeviceID, GenerationID: input.Retry.GenerationID, QuestionID: input.Retry.QuestionID, PreviousJobID: input.Retry.PreviousJobID, PreviousExecutionID: input.Retry.PreviousExecutionID}}
	_, machine, err := activeMachine(tx, child.MachineID)
	if err != nil {
		return nil, err
	}
	assignment, err = checkedExecutionAssignment(tx, cr, child, machine, assignment)
	if err != nil {
		return nil, err
	}
	executionJobID := domain.NewID()
	g := &child.SidechatRetries[index]
	g.ExecutionID, g.ExecutionJobID = assignment.ExecutionID, executionJobID
	g.Fork = &domain.SessionDeletionFork{JobID: r.ID, RuntimeID: input.RuntimeID, CheckpointDigest: output.CheckpointDigest, JobInputDigest: forkInputDigest(job.Input)}
	child.CurrentExecution = &domain.ExecutionSelection{ID: assignment.ExecutionID, InputID: inputID, AccountID: assignment.AccountID, ConnectionID: assignment.ConnectionID}
	child.NativeExecutionRootID, child.ActiveExecutionID, child.Dispatch, child.NextExecutionIntent = input.RuntimeID, assignment.ExecutionID, domain.DispatchClaimed, ""
	child.Execution, child.Startup, child.Problem = nil, nil, nil
	child.Outcome = domain.ExecutionNotStarted
	queued.SidechatRetryGeneration, queued.Delivery, queued.ExecutionID, queued.NativeRequestID = g.ID, domain.InputClaimed, assignment.ExecutionID, assignment.TurnRequestID
	if _, err := tx.Put(domain.QueueKind, inputID, queuedRecord.Revision, cr.ID, cr.ProjectID, queued); err != nil {
		return nil, err
	}
	if _, err := tx.Put(domain.SessionKind, cr.ID, cr.Revision, cr.ID, cr.ProjectID, child); err != nil {
		return nil, err
	}
	assignmentRaw, err := json.Marshal(assignment)
	if err != nil {
		return nil, err
	}
	if _, err := tx.PutJob(executionJobID, 0, cr.ID, cr.ProjectID, domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobQueued, MachineID: child.MachineID, Input: assignmentRaw, AcceptedAt: now}); err != nil {
		return nil, err
	}
	job.State, job.Output = domain.JobSucceeded, raw
	return tx.PutJob(r.ID, revision, r.SessionID, r.ProjectID, job)
}

func validateSidechatRetryWorkspace(input domain.ForkJobInput, preparation workspace.PrepareRequest, manifest workspace.Manifest) error {
	if input.Retry == nil || !bytes.Equal(input.Retry.ChildPreparation, mustForkValueJSON(preparation)) || !bytes.Equal(input.Retry.ChildManifest, mustForkValueJSON(manifest)) || preparation.SidechatSource == nil || manifest.Reference == nil || preparation.ForkProfile != workspace.CodexSidechatReferenceV1 || preparation.ForkSourceID != input.SourceSessionID {
		return retryConflict()
	}
	if !bytes.Equal(mustForkValueJSON(preparation.SidechatSource.Preparation), input.SourceAssignment.Preparation) || !bytes.Equal(mustForkValueJSON(preparation.SidechatSource.Manifest), input.SourceAssignment.Manifest) {
		return retryConflict()
	}
	return nil
}

// Stop/Archive cancel the original owner only. A possible native send stays
// fenced even after a terminal error until its independent cleanup is proved.
func cancelSidechatRetry(tx *store.Tx, child *domain.Session) (bool, error) {
	if child.SidechatActiveRetry == "" {
		return false, nil
	}
	for _, g := range child.SidechatRetries {
		if g.ID == child.SidechatActiveRetry {
			id := g.ForkJobID
			if g.ExecutionJobID != "" {
				id = g.ExecutionJobID
			}
			r, err := tx.Get(domain.JobKind, id)
			if err != nil {
				return false, err
			}
			job, err := store.Decode[domain.Job](r)
			if err != nil {
				return false, err
			}
			if g.ExecutionJobID != "" {
				return false, nil
			} // Existing execution Stop owns it.
			if job.State == domain.JobQueued || job.InstanceID == "" && job.State.Terminal() {
				now := time.Now().UTC()
				job.State, job.FinishedAt, job.Problem = domain.JobCanceled, &now, domain.Fail(domain.Canceled, "Sidechat retry was stopped before native admission.", "The previous answer remains available.")
				if _, err := tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job); err != nil {
					return false, err
				}
				child.SidechatActiveRetry = ""
				return false, nil
			}
			if err := tx.RequestJobCancellation(id); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, retryConflict()
}
