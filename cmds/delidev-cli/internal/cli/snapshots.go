// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func snapshotListCommand(ctx context.Context, c client, args []string) (any, error) {
	f := flags("snapshot list")
	session := f.String("session-id", "", "owning session UUID")
	limit := f.Uint("limit", 50, "page size: 1 through 100")
	page := f.String("page-token", "", "original opaque inventory page token")
	if err := parse(f, args); err != nil {
		return nil, err
	}
	if err := domain.ID(*session).Validate(); err != nil {
		return nil, err
	}
	if *limit == 0 || *limit > 100 {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid snapshot page size.", "Use 1 through 100.")
	}
	status, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if !slices.Contains(status.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_WORKSPACE_STORAGE_V1) {
		return nil, domain.Fail(domain.Unsupported, "The server does not support workspace snapshot inventory.", "Update the DeliDev server before listing managed snapshots.")
	}
	response, err := c.resources.ListResources(ctx, request(c, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_SNAPSHOT, SessionId: *session, PageSize: uint32(*limit), PageToken: *page}}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	return map[string]any{"resources": resourcesJSON(response.Msg.Resources), "next_page_token": response.Msg.NextPageToken}, nil
}
