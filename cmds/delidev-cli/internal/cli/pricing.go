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
	var id string
	if args[0] == "version" {
		f.StringVar(&id, "id", "", "immutable pricing version UUID")
	} else {
		f.StringVar(&id, "model-id", "", "model UUID")
	}
	var revision, modelRevision uint64
	path := "-"
	if args[0] == "set" {
		f.Uint64Var(&modelRevision, "model-revision", 0, "model revision returned with current pricing")
		f.Uint64Var(&revision, "revision", 0, "current pricing revision, zero for the first basis")
		f.StringVar(&path, "input", "-", "pricing JSON file, or - for stdin")
	}
	if args[0] != "get" && args[0] != "version" && args[0] != "set" {
		return nil, usage()
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if err := domain.ID(id).Validate(); err != nil {
		return nil, err
	}
	var message proto.Message
	switch args[0] {
	case "get":
		result, err := c.usage.GetModelPricing(ctx, request(c, &pb.GetModelPricingRequest{ModelId: id}))
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
		result, err := c.usage.SetModelPricing(ctx, request(c, &pb.SetModelPricingRequest{ExpectedModelRevision: modelRevision, Mutation: &pb.Mutation{Id: id, ExpectedRevision: revision, RequestId: string(o.requestID)}, Basis: &pb.TokenPricing{Currency: string(basis.Currency), Source: basis.Source, AsOf: basis.AsOf, InputMode: mode, InputPerMillion: basis.InputPerMillion, CachedInputPerMillion: basis.CachedInputPerMillion, OutputPerMillion: basis.OutputPerMillion, Exclusions: basis.Exclusions}}))
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
