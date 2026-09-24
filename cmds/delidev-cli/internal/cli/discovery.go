package cli

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func machineDiscover(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	fs := flags("machine discover")
	id := fs.String("id", "", "machine ID")
	revision := fs.Uint64("revision", 0, "expected machine revision")
	input := fs.String("input", "", "replacement executable selections, or omit to refresh existing selections")
	wait := fs.Bool("wait", false, "wait within the command deadline")
	protocol := fs.Bool("protocol", false, "validate native protocol without login or inference")
	if err := parse(fs, args); err != nil {
		return nil, err
	}
	if o.tokenStdin && *input == "-" {
		return nil, domain.Fail(domain.InvalidArgument, "Credential and configuration input cannot share stdin.", "Use --input FILE for executable selections.")
	}
	var raw []byte
	if *input != "" {
		var err error
		raw, err = readDocument(*input, streams.In)
		if err != nil {
			return nil, err
		}
	}
	response, err := c.workers.DiscoverHarnesses(ctx, request(c, &pb.DiscoverHarnessesRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}, SelectionsJson: raw, VerifyProtocol: *protocol}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	job, machine := response.Msg.Job, response.Msg.Machine
	result := func() any {
		return map[string]any{"job": resourceJSON(job), "machine": resourceJSON(machine), "replayed": response.Msg.Replayed}
	}
	if *wait {
		job, err = awaitJob(ctx, c, job)
		if err != nil {
			return result(), err
		}
		latest, err := c.resources.GetResource(ctx, request(c, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, Id: *id}))
		if err != nil {
			return result(), rpc.ClientError(err)
		}
		machine = latest.Msg.Resource
		var state domain.Job
		if err := domain.Decode(job.DocumentJson, &state); err != nil {
			return result(), err
		}
		if state.Problem != nil {
			return result(), state.Problem
		}
	}
	return result(), nil
}
