// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"slices"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// The source is derived from live account records inside the transaction, never
// from a renderer-provided provider/service or an observation of native models.
type agentWorkerModelSelection struct {
	ModelID       domain.ID `json:"model_id,omitempty"`
	NativeID      string    `json:"native_id,omitempty"`
	ModelRevision uint64    `json:"model_revision"`
}

type agentWorkerMutation struct {
	RouteModels []agentWorkerModelSelection `json:"route_models,omitempty"`
	ConfigurationMutation
	ModelID       domain.ID `json:"model_id,omitempty"`
	NativeID      string    `json:"native_id,omitempty"`
	ModelRevision uint64    `json:"model_revision"`
}

func saveAgentWorker(ctx context.Context, state *store.Store, input agentWorkerMutation) (store.Result, error) {
	var agent domain.Agent
	if e := domain.Decode(input.Document, &agent); e != nil {
		return store.Result{}, e
	}
	if len(agent.Routes) == 0 || len(input.RouteModels) != len(agent.Routes) || input.ModelID != "" || input.NativeID != "" || input.ModelRevision != 0 {
		return store.Result{}, domain.Fail(domain.InvalidArgument, "Inline source routes are required.", "Use schema 4 with one exact native selection per source.")
	}
	if input.ID == "" && input.ExpectedRevision != 0 {
		return store.Result{}, domain.Fail(domain.InvalidArgument, "A new Worker has no revision.", "Use revision zero for creation.")
	}
	for _, selection := range input.RouteModels {
		if selection.ModelID != "" || selection.ModelRevision != 0 || domain.Text(selection.NativeID, "native model ID", 256, true) != nil {
			return store.Result{}, domain.Fail(domain.Unsupported, "Saved Model selection is retired.", "Enter the exact native model ID for its account source.")
		}
	}
	return state.Mutate(ctx, input.RequestID, "configuration.agent-worker.save", input, func(tx *store.Tx) (any, error) {
		routes := slices.Clone(agent.Routes)
		seen := map[string]bool{}
		for i, route := range routes {
			if len(route.Accounts) == 0 {
				return nil, domain.Fail(domain.MissingInput, "Select an account for every source.", "Choose original accounts before saving.")
			}
			var source domain.Account
			for index, link := range route.Accounts {
				r, e := tx.Get(domain.AccountKind, link.ID)
				if e != nil {
					return nil, e
				}
				a, e := store.Decode[domain.Account](r)
				if e != nil {
					return nil, e
				}
				if e = a.Validate(); e != nil {
					return nil, e
				}
				if index == 0 {
					source = a
				}
				if a.Type != source.Type || a.ProviderID != source.ProviderID || a.SubscriptionService != source.SubscriptionService {
					return nil, domain.Fail(domain.InvalidArgument, "Selected accounts use different sources.", "Keep each provider or subscription source in its own route.")
				}
			}
			identity := domain.ModelIdentity{ProviderID: source.ProviderID, SubscriptionService: source.SubscriptionService, NativeID: input.RouteModels[i].NativeID}
			if e := identity.Validate(); e != nil {
				return nil, e
			}
			key := string(identity.ProviderID) + "|" + string(identity.SubscriptionService)
			if seen[key] {
				return nil, domain.Fail(domain.InvalidArgument, "Duplicate account source.", "Combine accounts from the same source.")
			}
			seen[key] = true
			model := domain.InlineModel{ModelIdentity: identity, MetadataSource: domain.Unknown}
			if route.Model != nil {
				model = *route.Model
				model.ModelIdentity = identity
				if model.MetadataSource == domain.Known {
					model.MetadataSource = domain.UserDeclared
				}
			}
			if e := model.Validate(agent.Harness); e != nil {
				return nil, e
			}
			routes[i].Model = &model
			routes[i].ModelID = identity.Key()
		}
		agent.Routes = routes
		if e := agent.Validate(); e != nil {
			return nil, e
		}
		id := input.ID
		if id == "" {
			id = domain.NewID()
		}
		if e := validateNewProviderSelections(tx, input.ConfigurationMutation, id, &agent); e != nil {
			return nil, e
		}
		if e := validateRelationships(tx, domain.AgentKind, id, input.ExpectedRevision, &agent); e != nil {
			return nil, e
		}
		return tx.Put(domain.AgentKind, id, input.ExpectedRevision, "", "", agent)
	})
}

func (s *Service) SaveAgentWorker(ctx context.Context, req *connect.Request[pb.SaveAgentWorkerRequest]) (*connect.Response[pb.SaveConfigurationResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if req.Msg.Mutation == nil || req.Msg.Model != nil || len(req.Msg.RouteModels) == 0 || req.Msg.SchemaVersion != 4 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "A supported Worker document, mutation and typed model selection are required.", "Use the current Worker revision and one model selection."), correlation)
	}
	if rpc.ResourceSchemaVersion(domain.AgentKind, req.Msg.DocumentJson) != req.Msg.SchemaVersion {
		return nil, rpc.Error(domain.Fail(domain.Unsupported, "Worker schema does not match its account routes.", "Use schema 4 for inline source routes."), correlation)
	}
	input := agentWorkerMutation{ConfigurationMutation: ConfigurationMutation{RequestID: domain.ID(req.Msg.Mutation.RequestId), ID: domain.ID(req.Msg.Mutation.Id), ExpectedRevision: req.Msg.Mutation.ExpectedRevision, Kind: domain.AgentKind, Document: req.Msg.DocumentJson}}
	selection := func(value *pb.AgentWorkerModelSelection) agentWorkerModelSelection {
		if value == nil {
			return agentWorkerModelSelection{}
		}
		result := agentWorkerModelSelection{ModelRevision: value.ExpectedModelRevision}
		switch picked := value.Selection.(type) {
		case *pb.AgentWorkerModelSelection_ModelId:
			result.ModelID = domain.ID(picked.ModelId)
		case *pb.AgentWorkerModelSelection_NativeId:
			result.NativeID = picked.NativeId
		}
		return result
	}
	if req.Msg.Model != nil {
		picked := selection(req.Msg.Model)
		input.ModelID, input.NativeID, input.ModelRevision = picked.ModelID, picked.NativeID, picked.ModelRevision
	}
	for _, value := range req.Msg.RouteModels {
		input.RouteModels = append(input.RouteModels, selection(value))
	}
	result, err := saveAgentWorker(ctx, s.Store, input)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var record store.Record
	if err := json.Unmarshal(result.Data, &record); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if record.Kind != domain.AgentKind {
		return nil, rpc.Error(domain.Fail(domain.NotFound, "The accepted Worker was subsequently deleted.", "The original request cannot recreate it."), correlation)
	}
	s.logger.Info("agent_worker_configuration_saved", "request_id", result.RequestID, "agent_id", record.ID, "revision", record.Revision, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.SaveConfigurationResponse{Resource: rpc.Resource(record), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
