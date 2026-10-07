package cli

import (
	"bytes"
	"context"
	"io"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func readPAT(input io.Reader) ([]byte, error) {
	if terminalInput(input) {
		return nil, domain.Fail(domain.MissingInput, "A PAT is required through protected stdin.", "Pipe the token from protected storage; never include it in command arguments.")
	}
	raw, err := io.ReadAll(io.LimitReader(input, 515))
	if err != nil {
		clear(raw)
		return nil, domain.Fail(domain.InvalidArgument, "The PAT input could not be read.", "Provide only one token through stdin.")
	}
	token := raw
	if bytes.HasSuffix(token, []byte("\r\n")) {
		token = token[:len(token)-2]
	} else if bytes.HasSuffix(token, []byte("\n")) {
		token = token[:len(token)-1]
	}
	if err = credentials.ValidatePAT(token); err != nil {
		clear(raw)
		return nil, err
	}
	return token, nil
}
func integrationCommand(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	operation := args[0]
	if operation == "token-form" {
		return githubTokenFormCommand(ctx, c, args[1:])
	}
	switch operation {
	case "create", "edit", "replace-token", "validate", "delete":
	default:
		return nil, usage()
	}
	f := flags("integration " + operation)
	id := f.String("id", "", "")
	revision := f.Uint64("revision", 0, "")
	var input string
	var tokenStdin bool
	if operation == "create" || operation == "edit" {
		f.StringVar(&input, "input", "-", "")
	}
	if operation == "replace-token" {
		f.BoolVar(&tokenStdin, "pat-stdin", false, "")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if operation == "create" {
		if *id != "" || *revision != 0 {
			return nil, domain.Fail(domain.InvalidArgument, "New profiles cannot select an existing identity.", "Omit --id and --revision.")
		}
	} else if domain.ID(*id).Validate() != nil || *revision == 0 {
		return nil, domain.Fail(domain.MissingInput, "A profile ID and current revision are required.", "Read integration get --id ID, then provide --id and --revision.")
	}
	mutation := &pb.Mutation{Id: *id, ExpectedRevision: *revision, RequestId: string(o.requestID)}
	if operation == "create" || operation == "edit" {
		if o.tokenStdin && input == "-" {
			return nil, domain.Fail(domain.InvalidArgument, "Server authentication and profile JSON cannot share stdin.", "Select a profile file with --input.")
		}
		raw, err := readDocument(input, streams.In)
		if err != nil {
			return nil, err
		}
		reply, err := c.integrations.SaveIntegrationProfile(ctx, request(c, &pb.SaveIntegrationProfileRequest{Mutation: mutation, SchemaVersion: 1, DocumentJson: raw}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"profile": resourceJSON(reply.Msg.Profile), "replayed": reply.Msg.Replayed}, nil
	}
	var profile *pb.Resource
	var replayed, deleted bool
	var problem []byte
	switch operation {
	case "replace-token":
		if !tokenStdin || o.tokenStdin {
			return nil, domain.Fail(domain.MissingInput, "A separate PAT stdin input is required.", "Use --pat-stdin with an already paired client or local owner connection.")
		}
		token, err := readPAT(streams.In)
		if err != nil {
			return nil, err
		}
		defer clear(token)
		reply, err := c.integrations.ReplaceIntegrationToken(ctx, request(c, &pb.ReplaceIntegrationTokenRequest{Mutation: mutation, Token: token}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		profile, replayed, problem = reply.Msg.Profile, reply.Msg.Replayed, reply.Msg.ProblemJson
	case "validate":
		reply, err := c.integrations.ValidateIntegrationProfile(ctx, request(c, &pb.ValidateIntegrationProfileRequest{Mutation: mutation}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		profile, replayed, problem = reply.Msg.Profile, reply.Msg.Replayed, reply.Msg.ProblemJson
	case "delete":
		reply, err := c.integrations.DeleteIntegrationProfile(ctx, request(c, &pb.DeleteIntegrationProfileRequest{Mutation: mutation}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		profile, replayed, problem, deleted = reply.Msg.Profile, reply.Msg.Replayed, reply.Msg.ProblemJson, reply.Msg.Deleted
	}
	value := map[string]any{"replayed": replayed}
	if profile != nil {
		value["profile"] = resourceJSON(profile)
	}
	if operation == "delete" {
		value["deleted"] = deleted
	}
	if len(problem) != 0 {
		var failure domain.Error
		if err := domain.Decode(problem, &failure); err != nil {
			return value, err
		}
		return value, &failure
	}
	return value, nil
}
