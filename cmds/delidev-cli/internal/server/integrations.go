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
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Production always opens the native PAT store and the closed GitHub.com client.
// These interfaces permit private deterministic failure injection in tests only.
type integrationSecrets interface {
	Put(context.Context, credentials.PATRef, []byte) error
	Get(context.Context, credentials.PATRef) ([]byte, error)
	Delete(context.Context, credentials.PATRef) error
	UnremovedReferences(context.Context, domain.ID) ([]credentials.PATRef, error)
}
type githubIdentity interface {
	Identity(context.Context, []byte) (gh.IdentityObservation, error)
}

type integrationInput struct {
	Actor        domain.Principal              `json:"actor"`
	ID           domain.ID                     `json:"id"`
	Revision     uint64                        `json:"revision"`
	Commitment   string                        `json:"commitment,omitempty"`
	Definition   *domain.IntegrationDefinition `json:"definition,omitempty"`
	GenerationID domain.ID                     `json:"generation_id,omitempty"`
}
type integrationReceipt struct {
	ID           domain.ID `json:"id"`
	CompletionID domain.ID `json:"completion_id,omitempty"`
	ValidationID domain.ID `json:"validation_id,omitempty"`
	Deleted      bool      `json:"deleted,omitempty"`
}

func integrationActor(ctx context.Context) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return actor, domain.Fail(domain.PermissionDenied, "An owner or paired client is required.", "Use the selected server's product connection.")
	}
	return actor, nil
}
func integrationMutation(ctx context.Context, m *pb.Mutation, create bool) (integrationInput, error) {
	actor, err := integrationActor(ctx)
	if err != nil {
		return integrationInput{}, err
	}
	if m == nil || domain.ID(m.RequestId).Validate() != nil || (m.Id == "" && (!create || m.ExpectedRevision != 0)) || (m.Id != "" && (domain.ID(m.Id).Validate() != nil || m.ExpectedRevision == 0)) {
		return integrationInput{}, domain.Fail(domain.InvalidArgument, "Invalid integration mutation identity or revision.", "Use the current profile revision and retain the original request ID for retries; new profiles use no ID and revision zero.")
	}
	return integrationInput{Actor: actor, ID: domain.ID(m.Id), Revision: m.ExpectedRevision}, nil
}
func (s *Service) lockIntegrations(ctx context.Context) (func(), error) {
	s.integrationOnce.Do(func() { s.integrationGate = make(chan struct{}, 1) })
	if ctx.Err() != nil {
		return nil, domain.SafeError(ctx.Err())
	}
	select {
	case s.integrationGate <- struct{}{}:
	case <-ctx.Done():
		return nil, domain.SafeError(ctx.Err())
	}
	if ctx.Err() != nil {
		<-s.integrationGate
		return nil, domain.SafeError(ctx.Err())
	}
	return func() { <-s.integrationGate }, nil
}

// The integration gate serializes native generation changes; network requests
// release it and independently check authority and generation on publication.
func (s *Service) patSecrets() (integrationSecrets, error) {
	if s.integrationSecrets != nil {
		return s.integrationSecrets, nil
	}
	key := []byte(s.Identity.Token)
	defer clear(key)
	vault, err := credentials.OpenPAT(filepath.Join(s.Store.Root(), "github-pats"), s.Identity.ServerID, key, s.logger)
	if err != nil {
		return nil, err
	}
	s.integrationSecrets, s.ownedPAT = vault, vault
	return vault, nil
}
func (s *Service) closeIntegrationSecrets() error {
	unlock, err := s.lockIntegrations(context.Background())
	if err != nil {
		return err
	}
	defer unlock()
	for id := range s.integrationChecks {
		if err := s.stopIntegrationCheck(context.Background(), id); err != nil {
			return err
		}
	}
	if s.ownedPAT == nil {
		return nil
	}
	err = s.ownedPAT.Close()
	s.ownedPAT = nil
	return err
}
func integrationFromTx(tx *store.Tx, id domain.ID, revision uint64) (store.Record, domain.Integration, error) {
	if err := tx.Authorize(); err != nil {
		return store.Record{}, domain.Integration{}, err
	}
	record, err := tx.Get(domain.IntegrationKind, id)
	if err != nil {
		return record, domain.Integration{}, err
	}
	if revision != 0 && record.Revision != revision {
		return record, domain.Integration{}, domain.Fail(domain.Conflict, "The integration profile changed.", "Read its current revision before starting a new operation.")
	}
	value, err := store.Decode[domain.Integration](record)
	if err == nil {
		err = value.Validate()
	}
	return record, value, err
}
func (s *Service) integrationRecord(ctx context.Context, id domain.ID) (store.Record, domain.Integration, error) {
	var r store.Record
	var v domain.Integration
	err := s.Store.Read(ctx, func(tx *store.Tx) error { var e error; r, v, e = integrationFromTx(tx, id, 0); return e })
	return r, v, err
}
func (s *Service) SaveIntegrationProfile(ctx context.Context, req *connect.Request[pb.SaveIntegrationProfileRequest]) (*connect.Response[pb.SaveIntegrationProfileResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	input, err := integrationMutation(ctx, req.Msg.Mutation, true)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var definition domain.IntegrationDefinition
	if req.Msg.SchemaVersion != 1 {
		return nil, rpc.Error(domain.Fail(domain.Unsupported, "Unsupported integration schema.", "Use schema version 1."), correlation)
	}
	if err = domain.Decode(req.Msg.DocumentJson, &definition); err == nil {
		err = definition.Validate()
	}
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	input.Definition = &definition
	unlock, err := s.lockIntegrations(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	defer unlock()
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.Mutation.RequestId), "integration.save", input, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		id := input.ID
		value := domain.Integration{IntegrationDefinition: definition}
		if id == "" {
			id = domain.NewID()
		} else {
			_, old, e := integrationFromTx(tx, id, input.Revision)
			if e != nil {
				return nil, e
			}
			if old.Provider != definition.Provider || old.TokenKind != definition.TokenKind || old.ResourceOwner != definition.ResourceOwner {
				return nil, domain.Fail(domain.Conflict, "A profile's token type and resource owner are fixed.", "Create a separate profile for a different token type or resource owner.")
			}
			value.Connection, value.Pending = old.Connection, old.Pending
		}
		if _, err := tx.Put(domain.IntegrationKind, id, input.Revision, "", "", value); err != nil {
			return nil, err
		}
		return integrationReceipt{ID: id}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt integrationReceipt
	if err = domain.Decode(result.Data, &receipt); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	record, _, err := s.integrationRecord(ctx, receipt.ID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.Info("integration_profile_saved", "profile_id", receipt.ID, "request_id", req.Msg.Mutation.RequestId, "replayed", result.Replayed, "correlation_id", correlation)
	response := connect.NewResponse(&pb.SaveIntegrationProfileResponse{Profile: rpc.Resource(record), RequestId: req.Msg.Mutation.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) integrationCommitment(request domain.ID, token []byte) string {
	mac := hmac.New(sha256.New, []byte(s.Identity.Token))
	mac.Write([]byte("delidev/integration-replace/receipt/v1\x00" + string(s.Identity.ServerID) + "\x00" + string(request) + "\x00"))
	mac.Write(token)
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *Service) beginIntegrationChange(ctx context.Context, input integrationInput, request domain.ID, operation domain.IntegrationOperation) (store.Result, integrationReceipt, error) {
	result, err := s.Store.Mutate(ctx, request, "integration."+string(operation), input, func(tx *store.Tx) (any, error) {
		_, value, err := integrationFromTx(tx, input.ID, input.Revision)
		if err != nil {
			return nil, err
		}
		// Explicit deletion can abandon a replacement whose token was lost. It still
		// cleans all owned staged generations and never resumes the old connection.
		if value.Pending != nil && (operation != domain.IntegrationDelete || value.Pending.Operation == domain.IntegrationDelete) {
			return nil, domain.Fail(domain.Conflict, "An integration change is pending.", "Retry its original request and token, or explicitly delete the profile with its current revision.")
		}
		accepted := integrationReceipt{ID: input.ID, CompletionID: domain.NewID()}
		value.Connection = nil
		value.Pending = &domain.IntegrationPending{RequestID: request, CompletionID: accepted.CompletionID, ExpectedRevision: input.Revision, Operation: operation, StartedAt: time.Now().UTC().Truncate(time.Millisecond)}
		if operation == domain.IntegrationReplaceToken {
			value.Pending.GenerationID = request
			accepted.ValidationID = domain.NewID()
		}
		if _, err = tx.Put(domain.IntegrationKind, input.ID, input.Revision, "", "", value); err != nil {
			return nil, err
		}
		return accepted, nil
	})
	var accepted integrationReceipt
	if err == nil {
		err = domain.Decode(result.Data, &accepted)
	}
	if err == nil && (accepted.ID != input.ID || (!accepted.Deleted && accepted.CompletionID.Validate() != nil)) {
		err = domain.Fail(domain.RecoveryRequired, "The integration change receipt is invalid.", "Preserve the current profile and inspect its original operation.")
	}
	return result, accepted, err
}

// Must hold the gate. Exact completed receipts do no native work, including
// after a newer replacement or deletion. No token enters a transaction input.
func (s *Service) finishIntegrationChange(ctx context.Context, accepted integrationReceipt, request domain.ID, operation domain.IntegrationOperation, token []byte) error {
	if accepted.Deleted {
		return nil
	}
	_, value, err := s.integrationRecord(ctx, accepted.ID)
	if err != nil {
		return err
	}
	if value.Pending == nil || value.Pending.RequestID != request || value.Pending.Operation != operation {
		return nil
	}
	if err := s.stopIntegrationCheck(ctx, accepted.ID); err != nil {
		return err
	}
	vault, err := s.patSecrets()
	if err != nil {
		return err
	}
	selected := credentials.PATRef{ProfileID: accepted.ID, GenerationID: request}
	if operation == domain.IntegrationReplaceToken {
		if err = vault.Put(ctx, selected, token); err != nil {
			return err
		}
	}
	refs, err := vault.UnremovedReferences(ctx, accepted.ID)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if operation == domain.IntegrationReplaceToken && ref == selected {
			continue
		}
		if err = vault.Delete(ctx, ref); err != nil {
			return err
		}
	}
	_, err = s.Store.Mutate(ctx, accepted.CompletionID, "integration.complete", struct{ ID, RequestID domain.ID }{accepted.ID, request}, func(tx *store.Tx) (any, error) {
		record, current, err := integrationFromTx(tx, accepted.ID, 0)
		if err != nil {
			return nil, err
		}
		if current.Pending == nil || current.Pending.RequestID != request || current.Pending.CompletionID != accepted.CompletionID || current.Pending.Operation != operation {
			return nil, domain.Fail(domain.Conflict, "The integration generation changed.", "Read the current profile.")
		}
		if operation == domain.IntegrationDelete {
			if err := tx.Delete(domain.IntegrationKind, accepted.ID, record.Revision); err != nil {
				return nil, err
			}
			return integrationReceipt{ID: accepted.ID, Deleted: true}, nil
		}
		current.Pending = nil
		current.Connection = &domain.IntegrationConnection{GenerationID: request, ConnectedAt: time.Now().UTC().Truncate(time.Millisecond)}
		if _, err = tx.Put(domain.IntegrationKind, accepted.ID, record.Revision, "", "", current); err != nil {
			return nil, err
		}
		return integrationReceipt{ID: accepted.ID}, nil
	})
	return err
}
func integrationErrorCode(err error) domain.Code {
	if err == nil {
		return ""
	}
	return domain.SafeError(err).Code
}
func integrationProblem(err error, correlation string) []byte {
	if err == nil {
		return nil
	}
	problem := *domain.SafeError(err)
	problem.CorrelationID = correlation
	raw, _ := json.Marshal(problem)
	return raw
}
func (s *Service) ReplaceIntegrationToken(ctx context.Context, req *connect.Request[pb.ReplaceIntegrationTokenRequest]) (*connect.Response[pb.ReplaceIntegrationTokenResponse], error) {
	defer clear(req.Msg.Token)
	correlation := req.Header().Get(rpc.CorrelationHeader)
	input, err := integrationMutation(ctx, req.Msg.Mutation, false)
	if err == nil {
		err = credentials.ValidatePAT(req.Msg.Token)
	}
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	request := domain.ID(req.Msg.Mutation.RequestId)
	input.Commitment = s.integrationCommitment(request, req.Msg.Token)
	unlock, err := s.lockIntegrations(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	result, accepted, err := s.beginIntegrationChange(ctx, input, request, domain.IntegrationReplaceToken)
	if err != nil {
		unlock()
		return nil, rpc.Error(err, correlation)
	}
	changeErr := s.finishIntegrationChange(ctx, accepted, request, domain.IntegrationReplaceToken, req.Msg.Token)
	clear(req.Msg.Token)
	// Read accepted state even when native storage outlives the caller deadline.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	record, value, readErr := s.integrationRecord(readCtx, input.ID)
	cancel()
	unlock()
	if readErr != nil {
		return nil, rpc.Error(readErr, correlation)
	}
	if changeErr == nil && value.Connection != nil && value.Connection.GenerationID == request && value.Connection.Validation == nil {
		_, changeErr = s.inspectIntegration(ctx, &pb.Mutation{Id: string(input.ID), ExpectedRevision: record.Revision, RequestId: string(accepted.ValidationID)}, request, correlation)
	}
	readCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	record, value, err = s.integrationRecord(readCtx, input.ID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if changeErr == nil && value.Connection != nil && value.Connection.GenerationID == request && value.Connection.Validation != nil && value.Connection.Validation.Problem != nil {
		changeErr = value.Connection.Validation.Problem
	}
	s.logger.Info("integration_token_replaced", "profile_id", input.ID, "request_id", request, "replayed", result.Replayed, "pending", value.Pending != nil, "error_code", integrationErrorCode(changeErr), "correlation_id", correlation)
	response := connect.NewResponse(&pb.ReplaceIntegrationTokenResponse{Profile: rpc.Resource(record), RequestId: string(request), Replayed: result.Replayed, ProblemJson: integrationProblem(changeErr, correlation)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) DeleteIntegrationProfile(ctx context.Context, req *connect.Request[pb.DeleteIntegrationProfileRequest]) (*connect.Response[pb.DeleteIntegrationProfileResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	input, err := integrationMutation(ctx, req.Msg.Mutation, false)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	unlock, err := s.lockIntegrations(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	defer unlock()
	request := domain.ID(req.Msg.Mutation.RequestId)
	result, accepted, err := s.beginIntegrationChange(ctx, input, request, domain.IntegrationDelete)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	cleanupErr := s.finishIntegrationChange(ctx, accepted, request, domain.IntegrationDelete, nil)
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	record, _, readErr := s.integrationRecord(readCtx, input.ID)
	deleted := readErr != nil && domain.SafeError(readErr).Code == domain.NotFound
	if readErr != nil && !deleted {
		return nil, rpc.Error(readErr, correlation)
	}
	message := &pb.DeleteIntegrationProfileResponse{RequestId: string(request), Replayed: result.Replayed, Deleted: deleted, ProblemJson: integrationProblem(cleanupErr, correlation)}
	if !deleted {
		message.Profile = rpc.Resource(record)
	}
	s.logger.Info("integration_profile_deleted", "profile_id", input.ID, "request_id", request, "deleted", deleted, "replayed", result.Replayed, "error_code", integrationErrorCode(cleanupErr), "correlation_id", correlation)
	response := connect.NewResponse(message)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
