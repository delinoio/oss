package cli

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func sessionSubagents(ctx context.Context, c client, args []string) (any, error) {
	f := flags("session subagents")
	id := f.String("id", "", "session UUID")
	limit := f.Uint("limit", 50, "page size")
	page := f.String("page-token", "", "original read cursor")
	if err := parse(f, args); err != nil {
		return nil, err
	}
	if err := domain.ID(*id).Validate(); err != nil {
		return nil, err
	}
	if *limit == 0 || *limit > 100 {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid child page size.", "Use --limit from 1 to 100.")
	}
	response, err := c.resources.ListResources(ctx, request(c, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_SUBAGENT, SessionId: *id, PageSize: uint32(*limit), PageToken: *page}}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	return map[string]any{"subagents": resourcesJSON(response.Msg.Resources), "next_page_token": response.Msg.NextPageToken}, nil
}
