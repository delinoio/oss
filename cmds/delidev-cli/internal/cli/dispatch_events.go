// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func dispatchEvents(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	fs := flags("events")
	cursor := fs.String("cursor", "", "snapshot event cursor")
	session := fs.String("session-id", "", "session scope")
	if err := parse(fs, rest); err != nil {
		return emit(nil, err), true
	}
	if *cursor == "" {
		return emit(nil, domain.Fail(domain.MissingInput, "An event cursor is required.", "Fetch a resource snapshot before subscribing.")), true
	}
	stream, err := c.resources.WatchEvents(ctx, request(c, &pb.WatchEventsRequest{Cursor: *cursor, SessionId: *session}))
	if err != nil {
		return emit(nil, rpc.ClientError(err)), true
	}
	defer stream.Close()
	for stream.Receive() {
		if code := emit(stream.Msg(), nil); code != 0 {
			return code, true
		}
	}
	if err := stream.Err(); err != nil {
		if ctx.Err() != nil {
			return emit(nil, domain.SafeError(ctx.Err())), true
		}
		return emit(nil, rpc.ClientError(err)), true
	}
	return 0, true
}
