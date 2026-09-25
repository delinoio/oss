package server

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type validationReceipt struct {
	ID         domain.ID                `json:"id"`
	Validation domain.AccountValidation `json:"validation"`
}

func (s *Service) ValidateAccount(ctx context.Context, req *connect.Request[pb.ValidateAccountRequest]) (*connect.Response[pb.ValidateAccountResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	meta := req.Msg.Mutation
	result, err := s.inspectAccount(ctx, meta, validationInspection, correlation, func(tx *store.Tx, account domain.Account, observation accountInspection) (any, error) {
		validation := domain.AccountValidation{RequestID: domain.ID(meta.RequestId), ConnectionID: account.Connection.ID, ObservedAt: observation.ObservedAt, State: domain.Observed, Authentication: observation.Authentication, ModelCount: uint32(len(observation.Models)), HTTPStatus: uint32(observation.HTTPStatus), RetryAfterSeconds: observation.RetryAfterSeconds, Problem: observation.Problem}
		account.Health = domain.AccountReady
		if validation.Problem != nil {
			validation.State = domain.ObservationFailed
			account.Health = domain.AccountFailed
			if validation.Problem.Code == domain.Unsupported {
				validation.State = domain.ObservationUnsupported
				account.Health = domain.AccountUnverified
			}
		} else if observation.Authentication == providers.AuthenticationUnknown {
			validation.State = domain.ObservationUnsupported
			account.Health = domain.AccountUnverified
			validation.Problem = domain.Fail(domain.Unsupported, "The model endpoint responded, but credential validity is unobservable through this interface.", "Use a supported authenticated provider check; model discovery and manual model configuration remain separate.")
			validation.Problem.CorrelationID = correlation
		}
		account.Validation = &validation
		// A catalog/credential check cannot clear exhaustion or manufacture quota.
		if _, err := tx.Put(domain.AccountKind, domain.ID(meta.Id), meta.ExpectedRevision, "", "", account); err != nil {
			return nil, err
		}
		return validationReceipt{ID: domain.ID(meta.Id), Validation: validation}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt validationReceipt
	if domain.Decode(result.Data, &receipt) != nil || receipt.ID != domain.ID(meta.Id) {
		return nil, rpc.Error(domain.Fail(domain.NotFound, "The validated account was subsequently deleted.", "An old validation request cannot recreate it."), correlation)
	}
	record, err := s.accountRecord(ctx, domain.ID(meta.Id))
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	raw, _ := json.Marshal(receipt.Validation)
	response := connect.NewResponse(&pb.ValidateAccountResponse{Account: rpc.Resource(record), RequestId: meta.RequestId, Replayed: result.Replayed, ValidationJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
