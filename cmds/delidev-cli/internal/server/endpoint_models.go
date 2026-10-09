// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"reflect"
	"time"
)

const endpointListing inspectionOperation = "provider.list-endpoint-models"

func (s *Service) ListEndpointModels(ctx context.Context, req *connect.Request[pb.ListEndpointModelsRequest]) (*connect.Response[pb.ListEndpointModelsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, e := pricingActor(ctx); e != nil {
		return nil, rpc.Error(e, correlation)
	}
	input := disconnectAccountInput{ID: domain.ID(req.Msg.AccountId), Revision: req.Msg.ExpectedAccountRevision}
	if input.ID.Validate() != nil || input.Revision == 0 || req.Msg.ExpectedProviderRevision == 0 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Original account and provider revisions are required.", "Read the selected account before listing its endpoint models."), correlation)
	}
	unlock, e := s.lockAccounts(ctx)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	var account domain.Account
	var provider domain.Provider
	preflight := func(tx *store.Tx) error {
		if e := tx.Authorize(); e != nil {
			return e
		}
		a, p, e := inspectionPreflight(tx, input, endpointListing)
		if e != nil {
			return e
		}
		row, e := tx.Get(domain.ProviderKind, a.ProviderID)
		if e != nil {
			return e
		}
		if row.Revision != req.Msg.ExpectedProviderRevision {
			return domain.Fail(domain.Conflict, "The selected provider changed.", "Read the original account profile again.")
		}
		if !a.Enabled || !p.EnabledValue() {
			return providerDisabled()
		}
		account, provider = a, p
		return nil
	}
	e = s.Store.Read(ctx, preflight)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	original := *account.Connection
	checkCtx, finish, e := s.startAccountCheck(ctx, input.ID, domain.NewID(), account.ProviderID, endpointListing)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	defer func() {
		if locked {
			unlock()
			locked = false
		}
		finish()
	}()
	unlock()
	locked = false
	var key []byte
	project := ""
	if account.Connection.Authentication != domain.KeylessAuth {
		credential, e := s.resolveAPICredential(checkCtx, input.ID, account.Connection.ID, account.ProviderID)
		if e != nil {
			return nil, rpc.Error(e, correlation)
		}
		key, project = credential.key, credential.quotaProject
		defer clear(key)
	}
	started := time.Now()
	observation, e := providers.ListEndpointModels(checkCtx, provider, key, project, s.outboundResolver())
	clear(key)
	if e == nil {
		if problem := observation.Problem(); problem != nil {
			e = problem
		}
	}
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	unlock, e = s.lockAccounts(checkCtx)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	locked = true
	e = s.Store.Read(checkCtx, func(tx *store.Tx) error {
		if e := preflight(tx); e != nil {
			return e
		}
		if account.Connection == nil || !reflect.DeepEqual(*account.Connection, original) {
			return domain.Fail(domain.Conflict, "The original account connection changed.", "List models only through the current selected connection.")
		}
		return nil
	})
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	value := &pb.ListEndpointModelsResponse{AccountId: string(input.ID), AccountRevision: input.Revision, ProviderId: string(account.ProviderID), ProviderRevision: req.Msg.ExpectedProviderRevision, ConnectionId: string(original.ID), ObservedAtUnixMs: observation.ObservedAt.UnixMilli()}
	for _, m := range observation.Models {
		value.Models = append(value.Models, &pb.EndpointModel{NativeId: m.ID, DisplayName: m.Name, ContextLimit: m.ContextLimit, InputModalities: m.InputModalities, OutputModalities: m.OutputModalities, Tools: m.Tools, Reasoning: m.Reasoning})
	}
	s.logger.InfoContext(ctx, "endpoint_models_listed", "correlation_id", correlation, "account_id", input.ID, "count", len(value.Models), "duration_ms", time.Since(started).Milliseconds())
	response := connect.NewResponse(value)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
