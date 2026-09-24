package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// This interface allows native failure injection only in tests. The server's
// production constructor always opens the protected OS-backed vault.
type accountSecrets interface {
	Put(context.Context, credentials.Ref, []byte) (string, error)
	Get(context.Context, credentials.Ref) ([]byte, error)
	Delete(context.Context, credentials.Ref) error
	UnremovedReferences(context.Context, domain.ID) ([]credentials.Ref, error)
}

func (s *Service) lockAccounts(ctx context.Context) (func(), error) {
	s.accountOnce.Do(func() { s.accountGate = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	select {
	case s.accountGate <- struct{}{}:
	case <-ctx.Done():
		return nil, domain.SafeError(ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		<-s.accountGate
		return nil, domain.SafeError(err)
	}
	return func() { <-s.accountGate }, nil
}

// Called only while holding accountGate. A transient open failure is retryable;
// metadata/status operations never require an unlocked native store.
func (s *Service) secrets() (accountSecrets, error) {
	if s.accountSecrets != nil {
		return s.accountSecrets, nil
	}
	vault, err := credentials.Open(filepath.Join(s.Store.Root(), "secrets"), s.Identity.ServerID, s.logger)
	if err != nil {
		return nil, err
	}
	s.accountSecrets = vault
	s.ownedVault = vault
	return vault, nil
}
func (s *Service) closeAccountSecrets() error {
	// No native operation may outlive the server's scope lock or final stopped log.
	unlock, err := s.lockAccounts(context.Background())
	if err != nil {
		return err
	}
	defer unlock()
	if s.ownedVault == nil {
		return nil
	}
	err = s.ownedVault.Close()
	s.ownedVault = nil
	return err
}
func validateAccountMutation(m *pb.Mutation) error {
	if m == nil || m.Id == "" || m.ExpectedRevision == 0 {
		return domain.Fail(domain.MissingInput, "An account ID, current revision and request ID are required.", "Read account status before changing its connection.")
	}
	if err := domain.ID(m.Id).Validate(); err != nil {
		return err
	}
	return domain.ID(m.RequestId).Validate()
}

type connectAccountInput struct {
	ID         domain.ID `json:"id"`
	Revision   uint64    `json:"revision"`
	Keyless    bool      `json:"keyless"`
	Commitment string    `json:"commitment,omitempty"`
}
type disconnectAccountInput struct {
	ID       domain.ID `json:"id"`
	Revision uint64    `json:"revision"`
}
type accountReceipt struct {
	ID           domain.ID `json:"id"`
	CompletionID domain.ID `json:"completion_id,omitempty"`
}

// Request receipts must remain comparable after a credential has been deleted.
// Bind input with the existing private, durable server owner identity, separately
// domain-separated from cursor signatures. No upstream key or unkeyed key digest
// enters SQLite. Restoring a scope must preserve its matching owner identity.
func (s *Service) accountCommitment(request domain.ID, key []byte) string {
	mac := hmac.New(sha256.New, []byte(s.Identity.Token))
	mac.Write([]byte("delidev/account-connect/receipt/v1\x00" + string(s.Identity.ServerID) + "\x00" + string(request) + "\x00"))
	mac.Write(key)
	return hex.EncodeToString(mac.Sum(nil))
}
func accountFromTx(tx *store.Tx, id domain.ID, revision uint64) (store.Record, domain.Account, error) {
	if err := tx.Authorize(); err != nil {
		return store.Record{}, domain.Account{}, err
	}
	record, err := tx.Get(domain.AccountKind, id)
	if err != nil {
		return record, domain.Account{}, err
	}
	if revision != 0 && record.Revision != revision {
		return record, domain.Account{}, domain.Fail(domain.Conflict, "The account revision changed.", "Read current account status before starting a new mutation.")
	}
	account, err := store.Decode[domain.Account](record)
	return record, account, err
}
func accountConnectPreflight(tx *store.Tx, input connectAccountInput) (domain.Account, domain.Provider, error) {
	_, account, err := accountFromTx(tx, input.ID, input.Revision)
	if err != nil {
		return account, domain.Provider{}, err
	}
	if account.Type != domain.APIAccount {
		return account, domain.Provider{}, domain.Fail(domain.Unsupported, "Subscription accounts require their official login flow.", "Use the provider's isolated subscription authentication operation.")
	}
	if account.Connection != nil || account.Removal != nil || account.Health != domain.AccountDisconnected {
		return account, domain.Provider{}, domain.Fail(domain.Conflict, "The account already has a connection or pending cleanup.", "Disconnect it and complete credential removal before connecting a replacement.")
	}
	record, err := tx.Get(domain.ProviderKind, account.ProviderID)
	if err != nil {
		return account, domain.Provider{}, err
	}
	provider, err := store.Decode[domain.Provider](record)
	if err != nil {
		return account, provider, err
	}
	if err = provider.Validate(); err != nil {
		return account, provider, err
	}
	if input.Keyless != (provider.Authentication == domain.KeylessAuth) || provider.Protocol == domain.NativeSubscription {
		return account, provider, domain.Fail(domain.InvalidArgument, "The connection input does not match the provider's authentication.", "Use explicit keyless input for a keyless local provider, or supply its API key.")
	}
	return account, provider, nil
}
func (s *Service) accountRecord(ctx context.Context, id domain.ID) (store.Record, error) {
	var record store.Record
	err := s.Store.Read(ctx, func(tx *store.Tx) error { var err error; record, _, err = accountFromTx(tx, id, 0); return err })
	return record, err
}
func (s *Service) ConnectAccount(ctx context.Context, req *connect.Request[pb.ConnectAccountRequest]) (*connect.Response[pb.ConnectAccountResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	// Clear the decoded RPC input on every exit; no receipt/log captures this struct.
	defer clear(req.Msg.ApiKey)
	if err := validateAccountMutation(req.Msg.Mutation); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := domain.ValidateAPIKey(req.Msg.ApiKey, req.Msg.Keyless); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	defer unlock()
	meta := req.Msg.Mutation
	input := connectAccountInput{ID: domain.ID(meta.Id), Revision: meta.ExpectedRevision, Keyless: req.Msg.Keyless}
	if !input.Keyless {
		input.Commitment = s.accountCommitment(domain.ID(meta.RequestId), req.Msg.ApiKey)
	}
	result, replayed, err := s.Store.Replay(ctx, domain.ID(meta.RequestId), "account.connect", input)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if !replayed {
		err = s.Store.Read(ctx, func(tx *store.Tx) error { _, _, err := accountConnectPreflight(tx, input); return err })
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if !input.Keyless {
			vault, e := s.secrets()
			if e != nil {
				return nil, rpc.Error(e, correlation)
			}
			_, err = vault.Put(ctx, credentials.Ref{Owner: input.ID, ID: domain.ID(meta.RequestId), Purpose: credentials.AccountAPI}, req.Msg.ApiKey)
			if err != nil {
				return nil, rpc.Error(err, correlation)
			}
		}
		result, err = s.Store.Mutate(ctx, domain.ID(meta.RequestId), "account.connect", input, func(tx *store.Tx) (any, error) {
			account, provider, err := accountConnectPreflight(tx, input)
			if err != nil {
				return nil, err
			}
			account.Connection = &domain.AccountConnection{ID: domain.ID(meta.RequestId), Authentication: provider.Authentication, ConnectedAt: time.Now().UTC().Truncate(time.Millisecond)}
			account.Health = domain.AccountUnverified
			account.Validation = nil
			account.Catalog = nil
			account.Quota = nil
			account.ConfirmedExhausted = false
			if _, err = tx.Put(domain.AccountKind, input.ID, input.Revision, "", "", account); err != nil {
				return nil, err
			}
			return accountReceipt{ID: input.ID}, nil
		})
		// Never remove the staged reference here. Commit errors/cancellation can be
		// uncertain; an explicit disconnect reconciles all of this owner's intents.
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	var accepted accountReceipt
	if err = domain.Decode(result.Data, &accepted); err != nil || accepted.ID != input.ID {
		return nil, rpc.Error(domain.Fail(domain.NotFound, "The accepted account was subsequently deleted.", "The original request cannot recreate it."), correlation)
	}
	record, err := s.accountRecord(ctx, input.ID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.Info("account_connection_saved", "account_id", input.ID, "request_id", meta.RequestId, "replayed", result.Replayed, "correlation_id", correlation)
	response := connect.NewResponse(&pb.ConnectAccountResponse{Account: rpc.Resource(record), RequestId: meta.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) DisconnectAccount(ctx context.Context, req *connect.Request[pb.DisconnectAccountRequest]) (*connect.Response[pb.DisconnectAccountResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := validateAccountMutation(req.Msg.Mutation); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	defer unlock()
	meta := req.Msg.Mutation
	input := disconnectAccountInput{domain.ID(meta.Id), meta.ExpectedRevision}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "account.disconnect", input, func(tx *store.Tx) (any, error) {
		_, account, err := accountFromTx(tx, input.ID, input.Revision)
		if err != nil {
			return nil, err
		}
		if account.Type != domain.APIAccount {
			return nil, domain.Fail(domain.Unsupported, "Subscription accounts require their logout flow.", "Use the provider's isolated subscription lifecycle operation.")
		}
		if account.Removal != nil {
			return nil, domain.Fail(domain.Conflict, "Account credential removal is already pending.", "Retry the recorded disconnect request with its original revision and request ID.")
		}
		completionID := domain.NewID()
		removal := &domain.AccountRemoval{RequestID: domain.ID(meta.RequestId), ExpectedRevision: input.Revision}
		account.Connection = nil
		account.Health = domain.AccountDisconnected
		account.Validation = nil
		account.Catalog = nil
		account.Removal = removal
		account.Quota = nil
		account.ConfirmedExhausted = false
		if _, err = tx.Put(domain.AccountKind, input.ID, input.Revision, "", "", account); err != nil {
			return nil, err
		}
		return accountReceipt{ID: input.ID, CompletionID: completionID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var accepted accountReceipt
	if err = domain.Decode(result.Data, &accepted); err != nil || accepted.ID != input.ID || accepted.CompletionID.Validate() != nil {
		return nil, rpc.Error(domain.Fail(domain.NotFound, "The disconnected account was subsequently deleted.", "The original request cannot recreate it."), correlation)
	}
	cleanupErr := s.finishAccountRemoval(ctx, accepted, domain.ID(meta.RequestId))
	// The accepted disconnect is returned even if the caller's deadline expires
	// during native cleanup. If transport delivery fails, its durable receipt and
	// removal marker still allow the same request to resume after reconnect.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	record, err := s.accountRecord(readCtx, input.ID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	message := &pb.DisconnectAccountResponse{Account: rpc.Resource(record), RequestId: meta.RequestId, Replayed: result.Replayed}
	if cleanupErr != nil {
		problem := *domain.SafeError(cleanupErr)
		problem.CorrelationID = correlation
		message.CleanupProblemJson, _ = json.Marshal(problem)
		s.logger.Warn("account_credential_cleanup_pending", "account_id", input.ID, "request_id", meta.RequestId, "error_code", problem.Code, "correlation_id", correlation)
	} else {
		s.logger.Info("account_disconnected", "account_id", input.ID, "request_id", meta.RequestId, "replayed", result.Replayed, "correlation_id", correlation)
	}
	response := connect.NewResponse(message)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) finishAccountRemoval(ctx context.Context, accepted accountReceipt, requestID domain.ID) error {
	var pending bool
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, account, err := accountFromTx(tx, accepted.ID, 0)
		if err != nil {
			return err
		}
		pending = account.Removal != nil && account.Removal.RequestID == requestID
		return nil
	})
	if err != nil || !pending {
		return err
	}
	s.cancelAccountChecks(accepted.ID)
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	refs, err := vault.UnremovedReferences(ctx, accepted.ID)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if err = vault.Delete(ctx, ref); err != nil {
			return err
		}
	}
	_, err = s.Store.Mutate(ctx, accepted.CompletionID, "account.cleanup", struct{ ID, RequestID domain.ID }{accepted.ID, requestID}, func(tx *store.Tx) (any, error) {
		record, account, err := accountFromTx(tx, accepted.ID, 0)
		if err != nil {
			return nil, err
		}
		if account.Connection != nil || account.Removal == nil || account.Removal.RequestID != requestID {
			return nil, domain.Fail(domain.Conflict, "The account cleanup generation changed.", "Read current account status.")
		}
		account.Removal = nil
		if _, err = tx.Put(domain.AccountKind, accepted.ID, record.Revision, "", "", account); err != nil {
			return nil, err
		}
		return accountReceipt{ID: accepted.ID}, nil
	})
	return err
}
func (s *Service) GetAccountStatus(ctx context.Context, req *connect.Request[pb.GetAccountStatusRequest]) (*connect.Response[pb.GetAccountStatusResponse], error) {
	record, err := s.accountRecord(ctx, domain.ID(req.Msg.Id))
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.GetAccountStatusResponse{Account: rpc.Resource(record)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
