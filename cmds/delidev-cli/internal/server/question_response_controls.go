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

type questionClaimIdentity struct {
	Interaction, Response, Job, Machine, Instance, Device domain.ID
	Revision                                              uint64
}

type questionClaimReceipt struct {
	InteractionID domain.ID `json:"interaction_id"`
}

func (s *Service) ClaimQuestionResponse(ctx context.Context, req *connect.Request[pb.ClaimQuestionResponseRequest]) (*connect.Response[pb.ClaimQuestionResponseResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	if meta == nil || meta.ExpectedRevision == 0 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "A response claim requires its interaction revision.", "Use the exact response control from the owning work stream."), correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := questionClaimIdentity{domain.ID(meta.Id), domain.ID(req.Msg.ResponseId), domain.ID(req.Msg.JobId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), actor.DeviceID, meta.ExpectedRevision}
	for _, id := range []domain.ID{identity.Interaction, identity.Response, identity.Job, identity.Machine, identity.Instance, identity.Device} {
		if err := id.Validate(); err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	claimID := domain.ID(meta.RequestId)
	result, err := s.Store.Mutate(ctx, claimID, "question.claim", identity, func(tx *store.Tx) (any, error) {
		r, value, err := s.currentClaimQuestion(tx, identity)
		if err != nil {
			return nil, err
		}
		response := value.Response
		if r.Revision != identity.Revision || response.State != domain.QuestionResponseQueued || response.Claim != nil {
			return nil, executionEventConflict()
		}
		response.State = domain.QuestionResponseClaimed
		response.Claim = &domain.QuestionResponseClaim{ID: claimID, JobID: identity.Job, MachineID: identity.Machine, InstanceID: identity.Instance, DeviceID: identity.Device, ClaimedAt: time.Now().UTC()}
		if _, err := tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value); err != nil {
			return nil, err
		}
		return questionClaimReceipt{InteractionID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt questionClaimReceipt
	if err := domain.Decode(result.Data, &receipt); err != nil || receipt.InteractionID != identity.Interaction {
		return nil, rpc.Error(executionEventConflict(), correlation)
	}
	// A receipt is not a permanent right to retrieve answers or resend them.
	// Recheck current authority even on replay, and return no content after
	// closure, cancellation, Worker replacement or an uncertain delivery.
	var record store.Record
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		r, value, err := s.currentClaimQuestion(tx, identity)
		if err != nil {
			return err
		}
		claim := value.Response.Claim
		if value.Response.State != domain.QuestionResponseClaimed || claim == nil || claim.ID != claimID || claim.JobID != identity.Job || claim.MachineID != identity.Machine || claim.InstanceID != identity.Instance || claim.DeviceID != identity.Device {
			return executionEventConflict()
		}
		record = r
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "question_response_claimed", "job_id", identity.Job, "interaction_id", identity.Interaction, "response_id", identity.Response, "claim_id", claimID, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ClaimQuestionResponseResponse{Interaction: rpc.Resource(record), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) currentClaimQuestion(tx *store.Tx, identity questionClaimIdentity) (store.Record, domain.ExecutionInteraction, error) {
	r, err := tx.Get(domain.InteractionKind, identity.Interaction)
	if err != nil {
		return r, domain.ExecutionInteraction{}, err
	}
	value, err := store.Decode[domain.ExecutionInteraction](r)
	if err != nil {
		return r, value, err
	}
	if value.Response == nil || value.Response.ID != identity.Response || value.Response.Input.Validate(value.Questions) != nil {
		return r, value, executionEventConflict()
	}
	grant, err := s.questionResponseScope(tx, r, value)
	if err != nil {
		return r, value, err
	}
	if grant.JobID != identity.Job || grant.MachineID != identity.Machine || grant.InstanceID != identity.Instance || grant.DeviceID != identity.Device {
		return r, value, executionDenied()
	}
	return r, value, nil
}

// Controls contain references only and cannot independently authorize a native
// reply. A matching live Worker must claim the response before retrieving its
// content. A paused/revoked execution continues receiving lifecycle controls
// and heartbeats, but no new question response controls.
func (s *Service) pendingQuestionResponses(tx *store.Tx, record store.Record, job domain.Job, sent map[domain.ID]bool) ([]*pb.QuestionResponseControl, error) {
	if job.Type != domain.ExecuteSessionJob {
		return nil, nil
	}
	var input domain.ExecutionJobInput
	if domain.Decode(job.Input, &input) != nil || input.Validate() != nil {
		return nil, executionEventConflict()
	}
	ids, err := tx.OpenExecutionInteractions(input.ExecutionID)
	if err != nil {
		return nil, err
	}
	var controls []*pb.QuestionResponseControl
	for _, id := range ids {
		r, err := tx.Get(domain.InteractionKind, id)
		if err != nil {
			return nil, err
		}
		value, err := store.Decode[domain.ExecutionInteraction](r)
		if err != nil {
			return nil, err
		}
		response := value.Response
		if response == nil || response.State != domain.QuestionResponseQueued || sent[response.ID] {
			continue
		}
		grant, err := s.questionResponseScope(tx, r, value)
		if err != nil {
			if code := domain.SafeError(err).Code; code == domain.PermissionDenied || code == domain.Conflict {
				continue
			}
			return nil, err
		}
		if r.SessionID != record.SessionID || grant.JobID != record.ID || grant.InstanceID != job.InstanceID || grant.MachineID != job.MachineID || response.ID.Validate() != nil || response.Claim != nil {
			return nil, executionEventConflict()
		}
		controls = append(controls, &pb.QuestionResponseControl{JobId: string(record.ID), InteractionId: string(id), ResponseId: string(response.ID), Revision: r.Revision})
	}
	return controls, nil
}

// Lost execution ownership prevents an unclaimed answer from ever sending.
// For a claimed answer, no report is proof of neither send nor acceptance.
// Keep the native request open until native closure is independently observed.
func invalidateQuestionResponses(tx *store.Tx, input domain.ExecutionJobInput) error {
	ids, err := tx.OpenExecutionInteractions(input.ExecutionID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		r, err := tx.Get(domain.InteractionKind, id)
		if err != nil {
			return err
		}
		value, err := store.Decode[domain.ExecutionInteraction](r)
		if err != nil {
			return err
		}
		if r.SessionID != input.SessionID || value.ExecutionID != input.ExecutionID {
			return executionEventConflict()
		}
		if value.Response == nil {
			continue
		}
		switch value.Response.State {
		case domain.QuestionResponseQueued:
			value.Response.State = domain.QuestionResponseCanceled
		case domain.QuestionResponseClaimed, domain.QuestionResponseTransmitted:
			value.Response.State = domain.QuestionResponseUncertain
		case domain.QuestionResponseCanceled, domain.QuestionResponseUncertain, domain.QuestionResponseAccepted:
			continue
		default:
			return executionEventConflict()
		}
		if _, err := tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value); err != nil {
			return err
		}
	}
	return nil
}
