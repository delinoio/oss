package cli

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func backupOutput(value proto.Message) (any, error) {
	encoded, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(value)
	return json.RawMessage(encoded), err
}

func backupCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	f := flags("backup " + args[0])
	switch args[0] {
	case "create":
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		result, err := c.system.CreateBackup(ctx, request(c, &pb.CreateBackupRequest{RequestId: string(o.requestID)}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return backupOutput(result.Msg)
	case "list":
		limit := f.Uint("limit", 50, "page size, from 1 to 100")
		page := f.String("page-token", "", "original inventory page token")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *limit < 1 || *limit > 100 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid backup page size.", "Use --limit between 1 and 100.")
		}
		result, err := c.system.ListBackups(ctx, request(c, &pb.ListBackupsRequest{PageSize: uint32(*limit), PageToken: *page}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return backupOutput(result.Msg)
	case "inspect":
		id := f.String("id", "", "managed backup UUID")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if err := domain.ID(*id).Validate(); err != nil {
			return nil, err
		}
		result, err := c.system.InspectBackup(ctx, request(c, &pb.InspectBackupRequest{Id: *id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return backupOutput(result.Msg)
	default:
		return nil, usage()
	}
}
