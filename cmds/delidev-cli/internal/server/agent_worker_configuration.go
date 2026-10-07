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
	if err := domain.Decode(input.Document, &agent); err != nil {
		return store.Result{}, err
	}
	selections := input.RouteModels
	if len(agent.Routes) > 0 {
		if input.ModelID != "" || input.NativeID != "" || input.ModelRevision != 0 || len(selections) != len(agent.Routes) {
			return store.Result{}, domain.Fail(domain.InvalidArgument, "Source routes require corresponding model selections.", "Use route_models in source order without the single model selection.")
		}
	} else {
		if len(selections) > 0 {
			return store.Result{}, domain.Fail(domain.InvalidArgument, "A single source cannot use route_models.", "Use the single model selection or submit ordered source routes.")
		}
		selections = []agentWorkerModelSelection{{ModelID: input.ModelID, NativeID: input.NativeID, ModelRevision: input.ModelRevision}}
	}
	for i, selection := range selections {
		if len(agent.SourceRoutes()[i].Accounts) == 0 {
			return store.Result{}, domain.Fail(domain.MissingInput, "Select at least one account.", "Choose accounts for every source.")
		}
		if (selection.ModelID == "") == (selection.NativeID == "") || selection.ModelID == "" && selection.ModelRevision != 0 || selection.ModelID != "" && selection.ModelRevision == 0 {
			return store.Result{}, domain.Fail(domain.InvalidArgument, "Select one model identity.", "Use a canonical model and its revision, or an exact native model ID.")
		}
		if selection.ModelID != "" {
			if err := selection.ModelID.Validate(); err != nil {
				return store.Result{}, err
			}
		}
		if selection.NativeID != "" {
			if err := domain.Text(selection.NativeID, "native model ID", 256, true); err != nil {
				return store.Result{}, err
			}
		}
	}
	if input.ID == "" && input.ExpectedRevision != 0 {
		return store.Result{}, domain.Fail(domain.InvalidArgument, "A new Worker has no revision.", "Use revision zero for creation.")
	}
	// Model identities are resolved inside the receipt transaction. Validate the
	// remaining complete document now without mutating the caller's route slice.
	proposed := agent
	proposed.Routes = slices.Clone(agent.Routes)
	if len(proposed.Routes) == 0 {
		proposed.ModelID = domain.NewID()
	} else {
		for i := range proposed.Routes {
			proposed.Routes[i].ModelID = domain.NewID()
		}
	}
	if err := proposed.Validate(); err != nil {
		return store.Result{}, err
	}
	return state.Mutate(ctx, input.RequestID, "configuration.agent-worker.save", input, func(tx *store.Tx) (any, error) {
		routes := slices.Clone(agent.SourceRoutes())
		seen := map[string]bool{}
		for i, route := range routes {
			modelID, source, err := resolveWorkerModel(tx, agent.WithSource(route), selections[i])
			if err != nil {
				return nil, err
			}
			if seen[source] {
				return nil, domain.Fail(domain.InvalidArgument, "An account source appears more than once.", "Combine accounts from the same source in one route.")
			}
			seen[source] = true
			routes[i].ModelID = modelID
		}
		if len(agent.Routes) > 0 {
			agent.Routes = routes
		} else {
			agent.ModelID = routes[0].ModelID
		}
		if err := agent.Validate(); err != nil {
			return nil, err
		}
		id := input.ID
		if id == "" {
			id = domain.NewID()
		}
		if err := validateNewProviderSelections(tx, input.ConfigurationMutation, id, &agent); err != nil {
			return nil, err
		}
		if err := validateRelationships(tx, domain.AgentKind, id, input.ExpectedRevision, &agent); err != nil {
			return nil, err
		}
		return tx.Put(domain.AgentKind, id, input.ExpectedRevision, "", "", agent)
	})
}

func resolveWorkerModel(tx *store.Tx, agent domain.Agent, input agentWorkerModelSelection) (domain.ID, string, error) {
	var source domain.Account
	for index, link := range agent.Accounts {
		record, err := tx.Get(domain.AccountKind, link.ID)
		if err != nil {
			return "", "", err
		}
		account, err := store.Decode[domain.Account](record)
		if err != nil {
			return "", "", err
		}
		if err := account.Validate(); err != nil {
			return "", "", err
		}
		if index == 0 {
			source = account
		}
		if account.Type != source.Type || account.ProviderID != source.ProviderID || account.SubscriptionService != source.SubscriptionService {
			return "", "", domain.Fail(domain.InvalidArgument, "Selected accounts use different sources.", "Choose accounts from one API provider or subscription service.")
		}
	}
	if source.Type == domain.SubscriptionAccount && source.SubscriptionService.Harness() != agent.Harness {
		return "", "", domain.Fail(domain.Unsupported, "The subscription service does not match this harness.", "Select its native harness or choose an API source.")
	}
	var record store.Record
	var found bool
	var err error
	if input.ModelID != "" {
		record, err = tx.Get(domain.ModelKind, input.ModelID)
		if err != nil {
			return "", "", err
		}
		if record.Revision != input.ModelRevision {
			return "", "", domain.Fail(domain.Conflict, "The selected model changed.", "Refresh and explicitly select the current model before saving.")
		}
		found = true
	} else {
		record, found, err = tx.ModelBySourceNative(source.ProviderID, source.SubscriptionService, input.NativeID)
		if err != nil {
			return "", "", err
		}
	}
	model := domain.Model{ProviderID: source.ProviderID, SubscriptionService: source.SubscriptionService, NativeID: input.NativeID, Name: input.NativeID, Harnesses: []domain.Harness{agent.Harness}, Manual: true, MetadataSource: domain.Unknown}
	modelID := domain.NewID()
	revision := uint64(0)
	if source.Type == domain.SubscriptionAccount {
		model.SourceKind = domain.SubscriptionModel
	}
	changed := !found
	if found {
		model, err = store.Decode[domain.Model](record)
		if err != nil {
			return "", "", err
		}
		if !model.MatchesAccount(source, agent.Harness) {
			return "", "", domain.Fail(domain.InvalidArgument, "The selected model uses another account source.", "Select a model from the accounts' source.")
		}
		modelID, revision = record.ID, record.Revision
		// This is an explicit configuration declaration, not native execution proof.
		if !slices.Contains(model.Harnesses, agent.Harness) {
			model.Harnesses = append(model.Harnesses, agent.Harness)
			changed = true
		}
	}
	if input.ModelID != "" && agent.ModelID != "" && agent.ModelID != modelID {
		return "", "", domain.Fail(domain.InvalidArgument, "Worker and model selection disagree.", "Submit the selected canonical model identity.")
	}
	if changed {
		if err := model.Validate(); err != nil {
			return "", "", err
		}
		modelInput := ConfigurationMutation{Kind: domain.ModelKind, ExpectedRevision: revision}
		if err := validateNewProviderSelections(tx, modelInput, modelID, &model); err != nil {
			return "", "", err
		}
		if err := validateRelationships(tx, domain.ModelKind, modelID, revision, &model); err != nil {
			return "", "", err
		}
		if _, err := tx.Put(domain.ModelKind, modelID, revision, "", "", model); err != nil {
			return "", "", err
		}
	}

	return modelID, workerSourceKey(source), nil
}
func workerSourceKey(account domain.Account) string {
	if account.Type == domain.SubscriptionAccount {
		return "subscription:" + string(account.SubscriptionService)
	}
	return "api:" + string(account.ProviderID)
}

func (s *Service) SaveAgentWorker(ctx context.Context, req *connect.Request[pb.SaveAgentWorkerRequest]) (*connect.Response[pb.SaveConfigurationResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if req.Msg.Mutation == nil || (req.Msg.Model == nil) == (len(req.Msg.RouteModels) == 0) || req.Msg.SchemaVersion != 1 && req.Msg.SchemaVersion != 3 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "A supported Worker document, mutation and typed model selection are required.", "Use the current Worker revision and one model selection."), correlation)
	}
	if rpc.ResourceSchemaVersion(domain.AgentKind, req.Msg.DocumentJson) != req.Msg.SchemaVersion {
		return nil, rpc.Error(domain.Fail(domain.Unsupported, "Worker schema does not match its account routes.", "Use schema 3 for ordered source routes."), correlation)
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
