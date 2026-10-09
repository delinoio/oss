// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// ListActivity keeps the allocated compatibility RPC without reading product
// state, validating old filters/cursors or admitting any work after authorization.
func (s *Service) ListActivity(ctx context.Context, req *connect.Request[pb.ListActivityRequest]) (*connect.Response[pb.ListActivityResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Only an owner or paired client can access product records.", "Use an authorized product client."), correlation)
	}
	return nil, rpc.Error(domain.Fail(domain.Unsupported, "Activity has been retired.", "Use Sessions, Inbox, Pull requests, Usage or Schedules to inspect their retained records."), correlation)
}
