package server

import (
	"context"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
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

// Account gate must be held. Network I/O never holds the gate or a transaction.
func (s *Service) cancelAccountChecks(id domain.ID) {
	for _, cancel := range s.accountChecks[id] {
		cancel()
	}
}
func (s *Service) startAccountCheck(ctx context.Context, id, requestID domain.ID) (context.Context, func(), error) {
	if len(s.accountChecks[id]) > 0 {
		return nil, nil, domain.Fail(domain.Conflict, "An account validation is already running.", "Wait for its result before starting another validation.")
	}
	if len(s.accountChecks) >= 8 {
		return nil, nil, domain.Fail(domain.ResourceExhausted, "The server's provider check limit is reached.", "Retry after an active provider check completes.")
	}
	if s.accountChecks == nil {
		s.accountChecks = map[domain.ID]map[domain.ID]context.CancelFunc{}
	}
	child, cancel := context.WithCancel(ctx)
	s.accountChecks[id] = map[domain.ID]context.CancelFunc{requestID: cancel}
	finish := func() {
		cancel()
		unlock, _ := s.lockAccounts(context.Background())
		delete(s.accountChecks[id], requestID)
		if len(s.accountChecks[id]) == 0 {
			delete(s.accountChecks, id)
		}
		unlock()
	}
	return child, finish, nil
}

func validationPreflight(tx *store.Tx, input disconnectAccountInput) (domain.Account, domain.Provider, error) {
	_, account, err := accountFromTx(tx, input.ID, input.Revision)
	if err != nil {
		return account, domain.Provider{}, err
	}
	if account.Type != domain.APIAccount {
		return account, domain.Provider{}, domain.Fail(domain.Unsupported, "Subscription accounts require native validation.", "Use their isolated official account interface.")
	}
	if account.Connection == nil || account.Removal != nil {
		return account, domain.Provider{}, domain.Fail(domain.Conflict, "The account has no active connection to validate.", "Connect it and complete any pending cleanup first.")
	}
	record, err := tx.Get(domain.ProviderKind, account.ProviderID)
	if err != nil {
		return account, domain.Provider{}, err
	}
	provider, err := store.Decode[domain.Provider](record)
	return account, provider, err
}

func (s *Service) ValidateAccount(ctx context.Context, req *connect.Request[pb.ValidateAccountRequest]) (*connect.Response[pb.ValidateAccountResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := validateAccountMutation(req.Msg.Mutation); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	input := disconnectAccountInput{ID: domain.ID(meta.Id), Revision: meta.ExpectedRevision}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	// The lock is released explicitly before HTTP and reacquired at publication.
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	result, replayed, err := s.Store.Replay(ctx, domain.ID(meta.RequestId), "account.validate", input)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if !replayed {
		var account domain.Account
		var provider domain.Provider
		err = s.Store.Read(ctx, func(tx *store.Tx) error { var e error; account, provider, e = validationPreflight(tx, input); return e })
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		checkCtx, finish, err := s.startAccountCheck(ctx, input.ID, domain.ID(meta.RequestId))
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		// finish takes the account gate, so release it before this defer runs.
		defer func() {
			if locked {
				unlock()
				locked = false
			}
			finish()
		}()
		var key []byte
		if account.Connection.Authentication != domain.KeylessAuth {
			vault, e := s.secrets()
			if e != nil {
				return nil, rpc.Error(e, correlation)
			}
			key, err = vault.Get(checkCtx, credentials.Ref{Owner: input.ID, ID: account.Connection.ID, Purpose: credentials.AccountAPI})
			if err != nil {
				return nil, rpc.Error(err, correlation)
			}
			defer clear(key)
		}
		unlock()
		locked = false
		s.logger.Info("account_validation_started", "account_id", input.ID, "request_id", meta.RequestId, "correlation_id", correlation)
		observation, err := providers.Inspect(checkCtx, provider, key)
		clear(key)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if checkCtx.Err() != nil {
			return nil, rpc.Error(domain.SafeError(checkCtx.Err()), correlation)
		}
		validation := domain.AccountValidation{
			RequestID: domain.ID(meta.RequestId), ConnectionID: account.Connection.ID, ObservedAt: observation.ObservedAt,
			State: domain.Observed, Authentication: observation.Authentication, ModelCount: uint32(len(observation.Models)),
			HTTPStatus: uint32(observation.HTTPStatus), RetryAfterSeconds: observation.RetryAfterSeconds, Problem: observation.Problem(),
		}
		health := domain.AccountReady
		if validation.Problem != nil {
			validation.State = domain.ObservationFailed
			health = domain.AccountFailed
			if observation.Failure == providers.Unsupported {
				validation.State = domain.ObservationUnsupported
				health = domain.AccountUnverified
			}
		} else if observation.Authentication == domain.AuthenticationUnknown {
			// Public/custom model listing success is preserved, but is not proof
			// that the configured secret authorizes execution on that endpoint.
			health = domain.AccountUnverified
			validation.Problem = domain.Fail(domain.Unsupported, "The model endpoint responded, but credential validity is unobservable through this interface.", "Use a supported authenticated provider check; model discovery and manual model configuration remain separate.")
		}
		if validation.Problem != nil {
			validation.Problem.CorrelationID = correlation
		}
		unlock, err = s.lockAccounts(checkCtx)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		locked = true
		result, err = s.Store.Mutate(checkCtx, domain.ID(meta.RequestId), "account.validate", input, func(tx *store.Tx) (any, error) {
			current, currentProvider, err := validationPreflight(tx, input)
			if err != nil {
				return nil, err
			}
			if current.Connection.ID != account.Connection.ID || currentProvider.Endpoint != provider.Endpoint || currentProvider.Protocol != provider.Protocol || currentProvider.Authentication != provider.Authentication {
				return nil, domain.Fail(domain.Conflict, "The validated account authority changed.", "Validate the current connection explicitly.")
			}
			current.Validation = &validation
			current.Health = health
			// Neither a successful check nor a passed reset can erase exhaustion
			// or invent quota evidence. Quota observations have their own lifecycle.
			if _, err = tx.Put(domain.AccountKind, input.ID, input.Revision, "", "", current); err != nil {
				return nil, err
			}
			return validationReceipt{ID: input.ID, Validation: validation}, nil
		})
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		s.logger.Info("account_validation_finished", "account_id", input.ID, "request_id", meta.RequestId, "state", validation.State, "authentication", validation.Authentication, "failure", observation.Failure, "duration_ms", time.Since(observation.ObservedAt).Milliseconds(), "correlation_id", correlation)
	}
	var receipt validationReceipt
	if domain.Decode(result.Data, &receipt) != nil || receipt.ID != input.ID {
		return nil, rpc.Error(domain.Fail(domain.NotFound, "The validated account was subsequently deleted.", "An old validation request cannot recreate it."), correlation)
	}
	record, err := s.accountRecord(ctx, input.ID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	raw, _ := json.Marshal(receipt.Validation)
	response := connect.NewResponse(&pb.ValidateAccountResponse{Account: rpc.Resource(record), RequestId: meta.RequestId, Replayed: result.Replayed, ValidationJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
