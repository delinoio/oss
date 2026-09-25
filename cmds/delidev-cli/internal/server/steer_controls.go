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

type steerClaimIdentity struct {
	Steer, Job, Machine, Instance, Device domain.ID
	Revision                              uint64
}

func (s *Service) ClaimSteerInput(ctx context.Context, req *connect.Request[pb.ClaimSteerInputRequest]) (*connect.Response[pb.ClaimSteerInputResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := validateSessionMutation(req.Msg.Mutation); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	meta := req.Msg.Mutation
	identity := steerClaimIdentity{domain.ID(meta.Id), domain.ID(req.Msg.JobId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), actor.DeviceID, meta.ExpectedRevision}
	for _, id := range []domain.ID{identity.Steer, identity.Job, identity.Machine, identity.Instance, identity.Device} {
		if err := id.Validate(); err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	claimID := domain.ID(meta.RequestId)
	result, err := s.Store.Mutate(ctx, claimID, "steer.claim", identity, func(tx *store.Tx) (any, error) {
		r, attempt, _, err := s.currentSteerClaim(tx, identity)
		if err != nil {
			return nil, err
		}
		if r.Revision != identity.Revision || attempt.State != domain.SteerQueued || attempt.Claim != nil {
			return nil, steerConflict()
		}
		attempt.State = domain.SteerClaimed
		attempt.Claim = &domain.SteerClaim{ID: claimID, MachineID: identity.Machine, InstanceID: identity.Instance, DeviceID: identity.Device, ClaimedAt: time.Now().UTC()}
		if _, err := tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, attempt); err != nil {
			return nil, err
		}
		return steerReceipt{SteerID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt steerReceipt
	if domain.Decode(result.Data, &receipt) != nil || receipt.SteerID != identity.Steer {
		return nil, rpc.Error(steerConflict(), correlation)
	}
	response := &pb.ClaimSteerInputResponse{Replayed: result.Replayed}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		r, attempt, ir, err := s.currentSteerClaim(tx, identity)
		if err != nil {
			return err
		}
		claim := attempt.Claim
		if attempt.State != domain.SteerClaimed || claim == nil || claim.ID != claimID || claim.MachineID != identity.Machine || claim.InstanceID != identity.Instance || claim.DeviceID != identity.Device {
			return steerConflict()
		}
		response.Steer, response.Input = rpc.Resource(r), rpc.Resource(ir)
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "steer_claimed", "steer_id", identity.Steer, "job_id", identity.Job, "claim_id", claimID, "replayed", result.Replayed)
	out := connect.NewResponse(response)
	rpc.CopyCorrelation(out, req.Header())
	return out, nil
}

func (s *Service) currentSteerClaim(tx *store.Tx, identity steerClaimIdentity) (store.Record, domain.SteerAttempt, store.Record, error) {
	r, err := tx.Get(domain.SteerKind, identity.Steer)
	if err != nil {
		return r, domain.SteerAttempt{}, store.Record{}, err
	}
	attempt, err := store.Decode[domain.SteerAttempt](r)
	if err != nil {
		return r, attempt, store.Record{}, err
	}
	sr, session, err := sessionRecord(tx, r.SessionID)
	if err != nil {
		return r, attempt, store.Record{}, err
	}
	if attempt.Version != 1 || attempt.JobID != identity.Job || session.PendingSteerID != r.ID || (attempt.State != domain.SteerQueued && attempt.State != domain.SteerClaimed) || attempt.Observation != nil {
		return r, attempt, store.Record{}, steerConflict()
	}
	_, grant, err := s.steerScope(tx, sr, session, attempt.ExecutionID, attempt.NativeTurnID)
	if err != nil {
		return r, attempt, store.Record{}, err
	}
	if grant.JobID != identity.Job || grant.MachineID != identity.Machine || grant.InstanceID != identity.Instance || grant.DeviceID != identity.Device || session.Execution.NativeThreadID != string(attempt.NativeThreadID) {
		return r, attempt, store.Record{}, executionDenied()
	}
	ir, input, err := steerInput(tx, r, attempt)
	if err != nil {
		return r, attempt, ir, err
	}
	if input.Delivery != domain.InputClaimed {
		return r, attempt, ir, steerConflict()
	}
	return r, attempt, ir, nil
}

func steerInput(tx *store.Tx, r store.Record, attempt domain.SteerAttempt) (store.Record, domain.QueuedInput, error) {
	ir, err := tx.Get(domain.QueueKind, attempt.InputID)
	if err != nil {
		return ir, domain.QueuedInput{}, err
	}
	input, err := store.Decode[domain.QueuedInput](ir)
	if err != nil {
		return ir, input, err
	}
	if ir.SessionID != r.SessionID || input.ContentRevision != attempt.ContentRevision || input.ExecutionID != attempt.ExecutionID || input.NativeRequestID != r.ID || input.Mode != attempt.Mode || domain.BindExecutionInput(ir.ID, input.Prompt).PromptDigest != attempt.PromptDigest {
		return ir, input, steerConflict()
	}
	return ir, input, nil
}

func (s *Service) pendingSteer(tx *store.Tx, jr store.Record, job domain.Job, sent map[domain.ID]bool) (*pb.SteerInputControl, error) {
	if job.Type != domain.ExecuteSessionJob {
		return nil, nil
	}
	_, session, err := sessionRecord(tx, jr.SessionID)
	if err != nil {
		return nil, err
	}
	if session.PendingSteerID == "" || sent[session.PendingSteerID] {
		return nil, nil
	}
	r, err := tx.Get(domain.SteerKind, session.PendingSteerID)
	if err != nil {
		return nil, err
	}
	attempt, err := store.Decode[domain.SteerAttempt](r)
	if err != nil {
		return nil, err
	}
	if attempt.State != domain.SteerQueued {
		return nil, nil
	}
	grant, err := tx.ExecutionGrantForJob(jr.ID)
	if err != nil {
		return nil, err
	}
	_, _, _, err = s.currentSteerClaim(tx, steerClaimIdentity{r.ID, jr.ID, job.MachineID, job.InstanceID, grant.DeviceID, r.Revision})
	if err != nil {
		if code := domain.SafeError(err).Code; code == domain.Conflict || code == domain.PermissionDenied {
			return nil, nil
		}
		return nil, err
	}
	return &pb.SteerInputControl{JobId: string(jr.ID), SteerId: string(r.ID), Revision: r.Revision}, nil
}

// Before a Worker claim, the server knows native delivery could not begin. A
// claimed attempt requires a native report; Stop alone cannot return it to FIFO.
func retireSteer(tx *store.Tx, sr store.Record, session *domain.Session, uncertainClaim bool) error {
	if session.PendingSteerID == "" {
		return nil
	}
	r, err := tx.Get(domain.SteerKind, session.PendingSteerID)
	if err != nil {
		return err
	}
	attempt, err := store.Decode[domain.SteerAttempt](r)
	if err != nil {
		return err
	}
	if r.SessionID != sr.ID || attempt.ExecutionID != session.ActiveExecutionID {
		return steerConflict()
	}
	if attempt.State == domain.SteerUncertain {
		return nil
	}
	if attempt.State == domain.SteerClaimed && !uncertainClaim {
		return nil
	}
	ir, input, err := steerInput(tx, r, attempt)
	if err != nil {
		return err
	}
	switch attempt.State {
	case domain.SteerQueued:
		if attempt.Claim != nil || input.Delivery != domain.InputClaimed {
			return steerConflict()
		}
		attempt.State = domain.SteerCanceled
		input.Delivery, input.ExecutionID, input.NativeRequestID = domain.InputQueued, "", ""
		session.PendingSteerID = ""
	case domain.SteerClaimed:
		attempt.State, input.Delivery = domain.SteerUncertain, domain.InputUncertain
		session.Recovery, session.Dispatch = domain.NeedsRecovery, domain.DispatchPaused
		if session.Problem == nil {
			session.Problem = domain.Fail(domain.RecoveryRequired, "Native Steer delivery was not confirmed.", "Inspect the original accepted attempt before sending any more input; never replay it blindly.")
		}
	default:
		return steerConflict()
	}
	if _, err := tx.Put(ir.Kind, ir.ID, ir.Revision, ir.SessionID, ir.ProjectID, input); err != nil {
		return err
	}
	_, err = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, attempt)
	return err
}
