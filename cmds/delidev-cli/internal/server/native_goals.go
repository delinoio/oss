// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type goalAcceptance struct {
	Session  domain.ID
	Revision uint64
	Actor    domain.Principal
	Input    domain.NativeGoalActionInput
}

func goalCommand(req *pb.RequestSessionGoalActionRequest) (domain.NativeGoalActionInput, error) {
	a := domain.NativeGoalActionInput{Version: 1, RequestID: domain.ID(req.Mutation.RequestId), ExecutionID: domain.ID(req.ExpectedExecutionId), NativeThreadID: domain.ID(req.ExpectedNativeThreadId)}
	switch req.Action {
	case pb.NativeGoalAction_NATIVE_GOAL_ACTION_SET:
		a.Command = domain.NativeGoalSetCommand
	case pb.NativeGoalAction_NATIVE_GOAL_ACTION_READ:
		a.Command = domain.NativeGoalReadCommand
	case pb.NativeGoalAction_NATIVE_GOAL_ACTION_CLEAR:
		a.Command = domain.NativeGoalClearCommand
	default:
		return a, domain.NativeGoalUncertain()
	}
	if req.Set != nil {
		a.Set = &domain.NativeGoalSet{Objective: req.Set.Objective}
		if req.Set.Status != nil {
			statuses := map[pb.NativeGoalStatus]domain.NativeGoalStatus{pb.NativeGoalStatus_NATIVE_GOAL_STATUS_ACTIVE: domain.NativeGoalActive, pb.NativeGoalStatus_NATIVE_GOAL_STATUS_PAUSED: domain.NativeGoalPaused, pb.NativeGoalStatus_NATIVE_GOAL_STATUS_BLOCKED: domain.NativeGoalBlocked, pb.NativeGoalStatus_NATIVE_GOAL_STATUS_USAGE_LIMITED: domain.NativeGoalUsageLimited, pb.NativeGoalStatus_NATIVE_GOAL_STATUS_BUDGET_LIMITED: domain.NativeGoalBudgetLimited, pb.NativeGoalStatus_NATIVE_GOAL_STATUS_COMPLETE: domain.NativeGoalComplete}
			status, ok := statuses[*req.Set.Status]
			if !ok {
				return a, domain.NativeGoalUncertain()
			}
			a.Set.Status = &status
		}
		switch b := req.Set.Budget.(type) {
		case nil:
		case *pb.NativeGoalSet_TokenBudget:
			if b.TokenBudget <= 0 {
				return a, domain.NativeGoalUncertain()
			}
			a.Set.TokenBudget = json.RawMessage(strconv.FormatInt(b.TokenBudget, 10))
		case *pb.NativeGoalSet_ResetTokenBudget:
			if !b.ResetTokenBudget {
				return a, domain.NativeGoalUncertain()
			}
			a.Set.TokenBudget = json.RawMessage("null")
		default:
			return a, domain.NativeGoalUncertain()
		}
	}
	if a.RequestID.Validate() != nil || a.ExecutionID.Validate() != nil || a.NativeThreadID.Validate() != nil || a.Command == domain.NativeGoalSetCommand && (a.Set == nil || a.Set.Validate() != nil) || a.Command != domain.NativeGoalSetCommand && a.Set != nil {
		return a, domain.NativeGoalUncertain()
	}
	return a, nil
}

// Scope is re-evaluated at acceptance, claim and result. Neither a stored
// capability nor a requested native flag substitutes for the original observed
// enabled feature, retained grant, current account and execution policy.
func (s *Service) goalScope(tx *store.Tx, sr store.Record, session domain.Session, execution, thread domain.ID) (domain.ExecutionJobInput, store.ExecutionGrant, error) {
	var input domain.ExecutionJobInput
	p, g := session.Execution, session.NativeGoal
	if s.executionAuthority == nil || session.IsSidechat() || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.ActiveExecutionID != execution || session.Dispatch != domain.DispatchClaimed || p == nil || p.ExecutionID != execution || p.NativeThreadID != string(thread) || p.CleanupVerified || g == nil || !g.Enabled || g.SourceExecutionID != execution || g.SourceNativeThreadID != thread {
		return input, store.ExecutionGrant{}, domain.NativeGoalUncertain()
	}
	jr, err := tx.SessionExecutionJob(sr.ID, execution)
	if err != nil {
		return input, store.ExecutionGrant{}, err
	}
	job, err := store.Decode[domain.Job](jr)
	if err != nil {
		return input, store.ExecutionGrant{}, err
	}
	if jr.ID != p.JobID || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || !input.NativeGoals || input.Configuration.Harness != domain.Codex || !session.OwnsExecution(input) {
		return input, store.ExecutionGrant{}, domain.NativeGoalUncertain()
	}
	_, machine, err := activeMachine(tx, input.MachineID)
	if err != nil || !slices.Contains(machine.WorkerCapabilities, domain.NativeCodexGoalsV1) {
		return input, store.ExecutionGrant{}, domain.NativeGoalUncertain()
	}
	grant, err := tx.ExecutionGrantForJob(jr.ID)
	if err != nil {
		return input, grant, err
	}
	if _, err = s.executionAuthority.scope(tx, grant); err != nil {
		return input, grant, err
	}
	return input, grant, nil
}
func (s *Service) RequestSessionGoalAction(ctx context.Context, req *connect.Request[pb.RequestSessionGoalActionRequest]) (*connect.Response[pb.RequestSessionGoalActionResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return nil, rpc.Error(executionDenied(), correlation)
	}
	if err := validateSessionMutation(req.Msg.Mutation); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	input, err := goalCommand(req.Msg)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	identity := goalAcceptance{domain.ID(req.Msg.Mutation.Id), req.Msg.Mutation.ExpectedRevision, actor, input}
	result, err := s.Store.Mutate(ctx, input.RequestID, "session.goal", identity, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, identity.Session)
		if err != nil {
			return nil, err
		}
		if sr.Revision != identity.Revision || session.PendingGoalActionID != "" {
			return nil, domain.NativeGoalUncertain()
		}
		_, grant, err := s.goalScope(tx, sr, session, input.ExecutionID, input.NativeThreadID)
		if err != nil {
			return nil, err
		}
		input.ExecutionJobID = grant.JobID
		raw, _ := json.Marshal(input)
		job := domain.Job{Type: domain.NativeGoalActionJob, State: domain.JobQueued, MachineID: grant.MachineID, InstanceID: grant.InstanceID, AssignedDeviceID: grant.DeviceID, ParentID: grant.JobID, Input: raw, AcceptedAt: time.Now().UTC()}
		if _, err = tx.Put(domain.JobKind, input.RequestID, 0, sr.ID, sr.ProjectID, job); err != nil {
			return nil, err
		}
		session.PendingGoalActionID = input.RequestID
		session.NativeGoal.ActionID = input.RequestID
		session.NativeGoal.ActionState = domain.NativeGoalAccepted
		session.NativeGoal.ProblemCode = ""
		if _, err = tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return sessionReceipt{SessionID: sr.ID, InputID: input.RequestID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := &pb.RequestSessionGoalActionResponse{RequestId: string(result.RequestID), Replayed: result.Replayed}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		ar, err := tx.Get(domain.JobKind, input.RequestID)
		if err != nil {
			return err
		}
		sr, _, err := sessionRecord(tx, identity.Session)
		if err != nil {
			return err
		}
		if ar.SessionID != sr.ID {
			return executionDenied()
		}
		response.Action, response.Session = rpc.Resource(ar), rpc.Resource(sr)
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "native_goal_action_accepted", "action_id", input.RequestID, "execution_id", input.ExecutionID, "command", input.Command, "replayed", result.Replayed)
	out := connect.NewResponse(response)
	rpc.CopyCorrelation(out, req.Header())
	return out, nil
}
func (s *Service) GetSessionGoalState(ctx context.Context, req *connect.Request[pb.GetSessionGoalStateRequest]) (*connect.Response[pb.GetSessionGoalStateResponse], error) {
	response := &pb.GetSessionGoalStateResponse{}
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		sr, session, err := sessionRecord(tx, domain.ID(req.Msg.SessionId))
		if err != nil {
			return err
		}
		response.Session = rpc.Resource(sr)
		if session.NativeGoal != nil && session.NativeGoal.ActionID != "" {
			ar, err := tx.Get(domain.JobKind, session.NativeGoal.ActionID)
			if err != nil {
				return err
			}
			if ar.SessionID != sr.ID {
				return executionDenied()
			}
			response.Action = rpc.Resource(ar)
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	out := connect.NewResponse(response)
	rpc.CopyCorrelation(out, req.Header())
	return out, nil
}

type goalClaimScope struct {
	Action, Job, Machine, Instance, Device domain.ID
	Revision                               uint64
}

func (s *Service) currentGoalAction(tx *store.Tx, id goalClaimScope) (store.Record, domain.Job, domain.NativeGoalActionInput, store.Record, domain.Session, error) {
	ar, err := tx.Get(domain.JobKind, id.Action)
	if err != nil {
		return ar, domain.Job{}, domain.NativeGoalActionInput{}, store.Record{}, domain.Session{}, err
	}
	job, err := store.Decode[domain.Job](ar)
	var input domain.NativeGoalActionInput
	if err != nil || job.Type != domain.NativeGoalActionJob || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.RequestID != ar.ID || input.ExecutionJobID != id.Job || job.ParentID != id.Job || job.MachineID != id.Machine || job.InstanceID != id.Instance || job.AssignedDeviceID != id.Device {
		return ar, job, input, store.Record{}, domain.Session{}, executionDenied()
	}
	sr, session, err := sessionRecord(tx, ar.SessionID)
	if err != nil {
		return ar, job, input, sr, session, err
	}
	if session.PendingGoalActionID != ar.ID || session.NativeGoal == nil || session.NativeGoal.ActionID != ar.ID {
		return ar, job, input, sr, session, domain.NativeGoalUncertain()
	}
	_, grant, err := s.goalScope(tx, sr, session, input.ExecutionID, input.NativeThreadID)
	if err == nil && (grant.JobID != id.Job || grant.MachineID != id.Machine || grant.InstanceID != id.Instance || grant.DeviceID != id.Device) {
		err = executionDenied()
	}
	return ar, job, input, sr, session, err
}
func (s *Service) ClaimSessionGoalAction(ctx context.Context, req *connect.Request[pb.ClaimSessionGoalActionRequest]) (*connect.Response[pb.ClaimSessionGoalActionResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := validateSessionMutation(req.Msg.Mutation); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	meta := req.Msg.Mutation
	identity := goalClaimScope{domain.ID(meta.Id), domain.ID(req.Msg.JobId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), actor.DeviceID, meta.ExpectedRevision}
	claim := domain.ID(meta.RequestId)
	result, err := s.Store.Mutate(ctx, claim, "goal.claim", identity, func(tx *store.Tx) (any, error) {
		ar, job, input, sr, session, err := s.currentGoalAction(tx, identity)
		if err != nil {
			return nil, err
		}
		if ar.Revision != identity.Revision || job.State != domain.JobQueued || input.ClaimID != "" {
			return nil, domain.NativeGoalUncertain()
		}
		input.ClaimID = claim
		job.Input, _ = json.Marshal(input)
		job.State = domain.JobClaimed
		if _, err = tx.Put(ar.Kind, ar.ID, ar.Revision, ar.SessionID, ar.ProjectID, job); err != nil {
			return nil, err
		}
		session.NativeGoal.ActionState = domain.NativeGoalClaimed
		if _, err = tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return sessionReceipt{SessionID: sr.ID, InputID: ar.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := &pb.ClaimSessionGoalActionResponse{Replayed: result.Replayed}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		ar, job, input, _, _, err := s.currentGoalAction(tx, identity)
		if err != nil {
			return err
		}
		if job.State != domain.JobClaimed || input.ClaimID != claim {
			return domain.NativeGoalUncertain()
		}
		response.Action = rpc.Resource(ar)
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "native_goal_action_claimed", "action_id", identity.Action, "claim_id", claim, "replayed", result.Replayed)
	out := connect.NewResponse(response)
	rpc.CopyCorrelation(out, req.Header())
	return out, nil
}
func (s *Service) ReportSessionGoalAction(ctx context.Context, req *connect.Request[pb.ReportSessionGoalActionRequest]) (*connect.Response[pb.ReportSessionGoalActionResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := validateSessionMutation(req.Msg.Mutation); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	meta := req.Msg.Mutation
	identity := goalClaimScope{domain.ID(meta.Id), domain.ID(req.Msg.JobId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), actor.DeviceID, meta.ExpectedRevision}
	var observed domain.NativeGoalActionResult
	if len(req.Msg.ResultJson) > 32768 || domain.Decode(req.Msg.ResultJson, &observed) != nil || observed.Version != 1 || observed.ClaimID.Validate() != nil {
		return nil, rpc.Error(domain.NativeGoalUncertain(), correlation)
	}
	reportIdentity := struct {
		Scope  goalClaimScope
		Result domain.NativeGoalActionResult
	}{identity, observed}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "goal.report", reportIdentity, func(tx *store.Tx) (any, error) {
		ar, job, input, sr, session, err := s.currentGoalAction(tx, identity)
		if err != nil {
			return nil, err
		}
		if ar.Revision != identity.Revision || job.State != domain.JobClaimed || observed.ClaimID != input.ClaimID || observed.RequestID != input.RequestID || observed.ExecutionID != input.ExecutionID || observed.NativeThreadID != input.NativeThreadID {
			return nil, domain.NativeGoalUncertain()
		}
		if observed.Problem != nil {
			// A failed native call can already have recorded user intent. Even a later
			// observed absence cannot replace the original missing acknowledgment.
			if len(observed.Observation) != 0 || observed.Cleared != nil {
				return nil, domain.NativeGoalUncertain()
			}
			job.State = domain.JobUncertain
			job.Problem = domain.NativeGoalUncertain()
			session.NativeGoal.ActionState = domain.NativeGoalUncertainState
			session.NativeGoal.ProblemCode = domain.RecoveryRequired
		} else {
			switch input.Command {
			case domain.NativeGoalClearCommand:
				if observed.Cleared == nil || len(observed.Observation) != 0 {
					return nil, domain.NativeGoalUncertain()
				}
				if *observed.Cleared {
					session.NativeGoal.Observation = json.RawMessage("null")
				}
			case domain.NativeGoalSetCommand, domain.NativeGoalReadCommand:
				if observed.Cleared != nil || len(observed.Observation) == 0 || input.Command == domain.NativeGoalSetCommand && string(observed.Observation) == "null" {
					return nil, domain.NativeGoalUncertain()
				}
				if string(observed.Observation) != "null" {
					var snapshot domain.NativeGoalSnapshot
					if domain.Decode(observed.Observation, &snapshot) != nil || snapshot.Validate() != nil {
						return nil, domain.NativeGoalUncertain()
					}
				}
				session.NativeGoal.Observation = append(json.RawMessage(nil), observed.Observation...)
			default:
				return nil, domain.NativeGoalUncertain()
			}
			now := time.Now().UTC()
			session.NativeGoal.ObservedAt = &now
			session.NativeGoal.ActionState = domain.NativeGoalAcknowledged
			session.NativeGoal.ProblemCode = ""
			session.PendingGoalActionID = ""
			job.State = domain.JobSucceeded
			job.FinishedAt = &now
		}
		job.Output = append(json.RawMessage(nil), req.Msg.ResultJson...)
		if _, err = tx.Put(ar.Kind, ar.ID, ar.Revision, ar.SessionID, ar.ProjectID, job); err != nil {
			return nil, err
		}
		if _, err = tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return sessionReceipt{SessionID: sr.ID, InputID: ar.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := &pb.ReportSessionGoalActionResponse{Replayed: result.Replayed}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		ar, err := tx.Get(domain.JobKind, identity.Action)
		if err != nil {
			return err
		}
		sr, _, err := sessionRecord(tx, ar.SessionID)
		if err != nil {
			return err
		}
		response.Action, response.Session = rpc.Resource(ar), rpc.Resource(sr)
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "native_goal_action_reported", "action_id", identity.Action, "uncertain", observed.Problem != nil, "replayed", result.Replayed)
	out := connect.NewResponse(response)
	rpc.CopyCorrelation(out, req.Header())
	return out, nil
}
func (s *Service) pendingGoalAction(tx *store.Tx, jr store.Record, job domain.Job, sent map[domain.ID]bool) (*pb.GoalActionControl, error) {
	if job.Type != domain.ExecuteSessionJob {
		return nil, nil
	}
	_, session, err := sessionRecord(tx, jr.SessionID)
	if err != nil {
		return nil, err
	}
	id := session.PendingGoalActionID
	if id == "" || sent[id] {
		return nil, nil
	}
	ar, err := tx.Get(domain.JobKind, id)
	if err != nil {
		return nil, err
	}
	action, err := store.Decode[domain.Job](ar)
	if err != nil {
		return nil, err
	}
	if action.State != domain.JobQueued {
		return nil, nil
	}
	grant, err := tx.ExecutionGrantForJob(jr.ID)
	if err != nil {
		return nil, err
	}
	_, _, _, _, _, err = s.currentGoalAction(tx, goalClaimScope{id, jr.ID, job.MachineID, job.InstanceID, grant.DeviceID, ar.Revision})
	if err != nil {
		if c := domain.SafeError(err).Code; c == domain.Conflict || c == domain.PermissionDenied || c == domain.RecoveryRequired {
			return nil, nil
		}
		return nil, err
	}
	return &pb.GoalActionControl{JobId: string(jr.ID), ActionId: string(id), Revision: ar.Revision}, nil
}

func publishNativeGoalEvent(input domain.ExecutionJobInput, session *domain.Session, progress *domain.ExecutionProgress, event domain.ExecutionEvent, at time.Time) error {
	g := session.NativeGoal
	if !input.NativeGoals || input.Configuration.Harness != domain.Codex || input.Configuration.SidechatPolicy != "" || g == nil || !g.Enabled || g.SourceExecutionID != input.ExecutionID || g.SourceNativeThreadID != domain.ID(event.NativeThreadID) || progress.CleanupVerified || progress.Outcome != domain.ExecutionRunning {
		return domain.NativeGoalUncertain()
	}
	switch event.Kind {
	case domain.ExecutionGoalObserved:
		if progress.NativeTurnID != event.NativeTurnID || event.Goal == nil || event.Goal.Validate() != nil {
			return domain.NativeGoalUncertain()
		}
		g.Observation = append(json.RawMessage(nil), event.Goal.Snapshot...)
		g.ObservedAt = &at
	case domain.ExecutionGoalTurnStarted:
		var snapshot domain.NativeGoalSnapshot
		if event.GoalTurn == nil || len(progress.GoalTurns) >= 4096 || domain.Decode(g.Observation, &snapshot) != nil || snapshot.Status != domain.NativeGoalActive || snapshot.Validate() != nil || string(event.GoalTurn.PreviousNativeTurnID) != progress.NativeTurnID || event.NativeTurnID == progress.NativeTurnID {
			return domain.NativeGoalUncertain()
		}
		if len(progress.GoalTurns) == 0 || progress.GoalTurns[len(progress.GoalTurns)-1].Status == "inProgress" {
			return domain.NativeGoalUncertain()
		}
		for _, turn := range progress.GoalTurns {
			if turn.NativeTurnID == event.GoalTurn.NativeTurnID {
				return domain.NativeGoalUncertain()
			}
		}
		progress.NativeTurnID = event.NativeTurnID
		progress.GoalTurns = append(progress.GoalTurns, *event.GoalTurn)
		progress.TurnTiming = nil
	case domain.ExecutionGoalTurnFinished:
		if event.GoalTurn == nil || progress.NativeTurnID != event.NativeTurnID || len(progress.GoalTurns) >= 4096 {
			return domain.NativeGoalUncertain()
		}
		if len(progress.GoalTurns) == 0 {
			progress.GoalTurns = append(progress.GoalTurns, *event.GoalTurn)
		} else {
			last := &progress.GoalTurns[len(progress.GoalTurns)-1]
			if last.NativeTurnID != event.GoalTurn.NativeTurnID || last.Status != "inProgress" {
				return domain.NativeGoalUncertain()
			}
			last.Status = event.GoalTurn.Status
		}
	default:
		return domain.NativeGoalUncertain()
	}
	return nil
}

// A control that never acquired its original claim can be canceled safely.
// A claimed control remains uncertain until its original native report; Stop,
// account loss, process exit and read-only observation cannot acknowledge it.
func retireGoalAction(tx *store.Tx, sr store.Record, session *domain.Session, uncertainClaim bool) error {
	if session.PendingGoalActionID == "" {
		return nil
	}
	ar, err := tx.Get(domain.JobKind, session.PendingGoalActionID)
	if err != nil {
		return err
	}
	job, err := store.Decode[domain.Job](ar)
	var input domain.NativeGoalActionInput
	if err != nil || ar.SessionID != sr.ID || job.Type != domain.NativeGoalActionJob || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || session.NativeGoal == nil || session.NativeGoal.ActionID != ar.ID {
		return domain.NativeGoalUncertain()
	}
	switch job.State {
	case domain.JobQueued:
		if input.ClaimID != "" {
			return domain.NativeGoalUncertain()
		}
		now := time.Now().UTC()
		job.State = domain.JobCanceled
		job.FinishedAt = &now
		session.PendingGoalActionID = ""
		session.NativeGoal.ActionState = domain.NativeGoalCanceled
	case domain.JobClaimed:
		if !uncertainClaim {
			return nil
		}
		job.State = domain.JobUncertain
		job.Problem = domain.NativeGoalUncertain()
		session.NativeGoal.ActionState = domain.NativeGoalUncertainState
		session.NativeGoal.ProblemCode = domain.RecoveryRequired
		session.Recovery = domain.NeedsRecovery
		session.Dispatch = domain.DispatchPaused
	case domain.JobUncertain:
		return nil
	default:
		return domain.NativeGoalUncertain()
	}
	_, err = tx.Put(ar.Kind, ar.ID, ar.Revision, ar.SessionID, ar.ProjectID, job)
	return err
}
