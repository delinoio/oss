package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type catalogReceipt struct {
	ID          domain.ID                 `json:"id"`
	Observation domain.CatalogObservation `json:"observation"`
}

func (s *Service) ListProviderPresets(ctx context.Context, req *connect.Request[pb.ListProviderPresetsRequest]) (*connect.Response[pb.ListProviderPresetsResponse], error) {
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return tx.Authorize() }); err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	raw, _ := json.Marshal(providers.Presets())
	response := connect.NewResponse(&pb.ListProviderPresetsResponse{PresetsJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) DiscoverModels(ctx context.Context, req *connect.Request[pb.DiscoverModelsRequest]) (*connect.Response[pb.DiscoverModelsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	meta := req.Msg.Mutation
	result, err := s.discoverModels(ctx, meta, correlation)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt catalogReceipt
	if domain.Decode(result.Data, &receipt) != nil || receipt.ID != domain.ID(meta.Id) {
		return nil, rpc.Error(domain.Fail(domain.NotFound, "The discovery source or a published model was subsequently deleted.", "An old request cannot recreate deleted catalog state."), correlation)
	}
	account, err := s.accountRecord(ctx, domain.ID(meta.Id))
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	raw, _ := json.Marshal(receipt.Observation)
	response := connect.NewResponse(&pb.DiscoverModelsResponse{Account: rpc.Resource(account), RequestId: meta.RequestId, Replayed: result.Replayed, ObservationJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) discoverModels(ctx context.Context, meta *pb.Mutation, correlation string) (store.Result, error) {
	result, err := s.inspectAccount(ctx, meta, catalogInspection, correlation, func(tx *store.Tx, account domain.Account, result accountInspection) (any, error) {
		observation := domain.CatalogObservation{RequestID: domain.ID(meta.RequestId), ConnectionID: account.Connection.ID, ObservedAt: result.ObservedAt, State: domain.Observed, Received: uint32(len(result.Models)), RetryAfterSeconds: result.RetryAfterSeconds, Problem: result.Problem}
		if account.Catalog != nil {
			observation.LastSuccessAt = account.Catalog.LastSuccessAt
		}
		if observation.Problem == nil {
			plan, err := catalogPlan(tx, account.ProviderID, result)
			if err != nil {
				if domain.SafeError(err).Code != domain.ResourceExhausted {
					return nil, err
				}
				observation.Problem = domain.SafeError(err)
			} else {
				for _, change := range plan {
					if _, err := tx.Put(domain.ModelKind, change.record.ID, change.record.Revision, "", "", change.model); err != nil {
						return nil, err
					}
					if change.record.Revision == 0 {
						observation.Added++
					} else {
						observation.Updated++
					}
				}
				observed := result.ObservedAt
				observation.LastSuccessAt = &observed
			}
		}
		if observation.Problem != nil {
			observation.State = domain.ObservationFailed
			if observation.Problem.Code == domain.Unsupported {
				observation.State = domain.ObservationUnsupported
			}
			copy := *observation.Problem
			copy.CorrelationID = correlation
			observation.Problem = &copy
		}
		account.Catalog = &observation
		if _, err := tx.Put(domain.AccountKind, domain.ID(meta.Id), meta.ExpectedRevision, "", "", account); err != nil {
			return nil, err
		}
		return catalogReceipt{ID: domain.ID(meta.Id), Observation: observation}, nil
	})
	if err == nil && !result.Replayed {
		var receipt catalogReceipt
		if domain.Decode(result.Data, &receipt) == nil {
			o := receipt.Observation
			var problemCode domain.Code
			if o.Problem != nil {
				problemCode = o.Problem.Code
			}
			s.logger.Info("catalog_published", "account_id", receipt.ID, "request_id", o.RequestID, "state", o.State, "received", o.Received, "added", o.Added, "updated", o.Updated, "error_code", problemCode, "correlation_id", correlation)
		}
	}
	return result, err
}

type modelChange struct {
	record store.Record
	model  domain.Model
}

func modelAdvisoryBytes(model domain.Model) string {
	// Compare only evidence-bearing fields. Display preferences and explicitly
	// configured harness compatibility are not observations from the provider.
	raw, _ := json.Marshal(struct {
		ContextLimit     *uint64
		Input, Output    []string
		Tools, Reasoning *bool
	}{model.ContextLimit, model.InputModalities, model.OutputModalities, model.Tools, model.Reasoning})
	return string(raw)
}

func catalogPlan(tx *store.Tx, provider domain.ID, result accountInspection) ([]modelChange, error) {
	existing, err := tx.ModelsForProvider(provider)
	if err != nil {
		return nil, err
	}
	if err = store.CatalogBound(len(existing)); err != nil {
		return nil, err
	}
	byNative := map[string]store.Record{}
	for _, record := range existing {
		model, err := store.Decode[domain.Model](record)
		if err != nil {
			return nil, err
		}
		byNative[model.NativeID] = record
	}
	plan := []modelChange{}
	added := 0
	for _, item := range result.Models {
		record, exists := byNative[item.ID]
		var model domain.Model
		if exists {
			model, err = store.Decode[domain.Model](record)
			if err != nil {
				return nil, err
			}
			// Manual registrations and user-declared advisory data retain their
			// original values even when this provider now reports the same ID.
			if model.Manual || model.Discovery == nil || model.MetadataSource == domain.UserDeclared {
				continue
			}
		} else {
			suppressed, err := tx.ModelSuppressed(provider, item.ID)
			if err != nil {
				return nil, err
			}
			if suppressed {
				continue
			}
			record.ID = domain.NewID()
			model = domain.Model{ProviderID: provider, NativeID: item.ID, Name: item.Name, New: true, Harnesses: []domain.Harness{}, Discovery: &domain.ModelDiscovery{FirstSeenAt: result.ObservedAt, UpdatedAt: result.ObservedAt}}
			added++
		}
		before, _ := json.Marshal(model)
		model.ContextLimit = item.ContextLimit
		model.InputModalities = item.InputModalities
		model.OutputModalities = item.OutputModalities
		model.Tools = item.Tools
		model.Reasoning = item.Reasoning
		model.MetadataSource = domain.Unknown
		if item.ContextLimit != nil || len(item.InputModalities) > 0 || len(item.OutputModalities) > 0 || item.Tools != nil || item.Reasoning != nil {
			model.MetadataSource = domain.Known
		}
		after, _ := json.Marshal(model)
		if exists && bytes.Equal(before, after) {
			continue
		}
		model.Discovery.UpdatedAt = result.ObservedAt
		if err := model.Validate(); err != nil {
			return nil, err
		}
		plan = append(plan, modelChange{record, model})
	}
	if err := store.CatalogBound(len(existing) + added); err != nil {
		return nil, err
	}
	return plan, nil
}
func (s *Service) SearchModels(ctx context.Context, req *connect.Request[pb.SearchModelsRequest]) (*connect.Response[pb.SearchModelsResponse], error) {
	f := store.ModelSearch{Query: req.Msg.Query, ProviderID: domain.ID(req.Msg.ProviderId), IncludeHidden: req.Msg.IncludeHidden, Limit: int(req.Msg.PageSize)}
	if f.Limit == 0 {
		f.Limit = 50
	}
	raw, _ := json.Marshal(f)
	hash := sha256.Sum256(raw)
	scope := "models:" + hex.EncodeToString(hash[:])
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
		}
		f.After = cursor.After
		f.Epoch = cursor.Sequence
	}
	models, providers, epoch, err := s.Store.SearchModels(ctx, f)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	message := &pb.SearchModelsResponse{}
	for _, record := range models {
		message.Models = append(message.Models, rpc.Resource(record))
	}
	for _, record := range providers {
		message.Providers = append(message.Providers, rpc.Resource(record))
	}
	if len(models) == f.Limit {
		message.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: models[len(models)-1].ID, Sequence: epoch})
		if err != nil {
			return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
		}
	}
	response := connect.NewResponse(message)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ResolveModel(ctx context.Context, req *connect.Request[pb.ResolveModelRequest]) (*connect.Response[pb.ResolveModelResponse], error) {
	model, err := s.Store.ResolveModel(ctx, req.Msg.Selector, domain.ID(req.Msg.ProviderId))
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.ResolveModelResponse{Model: rpc.Resource(model)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
