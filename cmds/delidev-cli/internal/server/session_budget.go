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

func budgetActor(ctx context.Context) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return actor, domain.Fail(domain.PermissionDenied, "Only an owner or paired client can access a session budget.", "Use an authorized product client.")
	}
	return actor, nil
}
func sessionBudgetView(tx *store.Tx, id domain.ID) (*pb.SessionBudgetView, error) {
	row, value, err := sessionRecord(tx, id)
	if err != nil {
		return nil, err
	}
	view := &pb.SessionBudgetView{Session: rpc.Resource(row), State: pb.BudgetState_BUDGET_STATE_DISABLED, Coverage: pb.UsageCoverage_USAGE_COVERAGE_OBSERVED_ROOT_RESPONSES}
	if value.EstimatedCostBudget == nil {
		return view, nil
	}
	b := value.EstimatedCostBudget
	if b.Validate() != nil {
		return nil, domain.Fail(domain.RecoveryRequired, "The retained budget is invalid.", "Preserve its original configuration; remove or replace it explicitly.")
	}
	view.Budget = &pb.EstimatedCostBudget{Currency: string(b.Currency), Threshold: b.Threshold}
	selected, err := tx.SessionEstimate(id, b.Currency)
	if err != nil {
		return nil, err
	}
	unpriced, err := tx.SessionEstimate(id, "")
	if err != nil {
		return nil, err
	}
	view.UnpricedResponses = unpriced.UnavailableResponses
	view.OtherCurrencyResponses, err = tx.OtherBudgetCurrencies(id, b.Currency)
	if err != nil {
		return nil, err
	}
	view.SelectedCurrency = &pb.BudgetEvidence{Currency: string(selected.Currency), KnownAmount: selected.KnownAmount, CompleteResponses: selected.CompleteResponses, PartialResponses: selected.PartialResponses, UnavailableResponses: selected.UnavailableResponses}
	view.State = pb.BudgetState_BUDGET_STATE_ALLOW_INCOMPLETE
	reached, err := b.Reached(selected)
	if err != nil {
		return nil, err
	}
	if reached {
		view.State = pb.BudgetState_BUDGET_STATE_THRESHOLD_REACHED
	}
	return view, nil
}
func (s *Service) readSessionBudget(ctx context.Context, id domain.ID) (*pb.SessionBudgetView, error) {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var view *pb.SessionBudgetView
	err := s.Store.Read(bounded, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		view, err = sessionBudgetView(tx, id)
		return err
	})
	return view, err
}
func (s *Service) GetSessionBudget(ctx context.Context, req *connect.Request[pb.GetSessionBudgetRequest]) (*connect.Response[pb.GetSessionBudgetResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := budgetActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	view, err := s.readSessionBudget(ctx, domain.ID(req.Msg.SessionId))
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.GetSessionBudgetResponse{View: view})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) SetSessionBudget(ctx context.Context, req *connect.Request[pb.SetSessionBudgetRequest]) (*connect.Response[pb.SetSessionBudgetResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := budgetActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	if err = validateSessionMutation(meta); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var budget *domain.EstimatedCostBudget
	switch change := req.Msg.Change.(type) {
	case *pb.SetSessionBudgetRequest_Budget:
		if change.Budget == nil {
			return nil, rpc.Error(domain.Fail(domain.MissingInput, "A budget is required.", "Provide a currency and threshold, or explicitly remove the budget."), correlation)
		}
		budget = &domain.EstimatedCostBudget{Currency: domain.Currency(change.Budget.Currency), Threshold: change.Budget.Threshold}
		if err = budget.Validate(); err != nil {
			return nil, rpc.Error(err, correlation)
		}
	case *pb.SetSessionBudgetRequest_Remove:
		if !change.Remove {
			return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Budget removal must be explicit.", "Set remove to true or provide a replacement budget."), correlation)
		}
	default:
		return nil, rpc.Error(domain.Fail(domain.MissingInput, "A budget change is required.", "Provide a replacement budget or explicitly remove it."), correlation)
	}
	identity := struct {
		Session  domain.ID
		Revision uint64
		Budget   *domain.EstimatedCostBudget
		Actor    domain.Principal
	}{domain.ID(meta.Id), meta.ExpectedRevision, budget, actor}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := s.Store.Mutate(bounded, domain.ID(meta.RequestId), "session.budget", identity, func(tx *store.Tx) (any, error) {
		row, value, err := sessionRecord(tx, identity.Session)
		if err != nil {
			return nil, err
		}
		value.EstimatedCostBudget = budget
		// This removes only stale budget diagnostics. Existing paused/archive/recovery
		// and accepted execution ownership never change with a budget edit.
		if value.Problem != nil && value.Problem.Code == domain.BudgetReached {
			value.Problem = nil
		}
		if _, err = tx.Put(domain.SessionKind, row.ID, identity.Revision, row.ID, row.ProjectID, value); err != nil {
			return nil, err
		}
		return sessionReceipt{SessionID: row.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt sessionReceipt
	if domain.Decode(result.Data, &receipt) != nil || receipt.SessionID != identity.Session {
		return nil, rpc.Error(domain.Fail(domain.NotFound, "The original budget session is no longer retained.", "Refresh sessions; an old receipt cannot recreate a removed session."), correlation)
	}
	view, err := s.readSessionBudget(bounded, receipt.SessionID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "session_budget_recorded", "correlation_id", correlation, "session_id", receipt.SessionID, "request_id", result.RequestID, "replayed", result.Replayed, "removed", budget == nil)
	response := connect.NewResponse(&pb.SetSessionBudgetResponse{View: view, RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
