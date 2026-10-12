// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func tailnetCLIJSON(raw []byte, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil, domain.Fail(domain.Unsupported, "Invalid Tailscale result.", "Inspect the original server.")
	}
	return value, nil
}
func tailscaleCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	fs := flags("tailscale " + args[0])
	var peer, id, name, code *string
	var allow *bool
	switch args[0] {
	case "check":
		peer = fs.String("peer-id", "", "original visible peer ID")
	case "connect", "register-worker":
		peer = fs.String("peer-id", "", "original visible peer ID")
		name = fs.String("name", "", "saved connection or Worker name")
	case "inspect", "cancel":
		id = fs.String("id", "", "original request ID")
	case "decide":
		id = fs.String("id", "", "original approval ID")
		code = fs.String("code", "", "matching confirmation code")
		allow = fs.Bool("allow", false, "explicitly allow the original request")
	}
	if err := parse(fs, args[1:]); err != nil {
		return nil, err
	}
	switch args[0] {
	case "discover":
		r, e := c.tailscale.DiscoverTailscale(ctx, request(c, &pb.DiscoverTailscaleRequest{}))
		if e != nil {
			return nil, e
		}
		return tailnetCLIJSON(r.Msg.DocumentJson, nil)
	case "check":
		r, e := c.tailscale.CheckTailscale(ctx, request(c, &pb.CheckTailscaleRequest{PeerId: *peer}))
		if e != nil {
			return nil, e
		}
		return tailnetCLIJSON(r.Msg.DocumentJson, nil)
	case "access":
		r, e := c.tailscale.GetTailscaleAccess(ctx, request(c, &pb.GetTailscaleAccessRequest{}))
		if e != nil {
			return nil, e
		}
		return tailnetCLIJSON(r.Msg.DocumentJson, nil)
	case "enable", "disable":
		if o.requestID.Validate() != nil {
			return nil, usage()
		}
		r, e := c.tailscale.SetTailscaleAccess(ctx, request(c, &pb.SetTailscaleAccessRequest{RequestId: string(o.requestID), Enabled: args[0] == "enable"}))
		if e != nil {
			return nil, e
		}
		return tailnetCLIJSON(r.Msg.DocumentJson, nil)
	case "connect", "register-worker":
		if o.requestID.Validate() != nil {
			return nil, usage()
		}
		kind := pb.DeviceType_DEVICE_TYPE_CLIENT
		if args[0] == "register-worker" {
			kind = pb.DeviceType_DEVICE_TYPE_WORKER
		}
		r, e := c.tailscale.BeginTailscaleConnection(ctx, request(c, &pb.BeginTailscaleConnectionRequest{RequestId: string(o.requestID), PeerId: *peer, Name: *name, Role: kind}))
		if e != nil {
			return nil, e
		}
		return tailnetCLIJSON(r.Msg.DocumentJson, nil)
	case "inspect":
		r, e := c.tailscale.ResumeTailscaleConnection(ctx, request(c, &pb.ResumeTailscaleConnectionRequest{RequestId: *id}))
		if e != nil {
			return nil, e
		}
		return tailnetCLIJSON(r.Msg.DocumentJson, nil)
	case "requests":
		r, e := c.tailscale.ListTailscaleConnections(ctx, request(c, &pb.ListTailscaleConnectionsRequest{}))
		if e != nil {
			return nil, e
		}
		return tailnetCLIJSON(r.Msg.DocumentJson, nil)
	case "approvals":
		r, e := c.tailscale.ListTailscaleApprovals(ctx, request(c, &pb.ListTailscaleApprovalsRequest{}))
		if e != nil {
			return nil, e
		}
		return tailnetCLIJSON(r.Msg.DocumentJson, nil)
	case "decide":
		r, e := c.tailscale.DecideTailscaleApproval(ctx, request(c, &pb.DecideTailscaleApprovalRequest{RequestId: *id, ConfirmationCode: *code, Allow: *allow}))
		if e != nil {
			return nil, e
		}
		return tailnetCLIJSON(r.Msg.DocumentJson, nil)
	case "cancel":
		r, e := c.tailscale.CancelTailscaleConnection(ctx, request(c, &pb.CancelTailscaleConnectionRequest{RequestId: *id}))
		if e != nil {
			return nil, e
		}
		return tailnetCLIJSON(r.Msg.DocumentJson, nil)
	default:
		return nil, usage()
	}
}
