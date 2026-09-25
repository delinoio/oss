package server

import (
	"context"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type approvalResponseReceipt struct {
	InteractionID domain.ID `json:"interaction_id"`
}

type approvalResponseIdentity struct {
	InteractionID domain.ID
	Revision      uint64
	Input         domain.ApprovalResponseInput
	Actor         domain.Principal
}

func (s *Service) RespondApproval(ctx context.Context, req *connect.Request[pb.RespondApprovalRequest]) (*connect.Response[pb.RespondApprovalResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Only an owner or paired client can respond to an approval.", "Use the original interaction through an authorized client."), correlation)
	}
	if err := validateSessionMutation(req.Msg.Mutation); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var input domain.ApprovalResponseInput
	if err := domain.Decode(req.Msg.ResponseJson, &input); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	identity := approvalResponseIdentity{domain.ID(meta.Id), meta.ExpectedRevision, input, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "approval.respond", identity, func(tx *store.Tx) (any, error) {
		r, err := s.acceptApprovalResponse(tx, domain.ID(meta.RequestId), identity.InteractionID, identity.Revision, input)
		return approvalResponseReceipt{InteractionID: r.ID}, err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt approvalResponseReceipt
	if domain.Decode(result.Data, &receipt) != nil || receipt.InteractionID != identity.InteractionID {
		return nil, rpc.Error(executionEventConflict(), correlation)
	}
	var current store.Record
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		current, err = tx.Get(domain.InteractionKind, receipt.InteractionID)
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "approval_response_accepted", "correlation_id", correlation, "interaction_id", receipt.InteractionID, "response_id", result.RequestID, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.RespondApprovalResponse{Interaction: rpc.Resource(current), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
