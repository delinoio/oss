package cli

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func budgetCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 || (args[0] != "get" && args[0] != "set" && args[0] != "remove") {
		return nil, usage()
	}
	f := flags("session budget " + args[0])
	id := f.String("id", "", "session UUID")
	var revision uint64
	var currency, threshold string
	if args[0] != "get" {
		f.Uint64Var(&revision, "revision", 0, "current session revision")
	}
	if args[0] == "set" {
		f.StringVar(&currency, "currency", "", "three-letter currency")
		f.StringVar(&threshold, "threshold", "", "exact decimal estimated-cost threshold")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if err := domain.ID(*id).Validate(); err != nil {
		return nil, err
	}
	var message proto.Message
	if args[0] == "get" {
		result, err := c.sessions.GetSessionBudget(ctx, request(c, &pb.GetSessionBudgetRequest{SessionId: *id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		message = result.Msg
	} else {
		change := &pb.SetSessionBudgetRequest{Mutation: &pb.Mutation{Id: *id, ExpectedRevision: revision, RequestId: string(o.requestID)}}
		if args[0] == "remove" {
			change.Change = &pb.SetSessionBudgetRequest_Remove{Remove: true}
		} else {
			if err := (domain.EstimatedCostBudget{Currency: domain.Currency(currency), Threshold: threshold}).Validate(); err != nil {
				return nil, err
			}
			change.Change = &pb.SetSessionBudgetRequest_Budget{Budget: &pb.EstimatedCostBudget{Currency: currency, Threshold: threshold}}
		}
		result, err := c.sessions.SetSessionBudget(ctx, request(c, change))
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
