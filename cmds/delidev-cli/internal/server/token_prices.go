// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"reflect"
	"sort"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/tokenprices"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func modelIdentity(p *pb.ModelIdentity) (domain.ModelIdentity, error) {
	if p == nil {
		return domain.ModelIdentity{}, domain.Fail(domain.MissingInput, "An exact source and native model ID are required.", "Select the original provider or subscription service.")
	}
	m := domain.ModelIdentity{ProviderID: domain.ID(p.ProviderId), SubscriptionService: rpc.SubscriptionService(p.SubscriptionService), NativeID: p.NativeId}
	return m, m.Validate()
}
func wireModel(m domain.ModelIdentity) *pb.ModelIdentity {
	return &pb.ModelIdentity{ProviderId: string(m.ProviderID), SubscriptionService: rpc.WireSubscriptionService(m.SubscriptionService), NativeId: m.NativeID}
}
func (s *Service) tokenPrices() *tokenprices.Manager {
	s.tokenPricesOnce.Do(func() {
		base := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: tokenprices.RequestTimeout}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: tokenprices.RequestTimeout, ResponseHeaderTimeout: tokenprices.RequestTimeout, MaxResponseHeaderBytes: 32 << 10, DisableCompression: true, DisableKeepAlives: true}
		s.prices = tokenprices.New(s.Store.Root(), &outbound.Transport{Base: base, Resolve: s.outboundResolver()}, s.logger, s.publishTokenPrices)
	})
	return s.prices
}

// Only an unchanged managed endpoint maps to an upstream provider namespace.
// User labels and canonical model aliases never establish a pricing identity.
func priceNamespace(tx *store.Tx, m domain.ModelIdentity) (string, uint64, error) {
	if m.SubscriptionService != "" {
		switch m.SubscriptionService {
		case domain.SubscriptionChatGPT:
			return "openai", 0, nil
		case domain.SubscriptionClaude:
			return "anthropic", 0, nil
		case domain.SubscriptionGrok:
			return "xai", 0, nil
		}
		return "", 0, nil
	}
	row, e := tx.Get(domain.ProviderKind, m.ProviderID)
	if domain.SafeError(e).Code == domain.NotFound {
		row, e = tx.RetiredConfiguration(domain.ProviderKind, m.ProviderID)
	}
	if e != nil {
		return "", 0, e
	}
	var p domain.Provider
	if domain.Decode(row.Data, &p) != nil {
		return "", 0, invalidUsageSummary()
	}
	if p.PresetID == nil {
		return "", row.Revision, nil
	}
	for _, preset := range providers.Presets() {
		if preset.ID == *p.PresetID && preset.Provider.LegacyAPIFormat() == p.LegacyAPIFormat() && reflect.DeepEqual(preset.Provider.APIFormats, p.APIFormats) {
			namespace := string(preset.ID)
			switch namespace {
			case "gemini":
				namespace = "google"
			case "vercel-ai-gateway":
				namespace = "vercel"
			}
			return namespace, row.Revision, nil
		}
	}
	return "", row.Revision, nil
}
func priceReference(tx *store.Tx, m domain.ModelIdentity, snapshot tokenprices.Snapshot) (*tokenprices.Reference, uint64, error) {
	namespace, rev, e := priceNamespace(tx, m)
	if e != nil {
		return nil, rev, e
	}
	if namespace == "" {
		return nil, rev, nil
	}
	ref, ok := snapshot.Catalog.References[namespace+"\x00"+m.NativeID]
	if !ok {
		return nil, rev, nil
	}
	return &ref, rev, nil
}

// Refresh publication and the first observation share this transaction check.
// No network or credential inspection takes place while retaining a price.
func (s *Service) applyReference(tx *store.Tx, m domain.ModelIdentity, snapshot tokenprices.Snapshot) error {
	policy, e := tx.PricingPolicy(m)
	if e != nil {
		return e
	}
	if policy.Mode == domain.ManualPricing {
		return nil
	}
	if snapshot.Checked.IsZero() {
		return nil
	}
	ref, _, e := priceReference(tx, m, snapshot)
	if e != nil {
		return e
	}
	if ref == nil || ref.Unsupported || ref.Basis == nil {
		return tx.ClearActivePricing(m)
	}
	current, e := tx.RetainedActivePricing(m.Key())
	if e != nil {
		return e
	}
	expected := uint64(0)
	if current != nil {
		expected = current.Revision
		old, newBasis := current.Basis, *ref.Basis
		old.AsOf = ""
		newBasis.AsOf = ""
		if current.Provenance != nil && current.Provenance.ProviderKey == ref.Provider && current.Provenance.ModelKey == ref.Model && reflect.DeepEqual(old, newBasis) {
			return nil
		}
	}
	_, e = tx.PutAutomaticPricing(m.Key(), expected, domain.NewID(), *ref.Basis, domain.PriceProvenance{ProviderKey: ref.Provider, ModelKey: ref.Model, SnapshotSHA256: snapshot.Catalog.Digest, RetrievedAt: snapshot.Checked})
	return e
}
func (s *Service) publishTokenPrices(ctx context.Context, snapshot tokenprices.Snapshot) error {
	// Background refresh owns server maintenance. Explicit refresh retains its
	// original actor; coalescing never upgrades that actor's transaction authority.
	if _, ok := domain.PrincipalFrom(ctx); !ok {
		ctx = domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	}
	_, e := s.Store.Mutate(ctx, domain.NewID(), "usage.refresh-token-prices", struct {
		Digest  string
		Checked time.Time
	}{snapshot.Catalog.Digest, snapshot.Checked}, func(tx *store.Tx) (any, error) {
		if e := tx.Authorize(); e != nil {
			return nil, e
		}
		models, e := tx.PricingIdentities()
		if e != nil {
			return nil, e
		}
		for _, m := range models {
			if e = s.applyReference(tx, m, snapshot); e != nil {
				return nil, e
			}
		}
		return struct{ Count int }{len(models)}, nil
	})
	return e
}
func (s *Service) readTokenPrice(tx *store.Tx, m domain.ModelIdentity) (*pb.GetTokenPricingResponse, error) {
	policy, e := tx.PricingPolicy(m)
	if e != nil {
		return nil, e
	}
	price, e := tx.RetainedActivePricing(m.Key())
	if e != nil {
		return nil, e
	}
	snapshot := s.tokenPrices().Snapshot()
	ref, revision, e := priceReference(tx, m, snapshot)
	if e != nil {
		return nil, e
	}
	mode := pb.TokenPricingMode_TOKEN_PRICING_MODE_AUTOMATIC
	if policy.Mode == domain.ManualPricing {
		mode = pb.TokenPricingMode_TOKEN_PRICING_MODE_MANUAL
	}
	reference := &pb.TokenPriceReference{State: string(snapshot.State), FailureCode: snapshot.Failure, ApiEquivalent: m.SubscriptionService != ""}
	if !snapshot.Checked.IsZero() {
		reference.CheckedAtUnixMs = snapshot.Checked.UnixMilli()
	}
	if ref != nil {
		reference.UnsupportedEstimate = ref.Unsupported
		reference.CostsJson = append([]byte(nil), ref.Costs...)
		reference.UpstreamUpdated = ref.Updated
		if ref.Basis != nil {
			reference.Basis = wireTokenPricing(*ref.Basis)
		}
	}
	return &pb.GetTokenPricingResponse{Pricing: pricingVersion(price), Policy: &pb.TokenPricingPolicy{Mode: mode, Revision: policy.Revision}, Reference: reference, ProviderRevision: revision}, nil
}
func (s *Service) GetTokenPricing(ctx context.Context, req *connect.Request[pb.GetTokenPricingRequest]) (*connect.Response[pb.GetTokenPricingResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, e := pricingActor(ctx); e != nil {
		return nil, rpc.Error(e, correlation)
	}
	m, e := modelIdentity(req.Msg.Model)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	var value *pb.GetTokenPricingResponse
	e = s.Store.Read(ctx, func(tx *store.Tx) error {
		if e := tx.Authorize(); e != nil {
			return e
		}
		var e error
		value, e = s.readTokenPrice(tx, m)
		return e
	})
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	response := connect.NewResponse(value)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

type tokenPriceMutation struct {
	Actor            domain.Principal
	Model            domain.ModelIdentity
	Mode             domain.PricingMode
	PolicyRevision   uint64
	ProviderRevision uint64
	PriceRevision    uint64
	Basis            *domain.TokenPricing
}

func (s *Service) mutateTokenPrice(ctx context.Context, id domain.ID, identity tokenPriceMutation) (store.Result, error) {
	return s.Store.Mutate(ctx, id, "usage.set-token-pricing", identity, func(tx *store.Tx) (any, error) {
		if e := tx.Authorize(); e != nil {
			return nil, e
		}
		_, revision, e := priceNamespace(tx, identity.Model)
		if e != nil {
			return nil, e
		}
		if revision != identity.ProviderRevision {
			return nil, domain.Fail(domain.Conflict, "The original provider configuration changed.", "Reload this source without discarding the staged price.")
		}
		_, e = tx.SetPricingPolicy(identity.Model, identity.Mode, identity.PolicyRevision)
		if e != nil {
			return nil, e
		}
		if identity.Basis != nil {
			_, e = tx.PutPricing(identity.Model.Key(), identity.PriceRevision, domain.NewID(), *identity.Basis)
		} else {
			e = s.applyReference(tx, identity.Model, s.tokenPrices().Snapshot())
		}
		if e != nil {
			return nil, e
		}
		// Retain the accepted policy and price together. A replay returns this exact
		// receipt, even if later refreshes select a different immutable version.
		return s.readTokenPrice(tx, identity.Model)
	})
}
func (s *Service) SetTokenPricing(ctx context.Context, req *connect.Request[pb.SetTokenPricingRequest]) (*connect.Response[pb.SetTokenPricingResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, e := pricingActor(ctx)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	m, e := modelIdentity(req.Msg.Model)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	basis, e := tokenPricing(req.Msg.Basis)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	result, e := s.mutateTokenPrice(ctx, domain.ID(req.Msg.RequestId), tokenPriceMutation{Actor: actor, Model: m, Mode: domain.ManualPricing, PolicyRevision: req.Msg.ExpectedPolicyRevision, ProviderRevision: req.Msg.ExpectedProviderRevision, PriceRevision: req.Msg.ExpectedRevision, Basis: &basis})
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	var original pb.GetTokenPricingResponse
	if json.Unmarshal(result.Data, &original) != nil {
		return nil, rpc.Error(invalidUsageSummary(), correlation)
	}
	response := connect.NewResponse(&pb.SetTokenPricingResponse{Pricing: original.Pricing, Policy: original.Policy, RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) SetTokenPricingMode(ctx context.Context, req *connect.Request[pb.SetTokenPricingModeRequest]) (*connect.Response[pb.SetTokenPricingModeResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, e := pricingActor(ctx)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	m, e := modelIdentity(req.Msg.Model)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	mode := domain.PricingMode("")
	switch req.Msg.Mode {
	case pb.TokenPricingMode_TOKEN_PRICING_MODE_AUTOMATIC:
		mode = domain.AutomaticPricing
	case pb.TokenPricingMode_TOKEN_PRICING_MODE_MANUAL:
		mode = domain.ManualPricing
	}
	if mode != domain.AutomaticPricing && mode != domain.ManualPricing {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Choose Automatic or Manual pricing.", "Read the current policy before changing its mode."), correlation)
	}
	result, e := s.mutateTokenPrice(ctx, domain.ID(req.Msg.RequestId), tokenPriceMutation{Actor: actor, Model: m, Mode: mode, PolicyRevision: req.Msg.ExpectedPolicyRevision, ProviderRevision: req.Msg.ExpectedProviderRevision})
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	var original pb.GetTokenPricingResponse
	if json.Unmarshal(result.Data, &original) != nil {
		return nil, rpc.Error(invalidUsageSummary(), correlation)
	}
	response := connect.NewResponse(&pb.SetTokenPricingModeResponse{Current: &original, RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) RefreshTokenPrices(ctx context.Context, req *connect.Request[pb.RefreshTokenPricesRequest]) (*connect.Response[pb.RefreshTokenPricesResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, e := pricingActor(ctx); e != nil {
		return nil, rpc.Error(e, correlation)
	}
	if e := s.Store.Read(ctx, func(tx *store.Tx) error { return tx.Authorize() }); e != nil {
		return nil, rpc.Error(e, correlation)
	}
	_ = s.tokenPrices().Refresh(ctx)
	if e := s.Store.Read(ctx, func(tx *store.Tx) error { return tx.Authorize() }); e != nil {
		return nil, rpc.Error(e, correlation)
	}
	snapshot := s.tokenPrices().Snapshot()
	value := &pb.RefreshTokenPricesResponse{State: string(snapshot.State), FailureCode: snapshot.Failure}
	if !snapshot.Checked.IsZero() {
		value.CheckedAtUnixMs = snapshot.Checked.UnixMilli()
	}
	response := connect.NewResponse(value)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func wireModelKey(key domain.ID) *pb.ModelIdentity {
	m, e := domain.ParseModelKey(key)
	if e != nil {
		return nil
	}
	return wireModel(m)
}

// ListTokenPricing lists retained active versions only. It creates no price,
// model or policy, and its cursor pins the exact filtered immutable version set.
func (s *Service) ListTokenPricing(ctx context.Context, req *connect.Request[pb.ListTokenPricingRequest]) (*connect.Response[pb.ListTokenPricingResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, e := pricingActor(ctx); e != nil {
		return nil, rpc.Error(e, correlation)
	}
	provider := domain.ID(req.Msg.ProviderId)
	service := rpc.SubscriptionService(req.Msg.SubscriptionService)
	if provider != "" && provider.Validate() != nil || service != "" && !service.Valid() || provider != "" && service != "" {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Select one pricing source.", "Choose an API provider or subscription service."), correlation)
	}
	size := int(req.Msg.PageSize)
	if size == 0 {
		size = 20
	}
	if size < 1 || size > 99 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Invalid price page size.", "Use 1 through 99 records."), correlation)
	}
	result := &pb.ListTokenPricingResponse{}
	e := s.Store.Read(ctx, func(tx *store.Tx) error {
		if e := tx.Authorize(); e != nil {
			return e
		}
		models, e := tx.PricingIdentities()
		if e != nil {
			return e
		}
		sort.Slice(models, func(i, j int) bool { return models[i].Key() < models[j].Key() })
		values := []*pb.PricingVersion{}
		for _, m := range models {
			if provider != "" && m.ProviderID != provider || service != "" && m.SubscriptionService != service {
				continue
			}
			price, e := tx.RetainedActivePricing(m.Key())
			if e != nil {
				return e
			}
			if price != nil {
				values = append(values, pricingVersion(price))
			}
		}
		ids := make([]string, len(values))
		for i, v := range values {
			ids[i] = v.Id
		}
		raw, _ := json.Marshal(struct {
			Provider domain.ID
			Service  domain.SubscriptionService
			IDs      []string
		}{provider, service, ids})
		digest := sha256.Sum256(raw)
		index := 0
		if req.Msg.PageToken != "" {
			cursor, e := base64.RawURLEncoding.DecodeString(req.Msg.PageToken)
			var c struct {
				Digest string
				Index  int
			}
			if e != nil || len(cursor) > 256 || domain.Decode(cursor, &c) != nil || c.Digest != hex.EncodeToString(digest[:]) || c.Index < 0 || c.Index >= len(values) {
				return domain.Fail(domain.Conflict, "The current price listing changed.", "Restart the source price listing.")
			}
			index = c.Index
		}
		end := min(index+size, len(values))
		result.Pricing = values[index:end]
		if end < len(values) {
			cursor, _ := json.Marshal(struct {
				Digest string
				Index  int
			}{hex.EncodeToString(digest[:]), end})
			result.NextPageToken = base64.RawURLEncoding.EncodeToString(cursor)
		}
		return nil
	})
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
