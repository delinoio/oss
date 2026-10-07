package server

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type inspectionOperation string

const (
	validationInspection inspectionOperation = "account.validate"
	catalogInspection    inspectionOperation = "catalog.discover"
	oauthInspection      inspectionOperation = "oauth.exchange"
)

type automaticInspectionKey struct{}

func automaticInspection(ctx context.Context) bool {
	value, _ := ctx.Value(automaticInspectionKey{}).(bool)
	return value
}

type accountCheck struct {
	automatic  bool
	cancel     context.CancelFunc
	providerID domain.ID
	operation  inspectionOperation
}
type accountInspection struct {
	providers.Observation
	Problem *domain.Error
}

// Account gate must be held. HTTP never holds this gate or a transaction.
func (s *Service) cancelAccountChecks(id domain.ID) {
	for _, check := range s.accountChecks[id] {
		check.cancel()
	}
}
func (s *Service) cancelCatalogChecks(provider domain.ID) {
	for _, checks := range s.accountChecks {
		for _, check := range checks {
			if check.providerID == provider && (check.operation == catalogInspection || check.operation == oauthInspection) {
				check.cancel()
			}
		}
	}
}

// Account gate must be held. Provider disablement cancels only automatic
// validation; discovery disablement retains its separate catalog-only boundary.
func (s *Service) cancelAutomaticChecks(id domain.ID, provider bool) {
	for accountID, checks := range s.accountChecks {
		for _, check := range checks {
			if check.automatic && ((!provider && accountID == id) || (provider && check.providerID == id)) {
				check.cancel()
			}
		}
	}
}
func automaticInspectionPreflight(account domain.Account, provider domain.Provider, automatic bool) error {
	if automatic && (!account.Enabled || !provider.EnabledValue()) {
		return providerDisabled()
	}
	return nil
}

func (s *Service) startAccountCheck(ctx context.Context, id, requestID, provider domain.ID, operation inspectionOperation) (context.Context, func(), error) {
	if len(s.accountChecks[id]) > 0 {
		return nil, nil, domain.Fail(domain.Conflict, "An account inspection is already running.", "Wait for its result before starting another inspection.")
	}
	if len(s.accountChecks) >= 8 {
		return nil, nil, domain.Fail(domain.ResourceExhausted, "The server's provider check limit is reached.", "Retry after an active provider check completes.")
	}
	if s.accountChecks == nil {
		s.accountChecks = map[domain.ID]map[domain.ID]accountCheck{}
	}
	child, cancel := context.WithCancel(ctx)
	s.accountChecks[id] = map[domain.ID]accountCheck{requestID: {cancel: cancel, providerID: provider, operation: operation, automatic: automaticInspection(ctx)}}
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
func inspectionPreflight(tx *store.Tx, input disconnectAccountInput, operation inspectionOperation) (domain.Account, domain.Provider, error) {
	_, account, err := accountFromTx(tx, input.ID, input.Revision)
	if err != nil {
		return account, domain.Provider{}, err
	}
	if account.Type != domain.APIAccount {
		return account, domain.Provider{}, domain.Fail(domain.Unsupported, "Subscription accounts require their native account interface.", "Use the isolated official subscription lifecycle.")
	}
	if account.Connection == nil || account.Removal != nil {
		return account, domain.Provider{}, domain.Fail(domain.Conflict, "The account has no active connection to inspect.", "Connect it and complete any pending cleanup first.")
	}
	record, err := tx.Get(domain.ProviderKind, account.ProviderID)
	if err != nil {
		return account, domain.Provider{}, err
	}
	provider, err := store.Decode[domain.Provider](record)
	if err == nil {
		provider, err = providers.ResolveAccountProfile(provider, account)
	}
	if err == nil {
		err = provider.Validate()
	}
	if err == nil && operation == catalogInspection && !provider.Discovery {
		err = domain.Fail(domain.Conflict, "Model discovery is disabled for this provider.", "Enable provider discovery or register models manually.")
	}
	if err == nil && operation == catalogInspection && !provider.EnabledValue() {
		err = providerDisabled()
	}
	return account, provider, err
}

// Native reads and HTTP precede the single publication transaction. Read-only
// inspection can be retried after an unaccepted crash; accepted results never
// repeat HTTP. No key, raw response or arbitrary provider diagnostic is retained.
func (s *Service) inspectAccount(ctx context.Context, meta *pb.Mutation, operation inspectionOperation, correlation string, apply func(*store.Tx, domain.Account, accountInspection) (any, error)) (store.Result, error) {
	if err := validateAccountMutation(meta); err != nil {
		return store.Result{}, err
	}
	input := disconnectAccountInput{ID: domain.ID(meta.Id), Revision: meta.ExpectedRevision}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return store.Result{}, err
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	result, replayed, err := s.Store.Replay(ctx, domain.ID(meta.RequestId), string(operation), input)
	if err != nil || replayed {
		return result, err
	}
	var account domain.Account
	var provider domain.Provider
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		var e error
		account, provider, e = inspectionPreflight(tx, input, operation)
		if e == nil {
			e = automaticInspectionPreflight(account, provider, automaticInspection(ctx))
		}
		return e
	})
	if err != nil {
		return store.Result{}, err
	}
	checkCtx, finish, err := s.startAccountCheck(ctx, input.ID, domain.ID(meta.RequestId), account.ProviderID, operation)
	if err != nil {
		return store.Result{}, err
	}
	defer func() {
		if locked {
			unlock()
			locked = false
		}
		finish()
	}()
	observation := accountInspection{Observation: providers.Observation{Authentication: domain.AuthenticationUnknown, ObservedAt: time.Now().UTC().Truncate(time.Millisecond)}}
	var key []byte
	var quotaProject string
	unlock()
	locked = false
	if account.Connection.Authentication != domain.KeylessAuth {
		credential, e := s.resolveAPICredential(checkCtx, input.ID, account.Connection.ID, account.ProviderID)
		key = credential.key
		quotaProject = credential.quotaProject
		if e != nil {
			observation.Problem = domain.SafeError(e)
		}
		defer clear(key)
	}
	started := time.Now()
	s.logger.Info("account_inspection_started", "operation", operation, "account_id", input.ID, "request_id", meta.RequestId, "correlation_id", correlation)
	if observation.Problem == nil {
		observation.Observation, err = providers.InspectOAuth(checkCtx, provider, key, quotaProject, s.outboundResolver())
		if err != nil {
			observation.Problem = domain.SafeError(err)
		} else {
			observation.Problem = observation.Observation.Problem()
		}
	}
	clear(key)
	observation.ObservedAt = time.Now().UTC().Truncate(time.Millisecond)
	if checkCtx.Err() != nil {
		s.logger.Info("account_inspection_canceled", "operation", operation, "account_id", input.ID, "request_id", meta.RequestId, "correlation_id", correlation)
		return store.Result{}, domain.SafeError(checkCtx.Err())
	}
	if observation.Problem != nil {
		copy := *observation.Problem
		copy.CorrelationID = correlation
		observation.Problem = &copy
	}
	unlock, err = s.lockAccounts(checkCtx)
	if err != nil {
		return store.Result{}, err
	}
	locked = true
	result, err = s.Store.Mutate(checkCtx, domain.ID(meta.RequestId), string(operation), input, func(tx *store.Tx) (any, error) {
		current, currentProvider, err := inspectionPreflight(tx, input, operation)
		if err != nil {
			return nil, err
		}
		if err := automaticInspectionPreflight(current, currentProvider, automaticInspection(ctx)); err != nil {
			return nil, err
		}
		if current.Connection.ID != account.Connection.ID || currentProvider.Endpoint != provider.Endpoint || currentProvider.Protocol != provider.Protocol || currentProvider.Authentication != provider.Authentication {
			return nil, domain.Fail(domain.Conflict, "The inspected account authority changed.", "Inspect the current connection explicitly.")
		}
		return apply(tx, current, observation)
	})
	if err != nil {
		s.logger.Warn("account_inspection_unpublished", "operation", operation, "account_id", input.ID, "request_id", meta.RequestId, "error_code", domain.SafeError(err).Code, "correlation_id", correlation)
		return store.Result{}, err
	}
	var problemCode domain.Code
	if observation.Problem != nil {
		problemCode = observation.Problem.Code
	}
	s.logger.Info("account_inspection_finished", "operation", operation, "account_id", input.ID, "request_id", meta.RequestId, "failure", observation.Failure, "inspection_stage", observation.Stage, "inspection_reason", observation.Reason, "error_code", problemCode, "authentication", observation.Authentication, "model_count", len(observation.Models), "http_status", observation.HTTPStatus, "duration_ms", time.Since(started).Milliseconds(), "correlation_id", correlation)
	return result, nil
}
