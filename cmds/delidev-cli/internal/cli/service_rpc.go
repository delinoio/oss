package cli

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func serviceRPC(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	fs := flags("service-control")
	kind := fs.String("kind", "", "server-host service kind")
	revision := fs.Uint64("revision", 0, "expected service revision")
	if err := parse(fs, args[1:]); err != nil {
		return nil, err
	}
	k := pb.UserServiceKind_USER_SERVICE_KIND_UNSPECIFIED
	switch *kind {
	case "server":
		k = pb.UserServiceKind_USER_SERVICE_KIND_SERVER
	case "worker":
		k = pb.UserServiceKind_USER_SERVICE_KIND_WORKER
	default:
		return nil, usage()
	}
	if args[0] == "status" {
		r, e := c.system.GetUserService(ctx, request(c, &pb.GetUserServiceRequest{Kind: k}))
		if e != nil {
			return nil, rpc.ClientError(e)
		}
		return r.Msg, nil
	}
	a := pb.UserServiceAction_USER_SERVICE_ACTION_UNSPECIFIED
	switch args[0] {
	case "install":
		a = pb.UserServiceAction_USER_SERVICE_ACTION_INSTALL
	case "start":
		a = pb.UserServiceAction_USER_SERVICE_ACTION_START
	case "stop":
		a = pb.UserServiceAction_USER_SERVICE_ACTION_STOP
	case "remove":
		a = pb.UserServiceAction_USER_SERVICE_ACTION_REMOVE
	default:
		return nil, usage()
	}
	r, e := c.system.ControlUserService(ctx, request(c, &pb.ControlUserServiceRequest{Kind: k, Action: a, RequestId: string(o.requestID), ExpectedRevision: *revision}))
	if e != nil {
		return nil, rpc.ClientError(e)
	}
	return r.Msg, nil
}
