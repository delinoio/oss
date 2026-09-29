package server

import (
	"context"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) SwitchSessionAccount(ctx context.Context, req *connect.Request[pb.SwitchSessionAccountRequest]) (*connect.Response[pb.SwitchSessionAccountResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	meta := req.Msg.Mutation
	if err := validateSessionMutation(meta); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	accountID := domain.ID(req.Msg.AccountId)
	if err := accountID.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Session, Account domain.ID
		Revision         uint64
		Actor            domain.Principal
	}{domain.ID(meta.Id), accountID, meta.ExpectedRevision, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "session.switch-account", identity, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		if sr.Revision != meta.ExpectedRevision {
			return nil, domain.Fail(domain.Conflict, "The session revision changed.", "Reload the session before explicitly selecting another account.")
		}
		if session.Dispatch != domain.DispatchPaused || session.Archive != domain.NotArchived || session.NextExecutionIntent != "" || session.Preparation == nil || session.Preparation.State != domain.PreparationReady || session.TitleState == domain.TitleQueued || session.TitleState == domain.TitleRunning || session.TitleState == domain.TitleUncertain {
			return nil, continuationConflict()
		}
		assignment, completion, digest, err := checkedContinuationPredecessor(tx, sr, session)
		if err != nil {
			return nil, err
		}
		if assignment.Configuration.Harness != domain.Codex || session.Execution.NativeHistory != domain.FullNativeHistory {
			return nil, domain.Fail(domain.Unsupported, "This session has no verified account-independent Codex API history.", "Retain its original account; subscription sessions and account-bound or unknown provider history cannot switch.")
		}
		if !slices.ContainsFunc(assignment.Configuration.Accounts, func(candidate domain.WeightedAccount) bool { return candidate.ID == accountID }) {
			return nil, domain.Fail(domain.PermissionDenied, "The account is outside the original candidate snapshot.", "Choose an eligible account from the session's immutable candidates.")
		}
		old, _ := session.ContinuationAccount()
		if old == accountID {
			return nil, domain.Fail(domain.Conflict, "The account is already selected.", "Retain the current selection or explicitly choose another compatible account.")
		}
		_, account, err := accountFromTx(tx, accountID, 0)
		if err != nil {
			return nil, err
		}
		if account.Connection == nil {
			return nil, domain.Fail(domain.Unsupported, "The selected account is disconnected.", "Connect and validate that API account before switching.")
		}
		_, machine, err := activeMachine(tx, session.MachineID)
		if err != nil {
			return nil, err
		}
		candidate := continuationAssignment(session, assignment, completion, digest, domain.ContinueExplicitly, accountID, account.Connection.ID)
		if _, err := checkedExecutionAssignment(tx, sr, session, machine, candidate); err != nil {
			return nil, err
		}
		if len(session.AccountChanges) >= 1024 {
			return nil, domain.Fail(domain.ResourceExhausted, "The session account-selection history is full.", "Preserve the original history; no selection was discarded.")
		}
		// No native side effect or input claim occurs here. Terminal publication
		// already revoked the old grant. Resume rechecks B and creates fresh scope.
		session.AccountChanges = append(session.AccountChanges, domain.SessionAccountChange{RequestID: domain.ID(meta.RequestId), Revision: sr.Revision + 1, AfterExecutionID: assignment.ExecutionID, PreviousAccountID: old, AccountID: accountID, ConnectionID: account.Connection.ID, ChangedAt: time.Now().UTC()})
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return sessionReceipt{SessionID: sr.ID}, nil
	})
	if err != nil {
		s.logger.InfoContext(ctx, "session_account_switch_denied", "session_id", meta.Id, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, correlation)
	}
	change, err := s.sessionResult(ctx, result)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "session_account_switch_committed", "session_id", meta.Id, "account_id", accountID, "request_id", meta.RequestId, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.SwitchSessionAccountResponse{Change: change})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
