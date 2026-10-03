package server

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func pricingActor(ctx context.Context) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return actor, domain.Fail(domain.PermissionDenied, "Only an owner or paired client can access token pricing.", "Use an authorized product client.")
	}
	return actor, nil
}
func tokenPricing(value *pb.TokenPricing) (domain.TokenPricing, error) {
	if value == nil {
		return domain.TokenPricing{}, domain.Fail(domain.MissingInput, "An explicit pricing basis is required.", "Provide the source, date, currency and applicable rates.")
	}
	mode := domain.InputPricingMode("")
	switch value.InputMode {
	case pb.InputPricingMode_INPUT_PRICING_MODE_UNIFORM:
		mode = domain.UniformInputPrice
	case pb.InputPricingMode_INPUT_PRICING_MODE_CACHED_DISCOUNT:
		mode = domain.CachedInputPrice
	}
	basis := domain.TokenPricing{Currency: domain.Currency(value.Currency), Source: value.Source, AsOf: value.AsOf, InputMode: mode, InputPerMillion: value.InputPerMillion, CachedInputPerMillion: value.CachedInputPerMillion, OutputPerMillion: value.OutputPerMillion, Exclusions: value.Exclusions}
	return basis, basis.Validate()
}
func pricingVersion(value *store.PricingVersion) *pb.PricingVersion {
	if value == nil {
		return nil
	}
	p := value.Basis
	mode := pb.InputPricingMode_INPUT_PRICING_MODE_UNIFORM
	if p.InputMode == domain.CachedInputPrice {
		mode = pb.InputPricingMode_INPUT_PRICING_MODE_CACHED_DISCOUNT
	}
	return &pb.PricingVersion{Id: string(value.ID), ModelId: string(value.ModelID), ProviderId: string(value.ProviderID), SubscriptionService: rpc.WireSubscriptionService(value.SubscriptionService), Revision: value.Revision, CreatedAtUnixMs: value.CreatedAt.UnixMilli(), Basis: &pb.TokenPricing{Currency: string(p.Currency), Source: p.Source, AsOf: p.AsOf, InputMode: mode, InputPerMillion: p.InputPerMillion, CachedInputPerMillion: p.CachedInputPerMillion, OutputPerMillion: p.OutputPerMillion, Exclusions: p.Exclusions}}
}
func (s *Service) readPricing(ctx context.Context, id domain.ID, active bool) (*pb.PricingVersion, uint64, error) {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var result *pb.PricingVersion
	var modelRevision uint64
	err := s.Store.Read(bounded, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		if active {
			model, err := tx.Get(domain.ModelKind, id)
			if err != nil {
				return err
			}
			modelRevision = model.Revision
			value, err := tx.ActivePricing(id)
			if err != nil {
				return err
			}
			result = pricingVersion(value)
			return nil
		}
		value, err := tx.Pricing(id)
		if err != nil {
			return err
		}
		result = pricingVersion(&value)
		return nil
	})
	return result, modelRevision, err
}
func (s *Service) GetModelPricing(ctx context.Context, req *connect.Request[pb.GetModelPricingRequest]) (*connect.Response[pb.GetModelPricingResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := pricingActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	value, revision, err := s.readPricing(ctx, domain.ID(req.Msg.ModelId), true)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.GetModelPricingResponse{Pricing: value, ModelRevision: revision})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) GetPricingVersion(ctx context.Context, req *connect.Request[pb.GetPricingVersionRequest]) (*connect.Response[pb.GetPricingVersionResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := pricingActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	value, _, err := s.readPricing(ctx, domain.ID(req.Msg.Id), false)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.GetPricingVersionResponse{Pricing: value})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

type pricingIdentity struct {
	ModelRevision uint64
	ModelID       domain.ID
	Revision      uint64
	Basis         domain.TokenPricing
	Actor         domain.Principal
}
type pricingReceipt struct {
	ModelID   domain.ID `json:"model_id"`
	PricingID domain.ID `json:"pricing_id"`
}

func (s *Service) SetModelPricing(ctx context.Context, req *connect.Request[pb.SetModelPricingRequest]) (*connect.Response[pb.SetModelPricingResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := pricingActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	if meta == nil || domain.ID(meta.Id).Validate() != nil || domain.ID(meta.RequestId).Validate() != nil || meta.ExpectedRevision >= 1<<63-1 || req.Msg.ExpectedModelRevision == 0 || req.Msg.ExpectedModelRevision >= 1<<63-1 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "A valid model and pricing revision are required.", "Read the selected model's price; use revision zero only when it has no basis."), correlation)
	}
	basis, err := tokenPricing(req.Msg.Basis)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	identity := pricingIdentity{req.Msg.ExpectedModelRevision, domain.ID(meta.Id), meta.ExpectedRevision, basis, actor}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := s.Store.Mutate(bounded, domain.ID(meta.RequestId), "usage.set-pricing", identity, func(tx *store.Tx) (any, error) {
		model, err := tx.Get(domain.ModelKind, identity.ModelID)
		if err != nil {
			return nil, err
		}
		if model.Revision != identity.ModelRevision {
			return nil, domain.Fail(domain.Conflict, "The model configuration changed.", "Read the model and retain your staged price before submitting a new revision.")
		}
		value, err := tx.PutPricing(identity.ModelID, identity.Revision, domain.NewID(), basis)
		if err != nil {
			return nil, err
		}
		return pricingReceipt{identity.ModelID, value.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt pricingReceipt
	if domain.Decode(result.Data, &receipt) != nil || receipt.ModelID != identity.ModelID || receipt.PricingID.Validate() != nil {
		return nil, rpc.Error(domain.Fail(domain.NotFound, "The original pricing receipt is no longer retained.", "Read current pricing; an old request cannot recreate deleted configuration."), correlation)
	}
	value, _, err := s.readPricing(bounded, receipt.PricingID, false)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if value.ModelId != string(identity.ModelID) || value.Revision != identity.Revision+1 {
		return nil, rpc.Error(invalidUsageSummary(), correlation)
	}
	s.logger.InfoContext(ctx, "model_pricing_recorded", "correlation_id", correlation, "model_id", identity.ModelID, "pricing_id", receipt.PricingID, "revision", value.Revision, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.SetModelPricingResponse{Pricing: value, RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
