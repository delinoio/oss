// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"time"
)

type changeAccountFormatInput struct {
	ID                                               domain.ID
	Revision                                         uint64
	ProviderRevision                                 uint64
	Protocol                                         domain.APIProtocol
	Alias                                            string
	Enabled, ExcludeAutomatic, RecoveryNotifications bool
	// Actor binds the retry receipt to the authenticated owner/client. Receipts
	// created before this field existed digest differently and fail closed.
	Actor domain.Principal
}

func (s *Service) ChangeAccountApiFormat(ctx context.Context, req *connect.Request[pb.ChangeAccountApiFormatRequest]) (*connect.Response[pb.ChangeAccountApiFormatResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	// Resolve the caller before any receipt lookup so a retry can never be
	// adopted by a different actor.
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Only an owner or paired client can change an API format.", "Use an authenticated product client."), correlation)
	}
	if err := validateAccountMutation(req.Msg.Mutation); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	input := changeAccountFormatInput{
		ID: domain.ID(meta.Id), Revision: meta.ExpectedRevision,
		ProviderRevision: req.Msg.ProviderRevision, Protocol: rpc.APIProtocol(req.Msg.ApiProtocol),
		Alias: req.Msg.Alias, Enabled: req.Msg.Enabled,
		ExcludeAutomatic: req.Msg.ExcludeAutomatic, RecoveryNotifications: req.Msg.RecoveryNotifications,
		Actor: actor,
	}
	if !input.Protocol.API() {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Choose a supported API format.", "Select a declared provider format."), correlation)
	}
	if input.ProviderRevision == 0 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "The inspected provider revision is required.", "Refresh the provider profile in an updated client before changing format."), correlation)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	defer unlock()
	// Replays return current metadata without key access or new side effects.
	_, replayed, err := s.Store.Replay(ctx, domain.ID(meta.RequestId), "account.change-format", input)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if !replayed {
		if len(s.accountChecks[input.ID]) != 0 {
			return nil, rpc.Error(domain.Fail(domain.Conflict, "An account inspection is running.", "Wait for its original result before changing format."), correlation)
		}
		var previous store.Record
		var account domain.Account
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			var e error
			previous, account, e = accountFromTx(tx, input.ID, input.Revision)
			return e
		})
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if account.Connection == nil && account.APIProtocol != input.Protocol {
			account.APIProtocol = input.Protocol
			raw, e := json.Marshal(account)
			if e != nil {
				return nil, rpc.Error(e, correlation)
			}
			if err := s.verifyAccountFormatCleanup(ctx, ConfigurationMutation{ID: input.ID, ExpectedRevision: previous.Revision, Document: raw}); err != nil {
				return nil, rpc.Error(err, correlation)
			}
		}
	}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "account.change-format", input, func(tx *store.Tx) (any, error) {
		record, account, e := accountFromTx(tx, input.ID, input.Revision)
		if e != nil {
			return nil, e
		}
		if account.Type != domain.APIAccount || account.Removal != nil {
			return nil, domain.Fail(domain.Conflict, "This account cannot change API format.", "Finish pending cleanup before editing an API account.")
		}
		providerRecord, e := tx.Get(domain.ProviderKind, account.ProviderID)
		if e != nil {
			return nil, e
		}
		if providerRecord.ID != account.ProviderID || providerRecord.Revision != input.ProviderRevision {
			s.logger.WarnContext(ctx, "account_api_format_provider_revision_conflict", "account_id", input.ID, "provider_id", account.ProviderID, "expected_revision", input.ProviderRevision, "actual_revision", providerRecord.Revision)
			return nil, domain.Fail(domain.Conflict, "The selected provider profile changed.", "Refresh the provider profile and review its API format before saving again.")
		}
		before, e := accountAPIProvider(tx, account)
		if e != nil {
			return nil, e
		}
		proposed := account
		proposed.APIProtocol, proposed.Connection = input.Protocol, nil
		after, e := accountAPIProvider(tx, proposed)
		if e != nil {
			return nil, e
		}
		if (before.Authentication == domain.KeylessAuth) != (after.Authentication == domain.KeylessAuth) {
			return nil, domain.Fail(domain.Conflict, "An account cannot change whether it owns credentials.", "Create a new account for a keyless or key-required profile.")
		}
		changed := before.LegacyAPIFormat() != after.LegacyAPIFormat()
		if changed && account.Connection != nil {
			if len(account.RetainedConnections) >= domain.MaxAccountConnectionGenerations {
				return nil, domain.Fail(domain.ResourceExhausted, "This account has reached its retained format limit.", "Keep its original sessions; create a new account for further formats.")
			}
			original := *account.Connection
			if original.APIFormat == nil {
				profile := before.LegacyAPIFormat()
				original.APIFormat = &profile
			}
			account.RetainedConnections = append(account.RetainedConnections, domain.AccountConnectionGeneration{Connection: original, Health: account.Health, Validation: account.Validation})
			profile := after.LegacyAPIFormat()
			account.Connection = &domain.AccountConnection{ID: domain.ID(meta.RequestId), CredentialID: original.CredentialReferenceID(), Authentication: profile.Authentication, APIFormat: &profile, ConnectedAt: time.Now().UTC().Truncate(time.Millisecond)}
			account.Health, account.Validation, account.Catalog = domain.AccountUnverified, nil, nil
			// Quota belongs to the shared credential, independently of format validation.
		}
		account.APIProtocol, account.Alias, account.Enabled = input.Protocol, input.Alias, input.Enabled
		account.ExcludeAutomatic, account.RecoveryNotifications = input.ExcludeAutomatic, input.RecoveryNotifications
		if e := account.Validate(); e != nil {
			return nil, e
		}
		if _, e = tx.Put(domain.AccountKind, input.ID, record.Revision, "", "", account); e != nil {
			return nil, e
		}
		if changed {
			if e := markFormatWorkersForReconfiguration(tx, input.ID, after.Protocol); e != nil {
				return nil, e
			}
		}
		return accountReceipt{ID: input.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	record, err := s.accountRecord(ctx, input.ID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "account_api_format_changed", "account_id", input.ID, "request_id", meta.RequestId, "replayed", result.Replayed, "api_protocol", input.Protocol)
	response := connect.NewResponse(&pb.ChangeAccountApiFormatResponse{Account: rpc.Resource(record), RequestId: meta.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

// Only original immutable execution inputs call this projection. Fresh account
// selection, validation and routing always use the current account generation.
func executionAccountFromTx(tx *store.Tx, id, connection domain.ID) (store.Record, domain.Account, error) {
	record, account, err := accountFromTx(tx, id, 0)
	if err == nil {
		account = account.ForConnection(connection)
	}
	return record, account, err
}

// Every retained generation remains a provider reference, even while its sessions
// are stopped. Editing a provider cannot relabel original execution authority.
func accountProfileReferences(account domain.Account) []domain.Account {
	references := []domain.Account{account}
	for _, generation := range account.RetainedConnections {
		references = append(references, account.ForConnection(generation.Connection.ID))
	}
	return references
}

// This marker blocks new routing without changing any immutable execution or
// automatically substituting accounts. Explicit Worker saving clears it.
func markFormatWorkersForReconfiguration(tx *store.Tx, id domain.ID, protocol domain.APIProtocol) error {
	records, err := all(tx, domain.AgentKind)
	if err != nil {
		return err
	}
	for _, record := range records {
		agent, err := store.Decode[domain.Agent](record)
		if err != nil {
			return err
		}
		if agent.ReconfigurationRequired || providers.HarnessMatches(agent.Harness, protocol) {
			continue
		}
		referenced := false
		for _, route := range agent.SourceRoutes() {
			for _, account := range route.Accounts {
				referenced = referenced || account.ID == id
			}
		}
		if !referenced {
			continue
		}
		agent.ReconfigurationRequired = true
		if _, err = tx.Put(domain.AgentKind, record.ID, record.Revision, "", "", agent); err != nil {
			return err
		}
	}
	return nil
}
