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
func wireTokenPricing(p domain.TokenPricing) *pb.TokenPricing {
	mode := pb.InputPricingMode_INPUT_PRICING_MODE_UNIFORM
	if p.InputMode == domain.CachedInputPrice {
		mode = pb.InputPricingMode_INPUT_PRICING_MODE_CACHED_DISCOUNT
	}
	return &pb.TokenPricing{Currency: string(p.Currency), Source: p.Source, AsOf: p.AsOf, InputMode: mode, InputPerMillion: p.InputPerMillion, CachedInputPerMillion: p.CachedInputPerMillion, OutputPerMillion: p.OutputPerMillion, Exclusions: p.Exclusions}
}
func pricingVersion(value *store.PricingVersion) *pb.PricingVersion {
	if value == nil {
		return nil
	}
	model, e := domain.ParseModelKey(value.ModelID)
	if e != nil {
		return nil
	}
	p := &pb.PricingVersion{Id: string(value.ID), Model: wireModel(model), ProviderId: string(value.ProviderID), SubscriptionService: rpc.WireSubscriptionService(value.SubscriptionService), Revision: value.Revision, CreatedAtUnixMs: value.CreatedAt.UnixMilli(), Basis: wireTokenPricing(value.Basis)}
	if value.Provenance != nil {
		v := value.Provenance
		p.Provenance = &pb.TokenPriceProvenance{ProviderKey: v.ProviderKey, ModelKey: v.ModelKey, SnapshotSha256: v.SnapshotSHA256, RetrievedAtUnixMs: v.RetrievedAt.UnixMilli()}
	}
	return p
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
	return nil, rpc.Error(domain.Fail(domain.Unsupported, "Model UUID pricing is unsupported by protocol 2.", "Select an exact source and native model ID."), req.Header().Get(rpc.CorrelationHeader))
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

func (s *Service) SetModelPricing(ctx context.Context, req *connect.Request[pb.SetModelPricingRequest]) (*connect.Response[pb.SetModelPricingResponse], error) {
	return nil, rpc.Error(domain.Fail(domain.Unsupported, "Model UUID pricing is unsupported by protocol 2.", "Select an exact source and native model ID."), req.Header().Get(rpc.CorrelationHeader))
}
