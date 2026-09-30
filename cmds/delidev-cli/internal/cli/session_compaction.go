// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func sessionCompactionCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	f := flags("session " + args[0])
	id := f.String("id", "", "")
	revision := new(uint64)
	if args[0] == "compact" {
		revision = f.Uint64("revision", 0, "")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if err := domain.ID(*id).Validate(); err != nil {
		return nil, err
	}
	if args[0] == "context" {
		r, err := c.sessions.GetSessionContext(ctx, request(c, &pb.GetSessionContextRequest{SessionId: *id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"context": json.RawMessage(r.Msg.DocumentJson), "capabilities": r.Msg.Capabilities}, nil
	}
	if *revision == 0 {
		return nil, domain.Fail(domain.MissingInput, "Compaction requires the current session revision.", "Provide --id and --revision; retain the exact request ID on retry.")
	}
	r, err := c.sessions.CompactSession(ctx, request(c, &pb.CompactSessionRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	return map[string]any{"job": resourceJSON(r.Msg.Job), "request_id": r.Msg.RequestId, "replayed": r.Msg.Replayed}, nil
}
