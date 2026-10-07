package server

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

func publicPrice(currency string) *pb.TokenPricing {
	input, output := "2.5", "10"
	return &pb.TokenPricing{Currency: currency, Source: "Explicit fixture rate card", AsOf: "2026-09-25", InputMode: pb.InputPricingMode_INPUT_PRICING_MODE_UNIFORM, InputPerMillion: &input, OutputPerMillion: &output, Exclusions: []string{"Provider fees are excluded."}}
}
func TestPricingRPCHistoricalSummaryReplayAndAuthority(t *testing.T) {
	f := newPublicationFixture(t)
	ctx := context.Background()
	c := delidevv1connect.NewUsageServiceClient(f.http.Client(), f.http.URL)
	model := string(f.input.Configuration.ModelID)
	current, err := c.GetModelPricing(ctx, ownerRequest(f.service.Identity, &pb.GetModelPricingRequest{ModelId: model}))
	if err != nil || current.Msg.Pricing != nil {
		t.Fatal("absent price", err)
	}
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	counts := reportedCounts(18)
	publish := func(sequence uint64, digest string) {
		e := f.event(domain.ExecutionResponseUsageObserved, sequence)
		e.ObservationID = domain.NewID()
		e.ResponseUsage = &domain.NativeResponseUsage{ResponseDigest: strings.Repeat(digest, 64), Counts: &counts, CostEvidence: domain.UsageCostUnspecified}
		f.publish(t, e)
	}
	publish(3, "a")
	request := &pb.SetModelPricingRequest{ExpectedModelRevision: 1, Mutation: &pb.Mutation{Id: model, RequestId: string(domain.NewID())}, Basis: publicPrice("USD")}
	first, err := c.SetModelPricing(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || first.Msg.Replayed || first.Msg.Pricing.Revision != 1 {
		t.Fatal("price create", err)
	}
	publish(4, "b")
	next := proto.Clone(request).(*pb.SetModelPricingRequest)
	next.Mutation.RequestId = string(domain.NewID())
	next.Mutation.ExpectedRevision = 1
	next.Basis.Currency = "EUR"
	next.Basis.InputPerMillion = nil
	second, err := c.SetModelPricing(ctx, ownerRequest(f.service.Identity, next))
	if err != nil || second.Msg.Pricing.Revision != 2 {
		t.Fatal("price replace", err)
	}
	publish(5, "c")
	publish(6, "b")
	replay, err := c.SetModelPricing(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || !replay.Msg.Replayed || !proto.Equal(replay.Msg.Pricing, first.Msg.Pricing) {
		t.Fatal("retry changed accepted price", err)
	}
	current, err = c.GetModelPricing(ctx, ownerRequest(f.service.Identity, &pb.GetModelPricingRequest{ModelId: model}))
	if err != nil || !proto.Equal(current.Msg.Pricing, second.Msg.Pricing) {
		t.Fatal("old retry changed active price", err)
	}
	historical, err := c.GetPricingVersion(ctx, ownerRequest(f.service.Identity, &pb.GetPricingVersionRequest{Id: first.Msg.Pricing.Id}))
	if err != nil || !proto.Equal(historical.Msg.Pricing, first.Msg.Pricing) {
		t.Fatal("historical read", err)
	}
	stale := proto.Clone(request).(*pb.SetModelPricingRequest)
	stale.Mutation.RequestId = string(domain.NewID())
	if _, err = c.SetModelPricing(ctx, ownerRequest(f.service.Identity, stale)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale accepted", err)
	}
	changed := proto.Clone(request).(*pb.SetModelPricingRequest)
	changed.Basis.Source = "Different source"
	if _, err = c.SetModelPricing(ctx, ownerRequest(f.service.Identity, changed)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("request identity changed", err)
	}
	summary, err := c.GetUsageSummary(ctx, ownerRequest(f.service.Identity, &pb.GetUsageSummaryRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	value := summary.Msg
	if value.ActualCost != pb.UsageCostState_USAGE_COST_STATE_UNAVAILABLE || value.EstimatedCost != pb.UsageCostState_USAGE_COST_STATE_KNOWN_SUBTOTAL || value.Totals.Responses != 3 || value.Estimates.UnpricedResponses != 1 || len(value.Estimates.Currencies) != 2 || len(value.Pricing) != 2 {
		t.Fatalf("wrong estimate: %v", value)
	}
	if value.Estimates.Currencies[0].Currency != "EUR" || value.Estimates.Currencies[0].KnownAmount != "0.00007" || value.Estimates.Currencies[0].PartialResponses != 1 || value.Estimates.Currencies[1].Currency != "USD" || value.Estimates.Currencies[1].KnownAmount != "0.0000975" || value.Estimates.Currencies[1].CompleteResponses != 1 {
		t.Fatal("mixed currencies or overlap", value.Estimates)
	}
	if value.Pricing[0].Input.KnownTokens != "11" || value.Pricing[0].Output.KnownTokens != "7" || value.Pricing[1].Input.MissingPriceResponses != 1 {
		t.Fatal("wrong covered categories", value.Pricing)
	}
	for _, action := range []func() error{
		func() error {
			r := connect.NewRequest(&pb.GetModelPricingRequest{ModelId: model})
			r.Header().Set("Authorization", "Bearer "+f.workerToken)
			_, e := c.GetModelPricing(ctx, r)
			return e
		},
		func() error {
			r := connect.NewRequest(request)
			r.Header().Set("Authorization", "Bearer "+f.workerToken)
			_, e := c.SetModelPricing(ctx, r)
			return e
		},
		func() error {
			r := connect.NewRequest(&pb.GetPricingVersionRequest{Id: first.Msg.Pricing.Id})
			r.Header().Set("Authorization", "Bearer "+f.workerToken)
			_, e := c.GetPricingVersion(ctx, r)
			return e
		},
	} {
		if err = action(); err != nil && connect.CodeOf(err) != connect.CodeAborted {
			t.Fatal("Worker pricing access", err)
		}
	}
	paired, device := pairedQuestionClient(t, f)
	if _, err = c.GetModelPricing(ctx, ownerRequest(paired, &pb.GetModelPricingRequest{ModelId: model})); err != nil {
		t.Fatal(err)
	}
	if _, err = c.SetModelPricing(ctx, ownerRequest(paired, request)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("actor reused another receipt", err)
	}
	devices := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
	if _, err = devices.RevokeDevice(ctx, ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: acctMutation(device, domain.NewID())})); err != nil {
		t.Fatal(err)
	}
	staleCtx := domain.WithPrincipal(ctx, domain.Principal{Type: domain.ClientDevice, DeviceID: domain.ID(device.Id)})
	if _, err = f.service.SetModelPricing(staleCtx, connect.NewRequest(next)); err == nil {
		t.Fatal("revoked principal wrote prices")
	}
	if _, err = f.service.GetModelPricing(staleCtx, connect.NewRequest(&pb.GetModelPricingRequest{ModelId: model})); err == nil {
		t.Fatal("revoked principal read prices")
	}
	// Deleting configuration redacts the model-owned mutation receipt, while the
	// original immutable basis remains readable for retained response history.
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.delete-priced-model", nil, func(tx *store.Tx) (any, error) { return nil, tx.Delete(domain.ModelKind, domain.ID(model), 1) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.SetModelPricing(ctx, ownerRequest(f.service.Identity, request)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("deleted model receipt replayed", err)
	}
	if _, err = c.GetPricingVersion(ctx, ownerRequest(f.service.Identity, &pb.GetPricingVersionRequest{Id: first.Msg.Pricing.Id})); err != nil {
		t.Fatal("history lost after configuration removal", err)
	}
}
