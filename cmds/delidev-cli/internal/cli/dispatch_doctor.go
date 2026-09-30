// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func dispatchDoctor(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) > 0 {
		return emit(nil, usage()), true
	}
	response, err := c.system.GetDoctor(ctx, request(c, &pb.GetDoctorRequest{}))
	if err != nil {
		return emit(nil, rpc.ClientError(err)), true
	}
	return emit(json.RawMessage(response.Msg.ReportJson), nil), true

}
