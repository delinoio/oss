package cli

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func sessionDiagnostics(ctx context.Context, c client, args []string) (any, error) {
	f := flags("session diagnostics")
	body := &pb.ListRequestDiagnosticsRequest{}
	f.StringVar(&body.SessionId, "id", "", "original session ID")
	f.StringVar(&body.ExecutionId, "execution-id", "", "optional exact execution ID")
	f.StringVar(&body.PageToken, "page-token", "", "original diagnostic continuation")
	limit := f.Uint("page-size", 50, "at most 100 observations")
	if err := parse(f, args); err != nil {
		return nil, err
	}
	if domain.ID(body.SessionId).Validate() != nil || body.ExecutionId != "" && domain.ID(body.ExecutionId).Validate() != nil || *limit < 1 || *limit > 100 {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid request diagnostic selection.", "Select an original session and a page size from 1 to 100.")
	}
	body.PageSize = uint32(*limit)
	response, err := c.sessions.ListRequestDiagnostics(ctx, request(c, body))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	raw, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(response.Msg)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	return json.RawMessage(raw), nil
}
