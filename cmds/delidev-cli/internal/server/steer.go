package server

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type steerAcceptanceIdentity struct {
	Session, Input, Execution, Turn domain.ID
	Revision                        uint64
	Actor                           domain.Principal
}

type steerReceipt struct {
	SteerID domain.ID `json:"steer_id"`
}

func steerConflict() *domain.Error {
	return domain.Fail(domain.Conflict, "The selected input cannot steer this active turn.", "Reload the current execution and queued input revision; Steer requires an unpaused supported turn without a pending interaction or input delivery.")
}

func (s *Service) SteerQueuedInput(ctx context.Context, req *connect.Request[pb.SteerQueuedInputRequest]) (*connect.Response[pb.SteerQueuedInputResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Only an owner or paired client can request Steer.", "Use the selected queued input through an authorized client."), correlation)
	}
	if err := validateSessionMutation(req.Msg.Mutation); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	identity := steerAcceptanceIdentity{domain.ID(req.Msg.SessionId), domain.ID(meta.Id), domain.ID(req.Msg.ExpectedExecutionId), domain.ID(req.Msg.ExpectedTurnId), meta.ExpectedRevision, actor}
	for _, id := range []domain.ID{identity.Session, identity.Input, identity.Execution, identity.Turn} {
		if err := id.Validate(); err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	attemptID := domain.ID(meta.RequestId)
	result, err := s.Store.Mutate(ctx, attemptID, "session.steer", identity, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, identity.Session)
		if err != nil {
			return nil, err
		}
		if session.PendingSteerID != "" {
			return nil, steerConflict()
		}
		assignment, grant, err := s.steerScope(tx, sr, session, identity.Execution, identity.Turn)
		if err != nil {
			return nil, err
		}
		progress := session.Execution
		primary := domain.BindExecutionInput(assignment.InputID, assignment.Input.Prompt)
		bindings, err := domain.CheckedExecutionInputs(primary.InputID, primary.PromptDigest, progress.AcceptedInputs)
		if err != nil {
			return nil, err
		}
		if progress.SteerAttempts >= domain.MaxExecutionSteers || len(bindings) >= domain.MaxAcceptedExecutionInputs {
			return nil, domain.Fail(domain.ResourceExhausted, "This execution reached its Steer limit.", "Keep further input queued for a later verified turn.")
		}
		ir, err := tx.Get(domain.QueueKind, identity.Input)
		if err != nil {
			return nil, err
		}
		input, err := store.Decode[domain.QueuedInput](ir)
		if err != nil {
			return nil, err
		}
		if ir.SessionID != sr.ID || ir.Revision != identity.Revision || input.Delivery != domain.InputQueued || input.ExecutionID != "" || input.NativeRequestID != "" || input.ContentRevision == 0 || input.Mode != assignment.Input.Mode || session.PendingInputs == 0 || session.PendingInputBytes < uint64(len(input.Prompt)) {
			return nil, steerConflict()
		}
		binding := domain.BindExecutionInput(ir.ID, input.Prompt)
		attempt := domain.SteerAttempt{Version: 1, JobID: grant.JobID, ExecutionID: identity.Execution, InputID: ir.ID, ContentRevision: input.ContentRevision, NativeThreadID: domain.ID(progress.NativeThreadID), NativeTurnID: identity.Turn, Mode: input.Mode, PromptDigest: binding.PromptDigest, State: domain.SteerQueued, AcceptedAt: time.Now().UTC()}
		if _, err := tx.Put(domain.SteerKind, attemptID, 0, sr.ID, sr.ProjectID, attempt); err != nil {
			return nil, err
		}
		input.Delivery, input.ExecutionID, input.NativeRequestID = domain.InputClaimed, identity.Execution, attemptID
		if _, err := tx.Put(ir.Kind, ir.ID, ir.Revision, sr.ID, sr.ProjectID, input); err != nil {
			return nil, err
		}
		session.PendingSteerID = attemptID
		progress.SteerAttempts++
		if _, err := tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return steerReceipt{SteerID: attemptID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt steerReceipt
	if domain.Decode(result.Data, &receipt) != nil || receipt.SteerID != attemptID {
		return nil, rpc.Error(steerConflict(), correlation)
	}
	response := &pb.SteerQueuedInputResponse{RequestId: string(result.RequestID), Replayed: result.Replayed}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		r, err := tx.Get(domain.SteerKind, attemptID)
		if err != nil {
			return err
		}
		attempt, err := store.Decode[domain.SteerAttempt](r)
		if err != nil {
			return err
		}
		if r.SessionID != identity.Session || attempt.InputID != identity.Input || attempt.ExecutionID != identity.Execution || attempt.NativeTurnID != identity.Turn {
			return steerConflict()
		}
		sr, session, err := sessionRecord(tx, r.SessionID)
		if err != nil {
			return err
		}
		ir, err := tx.Get(domain.QueueKind, attempt.InputID)
		if err != nil {
			return err
		}
		job, err := tx.SessionExecutionJob(sr.ID, session.ExecutionSelection().ID)
		if err != nil {
			return err
		}
		response.Steer = rpc.Resource(r)
		response.Change = &pb.SessionChange{Session: rpc.Resource(sr), Input: rpc.Resource(ir), ExecutionJob: rpc.Resource(job), RequestId: string(result.RequestID), Replayed: result.Replayed}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "steer_accepted", "session_id", identity.Session, "execution_id", identity.Execution, "input_id", identity.Input, "steer_id", attemptID, "replayed", result.Replayed)
	out := connect.NewResponse(response)
	rpc.CopyCorrelation(out, req.Header())
	return out, nil
}

// A Steer control cannot answer or bypass an actual question/approval. Reuse
// the execution relay's full account/Worker/epoch boundary without reselecting
// configuration or changing the active turn's original mode.
func (s *Service) steerScope(tx *store.Tx, sr store.Record, session domain.Session, execution, turn domain.ID) (domain.ExecutionJobInput, store.ExecutionGrant, error) {
	var input domain.ExecutionJobInput
	p := session.Execution
	if s.executionAuthority == nil || session.Outcome != domain.ExecutionRunning || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.Dispatch != domain.DispatchClaimed || session.ActiveExecutionID != execution || p == nil || p.ExecutionID != execution || p.NativeTurnID != string(turn) || p.Outcome != domain.ExecutionRunning || p.CleanupVerified || p.Waiting != (domain.NativeWaiting{}) || p.UnconfirmedResponses != 0 {
		return input, store.ExecutionGrant{}, steerConflict()
	}
	open, err := tx.OpenExecutionInteractions(execution)
	if err != nil {
		return input, store.ExecutionGrant{}, err
	}
	if len(open) != 0 {
		return input, store.ExecutionGrant{}, steerConflict()
	}
	jr, err := tx.SessionExecutionJob(sr.ID, execution)
	if err != nil {
		return input, store.ExecutionGrant{}, err
	}
	job, err := store.Decode[domain.Job](jr)
	if err != nil {
		return input, store.ExecutionGrant{}, err
	}
	if jr.ID != p.JobID || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || !session.OwnsExecution(input) {
		return input, store.ExecutionGrant{}, steerConflict()
	}
	if input.Configuration.Harness != domain.Codex || input.Installation.Version != domain.CodexProtocolVersion || !input.Installation.ProtocolVerified {
		return input, store.ExecutionGrant{}, domain.SessionExecutionUnavailable()
	}
	grant, err := tx.ExecutionGrantForJob(jr.ID)
	if err != nil {
		return input, grant, err
	}
	if _, err := s.executionAuthority.scope(tx, grant); err != nil {
		return input, grant, err
	}
	return input, grant, nil
}
