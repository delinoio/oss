package cli

import (
	"context"
	"encoding/json"
	"io"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func pricingCommand(ctx context.Context, c client, o options, args []string, input io.Reader) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	f := flags("usage pricing " + args[0])
	var id, provider, native, service, selectedMode string
	var revision, providerRevision, policyRevision uint64
	path := "-"
	switch args[0] {
	case "version":
		f.StringVar(&id, "id", "", "immutable pricing version UUID")
	case "get", "set", "mode":
		f.StringVar(&provider, "provider-id", "", "original API provider UUID")
		f.StringVar(&service, "subscription-service", "", "original closed subscription service")
		f.StringVar(&native, "native-id", "", "exact native model ID")
	case "refresh":
	default:
		return nil, usage()
	}
	if args[0] == "set" || args[0] == "mode" {
		f.Uint64Var(&providerRevision, "provider-revision", 0, "original current provider revision; zero for subscriptions")
		f.Uint64Var(&policyRevision, "policy-revision", 0, "current pricing policy revision")
	}
	if args[0] == "set" || args[0] == "mode" {
		f.Uint64Var(&revision, "revision", 0, "current price revision, zero initially")
	}
	if args[0] == "set" {
		f.StringVar(&path, "input", "-", "pricing JSON file, or - for stdin")
	}
	if args[0] == "mode" {
		f.StringVar(&selectedMode, "mode", "", "automatic or manual")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	identity := domain.ModelIdentity{ProviderID: domain.ID(provider), SubscriptionService: domain.SubscriptionService(service), NativeID: native}
	if args[0] == "version" {
		if err := domain.ID(id).Validate(); err != nil {
			return nil, err
		}
	} else if args[0] != "refresh" {
		if err := identity.Validate(); err != nil {
			return nil, err
		}
	}
	model := &pb.ModelIdentity{ProviderId: provider, SubscriptionService: rpc.WireSubscriptionService(identity.SubscriptionService), NativeId: native}
	var message proto.Message
	switch args[0] {
	case "get":
		result, err := c.usage.GetTokenPricing(ctx, request(c, &pb.GetTokenPricingRequest{Model: model}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		message = result.Msg
	case "version":
		result, err := c.usage.GetPricingVersion(ctx, request(c, &pb.GetPricingVersionRequest{Id: id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		message = result.Msg
	case "set":
		if path == "-" && o.tokenStdin {
			return nil, domain.Fail(domain.InvalidArgument, "Credential stdin and pricing stdin cannot share one stream.", "Pass the non-secret pricing document with --input PATH.")
		}
		raw, err := readDocument(path, input)
		if err != nil {
			return nil, err
		}
		var basis domain.TokenPricing
		if err = domain.Decode(raw, &basis); err != nil {
			return nil, err
		}
		if err = basis.Validate(); err != nil {
			return nil, err
		}
		mode := pb.InputPricingMode_INPUT_PRICING_MODE_UNIFORM
		if basis.InputMode == domain.CachedInputPrice {
			mode = pb.InputPricingMode_INPUT_PRICING_MODE_CACHED_DISCOUNT
		}
		result, err := c.usage.SetTokenPricing(ctx, request(c, &pb.SetTokenPricingRequest{Model: model, ExpectedProviderRevision: providerRevision, ExpectedPolicyRevision: policyRevision, ExpectedRevision: revision, RequestId: string(o.requestID), Basis: &pb.TokenPricing{Currency: string(basis.Currency), Source: basis.Source, AsOf: basis.AsOf, InputMode: mode, InputPerMillion: basis.InputPerMillion, CachedInputPerMillion: basis.CachedInputPerMillion, OutputPerMillion: basis.OutputPerMillion, Exclusions: basis.Exclusions}}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		message = result.Msg
	case "mode":
		mode := pb.TokenPricingMode_TOKEN_PRICING_MODE_UNSPECIFIED
		if selectedMode == "automatic" {
			mode = pb.TokenPricingMode_TOKEN_PRICING_MODE_AUTOMATIC
		} else if selectedMode == "manual" {
			mode = pb.TokenPricingMode_TOKEN_PRICING_MODE_MANUAL
		} else {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid pricing mode.", "Choose automatic or manual.")
		}
		result, err := c.usage.SetTokenPricingMode(ctx, request(c, &pb.SetTokenPricingModeRequest{Model: model, Mode: mode, ExpectedRevision: revision, ExpectedProviderRevision: providerRevision, ExpectedPolicyRevision: policyRevision, RequestId: string(o.requestID)}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		message = result.Msg
	case "refresh":
		result, err := c.usage.RefreshTokenPrices(ctx, request(c, &pb.RefreshTokenPricesRequest{}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		message = result.Msg
	}
	raw, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(message)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	return json.RawMessage(raw), nil
}
