// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func dispatchServer(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) != 1 {
		return emit(nil, usage()), true
	}
	switch rest[0] {
	case "overview":
		response, err := c.system.GetOverview(ctx, request(c, &pb.GetOverviewRequest{}))
		if err != nil {
			return emit(nil, rpc.ClientError(err)), true
		}
		return emit(overviewOutput(response.Msg), nil), true
	case "status":
		response, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
		if err != nil {
			return emit(nil, rpc.ClientError(err)), true
		}
		return emit(response.Msg, nil), true
	case "stop":
		ensureRequest(&o)
		response, err := c.system.StopServer(ctx, request(c, &pb.StopServerRequest{RequestId: string(o.requestID)}))
		if err != nil {
			if o.server == "" && !o.tokenStdin && (domain.SafeError(rpc.ClientError(err)).Code == domain.ServerUnavailable || domain.SafeError(rpc.ClientError(err)).Code == domain.Unavailable) {
				if suppressErr := server.SuppressLocalRestart(o.dataDir, o.requestID); suppressErr != nil {
					return emit(nil, suppressErr), true
				}
				return emit(nil, offlineStop()), true
			}
			return emit(nil, rpc.ClientError(err)), true
		}
		return emit(response.Msg, nil), true
	default:
		return emit(nil, usage()), true
	}

	return 0, false
}
