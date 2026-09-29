package cli

import (
	"context"
	"encoding/json"
	"io"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type networkCLIOutcome struct {
	Resource  *pb.Resource
	RequestId string
	Replayed  bool
	Deleted   bool
}

func networkCommand(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	profile := args[0] == "profile"
	if profile {
		args = args[1:]
		if len(args) == 0 {
			return nil, usage()
		}
	}
	operation := args[0]
	f := flags("network " + operation)
	id := f.String("id", "", "")
	revision := f.Uint64("revision", 0, "")
	machine := f.String("machine-id", "", "")
	profileID := f.String("profile-id", "", "")
	profileRevision := f.Uint64("profile-revision", 0, "")
	input := f.String("input", "-", "")
	credentialStdin := f.Bool("credential-stdin", false, "")
	clearCredential := f.Bool("clear-credential", false, "")
	pageToken := f.String("page-token", "", "")
	limit := f.Uint("limit", 50, "")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	mutation := &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}
	var response networkCLIOutcome
	if profile {
		switch operation {
		case "list":
			if *limit < 1 || *limit > 200 {
				return nil, domain.Fail(domain.InvalidArgument, "Invalid page size.", "Use 1–200.")
			}
			reply, err := c.resources.ListResources(ctx, request(c, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_NETWORK_PROFILE, PageSize: uint32(*limit), PageToken: *pageToken}}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			items := make([]any, 0, len(reply.Msg.Resources))
			for _, r := range reply.Msg.Resources {
				items = append(items, resourceJSON(r))
			}
			return map[string]any{"profiles": items, "next_page_token": reply.Msg.NextPageToken}, nil
		case "get":
			reply, err := c.resources.GetResource(ctx, request(c, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_NETWORK_PROFILE, Id: *id}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			return resourceJSON(reply.Msg.Resource), nil
		case "save":
			if o.tokenStdin && (*input == "-" || *credentialStdin) || *credentialStdin && (*input == "-" || *clearCredential || terminalInput(streams.In)) {
				return nil, domain.Fail(domain.InvalidArgument, "Network definition and credentials require separate input channels.", "Use a definition file with --credential-stdin and an existing owner/paired connection; credentials cannot enter argv or output.")
			}
			raw, err := readDocument(*input, streams.In)
			if err != nil {
				return nil, err
			}
			var credential []byte
			if *credentialStdin {
				credential, err = io.ReadAll(io.LimitReader(streams.In, 4097))
				defer clear(credential)
				if err != nil || len(credential) > 4096 {
					return nil, domain.Fail(domain.InvalidArgument, "Invalid proxy credential input.", "Provide at most 4096 bytes of username/password JSON through stdin.")
				}
			}
			reply, err := c.network.SaveNetworkProfile(ctx, request(c, &pb.SaveNetworkProfileRequest{Mutation: mutation, SchemaVersion: 1, DocumentJson: raw, CredentialJson: credential, ClearCredential: *clearCredential}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			response = networkCLIOutcome{Resource: reply.Msg.Resource, RequestId: reply.Msg.RequestId, Replayed: reply.Msg.Replayed, Deleted: reply.Msg.Deleted}
		case "delete":
			reply, err := c.network.DeleteNetworkProfile(ctx, request(c, &pb.DeleteNetworkProfileRequest{Mutation: mutation}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			response = networkCLIOutcome{Resource: reply.Msg.Resource, RequestId: reply.Msg.RequestId, Replayed: reply.Msg.Replayed, Deleted: reply.Msg.Deleted}
		default:
			return nil, usage()
		}
	} else {
		switch operation {
		case "status":
			reply, err := c.network.GetNetworkRoute(ctx, request(c, &pb.GetNetworkRouteRequest{MachineId: *machine}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			if reply.Msg.Route == nil {
				return map[string]any{"mode": domain.ProxyDirect, "desired_generation": "0"}, nil
			}
			return resourceJSON(reply.Msg.Route), nil
		case "select":
			reply, err := c.network.SelectNetworkProfile(ctx, request(c, &pb.SelectNetworkProfileRequest{Mutation: mutation, MachineId: *machine, ProfileId: *profileID, ProfileRevision: *profileRevision}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			response = networkCLIOutcome{Resource: reply.Msg.Resource, RequestId: reply.Msg.RequestId, Replayed: reply.Msg.Replayed, Deleted: reply.Msg.Deleted}
		case "export-metadata":
			reply, err := c.network.ExportWorkerNetworkMetadata(ctx, request(c, &pb.ExportWorkerNetworkMetadataRequest{MachineId: *machine, DesiredGeneration: *revision}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			return map[string]any{"metadata": json.RawMessage(reply.Msg.MetadataJson), "authentication": reply.Msg.Authentication}, nil
		default:
			return nil, usage()
		}
	}
	result := map[string]any{"request_id": response.RequestId, "replayed": response.Replayed, "deleted": response.Deleted}
	if response.Resource != nil {
		result["resource"] = resourceJSON(response.Resource)
	}
	return result, nil
}
