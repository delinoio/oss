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
type agentWorkerMutation struct {
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
	if len(agent.Accounts) == 0 {
		return store.Result{}, domain.Fail(domain.MissingInput, "Select at least one account.", "Choose one or more accounts from the same source.")
	}
	if (input.ModelID == "") == (input.NativeID == "") || input.ModelID == "" && input.ModelRevision != 0 || input.ModelID != "" && input.ModelRevision == 0 {
		return store.Result{}, domain.Fail(domain.InvalidArgument, "Select one model identity.", "Use a canonical model and its revision, or an exact native model ID.")
	}
	if input.ID == "" && input.ExpectedRevision != 0 {
		return store.Result{}, domain.Fail(domain.InvalidArgument, "A new Worker has no revision.", "Use revision zero for creation.")
	}
	if input.ModelID != "" {
		if err := input.ModelID.Validate(); err != nil {
			return store.Result{}, err
		}
	}
	if input.NativeID != "" {
		if err := domain.Text(input.NativeID, "native model ID", 256, true); err != nil {
			return store.Result{}, err
		}
	}
	// Validate every ordinary Agent field before opening the transaction. The
	// dedicated typed selection resolves the canonical model ID at commit time.
	proposed := agent
	proposed.ModelID = domain.NewID()
	if err := proposed.Validate(); err != nil {
		return store.Result{}, err
	}
	return state.Mutate(ctx, input.RequestID, "configuration.agent-worker.save", input, func(tx *store.Tx) (any, error) {
		var source domain.Account
		for index, link := range agent.Accounts {
			record, err := tx.Get(domain.AccountKind, link.ID)
			if err != nil {
				return nil, err
			}
			account, err := store.Decode[domain.Account](record)
			if err != nil {
				return nil, err
			}
			if err := account.Validate(); err != nil {
				return nil, err
			}
			if index == 0 {
				source = account
			}
			if account.Type != source.Type || account.ProviderID != source.ProviderID || account.SubscriptionService != source.SubscriptionService {
				return nil, domain.Fail(domain.InvalidArgument, "Selected accounts use different sources.", "Choose accounts from one API provider or subscription service.")
			}
		}
		if source.Type == domain.SubscriptionAccount && source.SubscriptionService.Harness() != agent.Harness {
			return nil, domain.Fail(domain.Unsupported, "The subscription service does not match this harness.", "Select its native harness or choose an API source.")
		}
		var record store.Record
		var found bool
		var err error
		if input.ModelID != "" {
			record, err = tx.Get(domain.ModelKind, input.ModelID)
			if err != nil {
				return nil, err
			}
			if record.Revision != input.ModelRevision {
				return nil, domain.Fail(domain.Conflict, "The selected model changed.", "Refresh and explicitly select the current model before saving.")
			}
			found = true
		} else {
			record, found, err = tx.ModelBySourceNative(source.ProviderID, source.SubscriptionService, input.NativeID)
			if err != nil {
				return nil, err
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
				return nil, err
			}
			if !model.MatchesAccount(source, agent.Harness) {
				return nil, domain.Fail(domain.InvalidArgument, "The selected model uses another account source.", "Select a model from the accounts' source.")
			}
			modelID, revision = record.ID, record.Revision
			// This is an explicit configuration declaration, not native execution proof.
			if !slices.Contains(model.Harnesses, agent.Harness) {
				model.Harnesses = append(model.Harnesses, agent.Harness)
				changed = true
			}
		}
		if input.ModelID != "" && agent.ModelID != "" && agent.ModelID != modelID {
			return nil, domain.Fail(domain.InvalidArgument, "Worker and model selection disagree.", "Submit the selected canonical model identity.")
		}
		if changed {
			if err := model.Validate(); err != nil {
				return nil, err
			}
			modelInput := ConfigurationMutation{Kind: domain.ModelKind, ExpectedRevision: revision}
			if err := validateNewProviderSelections(tx, modelInput, modelID, &model); err != nil {
				return nil, err
			}
			if err := validateRelationships(tx, domain.ModelKind, modelID, revision, &model); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.ModelKind, modelID, revision, "", "", model); err != nil {
				return nil, err
			}
		}
		agent.ModelID = modelID
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

func (s *Service) SaveAgentWorker(ctx context.Context, req *connect.Request[pb.SaveAgentWorkerRequest]) (*connect.Response[pb.SaveConfigurationResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if req.Msg.Mutation == nil || req.Msg.Model == nil || req.Msg.SchemaVersion != 1 && req.Msg.SchemaVersion != 2 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "A supported Worker document, mutation and typed model selection are required.", "Use the current Worker revision and one model selection."), correlation)
	}
	input := agentWorkerMutation{ConfigurationMutation: ConfigurationMutation{RequestID: domain.ID(req.Msg.Mutation.RequestId), ID: domain.ID(req.Msg.Mutation.Id), ExpectedRevision: req.Msg.Mutation.ExpectedRevision, Kind: domain.AgentKind, Document: req.Msg.DocumentJson}, ModelRevision: req.Msg.Model.ExpectedModelRevision}
	switch selection := req.Msg.Model.Selection.(type) {
	case *pb.AgentWorkerModelSelection_ModelId:
		input.ModelID = domain.ID(selection.ModelId)
	case *pb.AgentWorkerModelSelection_NativeId:
		input.NativeID = selection.NativeId
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
